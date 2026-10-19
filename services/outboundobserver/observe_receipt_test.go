package outboundobserver

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// A11-R1: receipt index/height/hash consistency, tail reorg handling and ERC20 log
// attribution strictness.

func TestObserveReceiptIndexAndMetadataConsistency(t *testing.T) {
	key := testKey(t, 41)
	from := crypto.PubkeyToAddress(key.PublicKey)
	to := common.HexToAddress("0x0000000000000000000000000000000000000001")
	amount := big.NewInt(15)
	tx := signTx(t, key, testChainID, &to, amount, nil, 9)
	intent := Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Amount: amount}

	observe := func(t *testing.T, mutate func(fixture *rpcFixture, blockHash common.Hash, block map[string]any)) Observation {
		t.Helper()
		fixture := newRPCFixture(t)
		blockHash, block := setupCommon(t, fixture, tx.Hash(), 800, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(800), &blockHash, 0))
		mutate(fixture, blockHash, block)
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		return observation
	}

	t.Run("receipt without index", func(t *testing.T) {
		observation := observe(t, func(fixture *rpcFixture, blockHash common.Hash, _ map[string]any) {
			fixture.mu.Lock()
			fixture.results["eth_getTransactionReceipt:"+tx.Hash().Hex()] = mustJSON(t, map[string]any{
				"transactionHash": tx.Hash(), "blockHash": blockHash, "blockNumber": "0x320", "status": "0x1", "logs": []any{},
			})
			fixture.mu.Unlock()
		})
		if observation.Reason != ReasonReceiptInconsistent {
			t.Fatalf("observation = %#v, want a receipt-inconsistency refusal", observation)
		}
	})

	t.Run("receipt index beyond the block", func(t *testing.T) {
		observation := observe(t, func(fixture *rpcFixture, blockHash common.Hash, _ map[string]any) {
			// The transaction metadata agrees with the receipt (index 5); the block only
			// holds one transaction, so the index cannot address this transaction.
			fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(800), &blockHash, 5))
			fixture.mu.Lock()
			fixture.results["eth_getTransactionReceipt:"+tx.Hash().Hex()] = mustJSON(t, map[string]any{
				"transactionHash": tx.Hash(), "blockHash": blockHash, "blockNumber": "0x320", "transactionIndex": "0x5", "status": "0x1", "logs": []any{},
			})
			fixture.mu.Unlock()
		})
		if observation.Reason != ReasonIndexMismatch {
			t.Fatalf("observation = %#v, want an index-mismatch refusal", observation)
		}
	})

	t.Run("transaction metadata conflicts with the receipt", func(t *testing.T) {
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 810, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(999), &blockHash, 0))
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Reason != ReasonReceiptInconsistent {
			t.Fatalf("observation = %#v, want a metadata-conflict refusal", observation)
		}
	})

	t.Run("transaction metadata index conflicts with the receipt", func(t *testing.T) {
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 820, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(820), &blockHash, 3))
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Reason != ReasonReceiptInconsistent {
			t.Fatalf("observation = %#v, want a metadata-index refusal", observation)
		}
	})

	t.Run("pending transaction metadata with an included receipt", func(t *testing.T) {
		fixture := newRPCFixture(t)
		setupCommon(t, fixture, tx.Hash(), 830, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, nil, nil, 0))
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusUnknown || observation.Reason != ReasonReceiptInconsistent {
			t.Fatalf("observation = %#v, want an unknown for incomplete transaction metadata", observation)
		}
		if observation.Evidence["tx_metadata"] != "incomplete" {
			t.Fatalf("evidence = %#v, want tx_metadata=incomplete", observation.Evidence)
		}
	})

	t.Run("block number mismatch fails closed", func(t *testing.T) {
		observation := observe(t, func(fixture *rpcFixture, blockHash common.Hash, _ map[string]any) {
			fixture.setBlock(800, map[string]any{"hash": blockHash, "number": hexutil.EncodeUint64(801), "transactions": []any{tx.Hash()}})
		})
		if observation.Status != StatusUnknown || observation.Reason != ReasonRPCError {
			t.Fatalf("observation = %#v, want a fail-closed unknown", observation)
		}
	})
}

func TestObserveReceiptStatusBoundaries(t *testing.T) {
	key := testKey(t, 42)
	to := common.HexToAddress("0x0000000000000000000000000000000000000002")
	amount := big.NewInt(21)
	tx := signTx(t, key, testChainID, &to, amount, nil, 10)
	intent := Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: crypto.PubkeyToAddress(key.PublicKey), To: to, Amount: amount}

	for _, status := range []string{"0x2", "0xff"} {
		t.Run("status "+status, func(t *testing.T) {
			fixture := newRPCFixture(t)
			blockHash, _ := setupCommon(t, fixture, tx.Hash(), 840, nil)
			fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(840), &blockHash, 0))
			fixture.mu.Lock()
			fixture.results["eth_getTransactionReceipt:"+tx.Hash().Hex()] = mustJSON(t, map[string]any{
				"transactionHash": tx.Hash(), "blockHash": blockHash, "blockNumber": "0x348", "transactionIndex": "0x0", "status": status, "logs": []any{},
			})
			fixture.mu.Unlock()
			observation, err := fixture.observer(t).Observe(context.Background(), intent)
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			if observation.Status != StatusUnknown || observation.Execution != ExecutionUnknown || observation.Reason != ReasonReceiptStatusUnknown {
				t.Fatalf("observation = %#v, want an unknown status refusal", observation)
			}
		})
	}
}

func TestObserveTailReorgAndHeadRegression(t *testing.T) {
	key := testKey(t, 43)
	to := common.HexToAddress("0x0000000000000000000000000000000000000003")
	amount := big.NewInt(33)
	tx := signTx(t, key, testChainID, &to, amount, nil, 11)
	intent := Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: crypto.PubkeyToAddress(key.PublicKey), To: to, Amount: amount}

	t.Run("tail block replacement", func(t *testing.T) {
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 850, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(850), &blockHash, 0))
		fixture.overrideBlock(850, map[string]any{"hash": crypto.Keccak256Hash([]byte("replaced")), "number": hexutil.EncodeUint64(850), "transactions": []any{tx.Hash()}}, 1)
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusUnknown || observation.Canonical || observation.Confirmations != 0 || observation.Reason != ReasonReorgDuringObserve {
			t.Fatalf("observation = %#v, want a reorg-during-observation unknown", observation)
		}
	})

	t.Run("head regression", func(t *testing.T) {
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 860, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(860), &blockHash, 0))
		fixture.sequence("eth_blockNumber", hexutil.EncodeUint64(863), hexutil.EncodeUint64(859))
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusUnknown || observation.Canonical || observation.Confirmations != 0 {
			t.Fatalf("observation = %#v, want an unknown reorg result", observation)
		}
	})
}

func TestObserveERC20LogAttributionStrictness(t *testing.T) {
	key := testKey(t, 44)
	from := crypto.PubkeyToAddress(key.PublicKey)
	token := common.HexToAddress("0x0000000000000000000000000000000000000004")
	to := common.HexToAddress("0x0000000000000000000000000000000000000005")
	amount := big.NewInt(606)
	tx := signTx(t, key, testChainID, &token, big.NewInt(0), erc20TransferData(to, amount), 12)
	intent := Intent{Kind: KindERC20, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Token: token, Amount: amount}

	run := func(t *testing.T, logs func(blockHash common.Hash) []map[string]any) Observation {
		t.Helper()
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 870, nil)
		fixture.mu.Lock()
		fixture.results["eth_getTransactionReceipt:"+tx.Hash().Hex()] = mustJSON(t, map[string]any{
			"transactionHash": tx.Hash(), "blockHash": blockHash, "blockNumber": "0x366", "transactionIndex": "0x0", "status": "0x1", "logs": logs(blockHash),
		})
		fixture.mu.Unlock()
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(870), &blockHash, 0))
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		return observation
	}

	t.Run("attributed transfer log is accepted", func(t *testing.T) {
		observation := run(t, func(blockHash common.Hash) []map[string]any {
			return []map[string]any{attributedTransferLog(token, from, to, amount, tx.Hash(), blockHash, 870, 0, false)}
		})
		if observation.Status != StatusIncluded || observation.Execution != ExecutionSuccess || !observation.IntentMatched {
			t.Fatalf("observation = %#v, want an accepted attributed log", observation)
		}
	})

	t.Run("log from another transaction is ignored", func(t *testing.T) {
		observation := run(t, func(blockHash common.Hash) []map[string]any {
			return []map[string]any{attributedTransferLog(token, from, to, amount, crypto.Keccak256Hash([]byte("other-tx")), blockHash, 870, 0, false)}
		})
		if observation.Execution != ExecutionUnknown || observation.Reason != ReasonMissingTransferLog || !observation.Canonical {
			t.Fatalf("observation = %#v, want unknown execution with the canonical facts kept", observation)
		}
	})

	t.Run("log from another block is ignored", func(t *testing.T) {
		observation := run(t, func(blockHash common.Hash) []map[string]any {
			return []map[string]any{attributedTransferLog(token, from, to, amount, tx.Hash(), crypto.Keccak256Hash([]byte("other-block")), 869, 0, false)}
		})
		if observation.Execution != ExecutionUnknown || observation.Reason != ReasonMissingTransferLog {
			t.Fatalf("observation = %#v, want the other-block log to be ignored", observation)
		}
	})

	t.Run("removed log is ignored", func(t *testing.T) {
		observation := run(t, func(blockHash common.Hash) []map[string]any {
			return []map[string]any{attributedTransferLog(token, from, to, amount, tx.Hash(), blockHash, 870, 0, true)}
		})
		if observation.Execution != ExecutionUnknown || observation.Reason != ReasonMissingTransferLog {
			t.Fatalf("observation = %#v, want the removed log to be ignored", observation)
		}
	})

	t.Run("log topic with non-zero high bytes is ignored", func(t *testing.T) {
		observation := run(t, func(blockHash common.Hash) []map[string]any {
			log := attributedTransferLog(token, from, to, amount, tx.Hash(), blockHash, 870, 0, false)
			topics := log["topics"].([]any)
			topics[1] = common.HexToHash("0xffffffffffffffffffffffff" + common.Bytes2Hex(common.LeftPadBytes(from.Bytes(), 20)))
			log["topics"] = topics
			return []map[string]any{log}
		})
		if observation.Execution != ExecutionUnknown || observation.Reason != ReasonMissingTransferLog {
			t.Fatalf("observation = %#v, want the malformed topic to be ignored", observation)
		}
	})

	t.Run("missing log never claims success", func(t *testing.T) {
		observation := run(t, func(common.Hash) []map[string]any { return []map[string]any{} })
		if observation.Execution != ExecutionUnknown || observation.IntentMatched || observation.Reason != ReasonMissingTransferLog {
			t.Fatalf("observation = %#v, want an unverifiable execution", observation)
		}
	})

	t.Run("calldata address word with non-zero high bytes is refused", func(t *testing.T) {
		malformed := make([]byte, 0, 68)
		malformed = append(malformed, erc20TransferSelector...)
		malformed = append(malformed, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}...)
		malformed = append(malformed, common.LeftPadBytes(to.Bytes(), 20)...)
		malformed = append(malformed, common.LeftPadBytes(amount.Bytes(), 32)...)
		base := signTx(t, key, testChainID, &token, big.NewInt(0), malformed, 13)
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(1000))
		fixture.set("eth_getTransactionByHash:"+base.Hash().Hex(), "", transactionResult(t, base, nil, nil, 0))
		observation, err := fixture.observer(t).Observe(context.Background(), Intent{Kind: KindERC20, ChainID: testChainID, Hash: base.Hash(), From: from, To: to, Token: token, Amount: amount})
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Reason != ReasonIntentUnsupportedCall || observation.IntentMatched {
			t.Fatalf("observation = %#v, want the malformed ABI word to be refused", observation)
		}
	})
}
