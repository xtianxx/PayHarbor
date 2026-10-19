package outboundobserver

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"core/constants"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// A11-R1: standard JSON-RPC wire compatibility (no vendor `raw` field), signature
// and hash verification, envelope integrity and non-EVM refusal.

func TestObserveStandardTransactionShapes(t *testing.T) {
	to := common.HexToAddress("0x0000000000000000000000000000000000000099")
	amount := big.NewInt(777)

	t.Run("legacy EIP-155", func(t *testing.T) {
		key := testKey(t, 31)
		from := crypto.PubkeyToAddress(key.PublicKey)
		tx := types.MustSignNewTx(key, types.LatestSignerForChainID(testChainID), &types.LegacyTx{
			Nonce: 1, GasPrice: big.NewInt(1), Gas: 21000, To: &to, Value: amount,
		})
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 700, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(700), &blockHash, 0))
		observation, err := fixture.observer(t).Observe(context.Background(), Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Amount: amount})
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusIncluded || observation.Execution != ExecutionSuccess || !observation.IntentMatched {
			t.Fatalf("observation = %#v, want an included legacy success", observation)
		}
	})

	t.Run("dynamic fee EIP-1559", func(t *testing.T) {
		key := testKey(t, 32)
		from := crypto.PubkeyToAddress(key.PublicKey)
		tx := signTx(t, key, testChainID, &to, amount, nil, 2)
		fixture := newRPCFixture(t)
		blockHash, _ := setupCommon(t, fixture, tx.Hash(), 701, nil)
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, u64(701), &blockHash, 0))
		observation, err := fixture.observer(t).Observe(context.Background(), Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Amount: amount})
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusIncluded || observation.Execution != ExecutionSuccess {
			t.Fatalf("observation = %#v, want an included 1559 success", observation)
		}
	})

	t.Run("unprotected legacy is refused", func(t *testing.T) {
		key := testKey(t, 33)
		from := crypto.PubkeyToAddress(key.PublicKey)
		tx := types.MustSignNewTx(key, types.HomesteadSigner{}, &types.LegacyTx{
			Nonce: 3, GasPrice: big.NewInt(1), Gas: 21000, To: &to, Value: amount,
		})
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(900))
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", transactionResult(t, tx, nil, nil, 0))
		observation, err := fixture.observer(t).Observe(context.Background(), Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Amount: amount})
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusUnknown || observation.Reason != ReasonChainIDUnprotected {
			t.Fatalf("observation = %#v, want the unprotected-transaction refusal", observation)
		}
	})

	t.Run("missing signature fields fail closed", func(t *testing.T) {
		key := testKey(t, 34)
		from := crypto.PubkeyToAddress(key.PublicKey)
		tx := signTx(t, key, testChainID, &to, amount, nil, 4)
		result := transactionResult(t, tx, nil, nil, 0)
		delete(result, "r")
		delete(result, "s")
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(900))
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", result)
		observation, err := fixture.observer(t).Observe(context.Background(), Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Amount: amount})
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Status != StatusUnknown || observation.Reason != ReasonRPCError {
			t.Fatalf("observation = %#v, want a fail-closed unknown", observation)
		}
	})

	t.Run("node from field is never trusted", func(t *testing.T) {
		key := testKey(t, 35)
		from := crypto.PubkeyToAddress(key.PublicKey)
		tx := signTx(t, key, testChainID, &to, amount, nil, 5)
		result := transactionResult(t, tx, nil, nil, 0)
		result["from"] = common.HexToAddress("0x00000000000000000000000000000000000000de")
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(900))
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", result)
		observation, err := fixture.observer(t).Observe(context.Background(), Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Amount: amount})
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Reason != ReasonReceiptPending && observation.Reason != "" {
			t.Fatalf("observation = %#v, want the recovered sender to be accepted", observation)
		}
		if evidence := observation.Evidence["sender"]; evidence != from.Hex() {
			t.Fatalf("sender evidence = %q, want the recovered %s", evidence, from.Hex())
		}
	})

	t.Run("returned hash mismatch is refused", func(t *testing.T) {
		key := testKey(t, 36)
		from := crypto.PubkeyToAddress(key.PublicKey)
		tx := signTx(t, key, testChainID, &to, amount, nil, 6)
		result := transactionResult(t, tx, nil, nil, 0)
		result["hash"] = crypto.Keccak256Hash([]byte("different"))
		fixture := newRPCFixture(t)
		fixture.set("eth_chainId", "", hexutil.EncodeBig(testChainID))
		fixture.set("eth_blockNumber", "", hexutil.EncodeUint64(900))
		fixture.set("eth_getTransactionByHash:"+tx.Hash().Hex(), "", result)
		observation, err := fixture.observer(t).Observe(context.Background(), Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: from, To: to, Amount: amount})
		if err != nil {
			t.Fatalf("observe: %v", err)
		}
		if observation.Reason != ReasonTxHashMismatch {
			t.Fatalf("observation = %#v, want the returned-hash refusal", observation)
		}
	})
}

func TestObserveEnvelopeIntegrityFailClosed(t *testing.T) {
	key := testKey(t, 37)
	to := common.HexToAddress("0x00000000000000000000000000000000000000ab")
	amount := big.NewInt(9)
	tx := signTx(t, key, testChainID, &to, amount, nil, 7)
	intent := Intent{Kind: KindNative, ChainID: testChainID, Hash: tx.Hash(), From: crypto.PubkeyToAddress(key.PublicKey), To: to, Amount: amount}

	envelopes := map[string]string{
		"wrong version":  `{"jsonrpc":"1.0","id":1,"result":"0x1"}`,
		"wrong id":       `{"jsonrpc":"2.0","id":7,"result":"0x1"}`,
		"missing id":     `{"jsonrpc":"2.0","result":"0x1"}`,
		"no result":      `{"jsonrpc":"2.0","id":1}`,
		"not json":       `not-json`,
		"error envelope": `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"boom"}}`,
	}
	for name, body := range envelopes {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			reader, err := NewJSONRPCReader(server.URL, nil)
			if err != nil {
				t.Fatalf("build reader: %v", err)
			}
			observer, err := NewObserver(reader)
			if err != nil {
				t.Fatalf("build observer: %v", err)
			}
			observation, err := observer.Observe(context.Background(), intent)
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			if observation.Status != StatusUnknown || observation.Reason != ReasonRPCError {
				t.Fatalf("observation = %#v, want an unknown rpc_error", observation)
			}
		})
	}

	t.Run("non 200 status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer server.Close()
		reader, _ := NewJSONRPCReader(server.URL, nil)
		observer, _ := NewObserver(reader)
		observation, _ := observer.Observe(context.Background(), intent)
		if observation.Status != StatusUnknown || observation.Reason != ReasonRPCError {
			t.Fatalf("observation = %#v, want an unknown rpc_error", observation)
		}
	})
}

func TestObserveNonEVMChainIDsAreUnsupportedWithoutRPC(t *testing.T) {
	key := testKey(t, 38)
	to := common.HexToAddress("0x00000000000000000000000000000000000000cd")
	amount := big.NewInt(3)
	tx := signTx(t, key, testChainID, &to, amount, nil, 8)
	from := crypto.PubkeyToAddress(key.PublicKey)

	nonEVM := map[string]*big.Int{
		"bitcoin":      big.NewInt(int64(constants.Bitcoin)),
		"solana":       big.NewInt(int64(constants.Solana)),
		"tron":         big.NewInt(int64(constants.TRON)),
		"tron testnet": big.NewInt(int64(constants.TRONTestnet)),
		"unknown":      big.NewInt(424242),
	}
	for name, chainID := range nonEVM {
		for _, kind := range []Kind{KindNative, KindERC20} {
			t.Run(name+"/"+string(kind), func(t *testing.T) {
				fixture := newRPCFixture(t)
				token := common.Address{}
				if kind == KindERC20 {
					token = common.HexToAddress("0x00000000000000000000000000000000000000ef")
				}
				intent := Intent{Kind: kind, ChainID: chainID, Hash: tx.Hash(), From: from, To: to, Token: token, Amount: amount}
				observation, err := fixture.observer(t).Observe(context.Background(), intent)
				if err != nil {
					t.Fatalf("observe: %v", err)
				}
				if observation.Reason != ReasonUnsupportedCoverage || observation.Status != StatusUnknown {
					t.Fatalf("observation = %#v, want unsupported coverage", observation)
				}
				if calls := fixture.requests(); calls != 0 {
					t.Fatalf("rpc calls = %d, want zero for a non-EVM network", calls)
				}
			})
		}
	}
}
