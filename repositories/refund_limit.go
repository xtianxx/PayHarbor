package repositories

import (
	"errors"
	"math/big"
	"strings"

	"core/models"
)

// Refund create guard contract (A7).
//
// The refund cap rule lives here exactly once so the API pre-check (friendly
// error) and the guarded create transaction (authoritative enforcement) can never
// drift apart. The rule itself is unchanged: donation links prefer the matched
// amount, otherwise the expected amount, and a resolvable related transaction
// raises the limit to its actual amount when that is larger.
var (
	// ErrRefundLimitExceeded means the requested amount plus the payment's active
	// refunds would exceed the refundable limit.
	ErrRefundLimitExceeded = errors.New("refund amount exceeds refundable payment amount")
	// ErrRefundAmountInvalid means the requested amount is not a positive integer.
	ErrRefundAmountInvalid = errors.New("refund amount must be a positive integer")
	// ErrRefundPaymentNotRefundable means the payment identity does not match the
	// caller's scope, is not paid, or has no usable chain.
	ErrRefundPaymentNotRefundable = errors.New("payment is not refundable")
	// ErrRefundExpectedAmountInvalid mirrors the API error branch for payments
	// whose expected/matched amount cannot form a limit.
	ErrRefundExpectedAmountInvalid = errors.New("payment expected amount is invalid")
	// ErrRefundAssetBusy means the merchant/domain/asset guard is currently held by
	// another transaction. Callers must not wait for it: retry later (the API maps
	// this to a retryable 503 and keeps the idempotency key usable).
	ErrRefundAssetBusy = errors.New("refund asset is busy, retry shortly")
	// ErrRefundAssetMismatch means the request described an asset that differs from
	// the locked payment session.
	ErrRefundAssetMismatch = errors.New("refund asset does not match the payment session")
	// ErrRefundCreateStatusInvalid means a creation request carried a status other
	// than pending (failed/rejected rows must not be created through the guard).
	ErrRefundCreateStatusInvalid = errors.New("refund creation must start in pending status")
)

// RefundCapFacts are the persisted payment/transaction facts the cap depends on.
type RefundCapFacts struct {
	DonationLink      bool
	ExpectedAmountRaw string
	MatchedAmountRaw  string
	TxUniqueHash      string
	TxAmountRaw       string
	TxResolved        bool
}

// RefundLimitRaw evaluates the refund cap for one payment. It is the single
// implementation of the rule; callers only gather facts.
func RefundLimitRaw(facts RefundCapFacts) (*big.Int, error) {
	var limit *big.Int
	if facts.DonationLink {
		if matched, ok := refundPositiveBigInt(facts.MatchedAmountRaw); ok {
			limit = matched
		}
	}
	if limit == nil {
		expected, ok := refundPositiveBigInt(facts.ExpectedAmountRaw)
		if !ok && !facts.DonationLink {
			return nil, ErrRefundExpectedAmountInvalid
		}
		if ok {
			limit = expected
		}
	}
	if !facts.TxResolved || strings.TrimSpace(facts.TxUniqueHash) == "" {
		if limit == nil {
			return nil, ErrRefundExpectedAmountInvalid
		}
		return new(big.Int).Set(limit), nil
	}
	actual, ok := refundPositiveBigInt(facts.TxAmountRaw)
	if ok && (limit == nil || actual.Cmp(limit) > 0) {
		limit = actual
	}
	if limit == nil {
		return nil, ErrRefundExpectedAmountInvalid
	}
	return new(big.Int).Set(limit), nil
}

// refundActiveStatuses is the refund lifecycle set that consumes refundable
// payment amount. failed and rejected deliberately do not.
func refundActiveStatuses() []string {
	return []string{
		models.RefundStatusPending,
		models.RefundStatusProcessing,
		models.RefundStatusApproved,
		models.RefundStatusSucceeded,
	}
}

func refundPositiveBigInt(value string) (*big.Int, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, ".") || strings.HasPrefix(value, "-") {
		return nil, false
	}
	amount, ok := new(big.Int).SetString(value, 10)
	return amount, ok && amount.Sign() > 0
}
