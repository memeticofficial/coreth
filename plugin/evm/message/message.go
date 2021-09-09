// (c) 2019-2021, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package message

import (
	"errors"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ava-labs/avalanchego/ids"
)

const (
	// TODO choose a sensible value

	// MaxEthTxsLen must be updated inside of EthTxsNotify's struct definition
	// as well when changed
	MaxEthTxsLen int = 10
)

var (
	// TODO: create a single gossip message used to notify peers of both EthTxs and
	// AtomicTxs
	_ Message = &AtomicTxNotify{}
	_ Message = &AtomicTx{}
	_ Message = &EthTxsNotify{}
	_ Message = &EthTxs{}

	errUnexpectedCodecVersion = errors.New("unexpected codec version")
)

type Message interface {
	// Handle this message with the correct message handler
	Handle(handler Handler, nodeID ids.ShortID, requestID uint32) error

	// initialize should be called whenever a message is built or parsed
	initialize([]byte)

	// Bytes returns the binary representation of this message
	//
	// Bytes should only be called after being initialized
	Bytes() []byte
}

type message []byte

func (m *message) initialize(bytes []byte) { *m = bytes }
func (m *message) Bytes() []byte           { return *m }

type AtomicTxNotify struct {
	message

	TxID ids.ID `serialize:"true"`
}

func (msg *AtomicTxNotify) Handle(handler Handler, nodeID ids.ShortID, requestID uint32) error {
	return handler.HandleAtomicTxNotify(nodeID, requestID, msg)
}

type AtomicTx struct {
	message

	Tx []byte `serialize:"true"`
}

func (msg *AtomicTx) Handle(handler Handler, nodeID ids.ShortID, requestID uint32) error {
	return handler.HandleAtomicTx(nodeID, requestID, msg)
}

type EthTxsNotify struct {
	message

	Txs []EthTxNotify `serialize:"true" len:"10"`
}

// Information about an Ethereum transaction for gossiping
type EthTxNotify struct {
	// The transaction's hash
	Hash common.Hash `serialize:"true"`

	// The transaction's sender
	Sender common.Address `serialize:"true"`

	// The transaction's nonce
	Nonce uint64 `serialize:"true"`
}

func (msg *EthTxsNotify) Handle(handler Handler, nodeID ids.ShortID, requestID uint32) error {
	return handler.HandleEthTxsNotify(nodeID, requestID, msg)
}

type EthTxs struct {
	message

	TxsBytes []byte `serialize:"true"`
}

func (msg *EthTxs) Handle(handler Handler, nodeID ids.ShortID, requestID uint32) error {
	return handler.HandleEthTxs(nodeID, requestID, msg)
}

func Parse(bytes []byte) (Message, error) {
	var msg Message
	version, err := c.Unmarshal(bytes, &msg)
	if err != nil {
		return nil, err
	}
	if version != codecVersion {
		return nil, errUnexpectedCodecVersion
	}
	msg.initialize(bytes)
	return msg, nil
}

func Build(msg Message) ([]byte, error) {
	bytes, err := c.Marshal(codecVersion, &msg)
	msg.initialize(bytes)
	return bytes, err
}