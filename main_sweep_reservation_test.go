package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"core/models"
)

func TestExecuteAutoSweepDepositWithJobUsesReservedTokenAndNativeTronSweep(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "executeAutoSweepDepositWithJob")
	for _, token := range []string{
		"chain.WithdrawToken(ctx, *userDetails, *txModel.Token, txModel.Amount, reserveAddr)",
		"constants.IsTRONChain(txModel.ChainID)",
		"chain.SweepTo(ctx, *userDetails, reserveAddr)",
		"chain.Withdraw(ctx, *userDetails, txModel.Amount, reserveAddr)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("executeAutoSweepDepositWithJob missing sweep transfer token %q", token)
		}
	}
	if strings.Contains(body, "chain.SweepERC20To(") {
		t.Fatal("executeAutoSweepDepositWithJob must not use full-token sweep; ledger amount stays txModel.Amount")
	}
}

func TestLifecycleWebhookCrashRecoveryCannotLeapfrogCanonicalAggregate(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "enqueueLifecycleWebhook")
	for _, token := range []string{
		"outbox.FindByEventID(ctx, payload.EventID)",
		"return event.ID",
		"outbox.HasAggregate(ctx, payload.EntityType, payload.EntityID)",
		"direct enqueue blocked for reconciliation",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("enqueueLifecycleWebhook missing canonical ordering guard %q", token)
		}
	}
	if strings.Contains(body, "EnqueueMoneyEvent") {
		t.Fatal("canonical lifecycle bridge must leave delivery creation to the ordered relay")
	}
	ownershipIndex := strings.Index(body, "outbox.HasAggregate")
	fallbackIndex := strings.Index(body, "WebhookDeliveryRepo.EnqueueLifecycle")
	if ownershipIndex < 0 || fallbackIndex < 0 || ownershipIndex > fallbackIndex {
		t.Fatal("aggregate ownership must be checked before the legacy direct-delivery fallback")
	}
}

func TestExecuteAutoSweepDepositWithJobRequiresDurableJobAndLedgerRepo(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "executeAutoSweepDepositWithJob")
	for _, token := range []string{
		"job == nil",
		"job.ID == uuid.Nil",
		"coreApplication.CORE.Router.LedgerRepo == nil",
		"repositories.ErrLedgerReservationRequired",
		"LedgerRepo.CreateSweepHold(ctx, *job, *txModel)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("executeAutoSweepDepositWithJob missing durable job/ledger guard token %q", token)
		}
	}
}

func TestSweepSchedulingUsesDurableJobFromFinalizedPaths(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	immediateBody := extractMainSweepFunctionBody(t, source, "handleDepositWebhook")
	if !strings.Contains(immediateBody, "enqueueSweepJob(ctx, txModel)") {
		t.Fatal("immediate finalized deposit path must enqueue a durable sweep job")
	}

	finalityBody := extractMainSweepFunctionBody(t, source, "finalizePendingTransactions")
	for _, token := range []string{
		"handlePaymentDeposit(ctx, notifier, finalized)",
		"enqueueSweepJob(ctx, finalized)",
	} {
		if !strings.Contains(finalityBody, token) {
			t.Fatalf("pending-finality path missing durable sweep scheduling token %q", token)
		}
	}
}

func TestEnqueueSweepJobSkipsReserveWalletsAndEmitsRequestedOnlyOnCreate(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "enqueueSweepJob")
	for _, token := range []string{
		"userWallet.HDAddressId == 0",
		"SweepJobRepo.EnqueueForTransaction(ctx, *txModel)",
		"if created && job != nil",
		"constants.WebhookEventSweepRequestedV1",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("enqueueSweepJob missing idempotent scheduling token %q", token)
		}
	}
	skipIndex := strings.Index(body, "userWallet.HDAddressId == 0")
	enqueueIndex := strings.Index(body, "SweepJobRepo.EnqueueForTransaction(ctx, *txModel)")
	eventIndex := strings.Index(body, "constants.WebhookEventSweepRequestedV1")
	createdIndex := strings.Index(body, "if created && job != nil")
	if skipIndex == -1 || enqueueIndex == -1 || skipIndex > enqueueIndex {
		t.Fatal("enqueueSweepJob must skip reserve/non-user wallets before creating sweep jobs")
	}
	if createdIndex == -1 || eventIndex == -1 || createdIndex > eventIndex {
		t.Fatal("sweep.requested.v1 must only be emitted when EnqueueForTransaction creates a new job")
	}
}

func TestShouldAttemptSweepPrefundUsesParentJobRetryWindow(t *testing.T) {
	t.Setenv("SWEEP_PREFUND_RETRY_AFTER", "10m")
	if !shouldAttemptSweepPrefund(nil) {
		t.Fatal("nil job should allow prefund attempt")
	}

	now := time.Now()
	if !shouldAttemptSweepPrefund(&models.SweepJob{}) {
		t.Fatal("job without prefunded_at should allow prefund attempt")
	}
	if shouldAttemptSweepPrefund(&models.SweepJob{PrefundedAt: &now}) {
		t.Fatal("recent prefund should suppress duplicate prefund attempt")
	}
	if shouldAttemptSweepPrefund(&models.SweepJob{PrefundLastAttemptAt: &now}) {
		t.Fatal("recent failed prefund attempt should suppress duplicate prefund attempt")
	}
	old := now.Add(-11 * time.Minute)
	if !shouldAttemptSweepPrefund(&models.SweepJob{PrefundedAt: &old, PrefundLastAttemptAt: &old}) {
		t.Fatal("prefund older than retry window should allow another attempt")
	}
}

func TestExecuteAutoSweepDepositReservesPrefundAttemptBeforeFunding(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "executeAutoSweepDepositWithJob")
	reserveIndex := strings.Index(body, "reserveSweepPrefundAttempt(ctx, job)")
	prefundIndex := strings.Index(body, "chain.PrefundGas(ctx, *reserveDetails, userDetails.Address)")
	if reserveIndex == -1 || prefundIndex == -1 {
		t.Fatal("token sweep prefund must reserve durable prefund attempt before funding broadcast")
	}
	if reserveIndex > prefundIndex {
		t.Fatal("prefund attempt reservation must happen before PrefundGas")
	}
}

func TestSweepPrefundMaxAttemptsHonorsEnv(t *testing.T) {
	t.Setenv("SWEEP_PREFUND_MAX_ATTEMPTS", "5")
	if got := sweepPrefundMaxAttempts(); got != 5 {
		t.Fatalf("prefund max attempts = %d, want 5", got)
	}
	t.Setenv("SWEEP_PREFUND_MAX_ATTEMPTS", "0")
	if got := sweepPrefundMaxAttempts(); got != 3 {
		t.Fatalf("invalid prefund max attempts = %d, want fallback", got)
	}
}

func TestSweepJobLockTimeoutCannotExpireBeforeExecutionTimeout(t *testing.T) {
	t.Setenv("SWEEP_JOB_LOCK_TIMEOUT", "10s")
	if got := sweepJobLockTimeout(); got <= sweepJobExecutionTimeout {
		t.Fatalf("lock timeout = %s, must exceed execution timeout %s", got, sweepJobExecutionTimeout)
	}
	t.Setenv("SWEEP_JOB_LOCK_TIMEOUT", "91s")
	if got := sweepJobLockTimeout(); got != sweepJobExecutionTimeout+30*time.Second {
		t.Fatalf("lock timeout = %s, want execution timeout plus safety buffer", got)
	}
}

func TestFinalizeProcessingTransfersRequiresFinalityEvidence(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "finalizeProcessingTransfers")
	for _, token := range []string{
		"outboundTerminalTransaction(ctx, router.TransactionRepo",
		"continue",
		"models.WithdrawalStatusFinalized",
		"constants.WebhookEventPayoutFinalizedV1",
		"constants.WebhookEventPayoutFailedV1",
		"constants.WebhookEventRefundSucceededV1",
		"constants.WebhookEventRefundFailedV1",
		"openOutboundLifecycleReconciliation",
		"terminalTx.Status == models.TransactionStatusFailed",
		"MarkResourceFinalized(ctx, models.OutboundResourceWithdrawal",
		"MarkResourceFinalized(ctx, models.OutboundResourceRefund",
		"MarkResourceTerminalFailed(ctx, models.OutboundResourceWithdrawal",
		"MarkResourceTerminalFailed(ctx, models.OutboundResourceRefund",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("finalizeProcessingTransfers missing finality evidence token %q", token)
		}
	}
	finalityIndex := strings.Index(body, "outboundTerminalTransaction(ctx, router.TransactionRepo")
	withdrawalFinalizeIndex := strings.Index(body, "router.WithdrawalRepo.FinalizeProcessingWithLedger")
	refundFinalizeIndex := strings.Index(body, "router.RefundRepo.MarkSucceededWithLedger")
	if finalityIndex == -1 || withdrawalFinalizeIndex == -1 || refundFinalizeIndex == -1 {
		t.Fatal("finalizer is missing finality checks or terminal update calls")
	}
	if finalityIndex > withdrawalFinalizeIndex {
		t.Fatal("withdrawal finalization must be gated by finality evidence before ledger debit")
	}
}

// A10 contract correction: the worker used to treat "executeSweepJob returned a
// hash" as success (MarkSucceeded + PostSweepRelease + succeeded event). A hash is
// broadcast evidence, not finality, so the worker now requires the hash, records
// the broadcast through the atomic record (job marker + outbound + reservation) and
// must never mark the job succeeded or release the ledger hold on the broadcast
// path. The money assertions below (hash required, no release without verified
// finality, reconciliation instead of an error-swallowing retry) are kept.
func TestProcessSweepJobsRequiresTxHashAndRecordsBroadcastWithoutRelease(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	body := extractMainSweepFunctionBody(t, source, "processSweepJobs")
	for _, token := range []string{
		"sweep broadcast missing transaction hash",
		"router.SweepJobRepo.MarkFailed(ctx, job.ID, err)",
		"broadcast observed, finality unverified",
		"errors.As(err, &recordErr)",
		"RecordSweepBroadcastEvidenceFallback",
		"repositories.ErrSweepTerminalDecisionKept",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("processSweepJobs missing guard token %q", token)
		}
	}
	for _, forbidden := range []string{
		"router.SweepJobRepo.MarkSucceeded(ctx, job.ID, txHash)",
		"router.LedgerRepo.PostSweepRelease(ctx, job, *txModel, txHash)",
		"constants.WebhookEventSweepSucceededV1",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("processSweepJobs must not treat a broadcast hash as finality: %q", forbidden)
		}
	}
	executor := extractMainSweepFunctionBody(t, source, "recordSweepOutboundBroadcast")
	for _, token := range []string{"RecordSweepBroadcastObserved", "sweepBroadcastRecordError"} {
		if !strings.Contains(executor, token) {
			t.Fatalf("recordSweepOutboundBroadcast missing token %q", token)
		}
	}
	repoSource, readErr := os.ReadFile("repositories/sweep_job_repo.go")
	if readErr != nil {
		t.Fatalf("read sweep job repository: %v", readErr)
	}
	for _, token := range []string{"openSweepBroadcastReconciliationWithDB", "sweepBroadcastObservedReason", "CreateScopedOpenIfMissing"} {
		if !strings.Contains(string(repoSource), token) {
			t.Fatalf("sweep broadcast record missing same-transaction reconciliation token %q", token)
		}
	}
	releaseFence := extractMainSweepFunctionBody(t, source, "releaseSweepHoldOnPreBroadcastDeadLetter")
	for _, token := range []string{"SweepBroadcastEvidence", "sweep_hold_release_evidence_unreadable", "sweep_dead_letter_broadcast_uncertain"} {
		if !strings.Contains(releaseFence, token) {
			t.Fatalf("releaseSweepHoldOnPreBroadcastDeadLetter missing evidence fence token %q", token)
		}
	}
}

func TestProcessSweepJobsSchedulesMissingFinalizedSweepsBeforeClaim(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "processSweepJobs")
	scheduleIndex := strings.Index(body, "scheduleMissingFinalizedSweepJobs(ctx, router)")
	claimIndex := strings.Index(body, "router.SweepJobRepo.ClaimDue(ctx, 25, sweepJobLockTimeout())")
	if scheduleIndex == -1 || claimIndex == -1 {
		t.Fatal("processSweepJobs must schedule missing finalized sweeps before claim")
	}
	if scheduleIndex > claimIndex {
		t.Fatal("missing finalized sweeps must be scheduled before ClaimDue")
	}
}

func TestSweepDeadLetterUnclassifiedFailureOpensReconciliation(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "releaseSweepHoldOnPreBroadcastDeadLetter")
	if !strings.Contains(body, "\"sweep_dead_letter_broadcast_uncertain\"") {
		t.Fatal("unclassified sweep dead-letter failures must open reconciliation")
	}
}

func TestProcessSweepJobsDeadLettersBroadcastUncertainBeforeRetry(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "processSweepJobs")
	for _, token := range []string{
		"sweepFailureBroadcastUncertain(err)",
		"markSweepBroadcastUncertainAndReconcile(ctx, router, job, txModel, \"sweep_broadcast_uncertain\", err)",
		"markSweepBroadcastUncertainAndReconcile(ctx, router, job, txModel, \"sweep_broadcast_missing_tx_hash\", err)",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("processSweepJobs missing broadcast-uncertain guard token %q", token)
		}
	}

	executeStart := strings.Index(body, "result, err := executeSweepJob")
	if executeStart == -1 {
		t.Fatal("processSweepJobs missing executeSweepJob call")
	}
	executeErrBranch := body[executeStart:]
	uncertainIndex := strings.Index(executeErrBranch, "sweepFailureBroadcastUncertain(err)")
	failedIndex := strings.Index(executeErrBranch, "router.SweepJobRepo.MarkFailed(ctx, job.ID, err)")
	if uncertainIndex == -1 || failedIndex == -1 || uncertainIndex > failedIndex {
		t.Fatal("processSweepJobs must route broadcast-uncertain failures before retryable MarkFailed")
	}
}

func TestProcessSweepJobsRoutesRecoveredProcessingToReconciliationBeforeExecution(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "processSweepJobs")
	staleIndex := strings.Index(body, "job.Status == models.SweepJobStatusProcessing")
	if staleIndex == -1 {
		t.Fatal("processSweepJobs must detect reclaimed stale processing jobs")
	}
	reconcileIndex := strings.Index(body, "\"sweep_stale_processing_recovered\"")
	executeIndex := strings.Index(body, "result, err := executeSweepJob")
	if reconcileIndex == -1 || executeIndex == -1 {
		t.Fatal("processSweepJobs missing stale reconciliation or execute call")
	}
	if staleIndex > executeIndex || reconcileIndex > executeIndex {
		t.Fatal("stale processing jobs must reconcile before executeSweepJob")
	}
}

func TestProcessSweepJobsHandlesBroadcastUncertainMarkFailuresAndReloadFallback(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	helper := extractMainSweepFunctionBody(t, string(sourceBytes), "markSweepBroadcastUncertainAndReconcile")
	for _, token := range []string{
		"MarkBroadcastUncertain(ctx, job.ID, err)",
		"if markErr != nil",
		"\"sweep_broadcast_uncertain_mark_failed\"",
		"if findErr != nil",
		"fallback.Status = models.SweepJobStatusDeadLetter",
	} {
		if !strings.Contains(helper, token) {
			t.Fatalf("broadcast uncertainty helper missing %q", token)
		}
	}
}

func TestPrepareSweepOutboundRoutesRecoveredBroadcastAttemptToOperatorAction(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := extractMainSweepFunctionBody(t, string(sourceBytes), "prepareSweepOutbound")
	for _, token := range []string{
		"outboundTx.Status == models.OutboundStatusBroadcastAttempted",
		`strings.TrimSpace(outboundTx.TxHash) == ""`,
		`MarkNeedsOperatorAction(ctx, outboundTx.ID, "broadcast_recovery_required", err.Error())`,
		`openSweepLedgerReconciliation(ctx, job, &txModel, "sweep_outbound_broadcast_attempt_recovered", err)`,
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("prepareSweepOutbound missing recovered broadcast-attempt guard token %q", token)
		}
	}
	recoveryIndex := strings.Index(body, "outboundTx.Status == models.OutboundStatusBroadcastAttempted")
	grantIndex := strings.Index(body, "AcquireSweepSendGrant(ctx, job, outboundTx.ID")
	if recoveryIndex == -1 || grantIndex == -1 || recoveryIndex > grantIndex {
		t.Fatal("recovered sweep broadcast attempts must be routed to operator action before the send grant")
	}
	for _, token := range []string{"sweep send grant refused", "models.SweepOperatorActionReconcileBroadcast"} {
		if !strings.Contains(body, token) {
			t.Fatalf("prepareSweepOutbound missing refused-grant token %q", token)
		}
	}
	if strings.Contains(body, "ReserveSequence(") || strings.Contains(body, "ReleaseResource(") {
		t.Fatal("prepareSweepOutbound must use the atomic sweep grant and never release a reservation")
	}
}

func TestOutboundTransactionWorkerPersistsHashBeforeLifecycleBroadcastEvent(t *testing.T) {
	sourceBytes, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(sourceBytes)
	workerBody := extractMainSweepFunctionBody(t, source, "executeOutboundTransaction")
	for _, token := range []string{
		"AcquireSendGrant(ctx, *persisted",
		"outboundSendFunc(router.WalletRepo, router.Blockchains()",
		"MarkBroadcasted(ctx, granted.ID, txHash)",
		"ConsumeResource(ctx, grant.Reservation.ID, txHash)",
		"recordOutboundBroadcast(ctx, granted, txHash)",
		"OutboundTransferFailureBroadcastUncertain(err)",
		`MarkNeedsOperatorAction(ctx, granted.ID, "broadcast_uncertain", errText)`,
	} {
		if !strings.Contains(workerBody, token) {
			t.Fatalf("executeOutboundTransaction missing durable outbound token %q", token)
		}
	}
	markIndex := strings.Index(workerBody, "MarkBroadcasted(ctx, granted.ID, txHash)")
	eventIndex := strings.Index(workerBody, "recordOutboundBroadcast(ctx, granted, txHash)")
	if markIndex == -1 || eventIndex == -1 || markIndex > eventIndex {
		t.Fatal("outbound worker must persist tx hash before lifecycle broadcast events")
	}

	recordBody := extractMainSweepFunctionBody(t, source, "recordOutboundBroadcast")
	for _, token := range []string{
		"WithdrawalRepo.RecordBroadcast",
		"RefundRepo.RecordBroadcast",
		"constants.WebhookEventPayoutBroadcastV1",
		"constants.WebhookEventRefundBroadcastV1",
	} {
		if !strings.Contains(recordBody, token) {
			t.Fatalf("recordOutboundBroadcast missing lifecycle token %q", token)
		}
	}
}

func TestSweepFailureBroadcastUncertainClassifiesPostBroadcastErrors(t *testing.T) {
	for _, errText := range []string{
		"ethereum tx broadcast failed: context deadline exceeded",
		"solana tx broadcast failed: rpc unavailable",
		"replacement transaction underpriced",
		"nonce too low",
		"transaction already known in mempool",
	} {
		if !sweepFailureBroadcastUncertain(errors.New(errText)) {
			t.Fatalf("expected broadcast-uncertain classification for %q", errText)
		}
	}
	for _, errText := range []string{
		"ledger reservation is required before outbound transfer",
		"wallet not found",
		"resource reservation failed: chain resource already reserved",
	} {
		if sweepFailureBroadcastUncertain(errors.New(errText)) {
			t.Fatalf("expected pre-broadcast classification for %q", errText)
		}
	}
}

func extractMainSweepFunctionBody(t *testing.T, source, functionName string) string {
	t.Helper()
	start := strings.Index(source, "func ")
	for start != -1 {
		remaining := source[start:]
		open := strings.Index(remaining, "{")
		if open == -1 {
			t.Fatalf("function %s has no opening brace", functionName)
		}
		signature := remaining[:open]
		if strings.Contains(signature, " "+functionName+"(") {
			index := start + open
			depth := 0
			for i := index; i < len(source); i++ {
				switch source[i] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						return source[index : i+1]
					}
				}
			}
			t.Fatalf("function %s has no closing brace", functionName)
		}
		next := strings.Index(remaining[5:], "func ")
		if next == -1 {
			break
		}
		start += 5 + next
	}
	t.Fatalf("function %s not found", functionName)
	return ""
}
