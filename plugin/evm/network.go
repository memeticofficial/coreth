// (c) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package evm

import (
	"time"

	"github.com/ava-labs/avalanchego/cache"
	"github.com/ava-labs/avalanchego/ids"

	commonEng "github.com/ava-labs/avalanchego/snow/engine/common"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"

	"github.com/ava-labs/coreth/core"
	"github.com/ava-labs/coreth/core/types"
	"github.com/ava-labs/coreth/plugin/evm/message"

	coreth "github.com/ava-labs/coreth/chain"
)

const (
	recentCacheSize = 100
)

type network struct {
	gossipActivationTime time.Time

	appSender commonEng.AppSender
	chain     *coreth.ETHChain
	mempool   *Mempool

	gossipHandler message.Handler

	recentAtomicTxs *cache.LRU
	recentEthTxs    *cache.LRU
}

func (vm *VM) NewNetwork(
	activationTime time.Time,
	appSender commonEng.AppSender,
	chain *coreth.ETHChain,
	mempool *Mempool,
) *network {
	net := &network{
		gossipActivationTime: activationTime,
		appSender:            appSender,
		chain:                chain,
		mempool:              mempool,
		recentAtomicTxs:      &cache.LRU{Size: recentCacheSize},
		recentEthTxs:         &cache.LRU{Size: recentCacheSize},
	}
	net.gossipHandler = &GossipHandler{
		vm:  vm,
		net: net,
	}
	return net
}

func (n *network) AppRequestFailed(nodeID ids.ShortID, requestID uint32) error {
	return nil
}

func (n *network) AppRequest(nodeID ids.ShortID, requestID uint32, msgBytes []byte) error {
	return nil
}

func (n *network) AppResponse(nodeID ids.ShortID, requestID uint32, msgBytes []byte) error {
	return nil
}

func (n *network) AppGossip(nodeID ids.ShortID, msgBytes []byte) error {
	return n.handle(
		n.gossipHandler,
		"Gossip",
		nodeID,
		0,
		msgBytes,
	)
}

func (n *network) GossipAtomicTx(tx *Tx) error {
	txID := tx.ID()
	if time.Now().Before(n.gossipActivationTime) {
		log.Debug(
			"not gossiping atomic tx before the gossiping activation time",
			"txID", txID,
		)
		return nil
	}

	// Don't gossip transaction if it has been recently gossiped.
	if _, has := n.recentAtomicTxs.Get(txID); has {
		return nil
	}

	msg := message.AtomicTxNotify{
		Tx: tx.Bytes(),
	}
	n.recentAtomicTxs.Put(txID, nil)

	msgBytes, err := message.Build(&msg)
	if err != nil {
		return err
	}

	log.Debug(
		"gossiping atomic tx",
		"txID", txID,
	)
	return n.appSender.SendAppGossip(msgBytes)
}

func (n *network) sendEthTxsNotify(txs []*types.Transaction) error {
	if len(txs) == 0 {
		return nil
	}
	txBytes, err := rlp.EncodeToBytes(txs)
	if err != nil {
		log.Warn(
			"failed to encode eth transactions",
			"len(txs)", len(txs),
			"err", err,
		)
		return nil
	}
	msg := message.EthTxsNotify{
		Txs: txBytes,
	}
	msgBytes, err := message.Build(&msg)
	if err != nil {
		return err
	}
	log.Debug(
		"gossiping eth txs",
		"len(txs)", len(txs),
		"size(txs)", len(msg.Txs),
	)
	return n.appSender.SendAppGossip(msgBytes)
}

func (n *network) GossipEthTxs(txs []*types.Transaction) error {
	if time.Now().Before(n.gossipActivationTime) {
		log.Debug(
			"not gossiping eth txs before the gossiping activation time",
			"len(txs)", len(txs),
		)
		return nil
	}

	pool := n.chain.GetTxPool()
	selectedTxs := make([]*types.Transaction, 0)
	for _, tx := range txs {
		txHash := tx.Hash()
		txStatus := pool.Status([]common.Hash{txHash})[0]
		if txStatus != core.TxStatusPending {
			continue
		}

		if _, has := n.recentEthTxs.Get(txHash); has {
			continue
		}

		selectedTxs = append(selectedTxs, tx)
		n.recentEthTxs.Put(txHash, nil)
	}

	if len(selectedTxs) == 0 {
		return nil
	}

	// Attempt to gossip [selectedTxs]
	msgTxs := make([]*types.Transaction, 0)
	msgTxsSize := common.StorageSize(0)
	for _, tx := range selectedTxs {
		size := tx.Size()
		if msgTxsSize+size > message.EthMsgSoftCapSize {
			if err := n.sendEthTxsNotify(msgTxs); err != nil {
				return err
			}
			msgTxs = msgTxs[:0]
			msgTxsSize = 0
		}
		msgTxs = append(msgTxs, tx)
		msgTxsSize += size
	}

	// Send any remaining [msgTxs]
	return n.sendEthTxsNotify(msgTxs)
}

func (n *network) handle(
	handler message.Handler,
	handlerName string,
	nodeID ids.ShortID,
	requestID uint32,
	msgBytes []byte,
) error {
	log.Debug(
		"App message handler called",
		"handler", handlerName,
		"peerID", nodeID,
		"requestID", requestID,
		"len(msg)", len(msgBytes),
	)

	if time.Now().Before(n.gossipActivationTime) {
		log.Debug("App message called before activation time")
		return nil
	}

	msg, err := message.Parse(msgBytes)
	if err != nil {
		log.Debug("dropping App message due to failing to parse message")
		return nil
	}

	return msg.Handle(handler, nodeID, requestID)
}

type GossipHandler struct {
	message.NoopHandler

	vm  *VM
	net *network
}

func (h *GossipHandler) HandleAtomicTxNotify(nodeID ids.ShortID, _ uint32, msg *message.AtomicTxNotify) error {
	log.Debug(
		"AppGossip called with AtomicTxNotify",
		"peerID", nodeID,
	)

	if len(msg.Tx) == 0 {
		log.Warn(
			"AppGossip received empty AtomicTxNotify Message",
			"peerID", nodeID,
		)
		return nil
	}

	// In the case that the gossip message contains a transaction,
	// attempt to parse it and add it as a remote.
	tx := Tx{}
	if _, err := Codec.Unmarshal(msg.Tx, &tx); err != nil {
		log.Trace(
			"AppGossip provided invalid tx",
			"err", err,
		)
		return nil
	}
	unsignedBytes, err := Codec.Marshal(codecVersion, &tx.UnsignedAtomicTx)
	if err != nil {
		log.Warn(
			"AppGossip failed to marshal unsigned tx",
			"err", err,
		)
		return nil
	}
	tx.Initialize(unsignedBytes, msg.Tx)

	txID := tx.ID()
	if _, dropped, found := h.net.mempool.GetTx(txID); found || dropped {
		return nil
	}

	if err := h.vm.issueTx(&tx, false /*=local*/); err != nil {
		log.Trace(
			"AppGossip provided invalid transaction",
			"peerID", nodeID,
			"err", err,
		)
	}

	return nil
}

func (h *GossipHandler) HandleEthTxsNotify(nodeID ids.ShortID, _ uint32, msg *message.EthTxsNotify) error {
	log.Debug(
		"AppGossip called with EthTxsNotify",
		"peerID", nodeID,
		"size(txs)", len(msg.Txs),
	)

	if len(msg.Txs) == 0 {
		log.Warn(
			"AppGossip received empty EthTxsNotify Message",
			"peerID", nodeID,
		)
		return nil
	}

	// The maximum size of this encoded object is enforced by the codec.
	txs := make([]*types.Transaction, 0)
	if err := rlp.DecodeBytes(msg.Txs, &txs); err != nil {
		log.Trace(
			"AppGossip provided invalid txs",
			"peerID", nodeID,
			"err", err,
		)
		return nil
	}
	errs := h.net.chain.GetTxPool().AddRemotes(txs)
	for i, err := range errs {
		if err != nil {
			log.Debug(
				"AppGossip failed to add to mempool",
				"err", err,
				"tx", txs[i].Hash(),
			)
		}
	}
	return nil
}
