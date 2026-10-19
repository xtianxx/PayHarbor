package outboundobserver

import (
	"bytes"
	"context"
	"errors"
	"math/big"

	"core/constants"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// erc20 transfer(address,uint256) selector and the Transfer event topic.
var (
	erc20TransferSelector = []byte{0xa9, 0x05, 0x9c, 0xbb}
	erc20TransferTopic    = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")
)

// evmChainIDs is the set of gateway-supported EVM networks this observer will look
// at. Non-EVM families (Bitcoin, Solana, TRON, TRON testnet) and unknown/custom
// chain ids are refused before any RPC call.
var evmChainIDs = map[string]struct{}{
	new(big.Int).SetUint64(uint64(constants.Ethereum)).String():    {},
	new(big.Int).SetUint64(uint64(constants.Chiliz)).String():      {},
	new(big.Int).SetUint64(uint64(constants.ChilizSpicy)).String(): {},
	new(big.Int).SetUint64(uint64(constants.Base)).String():        {},
	new(big.Int).SetUint64(uint64(constants.Arbitrum)).String():    {},
	new(big.Int).SetUint64(uint64(constants.Unichain)).String():    {},
	new(big.Int).SetUint64(uint64(constants.Avalanche)).String():   {},
	new(big.Int).SetUint64(uint64(constants.Binance)).String():     {},
}

// Observer answers read-only questions about persisted EVM outbound intents.
type Observer struct {
	reader Reader
}

// NewObserver binds the observer to one caller-supplied reader.
func NewObserver(reader Reader) (*Observer, error) {
	if reader == nil {
		return nil, errors.New("outbound observer: reader is required")
	}
	return &Observer{reader: reader}, nil
}

// Observe reads the chain and reports what can be observed for one intent. Every
// outcome is returned as an Observation; the error is reserved for a misconfigured
// observer. Unknown outcomes carry no side effects anywhere.
func (o *Observer) Observe(ctx context.Context, intent Intent) (Observation, error) {
	if o == nil || o.reader == nil {
		return Observation{}, errors.New("outbound observer: observer is not configured")
	}
	observation := newObservation()
	// The network check comes first: a declared EVM kind does not make a non-EVM or
	// unknown network observable, and it must be refused before any RPC call (note
	// that a non-EVM chain id may itself be zero).
	if intent.ChainID != nil {
		if _, supported := evmChainIDs[intent.ChainID.String()]; !supported {
			observation.Reason = ReasonUnsupportedCoverage
			observation.set("intent_chain_id", intent.ChainID.String())
			return observation, nil
		}
	}
	if err := intent.Validate(); err != nil {
		if errors.Is(err, ErrUnsupported) {
			observation.Reason = ReasonUnsupportedCoverage
		} else {
			observation.Reason = ReasonIntentInvalid
		}
		observation.set("intent_kind", string(intent.Kind))
		return observation, nil
	}
	observation.set("intent_kind", string(intent.Kind))

	chainID, err := o.reader.ChainID(ctx)
	if err != nil {
		observation.Reason = ReasonRPCError
		observation.set("rpc_stage", "chain_id")
		return observation, nil
	}
	if chainID == nil || chainID.Cmp(intent.ChainID) != 0 {
		observation.Reason = ReasonWrongNetwork
		if chainID != nil {
			observation.set("rpc_chain_id", chainID.String())
		}
		return observation, nil
	}

	head, err := o.reader.HeadNumber(ctx)
	if err != nil {
		observation.Reason = ReasonRPCError
		observation.set("rpc_stage", "head")
		return observation, nil
	}
	observation.set("head_number", new(big.Int).SetUint64(head).String())

	rawTransaction, err := o.reader.TransactionByHash(ctx, intent.Hash)
	if err != nil {
		observation.Reason = ReasonRPCError
		observation.set("rpc_stage", "transaction")
		return observation, nil
	}
	if rawTransaction == nil || rawTransaction.Tx == nil {
		observation.Reason = ReasonNotFound
		return observation, nil
	}
	transaction := rawTransaction.Tx
	if transaction.Hash() != rawTransaction.ResponseHash || transaction.Hash() != intent.Hash {
		// The rebuilt transaction, the returned hash and the requested hash must all
		// agree; the node's "from" convenience field is never trusted.
		observation.Reason = ReasonTxHashMismatch
		return observation, nil
	}
	signer := types.LatestSignerForChainID(intent.ChainID)
	txChainID := transaction.ChainId()
	if txChainID == nil || txChainID.Sign() == 0 {
		observation.Reason = ReasonChainIDUnprotected
		return observation, nil
	}
	if txChainID.Cmp(intent.ChainID) != 0 {
		observation.Reason = ReasonChainIDMismatch
		observation.set("tx_chain_id", txChainID.String())
		return observation, nil
	}
	sender, err := types.Sender(signer, transaction)
	if err != nil {
		observation.Reason = ReasonSenderMismatch
		return observation, nil
	}
	observation.set("sender", sender.Hex())
	if sender != intent.From {
		observation.Reason = ReasonSenderMismatch
		return observation, nil
	}

	matched, matchReason := matchIntent(transaction, intent)
	observation.IntentMatched = matched
	if !matched {
		// The chain transaction is not this intent: stop here and report the mismatch
		// instead of layering receipt facts on top of a transaction that is not ours.
		observation.Reason = matchReason
		return observation, nil
	}

	receipt, err := o.reader.TransactionReceipt(ctx, intent.Hash)
	if err != nil {
		observation.Reason = ReasonRPCError
		observation.set("rpc_stage", "receipt")
		return observation, nil
	}
	if receipt == nil {
		observation.Status = StatusPending
		observation.Execution = ExecutionUnknown
		observation.Reason = ReasonReceiptPending
		return observation, nil
	}
	if receipt.TxHash == nil || *receipt.TxHash != intent.Hash {
		observation.Reason = ReasonReceiptInconsistent
		return observation, nil
	}
	if receipt.BlockHash == nil || receipt.BlockNumber == nil {
		observation.Status = StatusPending
		observation.Execution = ExecutionUnknown
		observation.Reason = ReasonReceiptPending
		return observation, nil
	}
	if receipt.TxIndex == nil {
		observation.Reason = ReasonReceiptInconsistent
		return observation, nil
	}
	// Only 0 (reverted) and 1 (success) are defined execution statuses; anything else
	// is not an execution claim.
	if receipt.Status == nil || (*receipt.Status != 0 && *receipt.Status != 1) {
		observation.Reason = ReasonReceiptStatusUnknown
		return observation, nil
	}
	// The transaction metadata the node reports must agree with the receipt. Absent
	// metadata (a node that answered while the receipt already existed) is not proof
	// either, so it stays unknown for a later re-observation.
	txMetaComplete := rawTransaction.BlockHash != nil && rawTransaction.BlockNumber != nil && rawTransaction.TxIndex != nil
	if !txMetaComplete {
		observation.Reason = ReasonReceiptInconsistent
		observation.set("tx_metadata", "incomplete")
		return observation, nil
	}
	if *rawTransaction.BlockHash != *receipt.BlockHash ||
		*rawTransaction.BlockNumber != *receipt.BlockNumber ||
		*rawTransaction.TxIndex != *receipt.TxIndex {
		observation.Reason = ReasonReceiptInconsistent
		return observation, nil
	}
	observation.set("tx_index", new(big.Int).SetUint64(*receipt.TxIndex).String())
	observation.set("receipt_status", new(big.Int).SetUint64(*receipt.Status).String())
	observation.set("log_count", new(big.Int).SetUint64(uint64(len(receipt.Logs))).String())

	if head < *receipt.BlockNumber {
		observation.Reason = ReasonHeadBehindBlock
		return observation, nil
	}
	block, err := o.reader.BlockByNumber(ctx, *receipt.BlockNumber)
	if err != nil {
		observation.Reason = ReasonRPCError
		observation.set("rpc_stage", "block")
		return observation, nil
	}
	if block == nil {
		observation.Reason = ReasonBlockMissing
		return observation, nil
	}
	if block.Number != *receipt.BlockNumber {
		observation.Reason = ReasonReceiptInconsistent
		return observation, nil
	}
	if block.Hash != *receipt.BlockHash {
		observation.Reason = ReasonReceiptBlockOrphaned
		return observation, nil
	}
	// The receipt's transaction index must point at this exact transaction inside the
	// canonical block, not merely somewhere in it.
	if *receipt.TxIndex >= uint64(len(block.TxHashes)) || block.TxHashes[*receipt.TxIndex] != intent.Hash {
		observation.Reason = ReasonIndexMismatch
		return observation, nil
	}

	observation.Status = StatusIncluded
	observation.Canonical = true
	blockNumber := *receipt.BlockNumber
	blockHash := *receipt.BlockHash
	observation.BlockNumber = &blockNumber
	observation.BlockHash = &blockHash
	observation.Confirmations = head - blockNumber + 1
	observation.set("block_number", new(big.Int).SetUint64(blockNumber).String())
	observation.set("block_hash", blockHash.Hex())

	switch *receipt.Status {
	case 1:
		if intent.Kind == KindERC20 {
			if logErr := matchingTransferLog(receipt, intent); logErr != nil {
				// A successful status is not enough: without the token's own Transfer
				// log for this exact amount the execution cannot be called successful.
				observation.IntentMatched = false
				observation.Execution = ExecutionUnknown
				observation.Reason = ReasonMissingTransferLog
			} else {
				observation.Execution = ExecutionSuccess
			}
		} else {
			observation.Execution = ExecutionSuccess
		}
	case 0:
		observation.Execution = ExecutionReverted
		observation.Reason = ReasonExecutionReverted
	}

	// Tail check: the observed block must still be canonical at that height and the
	// head must still be at or beyond it; otherwise the answer was taken during a
	// reorg and the depth is reported from the fresher read only.
	finalHead, err := o.reader.HeadNumber(ctx)
	if err != nil {
		return invalidateObservation(observation, ReasonRPCError, "tail_head"), nil
	}
	finalBlock, err := o.reader.BlockByNumber(ctx, blockNumber)
	if err != nil {
		return invalidateObservation(observation, ReasonRPCError, "tail_block"), nil
	}
	if finalBlock == nil || finalBlock.Number != blockNumber || finalBlock.Hash != blockHash ||
		*receipt.TxIndex >= uint64(len(finalBlock.TxHashes)) || finalBlock.TxHashes[*receipt.TxIndex] != intent.Hash {
		return invalidateObservation(observation, ReasonReorgDuringObserve, ""), nil
	}
	if finalHead < blockNumber {
		return invalidateObservation(observation, ReasonReorgDuringObserve, ""), nil
	}
	observation.Confirmations = finalHead - blockNumber + 1
	observation.set("confirmations", new(big.Int).SetUint64(observation.Confirmations).String())
	return observation, nil
}

// invalidateObservation drops every terminal-looking fact from an observation that
// stopped being trustworthy.
func invalidateObservation(observation Observation, reason string, stage string) Observation {
	observation.Status = StatusUnknown
	observation.Canonical = false
	observation.Execution = ExecutionUnknown
	observation.Confirmations = 0
	observation.BlockNumber = nil
	observation.BlockHash = nil
	observation.Reason = reason
	if stage != "" {
		observation.set("rpc_stage", stage)
	}
	return observation
}

// matchIntent verifies the transaction against the persisted intent. It reads the
// calldata itself and never infers the amount from balance changes.
func matchIntent(transaction *types.Transaction, intent Intent) (bool, string) {
	to := transaction.To()
	if to == nil {
		return false, ReasonIntentNoTo
	}
	value := transaction.Value()
	if value == nil {
		value = new(big.Int)
	}
	switch intent.Kind {
	case KindNative:
		if *to != intent.To {
			return false, ReasonIntentToMismatch
		}
		if value.Cmp(intent.Amount) != 0 {
			return false, ReasonIntentAmountMismatch
		}
		if len(transaction.Data()) != 0 {
			return false, ReasonIntentUnexpectedData
		}
		return true, ""
	case KindERC20:
		if *to != intent.Token {
			return false, ReasonIntentTokenMismatch
		}
		if value.Sign() != 0 {
			return false, ReasonIntentValueOnERC20
		}
		data := transaction.Data()
		if len(data) != 68 || !bytes.Equal(data[:4], erc20TransferSelector) {
			// Only a standard, direct transfer(address,uint256) call is covered.
			return false, ReasonIntentUnsupportedCall
		}
		// The 32-byte word must be a left-padded address: non-zero high bytes are not
		// a valid ABI address and must never be truncated into one.
		if !bytes.Equal(data[4:16], make([]byte, 12)) {
			return false, ReasonIntentUnsupportedCall
		}
		recipient := common.BytesToAddress(data[16:36])
		if recipient != intent.To {
			return false, ReasonIntentToMismatch
		}
		amount := new(big.Int).SetBytes(data[36:68])
		if amount.Cmp(intent.Amount) != 0 {
			return false, ReasonIntentAmountMismatch
		}
		return true, ""
	}
	return false, ReasonUnsupportedCoverage
}

// matchingTransferLog requires the token contract itself to emit a non-removed
// Transfer log for this exact (from, to, amount) triple inside this receipt.
func matchingTransferLog(receipt *RawReceipt, intent Intent) error {
	for _, log := range receipt.Logs {
		if log.Removed || log.Address != intent.Token || len(log.Topics) != 3 || log.Topics[0] != erc20TransferTopic {
			continue
		}
		// Log attribution must agree with the receipt: a log from another transaction,
		// another block or another height can never justify this intent.
		if log.TxHash != nil && *log.TxHash != *receipt.TxHash {
			continue
		}
		if log.BlockHash != nil && *log.BlockHash != *receipt.BlockHash {
			continue
		}
		if log.BlockNumber != nil && *log.BlockNumber != *receipt.BlockNumber {
			continue
		}
		// Indexed address topics are also 32-byte words: reject non-zero high bytes
		// instead of truncating them into an address.
		fromWord := log.Topics[1].Bytes()
		toWord := log.Topics[2].Bytes()
		if !bytes.Equal(fromWord[:12], make([]byte, 12)) || !bytes.Equal(toWord[:12], make([]byte, 12)) {
			continue
		}
		if common.BytesToAddress(fromWord) != intent.From || common.BytesToAddress(toWord) != intent.To {
			continue
		}
		if len(log.Data) != 32 {
			continue
		}
		if new(big.Int).SetBytes(log.Data).Cmp(intent.Amount) != 0 {
			continue
		}
		return nil
	}
	return errors.New("no matching transfer log")
}
