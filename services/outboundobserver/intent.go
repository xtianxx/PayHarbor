// Package outboundobserver is a read-only observation component for EVM outbound
// broadcasts.
//
// It is deliberately not wired into any worker: it never writes to the database, the
// ledger or the outbox, never releases a sweep hold and never re-broadcasts. It only
// answers one question for an already-persisted intent: what can be observed about
// this broadcast from a caller-supplied RPC endpoint?
//
// Scope limits (reported as unsupported instead of an observation):
//   - sweep "max" transfers where the actual amount differs from the persisted
//     amount, fee-on-transfer or rebasing tokens, proxy/delegatecall routers,
//     cross-chain bridges, and all non-EVM families (BTC/SOL/TRON/...).
//
// A positive observation is evidence, not finality: confirmations and canonical
// membership are reported as read at observation time, and no PoS/L2 settlement is
// claimed. Thresholds, terminal transitions and any wiring are a separate step.
package outboundobserver

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// Kind is the outbound intent family this component can verify.
type Kind string

const (
	// KindNative is a plain EVM value transfer to the intent destination.
	KindNative Kind = "evm_native"
	// KindERC20 is a standard, direct ERC20 transfer(address,uint256) call to the
	// token contract itself.
	KindERC20 Kind = "evm_erc20"
)

// Intent is one trustworthy, already-persisted outbound broadcast. The observer
// never invents intent fields: everything it verifies comes from here or from the
// chain.
type Intent struct {
	Kind    Kind
	ChainID *big.Int
	// Hash is the persisted broadcast hash (exactly 32 bytes by type).
	Hash common.Hash
	From common.Address
	To   common.Address
	// Token is the ERC20 contract for KindERC20 and must be zero for native.
	Token common.Address
	// Amount is the exact raw integer the intent promised (no decimals scaling).
	Amount *big.Int
}

var (
	// ErrIntentInvalid marks an intent that cannot be observed at all.
	ErrIntentInvalid = errors.New("outbound observer: invalid intent")
	// ErrUnsupported marks an intent family this component refuses to guess about.
	ErrUnsupported = errors.New("outbound observer: unsupported intent")
)

// Validate rejects incomplete intents and reports unsupported families explicitly.
func (i Intent) Validate() error {
	if i.ChainID == nil || i.ChainID.Sign() <= 0 {
		return fmt.Errorf("%w: chain id must be a positive integer", ErrIntentInvalid)
	}
	if i.Hash == (common.Hash{}) {
		return fmt.Errorf("%w: transaction hash is required", ErrIntentInvalid)
	}
	if i.From == (common.Address{}) || i.To == (common.Address{}) {
		return fmt.Errorf("%w: from and to addresses are required", ErrIntentInvalid)
	}
	if i.Amount == nil || i.Amount.Sign() <= 0 {
		return fmt.Errorf("%w: amount must be a positive raw integer", ErrIntentInvalid)
	}
	switch i.Kind {
	case KindNative:
		if i.Token != (common.Address{}) {
			return fmt.Errorf("%w: a native intent must not carry an ERC20 token address", ErrIntentInvalid)
		}
	case KindERC20:
		if i.Token == (common.Address{}) {
			return fmt.Errorf("%w: an ERC20 intent requires the token address", ErrIntentInvalid)
		}
	default:
		return fmt.Errorf("%w: outbound kind %q is not covered by EVM observation", ErrUnsupported, i.Kind)
	}
	return nil
}
