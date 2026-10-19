package handlers

import (
	"strings"
	"testing"
)

// A7 wiring: the v1 refund create handler must enforce the refund cap through the
// guarded repository entry point (the authoritative transaction), not through an
// unlocked pre-check alone, and must keep the existing over-limit response.
func TestV1RefundCreateUsesGuardedCapEntryPoint(t *testing.T) {
	source := readHandlerSource(t, "v1api.go")
	handler := extractHandlerFunctionBody(t, source, "HandleV1RefundCreate")
	for _, token := range []string{
		"deps.RefundRepo.CreateWithHoldGuarded(",
		"repositories.RefundCreateGuardedRequest{",
		"MerchantID: domain.MerchantID",
		"DomainID:   domain.ID",
		"PaymentID:  session.ID",
	} {
		if !strings.Contains(handler, token) {
			t.Fatalf("HandleV1RefundCreate missing A7 guarded create token %q", token)
		}
	}
	if strings.Contains(handler, "deps.RefundRepo.CreateWithHold(") {
		t.Fatal("HandleV1RefundCreate must not use the unguarded refund create path")
	}
	if !strings.Contains(handler, "errors.Is(err, repositories.ErrRefundLimitExceeded)") ||
		!strings.Contains(handler, `"refund amount exceeds refundable payment amount"`) {
		t.Fatal("HandleV1RefundCreate must map ErrRefundLimitExceeded to the existing over-limit response")
	}
	limitHelper := extractHandlerFunctionBody(t, source, "v1RefundLimitRaw")
	if !strings.Contains(limitHelper, "repositories.RefundLimitRaw(") {
		t.Fatal("v1RefundLimitRaw must delegate to the shared refund cap rule")
	}
}
