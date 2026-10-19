package main

import (
	"os"
	"strings"
	"testing"
)

// A6-R1 wiring contract. The behavioural guarantees (single winner, persisted
// snapshot, no reservation for losers) are exercised by the PostgreSQL tests in
// main_outbound_send_grant_test.go and the repositories package; this test only
// pins the executor's structure so a refactor cannot silently drop a gate.
func TestOutboundExecutionNeverRebroadcastsTerminalOutbound(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)

	executor := extractMainSweepFunctionBody(t, source, "executeOutboundTransaction")
	preflight := strings.Index(executor, "InspectSendableForExecution")
	grant := strings.Index(executor, "AcquireSendGrant")
	send := strings.Index(executor, "outboundSendFunc(")
	if preflight == -1 || grant == -1 || send == -1 {
		t.Fatalf("executeOutboundTransaction lost an A6-R1 gate (preflight=%d grant=%d send=%d)", preflight, grant, send)
	}
	if preflight > grant || grant > send {
		t.Fatalf("A6-R1 gate ordering broken: preflight=%d grant=%d send=%d", preflight, grant, send)
	}
	for _, token := range []string{
		"blockOutboundExecution",
		"grant.Outbound",
		"outboundTransferParams(ctx, granted)",
		"RecordObservedBroadcastHash",
	} {
		if !strings.Contains(executor, token) {
			t.Fatalf("executeOutboundTransaction missing A6-R1 token %q", token)
		}
	}
	for _, forbidden := range []string{
		"MarkBroadcastAttempted(ctx",
		"MarkBroadcastAttemptedGuarded",
		"VerifySendableForExecution",
		"ReserveSequence(",
		"claimed.WalletID",
		"claimed.AmountRaw",
		"claimed.ToAddress",
	} {
		if strings.Contains(executor, forbidden) {
			t.Fatalf("executeOutboundTransaction must not contain %q", forbidden)
		}
	}

	worker := extractMainSweepFunctionBody(t, source, "processOutboundTransactions")
	for _, token := range []string{"ClaimUncertainForRecovery", "MarkBroadcastRecoveryRequired"} {
		if !strings.Contains(worker, token) {
			t.Fatalf("processOutboundTransactions missing A6 recovery token %q", token)
		}
	}
	recovery := worker[strings.Index(worker, "ClaimUncertainForRecovery"):]
	if strings.Contains(recovery, "executeOutboundTransaction") {
		t.Fatal("uncertain outbound rows must never be passed to the send executor")
	}

	grantBody := ""
	if configSource, readErr := os.ReadFile("repositories/outbound_transaction_repo.go"); readErr == nil {
		grantBody = string(configSource)
	} else {
		t.Fatalf("read outbound repository: %v", readErr)
	}
	for _, token := range []string{
		"func (r *OutboundTransactionRepo) AcquireSendGrant",
		"pg_advisory_xact_lock(hashtext(?))",
		"reserveResourceWithDB(ctx, tx,",
		"outboundTerminalCategoryGuardSQL",
	} {
		if !strings.Contains(grantBody, token) {
			t.Fatalf("outbound repository missing A6-R1 grant token %q", token)
		}
	}
}
