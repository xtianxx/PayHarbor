package outboundobserver

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"core/constants"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// A11: read-only EVM outbound observation, verified over a real HTTP JSON-RPC
// fixture with synthetic signed transactions and real decoding. Everything is local.

type rpcFixture struct {
	mu           sync.Mutex
	results      map[string]json.RawMessage
	errors       map[string]string
	delay        time.Duration
	status       int
	requestCount int
	muBlock      sync.Mutex
	// sequences let a test return different results for repeated calls (head re-read).
	sequences      map[string][]json.RawMessage
	sequenceCursor map[string]int
	// canonicalBlocks/blockReads let a test change the block hash between reads
	// (reorg): the replacement applies only after blockOverrideAfter reads.
	canonicalBlocks    map[uint64]json.RawMessage
	blockOverrideAfter map[uint64]int
	blockReads         map[uint64]int
	server             *httptest.Server
}

func newRPCFixture(t *testing.T) *rpcFixture {
	t.Helper()
	fixture := &rpcFixture{
		results:            map[string]json.RawMessage{},
		errors:             map[string]string{},
		sequences:          map[string][]json.RawMessage{},
		sequenceCursor:     map[string]int{},
		canonicalBlocks:    map[uint64]json.RawMessage{},
		blockOverrideAfter: map[uint64]int{},
		blockReads:         map[uint64]int{},
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		fixture.mu.Lock()
		fixture.requestCount++
		fixture.mu.Unlock()
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if fixture.delay > 0 {
			time.Sleep(fixture.delay)
		}
		if fixture.status != 0 {
			w.WriteHeader(fixture.status)
			return
		}
		key := request.Method
		if len(request.Params) > 0 {
			key += ":" + strings.Trim(string(request.Params[0]), `"`)
		}
		fixture.mu.Lock()
		blockOverride := json.RawMessage(nil)
		if request.Method == "eth_getBlockByNumber" && len(request.Params) > 0 {
			var tag string
			if err := json.Unmarshal(request.Params[0], &tag); err == nil {
				if number, err := hexutil.DecodeUint64(tag); err == nil {
					fixture.blockReads[number]++
					if fixture.blockReads[number] > fixture.blockOverrideAfter[number] {
						blockOverride = fixture.canonicalBlocks[number]
					}
				}
			}
		}
		result, hasResult := fixture.results[key]
		if queued, ok := fixture.sequences[key]; ok && len(queued) > 0 {
			cursor := fixture.sequenceCursor[key]
			if cursor >= len(queued) {
				cursor = len(queued) - 1
			}
			result = queued[cursor]
			hasResult = true
			fixture.sequenceCursor[key] = cursor + 1
		}
		rpcError := fixture.errors[key]
		fixture.mu.Unlock()

		if blockOverride != nil {
			result, hasResult = blockOverride, true
		}
		if rpcError != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32000, "message": rpcError}})
			return
		}
		if !hasResult {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":`))
		_, _ = w.Write(result)
		_, _ = w.Write([]byte(`}`))
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *rpcFixture) set(method, arg string, result any) {
	encoded, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := method
	if arg != "" {
		key += ":" + arg
	}
	f.results[key] = encoded
}

func (f *rpcFixture) setBlock(number uint64, result map[string]any) {
	encoded, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results["eth_getBlockByNumber:"+hexutil.EncodeUint64(number)] = encoded
}

func (f *rpcFixture) overrideBlock(number uint64, result map[string]any, afterReads int) {
	encoded, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canonicalBlocks[number] = encoded
	f.blockOverrideAfter[number] = afterReads
}

func (f *rpcFixture) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requestCount
}

func (f *rpcFixture) sequence(method string, results ...any) {
	encoded := make([]json.RawMessage, 0, len(results))
	for _, result := range results {
		raw, err := json.Marshal(result)
		if err != nil {
			panic(err)
		}
		encoded = append(encoded, raw)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sequences[method] = encoded
	f.sequenceCursor[method] = 0
}

func (f *rpcFixture) observer(t *testing.T) *Observer {
	t.Helper()
	reader, err := NewJSONRPCReader(f.server.URL, &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("build rpc reader: %v", err)
	}
	observer, err := NewObserver(reader)
	if err != nil {
		t.Fatalf("build observer: %v", err)
	}
	return observer
}

var (
	// Both must be networks the observer actually supports (the fixtures are still
	// fully synthetic: signing keys and localhost JSON-RPC only, no mainnet access).
	testChainID  = big.NewInt(int64(constants.Ethereum))
	otherChainID = big.NewInt(int64(constants.Base))
)

func testKey(t *testing.T, seed byte) *ecdsa.PrivateKey {
	t.Helper()
	raw := make([]byte, 32)
	raw[31] = seed
	raw[0] = 0x01
	key, err := crypto.ToECDSA(raw)
	if err != nil {
		t.Fatalf("synthetic key: %v", err)
	}
	return key
}

func signTx(t *testing.T, key *ecdsa.PrivateKey, chainID *big.Int, to *common.Address, value *big.Int, data []byte, nonce uint64) *types.Transaction {
	t.Helper()
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(2),
		Gas:       90000,
		To:        to,
		Value:     value,
		Data:      data,
	})
	signer := types.LatestSignerForChainID(chainID)
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		t.Fatalf("sign synthetic transaction: %v", err)
	}
	return signed
}

func erc20TransferData(recipient common.Address, amount *big.Int) []byte {
	data := make([]byte, 0, 68)
	data = append(data, erc20TransferSelector...)
	data = append(data, common.LeftPadBytes(recipient.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
	return data
}

// transactionResult builds the standard `eth_getTransactionByHash` result object:
// the signed transaction fields exactly as go-ethereum marshals them (v/r/s and the
// payload, no vendor "raw" field) plus the documented block metadata.
func transactionResult(t *testing.T, tx *types.Transaction, blockNumber *uint64, blockHash *common.Hash, index uint64) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("marshal synthetic transaction: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatalf("decode marshalled transaction: %v", err)
	}
	result["hash"] = tx.Hash()
	if blockNumber != nil {
		result["blockNumber"] = hexutil.EncodeUint64(*blockNumber)
		result["blockHash"] = blockHash
		result["transactionIndex"] = hexutil.EncodeUint64(index)
	}
	return result
}

func erc20TransferLog(token common.Address, from, to common.Address, amount *big.Int) map[string]any {
	return map[string]any{
		"address": token,
		"topics":  []any{erc20TransferTopic, common.BytesToHash(common.LeftPadBytes(from.Bytes(), 32)), common.BytesToHash(common.LeftPadBytes(to.Bytes(), 32))},
		"data":    hexutil.Encode(common.LeftPadBytes(amount.Bytes(), 32)),
	}
}

// attributedTransferLog adds the real receipt attribution a standard node reports.
func attributedTransferLog(token common.Address, from, to common.Address, amount *big.Int, txHash, blockHash common.Hash, blockNumber, logIndex uint64, removed bool) map[string]any {
	log := erc20TransferLog(token, from, to, amount)
	log["transactionHash"] = txHash
	log["blockHash"] = blockHash
	log["blockNumber"] = hexutil.EncodeUint64(blockNumber)
	log["logIndex"] = hexutil.EncodeUint64(logIndex)
	log["removed"] = removed
	return log
}

// setupCommon wires chain id, head and the canonical block for one included
// transaction and returns the block number/hash used.
func setupCommon(t *testing.T, fixture *rpcFixture, txHash common.Hash, blockNumber uint64, logs []map[string]any) (common.Hash, map[string]any) {
	t.Helper()
	fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
	fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(blockNumber+3))
	blockHash := crypto.Keccak256Hash([]byte("block"), new(big.Int).SetUint64(blockNumber).Bytes())
	block := map[string]any{"hash": blockHash, "number": hexutil.EncodeUint64(blockNumber), "transactions": []any{txHash}}
	fixture.setBlock(blockNumber, block)
	receipt := map[string]any{
		"transactionHash":  txHash,
		"blockHash":        blockHash,
		"blockNumber":      hexutil.EncodeUint64(blockNumber),
		"transactionIndex": "0x0",
		"status":           "0x1",
		"logs":             logs,
	}
	fixture.set("eth_getTransactionReceipt:"+txHash.Hex(), "", receipt)
	return blockHash, block
}

func TestObserveNativeIncludedSuccess(t *testing.T) {
	fixture := newRPCFixture(t)
	key := testKey(t, 1)
	to := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	amount := big.NewInt(12345)
	tx := signTx(t, key, testChainID, &to, amount, nil, 7)
	intent := Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: crypto.PubkeyToAddress(key.PublicKey), To: to, Amount: amount}
	blockHash, _ := setupCommon(t, fixture, tx.Hash(), 100, nil)
	fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(100), &blockHash, 0))

	observation, err := fixture.observer(t).Observe(context.Background(), intent)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if observation.Status != StatusIncluded || observation.Execution != ExecutionSuccess || !observation.IntentMatched || !observation.Canonical {
		t.Fatalf("observation = %#v, want included/success/matched/canonical", observation)
	}
	if observation.Confirmations != 4 || observation.BlockNumber == nil || *observation.BlockNumber != 100 {
		t.Fatalf("confirmations/block = %d/%v, want 4/100", observation.Confirmations, observation.BlockNumber)
	}
	if observation.BlockHash == nil || *observation.BlockHash != blockHash {
		t.Fatalf("block hash = %v, want %v", observation.BlockHash, blockHash)
	}
}

func TestObserveERC20IncludedSuccess(t *testing.T) {
	fixture := newRPCFixture(t)
	key := testKey(t, 2)
	from := crypto.PubkeyToAddress(key.PublicKey)
	token := common.HexToAddress("0x00000000000000000000000000000000000000bb")
	to := common.HexToAddress("0x00000000000000000000000000000000000000cc")
	amount := big.NewInt(5000)
	tx := signTx(t, key, testChainID, &token, big.NewInt(0), erc20TransferData(to, amount), 3)
	intent := Intent{Kind: KindERC20, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Token: token, Amount: amount}
	blockHash, _ := setupCommon(t, fixture, tx.Hash(), 200, []map[string]any{erc20TransferLog(token, from, to, amount)})
	fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(200), &blockHash, 0))

	observation, err := fixture.observer(t).Observe(context.Background(), intent)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if observation.Status != StatusIncluded || observation.Execution != ExecutionSuccess || !observation.IntentMatched {
		t.Fatalf("observation = %#v, want included ERC20 success with a matching transfer log", observation)
	}
}

func TestObserveERC20SuccessRequiresMatchingTransferLog(t *testing.T) {
	fixture := newRPCFixture(t)
	key := testKey(t, 3)
	from := crypto.PubkeyToAddress(key.PublicKey)
	token := common.HexToAddress("0x00000000000000000000000000000000000000dd")
	to := common.HexToAddress("0x00000000000000000000000000000000000000ee")
	amount := big.NewInt(700)
	tx := signTx(t, key, testChainID, &token, big.NewInt(0), erc20TransferData(to, amount), 4)
	intent := Intent{Kind: KindERC20, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Token: token, Amount: amount}
	// The receipt is successful but pays a different amount in the Transfer log.
	blockHash, _ := setupCommon(t, fixture, tx.Hash(), 300, []map[string]any{erc20TransferLog(token, from, to, big.NewInt(701))})
	fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(300), &blockHash, 0))

	observation, err := fixture.observer(t).Observe(context.Background(), intent)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if observation.IntentMatched || observation.Reason != ReasonMissingTransferLog {
		t.Fatalf("observation = %#v, want the missing transfer log refusal", observation)
	}
}

func TestObserveERC20RevertedIncluded(t *testing.T) {
	fixture := newRPCFixture(t)
	key := testKey(t, 4)
	from := crypto.PubkeyToAddress(key.PublicKey)
	token := common.HexToAddress("0x00000000000000000000000000000000000000ff")
	to := common.HexToAddress("0x0000000000000000000000000000000000000011")
	amount := big.NewInt(900)
	tx := signTx(t, key, testChainID, &token, big.NewInt(0), erc20TransferData(to, amount), 5)
	intent := Intent{Kind: KindERC20, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Token: token, Amount: amount}
	blockHash, _ := setupCommon(t, fixture, tx.Hash(), 400, nil)
	fixture.mu.Lock()
	fixture.results["eth_getTransactionReceipt:"+tx.Hash().Hex()] = mustJSON(t, map[string]any{
		"transactionHash": tx.Hash(), "blockHash": blockHash, "blockNumber": "0x190", "transactionIndex": "0x0", "status": "0x0", "logs": []any{},
	})
	fixture.mu.Unlock()
	fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(400), &blockHash, 0))

	observation, err := fixture.observer(t).Observe(context.Background(), intent)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if observation.Status != StatusIncluded || observation.Execution != ExecutionReverted || !observation.IntentMatched {
		t.Fatalf("observation = %#v, want included reverted with the intent still matched", observation)
	}
	if observation.Reason != ReasonExecutionReverted {
		t.Fatalf("reason = %q, want %q", observation.Reason, ReasonExecutionReverted)
	}
}

func TestObservePendingAndUnknownReceiptStates(t *testing.T) {
	key := testKey(t, 5)
	to := common.HexToAddress("0x0000000000000000000000000000000000000022")
	amount := big.NewInt(50)
	tx := signTx(t, key, testChainID, &to, amount, nil, 6)
	intent := Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: crypto.PubkeyToAddress(key.PublicKey), To: to, Amount: amount}

	t.Run("receipt null is pending", func(t *testing.T) {
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(10))
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, nil, nil, 0))
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusPending || observation.Reason != ReasonReceiptPending {
			t.Fatalf("observation = %#v, want pending", observation)
		}
	})

	t.Run("missing transaction is not found", func(t *testing.T) {
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(10))
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusUnknown || observation.Reason != ReasonNotFound {
			t.Fatalf("observation = %#v, want unknown not_found", observation)
		}
	})

	t.Run("receipt without block fields is pending", func(t *testing.T) {
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(10))
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, nil, nil, 0))
		fixture.set("eth_getTransactionReceipt:"+tx.Hash().Hex(), "", map[string]any{"transactionHash": tx.Hash(), "status": "0x1", "logs": []any{}})
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusPending || observation.Reason != ReasonReceiptPending {
			t.Fatalf("observation = %#v, want pending for a receipt without a block", observation)
		}
	})

	t.Run("receipt without status is unknown", func(t *testing.T) {
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 500, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(500), &blockHash, 0))
		fixture.mu.Lock()
		fixture.results["eth_getTransactionReceipt:"+tx.Hash().Hex()] = mustJSON(t, map[string]any{
			"transactionHash": tx.Hash(), "blockHash": blockHash, "blockNumber": "0x1f4", "transactionIndex": "0x0", "logs": []any{},
		})
		fixture.mu.Unlock()
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusUnknown || observation.Reason != ReasonReceiptStatusUnknown {
			t.Fatalf("observation = %#v, want a status-unknown refusal", observation)
		}
	})
}

func TestObserveRejectsMismatchedIntents(t *testing.T) {
	key := testKey(t, 6)
	from := crypto.PubkeyToAddress(key.PublicKey)
	otherKey := testKey(t, 7)
	to := common.HexToAddress("0x0000000000000000000000000000000000000033")
	otherTo := common.HexToAddress("0x0000000000000000000000000000000000000044")
	token := common.HexToAddress("0x0000000000000000000000000000000000000055")
	amount := big.NewInt(1000)

	nativeTx := signTx(t, key, testChainID, &to, amount, nil, 8)
	erc20Tx := signTx(t, key, testChainID, &token, big.NewInt(0), erc20TransferData(to, amount), 9)

	cases := []struct {
		name   string
		intent Intent
		tx     *types.Transaction
		reason string
	}{
		{name: "wrong sender", intent: Intent{Kind: KindNative, ChainID: testChainID, Hash: nativeTx.Hash(), From: crypto.PubkeyToAddress(otherKey.PublicKey), To: to, Amount: amount}, tx: nativeTx, reason: ReasonSenderMismatch},
		{name: "wrong destination", intent: Intent{Kind: KindNative, ChainID: testChainID, Hash: nativeTx.Hash(), From: from, To: otherTo, Amount: amount}, tx: nativeTx, reason: ReasonIntentToMismatch},
		{name: "wrong amount", intent: Intent{Kind: KindNative, ChainID: testChainID, Hash: nativeTx.Hash(), From: from, To: to, Amount: big.NewInt(1001)}, tx: nativeTx, reason: ReasonIntentAmountMismatch},
		{name: "eras wrong token", intent: Intent{Kind: KindERC20, ChainID: testChainID, Hash: erc20Tx.Hash(), From: from, To: to, Token: otherTo, Amount: amount}, tx: erc20Tx, reason: ReasonIntentTokenMismatch},
		{name: "erc20 recipient mismatch", intent: Intent{Kind: KindERC20, ChainID: testChainID, Hash: erc20Tx.Hash(), From: from, To: otherTo, Token: token, Amount: amount}, tx: erc20Tx, reason: ReasonIntentToMismatch},
		{name: "erc20 amount mismatch", intent: Intent{Kind: KindERC20, ChainID: testChainID, Hash: erc20Tx.Hash(), From: from, To: to, Token: token, Amount: big.NewInt(999)}, tx: erc20Tx, reason: ReasonIntentAmountMismatch},
		{name: "intent chain id mismatch", intent: Intent{Kind: KindNative, ChainID: otherChainID, Hash: nativeTx.Hash(), From: from, To: to, Amount: amount}, tx: nativeTx, reason: ReasonWrongNetwork},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newRPCFixture(t)
			fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
			fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(20))
			fixture.set("eth_getTransactionByHash:"+testCase.tx.Hash().Hex(), "", transactionResult(t, testCase.tx, nil, nil, 0))
			observation, err := fixture.observer(t).Observe(context.Background(), testCase.intent)
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			if observation.Reason != testCase.reason {
				t.Fatalf("reason = %q, want %q (%#v)", observation.Reason, testCase.reason, observation)
			}
			if observation.Status == StatusIncluded && observation.Execution == ExecutionSuccess && observation.IntentMatched {
				t.Fatalf("mismatched intent must never be reported as a matched success: %#v", observation)
			}
		})
	}

	t.Run("non standard ERC20 calldata is not covered", func(t *testing.T) {
		custom := signTx(t, key, testChainID, &token, big.NewInt(0), []byte{0xde, 0xad, 0xbe, 0xef}, 10)
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(20))
		fixture.set("eth_getTransactionByHash:"+custom.Hash().Hex(), "", transactionResult(t, custom, nil, nil, 0))
		intent := Intent{Kind: KindERC20, ChainID: testChainID, Hash: custom.Hash(), From: from, To: to, Token: token, Amount: amount}
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Reason != ReasonIntentUnsupportedCall || observation.IntentMatched {
			t.Fatalf("observation = %#v, want the non-standard calldata refusal", observation)
		}
	})

	t.Run("native intent with calldata is not covered", func(t *testing.T) {
		withData := signTx(t, key, testChainID, &to, amount, []byte{0x01}, 11)
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(20))
		fixture.set("eth_getTransactionByHash:"+withData.Hash().Hex(), "", transactionResult(t, withData, nil, nil, 0))
		intent := Intent{Kind: KindNative, ChainID: testChainID, Hash: withData.Hash(), From: from, To: to, Amount: amount}
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Reason != ReasonIntentUnexpectedData {
			t.Fatalf("observation = %#v, want the unexpected-calldata refusal", observation)
		}
	})
}

func TestObserveReorgAndInconsistentEvidence(t *testing.T) {
	key := testKey(t, 8)
	to := common.HexToAddress("0x0000000000000000000000000000000000000066")
	amount := big.NewInt(4242)
	tx := signTx(t, key, testChainID, &to, amount, nil, 12)
	intent := Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: crypto.PubkeyToAddress(key.PublicKey), To: to, Amount: amount}

	t.Run("receipt block is not canonical", func(t *testing.T) {
		fixture := newRPCFixture(t)
		setupCommon(t, fixture, tx.Hash(), 600, nil)
		fixture.mu.Lock()
		fixture.results["eth_getTransactionReceipt:"+tx.Hash().Hex()] = mustJSON(t, map[string]any{
			"transactionHash": tx.Hash(), "blockHash": crypto.Keccak256Hash([]byte("orphaned")), "blockNumber": "0x258", "transactionIndex": "0x0", "status": "0x1", "logs": []any{},
		})
		fixture.mu.Unlock()
		// The transaction metadata agrees with the receipt, so the refusal below is
		// about the canonical block at that height, not about missing metadata.
		orphanedReceiptHash := crypto.Keccak256Hash([]byte("orphaned"))
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(600), &orphanedReceiptHash, 0))
		observation, _ := fixture.observer(t).Observe(context.Background(), intent)
		if observation.Reason != ReasonReceiptBlockOrphaned || observation.Status == StatusIncluded {
			t.Fatalf("observation = %#v, want a non-canonical refusal", observation)
		}
	})

	t.Run("block does not contain the transaction", func(t *testing.T) {
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 610, nil)
		fixture.setBlock(610, map[string]any{"hash": blockHash, "number": hexutil.EncodeUint64(610), "transactions": []any{crypto.Keccak256Hash([]byte("nope"))}})
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(610), &blockHash, 0))
		observation, _ := fixture.observer(t).Observe(context.Background(), intent)
		// The receipt index must point at this exact transaction inside the canonical
		// block, which is stricter than "the block contains the hash somewhere".
		if observation.Reason != ReasonIndexMismatch {
			t.Fatalf("observation = %#v, want the index-mismatch refusal", observation)
		}
	})

	t.Run("head behind the transaction block", func(t *testing.T) {
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 620, nil)
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(619))
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(620), &blockHash, 0))
		observation, _ := fixture.observer(t).Observe(context.Background(), intent)
		if observation.Reason != ReasonHeadBehindBlock {
			t.Fatalf("observation = %#v, want the head-behind refusal", observation)
		}
	})

	t.Run("reorg during observation", func(t *testing.T) {
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 630, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(630), &blockHash, 0))
		// The final read of the same height returns a different canonical hash.
		fixture.sequence("eth_blockNumber", hexutil.EncodeUint64(633), hexutil.EncodeUint64(629))
		observation, _ := fixture.observer(t).Observe(context.Background(), intent)
		if observation.Status != StatusUnknown || observation.Reason != ReasonReorgDuringObserve || observation.Canonical || observation.Confirmations != 0 {
			t.Fatalf("observation = %#v, want an unknown reorg-during-observation result", observation)
		}
	})

	t.Run("receipt transaction hash mismatch", func(t *testing.T) {
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 640, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(640), &blockHash, 0))
		fixture.mu.Lock()
		fixture.results["eth_getTransactionReceipt:"+tx.Hash().Hex()] = mustJSON(t, map[string]any{
			"transactionHash": crypto.Keccak256Hash([]byte("another")), "blockHash": blockHash, "blockNumber": "0x280", "status": "0x1", "logs": []any{},
		})
		fixture.mu.Unlock()
		observation, _ := fixture.observer(t).Observe(context.Background(), intent)
		if observation.Reason != ReasonReceiptInconsistent {
			t.Fatalf("observation = %#v, want the receipt inconsistency refusal", observation)
		}
	})
}

func TestObserveRPCFailuresAreUnknown(t *testing.T) {
	key := testKey(t, 9)
	to := common.HexToAddress("0x0000000000000000000000000000000000000077")
	amount := big.NewInt(11)
	tx := signTx(t, key, testChainID, &to, amount, nil, 13)
	intent := Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: crypto.PubkeyToAddress(key.PublicKey), To: to, Amount: amount}

	run := func(t *testing.T, fixture *rpcFixture) Observation {
		t.Helper()
		observation, err := fixture.observer(t).Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		return observation
	}

	t.Run("http error status", func(t *testing.T) {
		fixture := newRPCFixture(t)
		fixture.status = http.StatusInternalServerError
		observation := run(t, fixture)
		if observation.Status != StatusUnknown || observation.Reason != ReasonRPCError {
			t.Fatalf("observation = %#v, want unknown rpc_error", observation)
		}
	})
	t.Run("jsonrpc error object", func(t *testing.T) {
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(9))
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, nil, nil, 0))
		fixture.errors["eth_getTransactionReceipt:"+tx.Hash().Hex()] = "receipt unavailable"
		observation := run(t, fixture)
		if observation.Status != StatusUnknown || observation.Reason != ReasonRPCError {
			t.Fatalf("observation = %#v, want unknown rpc_error", observation)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		fixture := newRPCFixture(t)
		fixture.delay = 150 * time.Millisecond
		reader, err := NewJSONRPCReader(fixture.server.URL, &http.Client{Timeout: 20 * time.Millisecond})
		if err != nil {
			t.Fatalf("build reader: %v", err)
		}
		observer, _ := NewObserver(reader)
		observation, err := observer.Observe(context.Background(), intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusUnknown || observation.Reason != ReasonRPCError {
			t.Fatalf("observation = %#v, want unknown rpc_error on timeout", observation)
		}
	})
	t.Run("context cancellation", func(t *testing.T) {
		fixture := newRPCFixture(t)
		fixture.delay = 150 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		observation, err := fixture.observer(t).Observe(ctx, intent)
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusUnknown || observation.Reason != ReasonRPCError {
			t.Fatalf("observation = %#v, want unknown rpc_error on cancellation", observation)
		}
	})
	t.Run("oversized response", func(t *testing.T) {
		fixture := newRPCFixture(t)
		fixture.mu.Lock()
		fixture.results["eth_chainId"] = json.RawMessage(`"` + strings.Repeat("a", int(defaultMaxResponseBytes)+16) + `"`)
		fixture.mu.Unlock()
		observation := run(t, fixture)
		if observation.Status != StatusUnknown || observation.Reason != ReasonRPCError {
			t.Fatalf("observation = %#v, want unknown rpc_error for an oversized response", observation)
		}
	})
}

func TestObserveUnsupportedFamiliesNeverSucceed(t *testing.T) {
	key := testKey(t, 10)
	to := common.HexToAddress("0x0000000000000000000000000000000000000088")
	amount := big.NewInt(5)
	tx := signTx(t, key, testChainID, &to, amount, nil, 14)
	from := crypto.PubkeyToAddress(key.PublicKey)

	kinds := []Kind{"", "evm_sweep_max", "btc", "solana", "tron", "evm_proxy_delegatecall", "cross_chain"}
	for _, kind := range kinds {
		t.Run("kind "+string(kind), func(t *testing.T) {
			fixture := newRPCFixture(t)
			fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
			fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(30))
			fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, nil, nil, 0))
			intent := Intent{Kind: kind, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Amount: amount}
			observation, err := fixture.observer(t).Observe(context.Background(), intent)
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			if observation.Reason != ReasonUnsupportedCoverage || observation.Status != StatusUnknown || observation.IntentMatched {
				t.Fatalf("observation = %#v, want an unsupported/unknown result", observation)
			}
		})
	}

	t.Run("invalid intents are refused before any rpc call", func(t *testing.T) {
		fixture := newRPCFixture(t)
		fixture.status = http.StatusInternalServerError // would fail any RPC call
		observer := fixture.observer(t)
		for name, intent := range map[string]Intent{
			"zero hash":    {Kind: KindNative, ChainID: testChainID, From: from, To: to, Amount: amount},
			"zero from":    {Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), To: to, Amount: amount},
			"zero amount":  {Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to},
			"erc20 no tok": {Kind: KindERC20, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Amount: amount},
		} {
			observation, err := observer.Observe(context.Background(), intent)
			if err != nil {
				t.Fatalf("%s: observe: %v", name, err)
			}
			if observation.Reason != ReasonIntentInvalid {
				t.Fatalf("%s: observation = %#v, want intent_invalid", name, observation)
			}
		}
	})
}

func TestObserverConstructionRequiresReader(t *testing.T) {
	if _, err := NewObserver(nil); err == nil {
		t.Fatal("NewObserver(nil) must fail")
	}
	if _, err := NewJSONRPCReader("   ", nil); err == nil {
		t.Fatal("NewJSONRPCReader with an empty endpoint must fail")
	}
}

// u64 keeps the tests on the module's Go 1.25 directive (new(expr) needs 1.26).
func u64(value uint64) *uint64 { return &value }

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture value: %v", err)
	}
	return encoded
}

var _ = errors.Is
