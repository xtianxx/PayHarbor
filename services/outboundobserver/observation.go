package outboundobserver

import "github.com/ethereum/go-ethereum/common"

// Status is what could be observed about the broadcast's presence on the chain.
type Status string

const (
	// StatusUnknown means the observer could not establish a trustworthy answer
	// (RPC failure, reorg, inconsistent evidence, undecodable payload). Callers must
	// treat it as "no conclusion" and never as a terminal signal.
	StatusUnknown Status = "unknown"
	// StatusPending means the transaction is not in a canonical block yet.
	StatusPending Status = "pending"
	// StatusIncluded means the transaction was found in a canonical block during the
	// whole observation. Confirmations are observational only.
	StatusIncluded Status = "included"
)

// Execution is the receipt-level outcome, independent of intent matching.
type Execution string

const (
	ExecutionUnknown  Execution = "unknown"
	ExecutionSuccess  Execution = "success"
	ExecutionReverted Execution = "reverted"
)

// Reason codes. Every non-matched or non-included observation carries exactly one.
const (
	ReasonIntentInvalid         = "intent_invalid"
	ReasonUnsupportedCoverage   = "unsupported_coverage"
	ReasonRPCError              = "rpc_error"
	ReasonWrongNetwork          = "wrong_network"
	ReasonNotFound              = "transaction_not_found"
	ReasonUndecodableTx         = "transaction_undecodable"
	ReasonTxHashMismatch        = "transaction_hash_mismatch"
	ReasonChainIDUnprotected    = "transaction_chain_id_missing"
	ReasonChainIDMismatch       = "transaction_chain_id_mismatch"
	ReasonSenderMismatch        = "sender_mismatch"
	ReasonIntentNoTo            = "intent_contract_creation"
	ReasonIntentTokenMismatch   = "intent_token_mismatch"
	ReasonIntentToMismatch      = "intent_destination_mismatch"
	ReasonIntentAmountMismatch  = "intent_amount_mismatch"
	ReasonIntentUnexpectedData  = "intent_unexpected_calldata"
	ReasonIntentValueOnERC20    = "intent_value_with_erc20"
	ReasonIntentUnsupportedCall = "intent_calldata_not_standard_transfer"
	ReasonReceiptPending        = "receipt_pending"
	ReasonReceiptInconsistent   = "receipt_inconsistent"
	ReasonReceiptStatusUnknown  = "receipt_status_unknown"
	ReasonBlockMissing          = "block_missing"
	ReasonReceiptBlockOrphaned  = "receipt_block_not_canonical"
	ReasonBlockMissingTx        = "block_does_not_contain_transaction"
	ReasonIndexMismatch         = "transaction_index_mismatch"
	ReasonHeadBehindBlock       = "head_behind_transaction_block"
	ReasonReorgDuringObserve    = "reorg_during_observation"
	ReasonExecutionReverted     = "execution_reverted"
	ReasonMissingTransferLog    = "missing_matching_transfer_log"
)

// Observation is a bounded, read-only statement about one intent.
type Observation struct {
	Status        Status
	Execution     Execution
	IntentMatched bool
	Canonical     bool
	BlockNumber   *uint64
	BlockHash     *common.Hash
	Confirmations uint64
	Reason        string
	// Evidence is bounded metadata only (counts, ids, small hex prefixes); it never
	// contains full payloads or credentials.
	Evidence map[string]string
}

func newObservation() Observation {
	return Observation{Status: StatusUnknown, Execution: ExecutionUnknown, Evidence: map[string]string{}}
}

func (o *Observation) set(key, value string) {
	if o.Evidence == nil {
		o.Evidence = map[string]string{}
	}
	if value == "" {
		return
	}
	o.Evidence[key] = value
}
