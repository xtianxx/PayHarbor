package outboundobserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// Reader is the bounded read-only chain access the observer needs. Implementations
// must not mutate anything, must not discover endpoints or fall back to another RPC,
// and must surface transport/RPC errors verbatim so the observer can report
// "unknown" instead of guessing.
type Reader interface {
	ChainID(ctx context.Context) (*big.Int, error)
	HeadNumber(ctx context.Context) (uint64, error)
	TransactionByHash(ctx context.Context, hash common.Hash) (*RawTransaction, error)
	TransactionReceipt(ctx context.Context, hash common.Hash) (*RawReceipt, error)
	// BlockByNumber returns the canonical block at that height (headers and the
	// transaction hash list are enough; full transaction bodies are not needed).
	BlockByNumber(ctx context.Context, number uint64) (*RawBlock, error)
}

// RawTransaction is the standard `eth_getTransactionByHash` result view: the signed
// transaction rebuilt from the documented JSON fields (no vendor-specific `raw`
// extension), plus the block metadata the node reports for it.
type RawTransaction struct {
	Tx *types.Transaction
	// ResponseHash is the hash the node returned; the observer cross-checks it.
	ResponseHash common.Hash
	BlockHash    *common.Hash
	BlockNumber  *uint64
	TxIndex      *uint64
}

// RawLog is one receipt log with its real attribution fields: a log only counts for
// this observation when it belongs to the transaction and block the receipt names.
type RawLog struct {
	Address     common.Address
	Topics      []common.Hash
	Data        hexutil.Bytes
	TxHash      *common.Hash
	BlockHash   *common.Hash
	BlockNumber *uint64
	LogIndex    *uint64
	Removed     bool
}

// RawReceipt is the wire-level receipt view. Every field that can legitimately be
// absent is a pointer, so "missing" never decodes into a successful value.
type RawReceipt struct {
	TxHash      *common.Hash
	BlockHash   *common.Hash
	BlockNumber *uint64
	TxIndex     *uint64
	Status      *uint64
	Logs        []RawLog
}

// RawBlock is the canonical header view plus the hashes it contains.
type RawBlock struct {
	Hash     common.Hash
	Number   uint64
	TxHashes []common.Hash
}

// defaultMaxResponseBytes bounds every RPC response the adapter will read.
const defaultMaxResponseBytes = 8 << 20

const jsonRPCVersion = "2.0"

// JSONRPCReader is the read-only HTTP JSON-RPC adapter. It talks only to the
// endpoint the caller supplies; it never scans for endpoints and has no fallback.
// It uses the caller's *http.Client as-is (including whatever proxy policy that
// client has); when none is supplied it builds one with a bounded timeout.
type JSONRPCReader struct {
	endpoint string
	client   *http.Client
	maxBytes int64
}

// NewJSONRPCReader builds the adapter for one explicit endpoint.
func NewJSONRPCReader(endpoint string, client *http.Client) (*JSONRPCReader, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("outbound observer: rpc endpoint is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &JSONRPCReader{endpoint: endpoint, client: client, maxBytes: defaultMaxResponseBytes}, nil
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
}

// call performs one bounded JSON-RPC call and returns the raw result, or (nil, nil)
// when the node answered with JSON null (for example an unknown hash).
func (r *JSONRPCReader) call(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	if r == nil || r.client == nil {
		return nil, errors.New("outbound observer: rpc reader is not configured")
	}
	const requestID = 1
	payload, err := json.Marshal(jsonRPCRequest{JSONRPC: jsonRPCVersion, ID: requestID, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, r.maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > r.maxBytes {
		return nil, fmt.Errorf("outbound observer: rpc response exceeds %d bytes", r.maxBytes)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("outbound observer: rpc http status %d", response.StatusCode)
	}
	var envelope jsonRPCResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("outbound observer: malformed rpc response: %w", err)
	}
	// Minimal envelope integrity: the version and the request id must match, so a
	// stale, truncated or mixed-up response can never be attributed to this call.
	if envelope.JSONRPC != jsonRPCVersion {
		return nil, fmt.Errorf("outbound observer: rpc envelope version %q is not %q", boundedText(envelope.JSONRPC, 16), jsonRPCVersion)
	}
	if envelope.ID == nil || *envelope.ID != requestID {
		return nil, errors.New("outbound observer: rpc envelope id does not match the request")
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("outbound observer: rpc error %d: %s", envelope.Error.Code, boundedText(envelope.Error.Message, 160))
	}
	if len(envelope.Result) == 0 {
		return nil, errors.New("outbound observer: rpc response has no result")
	}
	if bytes.Equal(bytes.TrimSpace(envelope.Result), []byte("null")) {
		return nil, nil
	}
	return envelope.Result, nil
}

func (r *JSONRPCReader) ChainID(ctx context.Context) (*big.Int, error) {
	raw, err := r.call(ctx, "eth_chainId", []any{})
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("outbound observer: chain id missing")
	}
	var value hexutil.Big
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("outbound observer: chain id is not a hex quantity: %w", err)
	}
	if value.ToInt() == nil {
		return nil, errors.New("outbound observer: chain id missing")
	}
	return new(big.Int).Set(value.ToInt()), nil
}

func (r *JSONRPCReader) HeadNumber(ctx context.Context) (uint64, error) {
	raw, err := r.call(ctx, "eth_blockNumber", []any{})
	if err != nil {
		return 0, err
	}
	if raw == nil {
		return 0, errors.New("outbound observer: head number missing")
	}
	var value hexutil.Uint64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("outbound observer: head number is not a hex quantity: %w", err)
	}
	return uint64(value), nil
}

type wireBlockRef struct {
	BlockHash   *common.Hash    `json:"blockHash"`
	BlockNumber *hexutil.Uint64 `json:"blockNumber"`
	TxIndex     *hexutil.Uint64 `json:"transactionIndex"`
}

func (r *JSONRPCReader) TransactionByHash(ctx context.Context, hash common.Hash) (*RawTransaction, error) {
	raw, err := r.call(ctx, "eth_getTransactionByHash", []any{hash})
	if err != nil || raw == nil {
		return nil, err
	}
	// The documented result object carries the signed transaction fields (v/r/s and
	// the payload); there is no `raw` field, so the transaction is rebuilt with
	// go-ethereum's own JSON decoder.
	var transaction types.Transaction
	if err := json.Unmarshal(raw, &transaction); err != nil {
		return nil, fmt.Errorf("outbound observer: transaction result is not a signed transaction: %w", err)
	}
	var reference struct {
		Hash *common.Hash `json:"hash"`
		wireBlockRef
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		return nil, err
	}
	if reference.Hash == nil {
		return nil, errors.New("outbound observer: transaction result has no hash")
	}
	result := &RawTransaction{Tx: &transaction, ResponseHash: *reference.Hash, BlockHash: reference.BlockHash}
	if reference.BlockNumber != nil {
		number := uint64(*reference.BlockNumber)
		result.BlockNumber = &number
	}
	if reference.TxIndex != nil {
		index := uint64(*reference.TxIndex)
		result.TxIndex = &index
	}
	return result, nil
}

type wireLog struct {
	Address     *common.Address `json:"address"`
	Topics      []common.Hash   `json:"topics"`
	Data        hexutil.Bytes   `json:"data"`
	TxHash      *common.Hash    `json:"transactionHash"`
	BlockHash   *common.Hash    `json:"blockHash"`
	BlockNumber *hexutil.Uint64 `json:"blockNumber"`
	LogIndex    *hexutil.Uint64 `json:"logIndex"`
	Removed     *bool           `json:"removed"`
}

type wireReceipt struct {
	TxHash      *common.Hash    `json:"transactionHash"`
	BlockHash   *common.Hash    `json:"blockHash"`
	BlockNumber *hexutil.Uint64 `json:"blockNumber"`
	TxIndex     *hexutil.Uint64 `json:"transactionIndex"`
	Status      *hexutil.Uint64 `json:"status"`
	Logs        []wireLog       `json:"logs"`
}

func (r *JSONRPCReader) TransactionReceipt(ctx context.Context, hash common.Hash) (*RawReceipt, error) {
	raw, err := r.call(ctx, "eth_getTransactionReceipt", []any{hash})
	if err != nil || raw == nil {
		return nil, err
	}
	var wire wireReceipt
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("outbound observer: receipt result is malformed: %w", err)
	}
	receipt := &RawReceipt{TxHash: wire.TxHash, BlockHash: wire.BlockHash}
	if wire.BlockNumber != nil {
		number := uint64(*wire.BlockNumber)
		receipt.BlockNumber = &number
	}
	if wire.TxIndex != nil {
		index := uint64(*wire.TxIndex)
		receipt.TxIndex = &index
	}
	if wire.Status != nil {
		status := uint64(*wire.Status)
		receipt.Status = &status
	}
	for _, log := range wire.Logs {
		if log.Address == nil {
			continue
		}
		removed := false
		if log.Removed != nil {
			removed = *log.Removed
		}
		entry := RawLog{Address: *log.Address, Topics: log.Topics, Data: log.Data, TxHash: log.TxHash, BlockHash: log.BlockHash, Removed: removed}
		if log.BlockNumber != nil {
			number := uint64(*log.BlockNumber)
			entry.BlockNumber = &number
		}
		if log.LogIndex != nil {
			index := uint64(*log.LogIndex)
			entry.LogIndex = &index
		}
		receipt.Logs = append(receipt.Logs, entry)
	}
	return receipt, nil
}

type wireBlock struct {
	Hash         *common.Hash    `json:"hash"`
	Number       *hexutil.Uint64 `json:"number"`
	Transactions []common.Hash   `json:"transactions"`
}

func (r *JSONRPCReader) BlockByNumber(ctx context.Context, number uint64) (*RawBlock, error) {
	raw, err := r.call(ctx, "eth_getBlockByNumber", []any{hexutil.EncodeUint64(number), false})
	if err != nil || raw == nil {
		return nil, err
	}
	var wire wireBlock
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("outbound observer: block result is malformed: %w", err)
	}
	if wire.Hash == nil || wire.Number == nil {
		return nil, errors.New("outbound observer: block result has no hash or number")
	}
	if uint64(*wire.Number) != number {
		return nil, errors.New("outbound observer: rpc returned a block with a different number")
	}
	return &RawBlock{Hash: *wire.Hash, Number: uint64(*wire.Number), TxHashes: wire.Transactions}, nil
}

// boundedText trims untrusted text (for example an RPC error message) to a bounded
// single line so observations never carry large or multi-line payloads.
func boundedText(value string, limit int) string {
	trimmed := strings.Join(strings.Fields(value), " ")
	if limit > 0 && len(trimmed) > limit {
		trimmed = trimmed[:limit]
	}
	return trimmed
}
