package repositories

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestOutboundTransactionRepoCreateAndClaimDueAreIdempotent(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)

	params := outboundTransactionRepoTestParams(models.OutboundResourceWithdrawal, constants.Ethereum)
	first, created, err := repo.Create(ctx, params)
	if err != nil {
		t.Fatalf("create outbound: %v", err)
	}
	if !created {
		t.Fatal("first create should create a row")
	}
	if first.Status != models.OutboundStatusPrepared || first.Symbol != "ETH" {
		t.Fatalf("created outbound = %#v, want prepared ETH row", first)
	}

	params.AmountRaw = "999"
	second, created, err := repo.Create(ctx, params)
	if err != nil {
		t.Fatalf("idempotent create: %v", err)
	}
	if created {
		t.Fatal("second create with same idempotency key must return existing row")
	}
	if second.ID != first.ID || second.AmountRaw != "1000000000000000000" {
		t.Fatalf("idempotent create returned %#v, want original row %s", second, first.ID)
	}

	claimed, err := repo.ClaimDue(ctx, []string{models.OutboundResourceWithdrawal}, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != first.ID || claimed[0].LockedUntil == nil {
		t.Fatalf("claimed = %#v, want locked original outbound", claimed)
	}

	claimedAgain, err := repo.ClaimDue(ctx, []string{models.OutboundResourceWithdrawal}, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim locked row again: %v", err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("locked outbound should not be claimed again, got %#v", claimedAgain)
	}
}

func TestOutboundTransactionRepoDefersForNetworkStateWithoutConsumingAttempt(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)
	outbound := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)

	claimed, err := repo.ClaimDue(ctx, []string{models.OutboundResourceWithdrawal}, 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim due = %#v, err=%v", claimed, err)
	}
	if err := repo.DeferForNetworkState(ctx, outbound.ID, "provider upgrade", time.Minute); err != nil {
		t.Fatalf("defer for network state: %v", err)
	}

	var reloaded models.OutboundTransaction
	if err := db.WithContext(ctx).First(&reloaded, "id = ?", outbound.ID).Error; err != nil {
		t.Fatalf("reload outbound: %v", err)
	}
	if reloaded.Status != models.OutboundStatusPrepared || reloaded.Attempts != 0 || reloaded.LockedUntil != nil || reloaded.NextRunAt == nil || !reloaded.NextRunAt.After(time.Now()) {
		t.Fatalf("deferred outbound state = %#v", reloaded)
	}
	if reloaded.ErrorCategory != "network_maintenance" || reloaded.ErrorDetail != "provider upgrade" {
		t.Fatalf("deferred outbound error metadata = %q/%q", reloaded.ErrorCategory, reloaded.ErrorDetail)
	}
}

func TestOutboundTransactionRepoSequenceReservationReleasesAndConsumes(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)
	sourceAddress := "0xreserve"

	first := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	second := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceRefund, constants.Ethereum)
	third := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceSweepJob, constants.Ethereum)

	firstReservation, created, err := repo.ReserveSequence(ctx, first, sourceAddress, time.Minute)
	if err != nil {
		t.Fatalf("reserve first sequence: %v", err)
	}
	if !created || firstReservation.Status != models.OutboundResourceReservationReserved {
		t.Fatalf("first reservation = %#v, created=%v", firstReservation, created)
	}

	replayed, created, err := repo.ReserveSequence(ctx, first, sourceAddress, time.Minute)
	if err != nil {
		t.Fatalf("replay first sequence reservation: %v", err)
	}
	if created || replayed.ID != firstReservation.ID {
		t.Fatalf("same outbound replay = %#v created=%v, want existing %s", replayed, created, firstReservation.ID)
	}

	if _, _, err := repo.ReserveSequence(ctx, second, sourceAddress, time.Minute); !errors.Is(err, ErrOutboundResourceAlreadyReserved) {
		t.Fatalf("competing sequence reservation err = %v, want ErrOutboundResourceAlreadyReserved", err)
	}
	if err := repo.ReleaseResource(ctx, firstReservation.ID); err != nil {
		t.Fatalf("release first sequence: %v", err)
	}

	secondReservation, created, err := repo.ReserveSequence(ctx, second, sourceAddress, time.Minute)
	if err != nil {
		t.Fatalf("reserve second sequence after release: %v", err)
	}
	if !created || secondReservation.ID == firstReservation.ID {
		t.Fatalf("second reservation = %#v created=%v, want new reservation", secondReservation, created)
	}
	if err := repo.ConsumeResource(ctx, secondReservation.ID, "0xtxhash"); err != nil {
		t.Fatalf("consume second sequence: %v", err)
	}

	thirdReservation, created, err := repo.ReserveSequence(ctx, third, sourceAddress, time.Minute)
	if err != nil {
		t.Fatalf("reserve sequence after consumed sequence: %v", err)
	}
	if !created || thirdReservation.ID == secondReservation.ID {
		t.Fatalf("third reservation = %#v created=%v, want consumed sequence not to block", thirdReservation, created)
	}
}

func TestOutboundTransactionRepoUTXOConsumptionBlocksReuse(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)

	first := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Bitcoin)
	second := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceRefund, constants.Bitcoin)
	vout := uint32(1)

	firstReservation, created, err := repo.ReserveResource(ctx, OutboundResourceReservationRequest{
		OutboundTransactionID: first.ID,
		ResourceType:          models.OutboundResourceReservationUTXO,
		ChainID:               constants.Bitcoin,
		WalletID:              first.WalletID,
		WalletAddress:         "bc1qsource",
		OwnerType:             first.ResourceType,
		OwnerID:               first.ResourceID,
		Intent:                "withdrawal:broadcast",
		UTXOTxID:              "0xabc",
		UTXOVout:              &vout,
		UTXOValueRaw:          "1000",
		LeaseFor:              time.Minute,
	})
	if err != nil {
		t.Fatalf("reserve first utxo: %v", err)
	}
	if !created {
		t.Fatal("first UTXO reservation should create a row")
	}
	if err := repo.ConsumeResource(ctx, firstReservation.ID, "0xspend"); err != nil {
		t.Fatalf("consume first utxo: %v", err)
	}

	_, _, err = repo.ReserveResource(ctx, OutboundResourceReservationRequest{
		OutboundTransactionID: second.ID,
		ResourceType:          models.OutboundResourceReservationUTXO,
		ChainID:               constants.Bitcoin,
		WalletID:              second.WalletID,
		WalletAddress:         "bc1qsource",
		OwnerType:             second.ResourceType,
		OwnerID:               second.ResourceID,
		Intent:                "refund:broadcast",
		UTXOTxID:              "0xabc",
		UTXOVout:              &vout,
		UTXOValueRaw:          "1000",
		LeaseFor:              time.Minute,
	})
	if !errors.Is(err, ErrOutboundResourceAlreadyReserved) {
		t.Fatalf("consumed UTXO reuse err = %v, want ErrOutboundResourceAlreadyReserved", err)
	}
}

func TestOutboundTransactionRepoResourceTerminalTransitions(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)

	finalized := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	if err := repo.MarkBroadcasted(ctx, finalized.ID, "0xfinal"); err != nil {
		t.Fatalf("mark broadcasted: %v", err)
	}
	if err := repo.MarkResourceFinalized(ctx, finalized.ResourceType, finalized.ResourceID); err != nil {
		t.Fatalf("mark resource finalized: %v", err)
	}
	var finalizedRow models.OutboundTransaction
	if err := db.WithContext(ctx).First(&finalizedRow, "id = ?", finalized.ID).Error; err != nil {
		t.Fatalf("reload finalized row: %v", err)
	}
	if finalizedRow.Status != models.OutboundStatusFinalized || finalizedRow.FinalizedAt == nil || finalizedRow.NextRunAt != nil || finalizedRow.LockedUntil != nil {
		t.Fatalf("finalized row = %#v, want terminal finalized without retry/lock", finalizedRow)
	}

	failed := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceRefund, constants.Ethereum)
	if err := repo.MarkBroadcasted(ctx, failed.ID, "0xfailed"); err != nil {
		t.Fatalf("mark failed candidate broadcasted: %v", err)
	}
	if err := repo.MarkResourceTerminalFailed(ctx, failed.ResourceType, failed.ResourceID, errors.New("chain terminal failure")); err != nil {
		t.Fatalf("mark resource terminal failed: %v", err)
	}
	var failedRow models.OutboundTransaction
	if err := db.WithContext(ctx).First(&failedRow, "id = ?", failed.ID).Error; err != nil {
		t.Fatalf("reload failed row: %v", err)
	}
	if failedRow.Status != models.OutboundStatusFailed || failedRow.FinalizedAt == nil || failedRow.ErrorCategory != "terminal_failed" || failedRow.NextRunAt != nil || failedRow.LockedUntil != nil {
		t.Fatalf("failed row = %#v, want terminal failed without retry/lock", failedRow)
	}
}

// TestOutboundTransactionRepoClaimNeverHandsOutBroadcastOrTerminalRows pins the
// A6 claim contract: the send path may only receive rows that are a clean first
// attempt, never anything carrying hash, attempt, broadcast or finality evidence.
func TestOutboundTransactionRepoClaimNeverHandsOutBroadcastOrTerminalRows(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)
	now := time.Now()

	sendablePrepared := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	sendableRetry := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	mustUpdateOutboundTransaction(t, db, sendableRetry.ID, map[string]any{
		"status":         models.OutboundStatusFailed,
		"error_category": "network_unavailable",
		"next_run_at":    now.Add(-time.Minute),
	})

	terminal := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	mustUpdateOutboundTransaction(t, db, terminal.ID, map[string]any{
		"status":         models.OutboundStatusFailed,
		"error_category": "terminal_failed",
		"finalized_at":   &now,
	})
	failedWithHash := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	mustUpdateOutboundTransaction(t, db, failedWithHash.ID, map[string]any{
		"status":         models.OutboundStatusFailed,
		"error_category": "network_unavailable",
		"tx_hash":        "0xdeadbeef",
		"broadcasted_at": &now,
	})
	failedWithAttempt := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	mustUpdateOutboundTransaction(t, db, failedWithAttempt.ID, map[string]any{
		"status":                 models.OutboundStatusFailed,
		"error_category":         "network_unavailable",
		"broadcast_attempted_at": &now,
	})
	uncertain := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	mustUpdateOutboundTransaction(t, db, uncertain.ID, map[string]any{
		"status":                 models.OutboundStatusBroadcastAttempted,
		"broadcast_attempted_at": &now,
	})
	broadcasted := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	mustUpdateOutboundTransaction(t, db, broadcasted.ID, map[string]any{
		"status":         models.OutboundStatusBroadcasted,
		"tx_hash":        "0xcafe",
		"broadcasted_at": &now,
	})
	needsOperator := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	mustUpdateOutboundTransaction(t, db, needsOperator.ID, map[string]any{"status": models.OutboundStatusNeedsOperatorAction})
	replaced := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	mustUpdateOutboundTransaction(t, db, replaced.ID, map[string]any{"status": models.OutboundStatusReplaced})

	rows, err := repo.ClaimDue(ctx, []string{models.OutboundResourceWithdrawal}, 50, 30*time.Second)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	claimed := map[uuid.UUID]models.OutboundTransaction{}
	for _, row := range rows {
		claimed[row.ID] = row
	}
	if len(rows) != 2 {
		t.Fatalf("claim due returned %d rows, want exactly the two sendable rows: %#v", len(rows), rows)
	}
	if _, ok := claimed[sendablePrepared.ID]; !ok {
		t.Fatalf("claim due skipped the clean prepared row: %#v", rows)
	}
	if _, ok := claimed[sendableRetry.ID]; !ok {
		t.Fatalf("claim due skipped the retryable pre-broadcast failure: %#v", rows)
	}
	for _, blocked := range []models.OutboundTransaction{terminal, failedWithHash, failedWithAttempt, uncertain, broadcasted, needsOperator, replaced} {
		if _, ok := claimed[blocked.ID]; ok {
			t.Fatalf("send claim handed out a non-sendable row: id=%s status=%s category=%s", blocked.ID, blocked.Status, blocked.ErrorCategory)
		}
	}

	recoveryRows, err := repo.ClaimUncertainForRecovery(ctx, []string{models.OutboundResourceWithdrawal}, 50, 30*time.Second)
	if err != nil {
		t.Fatalf("claim uncertain: %v", err)
	}
	if len(recoveryRows) != 1 || recoveryRows[0].ID != uncertain.ID || recoveryRows[0].Status != models.OutboundStatusBroadcastAttempted {
		t.Fatalf("recovery claim = %#v, want exactly the broadcast_attempted row", recoveryRows)
	}
}

func TestOutboundTransactionRepoClaimUncertainForRecoveryIsOperatorOnly(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)
	now := time.Now()

	prepared := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	uncertain := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	mustUpdateOutboundTransaction(t, db, uncertain.ID, map[string]any{
		"status":                 models.OutboundStatusBroadcastAttempted,
		"broadcast_attempted_at": &now,
	})

	recoveryRows, err := repo.ClaimUncertainForRecovery(ctx, nil, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("claim uncertain: %v", err)
	}
	if len(recoveryRows) != 1 || recoveryRows[0].ID != uncertain.ID {
		t.Fatalf("recovery claim = %#v, want only the uncertain row", recoveryRows)
	}
	sendRows, err := repo.ClaimDue(ctx, nil, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if len(sendRows) != 1 || sendRows[0].ID != prepared.ID {
		t.Fatalf("send claim = %#v, want only the prepared row", sendRows)
	}

	if err := repo.MarkBroadcastRecoveryRequired(ctx, uncertain.ID, "broadcast outcome unknown"); err != nil {
		t.Fatalf("mark broadcast recovery required: %v", err)
	}
	row := reloadOutboundTransaction(t, db, uncertain.ID)
	if row.Status != models.OutboundStatusNeedsOperatorAction || row.ErrorCategory != "broadcast_recovery_required" ||
		row.BroadcastAttemptedAt == nil || row.NextRunAt != nil || row.LockedUntil != nil {
		t.Fatalf("recovery-marked row = %#v, want operator action with preserved attempt evidence", row)
	}
	if err := repo.MarkBroadcastRecoveryRequired(ctx, uncertain.ID, "second attempt"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("second recovery marker error = %v, want gorm.ErrRecordNotFound", err)
	}
	sendRows, err = repo.ClaimDue(ctx, nil, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("claim due after recovery: %v", err)
	}
	for _, row := range sendRows {
		if row.ID == uncertain.ID {
			t.Fatalf("operator-marked outbound was handed to the sender: %#v", row)
		}
	}
}

// TestOutboundTransactionRepoAcquireSendGrantSerializesConcurrentWorkers replays
// the A6-R1 race: two workers acting on the same outbound row must not be able to
// share one chain reservation. Exactly one receives a grant; the loser receives a
// block carrying no reservation at all, so it cannot release the winner's
// resource, and a third intent from the same wallet is still refused while the
// winner's sequence reservation is occupied.
func TestOutboundTransactionRepoAcquireSendGrantSerializesConcurrentWorkers(t *testing.T) {
	db, ctx, repo, outbound, walletAddress := seedGrantableOutboundForTest(t)
	widenOutboundGrantTestPool(t, db)

	start := make(chan struct{})
	type grantOutcome struct {
		grant *OutboundSendGrant
		block *OutboundSendBlock
		err   error
	}
	results := make(chan grantOutcome, 2)
	for range 2 {
		go func() {
			<-start
			grant, block, err := repo.AcquireSendGrant(ctx, outbound, walletAddress, 30*time.Second)
			results <- grantOutcome{grant: grant, block: block, err: err}
		}()
	}
	close(start)

	var winner *OutboundSendGrant
	var loser *OutboundSendBlock
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("acquire send grant: %v", result.err)
		}
		if result.grant != nil {
			if winner != nil {
				t.Fatal("two workers received a send grant for one outbound row")
			}
			winner = result.grant
			continue
		}
		loser = result.block
	}
	if winner == nil {
		t.Fatal("no worker received a send grant")
	}
	if winner.Outbound.Status != models.OutboundStatusBroadcastAttempted || winner.Outbound.Attempts != 1 || winner.Outbound.BroadcastAttemptedAt == nil {
		t.Fatalf("granted snapshot = %#v, want exactly one recorded broadcast attempt", winner.Outbound)
	}
	if loser == nil || loser.Reason == "" || !loser.HardEvidence {
		t.Fatalf("loser block = %#v, want a hard-evidence refusal", loser)
	}

	var reservations []models.OutboundChainResourceReservation
	if err := db.Where("outbound_transaction_id = ?", outbound.ID).Find(&reservations).Error; err != nil {
		t.Fatalf("list reservations: %v", err)
	}
	if len(reservations) != 1 {
		t.Fatalf("reservations = %#v, want exactly one chain resource for the granted attempt", reservations)
	}
	if reservations[0].ID != winner.Reservation.ID || reservations[0].Status != models.OutboundResourceReservationReserved {
		t.Fatalf("loser mutated the winner's reservation: %#v", reservations[0])
	}

	// The loser must not be able to release the winner's resource even if it tried:
	// it holds no reservation id, and the reservation is still reserved after both
	// workers returned.
	var releasedRows int64
	if err := db.Model(&models.OutboundChainResourceReservation{}).
		Where("status = ?", models.OutboundResourceReservationReleased).
		Count(&releasedRows).Error; err != nil {
		t.Fatalf("count released reservations: %v", err)
	}
	if releasedRows != 0 {
		t.Fatalf("released reservations = %d, want none after the losing worker returned", releasedRows)
	}

	// A third intent of the same resource from the same wallet is still refused:
	// the sequence resource is occupied by the granted attempt.
	thirdIntent := createOutboundForResourceForTest(t, repo, ctx, outbound.ResourceType, outbound.ResourceID, outbound.MerchantID, outbound.WalletID, outbound.DomainID, "third-intent")
	if _, _, err := repo.ReserveSequence(ctx, thirdIntent, walletAddress, 30*time.Second); !errors.Is(err, ErrOutboundResourceAlreadyReserved) {
		t.Fatalf("third intent reservation error = %v, want ErrOutboundResourceAlreadyReserved", err)
	}
}

// TestOutboundTransactionRepoAcquireSendGrantRejectsSiblingEvidenceAndIntentChange
// covers the two non-race refusals: a sibling intent of the same resource that
// already carries broadcast evidence, and a caller snapshot that no longer
// matches the persisted intent. Both must refuse without leaving a reservation.
func TestOutboundTransactionRepoAcquireSendGrantRejectsSiblingEvidenceAndIntentChange(t *testing.T) {
	db, ctx, repo, outbound, walletAddress := seedGrantableOutboundForTest(t)
	now := time.Now()

	sibling := createOutboundForResourceForTest(t, repo, ctx, outbound.ResourceType, outbound.ResourceID, outbound.MerchantID, outbound.WalletID, outbound.DomainID, "sibling-broadcasted")
	mustUpdateOutboundTransaction(t, db, sibling.ID, map[string]any{"status": models.OutboundStatusBroadcasted, "tx_hash": "0xsibling", "broadcasted_at": &now})

	grant, block, err := repo.AcquireSendGrant(ctx, outbound, walletAddress, 30*time.Second)
	if err != nil {
		t.Fatalf("acquire send grant with sibling evidence: %v", err)
	}
	if grant != nil || block == nil || block.Reason != "conflicting_resource_intent" || !block.HardEvidence {
		t.Fatalf("sibling-evidence grant=%v block=%#v, want conflicting_resource_intent refusal", grant, block)
	}
	requirePostgresCount(t, db, &models.OutboundChainResourceReservation{}, "outbound_transaction_id = ?", outbound.ID, 0)
	if row := reloadOutboundTransaction(t, db, outbound.ID); row.Status != models.OutboundStatusPrepared {
		t.Fatalf("refused row = %#v, want untouched prepared row", row)
	}

	mustUpdateOutboundTransaction(t, db, sibling.ID, map[string]any{"status": models.OutboundStatusAbandoned})
	mustUpdateOutboundTransaction(t, db, outbound.ID, map[string]any{"amount_raw": "12345"})

	grant, block, err = repo.AcquireSendGrant(ctx, outbound, walletAddress, 30*time.Second)
	if err != nil {
		t.Fatalf("acquire send grant after intent change: %v", err)
	}
	if grant != nil || block == nil || block.Reason != "intent_changed_since_precheck" || block.HardEvidence {
		t.Fatalf("intent-change grant=%v block=%#v, want intent_changed_since_precheck refusal", grant, block)
	}
	requirePostgresCount(t, db, &models.OutboundChainResourceReservation{}, "outbound_transaction_id = ?", outbound.ID, 0)
}

func TestOutboundTransactionRepoInspectSendableForExecutionBlocksStaleClaims(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}, &models.Refund{}, &models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound and resource tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)
	now := time.Now()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	withdrawalID := uuid.New()
	seedProcessingWithdrawalForTest(t, db, withdrawalID, merchantID, domainID, walletID)

	outbound := createOutboundForResourceForTest(t, repo, ctx, models.OutboundResourceWithdrawal, withdrawalID, merchantID, walletID, &domainID, "inspect")
	row, reason, hardEvidence, err := repo.InspectSendableForExecution(ctx, outbound.ID)
	if err != nil {
		t.Fatalf("inspect sendable outbound: %v", err)
	}
	if row == nil || row.ID != outbound.ID || row.AmountRaw != outbound.AmountRaw || reason != "" || hardEvidence {
		t.Fatalf("inspect sendable = %#v/%q/%v, want the persisted row and no block", row, reason, hardEvidence)
	}

	updateWithdrawal := func(updates map[string]any) {
		t.Helper()
		if err := db.Model(&models.WithdrawalRequest{}).Where("id = ?", withdrawalID).Updates(updates).Error; err != nil {
			t.Fatalf("update withdrawal: %v", err)
		}
	}

	updateWithdrawal(map[string]any{"status": models.WithdrawalStatusFinalized, "finalized_at": &now})
	_, reason, hardEvidence, err = repo.InspectSendableForExecution(ctx, outbound.ID)
	if err != nil {
		t.Fatalf("inspect settled withdrawal: %v", err)
	}
	if reason != "resource_already_finalized" || !hardEvidence {
		t.Fatalf("settled withdrawal inspection = %q (hard=%v), want resource_already_finalized with hard evidence", reason, hardEvidence)
	}

	// A resource that already carries its own broadcast timestamp (but no hash yet)
	// is broadcast evidence and must never be sent again.
	updateWithdrawal(map[string]any{"status": models.WithdrawalStatusProcessing, "finalized_at": nil, "tx_hash": "", "broadcasted_at": &now})
	_, reason, hardEvidence, err = repo.InspectSendableForExecution(ctx, outbound.ID)
	if err != nil {
		t.Fatalf("inspect broadcasted resource: %v", err)
	}
	if reason != "resource_already_broadcast" || !hardEvidence {
		t.Fatalf("broadcasted-resource inspection = %q (hard=%v), want resource_already_broadcast with hard evidence", reason, hardEvidence)
	}

	updateWithdrawal(map[string]any{"tx_hash": "0xsettled", "broadcasted_at": nil})
	_, reason, _, err = repo.InspectSendableForExecution(ctx, outbound.ID)
	if err != nil {
		t.Fatalf("inspect withdrawal with hash: %v", err)
	}
	if reason != "resource_already_broadcast" {
		t.Fatalf("withdrawal-hash inspection = %q, want resource_already_broadcast", reason)
	}
	updateWithdrawal(map[string]any{"tx_hash": ""})

	// stale claim: another worker broadcast the row after it was claimed
	stale := createOutboundForResourceForTest(t, repo, ctx, models.OutboundResourceWithdrawal, withdrawalID, merchantID, walletID, &domainID, "stale")
	mustUpdateOutboundTransaction(t, db, stale.ID, map[string]any{"status": models.OutboundStatusBroadcasted, "tx_hash": "0xstale", "broadcasted_at": &now})
	_, reason, hardEvidence, err = repo.InspectSendableForExecution(ctx, stale.ID)
	if err != nil {
		t.Fatalf("inspect stale claim: %v", err)
	}
	if reason == "" || !hardEvidence {
		t.Fatalf("stale-claim inspection = %q (hard=%v), want a hard-evidence block", reason, hardEvidence)
	}

	// terminal rows and terminal categories are fail-closed for every status
	terminal := createOutboundForResourceForTest(t, repo, ctx, models.OutboundResourceWithdrawal, withdrawalID, merchantID, walletID, &domainID, "terminal")
	mustUpdateOutboundTransaction(t, db, terminal.ID, map[string]any{"status": models.OutboundStatusFailed, "error_category": "terminal_failed", "finalized_at": &now})
	_, reason, hardEvidence, err = repo.InspectSendableForExecution(ctx, terminal.ID)
	if err != nil {
		t.Fatalf("inspect terminal outbound: %v", err)
	}
	if reason != "terminal_failed" || !hardEvidence {
		t.Fatalf("terminal inspection = %q (hard=%v), want terminal_failed with hard evidence", reason, hardEvidence)
	}
	preparedTerminal := createOutboundForResourceForTest(t, repo, ctx, models.OutboundResourceWithdrawal, withdrawalID, merchantID, walletID, &domainID, "prepared-terminal-category")
	mustUpdateOutboundTransaction(t, db, preparedTerminal.ID, map[string]any{"status": models.OutboundStatusPrepared, "error_category": "replacement_requires_operator"})
	_, reason, hardEvidence, err = repo.InspectSendableForExecution(ctx, preparedTerminal.ID)
	if err != nil {
		t.Fatalf("inspect prepared row with terminal category: %v", err)
	}
	if reason != "terminal_failed" || !hardEvidence {
		t.Fatalf("prepared-terminal-category inspection = %q (hard=%v), want terminal_failed with hard evidence", reason, hardEvidence)
	}

	// a sibling intent of the same resource that already broadcasted blocks the row
	refundID := uuid.New()
	seedProcessingRefundForTest(t, db, refundID, merchantID, domainID)
	refundOutbound := createOutboundForResourceForTest(t, repo, ctx, models.OutboundResourceRefund, refundID, merchantID, walletID, &domainID, "refund-primary")
	refundSibling := createOutboundForResourceForTest(t, repo, ctx, models.OutboundResourceRefund, refundID, merchantID, walletID, &domainID, "refund-sibling")
	mustUpdateOutboundTransaction(t, db, refundSibling.ID, map[string]any{"status": models.OutboundStatusBroadcasted, "tx_hash": "0xsibling", "broadcasted_at": &now})
	_, reason, hardEvidence, err = repo.InspectSendableForExecution(ctx, refundOutbound.ID)
	if err != nil {
		t.Fatalf("inspect sibling intent: %v", err)
	}
	if reason != "conflicting_resource_intent" || !hardEvidence {
		t.Fatalf("sibling-intent inspection = %q (hard=%v), want conflicting_resource_intent with hard evidence", reason, hardEvidence)
	}
}

// TestOutboundTransactionRepoAcquireSendGrantRelocksResourceAfterPrecheck covers
// the window between the executor's unlocked pre-check and the grant: the resource
// may settle or broadcast in between, so the grant must refuse under its locks and
// leave no chain resource behind.
func TestOutboundTransactionRepoAcquireSendGrantRelocksResourceAfterPrecheck(t *testing.T) {
	db, ctx, repo, outbound, walletAddress := seedGrantableOutboundForTest(t)

	precheck, reason, hardEvidence, err := repo.InspectSendableForExecution(ctx, outbound.ID)
	if err != nil || precheck == nil || reason != "" || hardEvidence {
		t.Fatalf("pre-check = %#v/%q/%v/%v, want a sendable persisted row", precheck, reason, hardEvidence, err)
	}

	now := time.Now()
	updateWithdrawal := func(updates map[string]any) {
		t.Helper()
		if err := db.Model(&models.WithdrawalRequest{}).Where("id = ?", outbound.ResourceID).Updates(updates).Error; err != nil {
			t.Fatalf("update withdrawal: %v", err)
		}
	}

	for _, tc := range []struct {
		name    string
		updates map[string]any
		reason  string
	}{
		{name: "settled after precheck", updates: map[string]any{"status": models.WithdrawalStatusFinalized, "finalized_at": &now}, reason: "resource_already_finalized"},
		{name: "broadcast timestamp after precheck", updates: map[string]any{"status": models.WithdrawalStatusProcessing, "finalized_at": nil, "broadcasted_at": &now}, reason: "resource_already_broadcast"},
		{name: "hash after precheck", updates: map[string]any{"tx_hash": "0xlate", "broadcasted_at": nil}, reason: "resource_already_broadcast"},
	} {
		updateWithdrawal(tc.updates)
		grant, block, err := repo.AcquireSendGrant(ctx, *precheck, walletAddress, 30*time.Second)
		if err != nil {
			t.Fatalf("%s: acquire send grant: %v", tc.name, err)
		}
		if grant != nil || block == nil || block.Reason != tc.reason {
			t.Fatalf("%s: grant=%v block=%#v, want %q refusal", tc.name, grant, block, tc.reason)
		}
		requirePostgresCount(t, db, &models.OutboundChainResourceReservation{}, "outbound_transaction_id = ?", outbound.ID, 0)
	}
	if row := reloadOutboundTransaction(t, db, outbound.ID); row.Status != models.OutboundStatusPrepared || row.BroadcastAttemptedAt != nil {
		t.Fatalf("refused row = %#v, want untouched prepared row", row)
	}
}

// TestOutboundTransactionRepoTerminalEvidenceIsFailClosed proves that terminal
// broadcast evidence can neither be sent nor be erased by late lifecycle writes.
func TestOutboundTransactionRepoTerminalEvidenceIsFailClosed(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}, &models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	resourceID := uuid.New()
	seedProcessingWithdrawalForTest(t, db, resourceID, merchantID, domainID, walletID)

	preparedTerminal := createOutboundForResourceForTest(t, repo, ctx, models.OutboundResourceWithdrawal, resourceID, merchantID, walletID, &domainID, "prepared-terminal-category")
	mustUpdateOutboundTransaction(t, db, preparedTerminal.ID, map[string]any{"status": models.OutboundStatusPrepared, "error_category": "terminal_failed"})
	signedUncertain := createOutboundForResourceForTest(t, repo, ctx, models.OutboundResourceWithdrawal, resourceID, merchantID, walletID, &domainID, "signed-uncertain-category")
	mustUpdateOutboundTransaction(t, db, signedUncertain.ID, map[string]any{"status": models.OutboundStatusSigned, "error_category": "broadcast_uncertain"})

	claimed, err := repo.ClaimDue(ctx, []string{models.OutboundResourceWithdrawal}, 50, 30*time.Second)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	for _, row := range claimed {
		if row.ID == preparedTerminal.ID || row.ID == signedUncertain.ID {
			t.Fatalf("claim handed out a terminal-category row: %#v", row)
		}
	}

	for _, id := range []uuid.UUID{preparedTerminal.ID, signedUncertain.ID} {
		row, reason, hardEvidence, err := repo.InspectSendableForExecution(ctx, id)
		if err != nil {
			t.Fatalf("inspect terminal-category row %s: %v", id, err)
		}
		if row == nil || reason != "terminal_failed" || !hardEvidence {
			t.Fatalf("terminal-category inspection of %s = %q (hard=%v), want terminal_failed with hard evidence", id, reason, hardEvidence)
		}
		grant, block, err := repo.AcquireSendGrant(ctx, *row, "0xgrant", 30*time.Second)
		if err != nil || grant != nil || block == nil || block.Reason != "terminal_failed" {
			t.Fatalf("grant on terminal-category row %s = %v/%#v/%v, want terminal_failed refusal", id, grant, block, err)
		}
	}

	// Late lifecycle writes must not erase the terminal evidence.
	if err := repo.MarkBroadcastAttempted(ctx, preparedTerminal.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("generic broadcast attempt on terminal row error = %v, want gorm.ErrRecordNotFound", err)
	}
	if err := repo.DeferForNetworkState(ctx, preparedTerminal.ID, "admin disabled outbound", time.Minute); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("network-state defer on terminal row error = %v, want gorm.ErrRecordNotFound", err)
	}
	if err := repo.MarkFailed(ctx, preparedTerminal.ID, errors.New("worker failure"), 30*time.Second); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("mark failed on terminal row error = %v, want gorm.ErrRecordNotFound", err)
	}
	if row := reloadOutboundTransaction(t, db, preparedTerminal.ID); row.Status != models.OutboundStatusPrepared || row.ErrorCategory != "terminal_failed" || row.NextRunAt != nil {
		t.Fatalf("terminal row after late writes = %#v, want unchanged prepared terminal_failed row", row)
	}

	// The operator path may add a task but must keep the original evidence.
	if err := repo.MarkNeedsOperatorAction(ctx, preparedTerminal.ID, "broadcast_recovery_required", "operator review"); err != nil {
		t.Fatalf("mark operator action on terminal row: %v", err)
	}
	if row := reloadOutboundTransaction(t, db, preparedTerminal.ID); row.Status != models.OutboundStatusNeedsOperatorAction || row.ErrorCategory != "terminal_failed" {
		t.Fatalf("operator-marked terminal row = %#v, want preserved terminal_failed category", row)
	}
}

// TestOutboundTransactionRepoRecordObservedBroadcastHashBindsAttempt covers the
// recovery window: an uncertain attempt may already have been moved to the
// operator queue when its transfer lands, and the observed hash may then only be
// bound to that exact attempt, never overwriting an existing hash and never
// granting a new send right.
func TestOutboundTransactionRepoRecordObservedBroadcastHashBindsAttempt(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)

	row := createOutboundTransactionForTest(t, repo, ctx, models.OutboundResourceWithdrawal, constants.Ethereum)
	if err := repo.MarkBroadcastAttempted(ctx, row.ID); err != nil {
		t.Fatalf("mark broadcast attempted: %v", err)
	}
	attempted := reloadOutboundTransaction(t, db, row.ID)
	if attempted.BroadcastAttemptedAt == nil {
		t.Fatalf("attempted row = %#v, want a broadcast attempt timestamp", attempted)
	}
	wrongAttempt := attempted.BroadcastAttemptedAt.Add(-time.Hour)
	if err := repo.RecordObservedBroadcastHash(ctx, row.ID, &wrongAttempt, "0xwrong"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("hash backfill with a foreign attempt error = %v, want gorm.ErrRecordNotFound", err)
	}
	if stored := reloadOutboundTransaction(t, db, row.ID); stored.TxHash != "" {
		t.Fatalf("foreign attempt wrote a hash: %#v", stored)
	}

	// The recovery loop routes the uncertain attempt to the operator queue.
	if err := repo.MarkBroadcastRecoveryRequired(ctx, row.ID, "broadcast outcome unknown"); err != nil {
		t.Fatalf("mark broadcast recovery required: %v", err)
	}
	if err := repo.RecordObservedBroadcastHash(ctx, row.ID, attempted.BroadcastAttemptedAt, "0xobserved"); err != nil {
		t.Fatalf("hash backfill for the granted attempt: %v", err)
	}
	stored := reloadOutboundTransaction(t, db, row.ID)
	if stored.TxHash != "0xobserved" || stored.Status != models.OutboundStatusNeedsOperatorAction || stored.BroadcastAttemptedAt == nil {
		t.Fatalf("backfilled row = %#v, want the observed hash on the operator-marked attempt", stored)
	}
	if err := repo.RecordObservedBroadcastHash(ctx, row.ID, attempted.BroadcastAttemptedAt, "0xoverwrite"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("second hash backfill error = %v, want gorm.ErrRecordNotFound", err)
	}
	if stored = reloadOutboundTransaction(t, db, row.ID); stored.TxHash != "0xobserved" {
		t.Fatalf("hash was overwritten: %#v", stored)
	}

	// The row must stay unsendable with the hash recorded.
	claimed, err := repo.ClaimDue(ctx, []string{models.OutboundResourceWithdrawal}, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	for _, candidate := range claimed {
		if candidate.ID == row.ID {
			t.Fatalf("row with recorded hash was claimed for send: %#v", candidate)
		}
	}
	if _, reason, hardEvidence, err := repo.InspectSendableForExecution(ctx, row.ID); err != nil || reason == "" || !hardEvidence {
		t.Fatalf("inspect backfilled row = %q (hard=%v)/%v, want a hard-evidence block", reason, hardEvidence, err)
	}
}

func widenOutboundGrantTestPool(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get test sql db: %v", err)
	}
	// The send-grant race test needs two genuinely concurrent transactions, but
	// the shared outbox helper pins the pool to a single connection.
	sqlDB.SetMaxOpenConns(4)
}

func seedProcessingWithdrawalForTest(t *testing.T, db *gorm.DB, id, merchantID, domainID, walletID uuid.UUID) {
	t.Helper()
	request := models.WithdrawalRequest{
		ID:             id,
		MerchantID:     merchantID,
		DomainID:       &domainID,
		WalletID:       walletID,
		Chain:          "ethereum",
		Symbol:         "ETH",
		Decimals:       18,
		ToAddress:      "0xto",
		AmountRaw:      "10",
		Status:         models.WithdrawalStatusProcessing,
		IdempotencyKey: "withdrawal-" + uuid.NewString(),
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatalf("seed processing withdrawal %s: %v", id, err)
	}
}

func seedProcessingRefundForTest(t *testing.T, db *gorm.DB, id, merchantID, domainID uuid.UUID) {
	t.Helper()
	refund := models.Refund{
		ID:         id,
		MerchantID: merchantID,
		DomainID:   domainID,
		PaymentID:  uuid.New(),
		AmountRaw:  "10",
		Status:     models.RefundStatusProcessing,
		Chain:      "ethereum",
		Symbol:     "ETH",
		Decimals:   18,
		ToAddress:  "0xto",
	}
	if err := db.Create(&refund).Error; err != nil {
		t.Fatalf("seed processing refund %s: %v", id, err)
	}
}

func createOutboundForResourceForTest(t *testing.T, repo *OutboundTransactionRepo, ctx context.Context, resourceType string, resourceID, merchantID, walletID uuid.UUID, domainID *uuid.UUID, keySuffix string) models.OutboundTransaction {
	t.Helper()
	params := outboundTransactionRepoTestParams(resourceType, constants.Ethereum)
	params.IdempotencyKey += ":" + keySuffix + ":" + uuid.NewString()
	params.ResourceID = resourceID
	params.MerchantID = merchantID
	params.WalletID = walletID
	params.DomainID = domainID
	outbound, _, err := repo.Create(ctx, params)
	if err != nil {
		t.Fatalf("create outbound %s:%s: %v", resourceType, keySuffix, err)
	}
	return *outbound
}

// seedGrantableOutboundForTest builds a processing withdrawal with one prepared
// outbound row and returns the wallet address its sequence reservation uses.
func seedGrantableOutboundForTest(t *testing.T) (*gorm.DB, context.Context, *OutboundTransactionRepo, models.OutboundTransaction, string) {
	t.Helper()
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.WithdrawalRequest{}, &models.Refund{}, &models.OutboundTransaction{}, &models.OutboundChainResourceReservation{}); err != nil {
		t.Fatalf("automigrate outbound grant tables: %v", err)
	}
	ctx := context.Background()
	repo := NewOutboundTransactionRepo(db)
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	resourceID := uuid.New()
	seedProcessingWithdrawalForTest(t, db, resourceID, merchantID, domainID, walletID)
	outbound := createOutboundForResourceForTest(t, repo, ctx, models.OutboundResourceWithdrawal, resourceID, merchantID, walletID, &domainID, "grant")
	return db, ctx, repo, outbound, "0x" + strings.ReplaceAll(walletID.String(), "-", "")
}

// TestOutboundTransactionRepoAcquireSendGrantIsSingleIntentPerResource proves the
// resource-level exclusivity directly: once one intent holds the granted attempt,
// a second intent of the same withdrawal/refund can never obtain a grant, and the
// resource keeps exactly one chain reservation.
func TestOutboundTransactionRepoAcquireSendGrantIsSingleIntentPerResource(t *testing.T) {
	db, ctx, repo, outbound, walletAddress := seedGrantableOutboundForTest(t)

	sibling := createOutboundForResourceForTest(t, repo, ctx, outbound.ResourceType, outbound.ResourceID, outbound.MerchantID, outbound.WalletID, outbound.DomainID, "sibling-intent")

	winner, block, err := repo.AcquireSendGrant(ctx, outbound, walletAddress, 30*time.Second)
	if err != nil {
		t.Fatalf("acquire send grant for the primary intent: %v", err)
	}
	if winner == nil || block != nil {
		t.Fatalf("primary grant = %v/%#v, want a granted send right", winner, block)
	}
	if winner.Outbound.Status != models.OutboundStatusBroadcastAttempted || winner.Outbound.TxHash != "" {
		t.Fatalf("granted snapshot = %#v, want an attempted row without a hash", winner.Outbound)
	}

	siblingGrant, siblingBlock, err := repo.AcquireSendGrant(ctx, sibling, walletAddress, 30*time.Second)
	if err != nil {
		t.Fatalf("acquire send grant for the sibling intent: %v", err)
	}
	if siblingGrant != nil || siblingBlock == nil || siblingBlock.Reason != "conflicting_resource_intent" || !siblingBlock.HardEvidence {
		t.Fatalf("sibling grant = %v/%#v, want a conflicting_resource_intent refusal", siblingGrant, siblingBlock)
	}
	if row := reloadOutboundTransaction(t, db, sibling.ID); row.Status != models.OutboundStatusPrepared || row.BroadcastAttemptedAt != nil {
		t.Fatalf("sibling row = %#v, want an untouched prepared row", row)
	}
	reservations := []models.OutboundChainResourceReservation{}
	if err := db.Where("owner_type = ? AND owner_id = ?", outbound.ResourceType, outbound.ResourceID).Find(&reservations).Error; err != nil {
		t.Fatalf("list resource reservations: %v", err)
	}
	if len(reservations) != 1 || reservations[0].OutboundTransactionID != outbound.ID || reservations[0].Status != models.OutboundResourceReservationReserved {
		t.Fatalf("resource reservations = %#v, want exactly the primary intent's reserved chain resource", reservations)
	}
}

// TestOutboundTransactionRepoAcquireSendGrantLocksResourceBeforeOutbound proves
// the approved lock order without string inspection: while a separate connection
// holds the linked withdrawal row, the grant must be waiting on that resource
// query and must not yet hold the outbound row, so a third connection can still
// take the outbound row FOR UPDATE NOWAIT. With the reversed order this NOWAIT
// would fail (55P03).
func TestOutboundTransactionRepoAcquireSendGrantLocksResourceBeforeOutbound(t *testing.T) {
	db, ctx, repo, outbound, walletAddress := seedGrantableOutboundForTest(t)
	widenOutboundGrantTestPool(t, db)

	holder := db.Begin()
	if holder.Error != nil {
		t.Fatalf("begin resource holder: %v", holder.Error)
	}
	committed := false
	defer func() {
		if !committed {
			_ = holder.Rollback().Error
		}
	}()
	if err := holder.Exec("SELECT id FROM withdrawal_requests WHERE id = ? FOR UPDATE", outbound.ResourceID).Error; err != nil {
		t.Fatalf("hold linked resource row: %v", err)
	}

	type grantOutcome struct {
		grant *OutboundSendGrant
		block *OutboundSendBlock
		err   error
	}
	results := make(chan grantOutcome, 1)
	go func() {
		grant, block, err := repo.AcquireSendGrant(ctx, outbound, walletAddress, 30*time.Second)
		results <- grantOutcome{grant: grant, block: block, err: err}
	}()

	if err := waitForGrantWaitingOnResource(t, db); err != nil {
		t.Fatal(err)
	}
	var lockedOutboundID string
	if err := db.Raw("SELECT id::text FROM outbound_transactions WHERE id = ? FOR UPDATE NOWAIT", outbound.ID).Scan(&lockedOutboundID).Error; err != nil {
		t.Fatalf("outbound row must still be lock-free while the grant waits on the resource: %v", err)
	}
	if lockedOutboundID != outbound.ID.String() {
		t.Fatalf("NOWAIT read returned %q, want %s", lockedOutboundID, outbound.ID)
	}

	if err := holder.Commit().Error; err != nil {
		t.Fatalf("release linked resource row: %v", err)
	}
	committed = true

	result := <-results
	if result.err != nil {
		t.Fatalf("acquire send grant: %v", result.err)
	}
	if result.grant == nil {
		t.Fatalf("grant = nil (block %#v), want a granted send right", result.block)
	}
	if row := reloadOutboundTransaction(t, db, outbound.ID); row.Status != models.OutboundStatusBroadcastAttempted || row.Attempts != 1 {
		t.Fatalf("row = %#v, want exactly the granted attempt", row)
	}
	if reservations := listOutboundReservationsForTest(t, db, outbound.ID); len(reservations) != 1 {
		t.Fatalf("reservations = %#v, want exactly one granted chain resource", reservations)
	}
}

// TestOutboundTransactionRepoAcquireSendGrantRollsBackWhenAttemptTransitionDoesNotApply
// forces the guarded attempt update to affect zero rows (synthetic trigger) and
// requires the whole grant transaction to roll back: no send right, an untouched
// prepared row and no reservation left behind.
func TestOutboundTransactionRepoAcquireSendGrantRollsBackWhenAttemptTransitionDoesNotApply(t *testing.T) {
	db, ctx, repo, outbound, walletAddress := seedGrantableOutboundForTest(t)

	// Test-schema-only trigger: it exists purely to synthesise "the guarded update
	// did not apply". Nothing like this ships in a migration.
	if err := db.Exec(`CREATE FUNCTION skip_grant_attempt_transition() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NULL; END'`).Error; err != nil {
		t.Fatalf("create skip trigger function: %v", err)
	}
	if err := db.Exec(`CREATE TRIGGER skip_grant_attempt_transition BEFORE UPDATE ON outbound_transactions FOR EACH ROW WHEN (OLD.status = 'prepared' AND NEW.status = 'broadcast_attempted') EXECUTE FUNCTION skip_grant_attempt_transition()`).Error; err != nil {
		t.Fatalf("create skip trigger: %v", err)
	}

	grant, block, err := repo.AcquireSendGrant(ctx, outbound, walletAddress, 30*time.Second)
	if err != nil {
		t.Fatalf("acquire send grant: %v", err)
	}
	if grant != nil {
		t.Fatalf("grant = %#v, want no send right when the attempt transition does not apply", grant)
	}
	if block == nil || block.Reason != "send_grant_lost_race" || !block.HardEvidence {
		t.Fatalf("block = %#v, want a send_grant_lost_race refusal", block)
	}
	if row := reloadOutboundTransaction(t, db, outbound.ID); row.Status != models.OutboundStatusPrepared || row.BroadcastAttemptedAt != nil || row.Attempts != 0 {
		t.Fatalf("row = %#v, want an untouched prepared row", row)
	}
	if reservations := listOutboundReservationsForTest(t, db, outbound.ID); len(reservations) != 0 {
		t.Fatalf("reservations = %#v, want the failed grant's reservation rolled back", reservations)
	}
}

func listOutboundReservationsForTest(t *testing.T, db *gorm.DB, outboundID uuid.UUID) []models.OutboundChainResourceReservation {
	t.Helper()
	var rows []models.OutboundChainResourceReservation
	if err := db.Where("outbound_transaction_id = ?", outboundID).Find(&rows).Error; err != nil {
		t.Fatalf("list reservations for %s: %v", outboundID, err)
	}
	return rows
}

// waitForGrantWaitingOnResource waits until a grant transaction is observably
// blocked on its resource query.
func waitForGrantWaitingOnResource(t *testing.T, db *gorm.DB) error {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int64
		if err := db.Raw(`SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock'
			  AND query ILIKE '%withdrawal_requests%'
			  AND query ILIKE '%for update%'`).Scan(&waiting).Error; err != nil {
			return errors.New("inspect pg_stat_activity: " + err.Error())
		}
		if waiting > 0 {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.New("grant never appeared blocked on the resource row")
}

func mustUpdateOutboundTransaction(t *testing.T, db *gorm.DB, id uuid.UUID, updates map[string]any) {
	t.Helper()
	if err := db.Model(&models.OutboundTransaction{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		t.Fatalf("update outbound %s: %v", id, err)
	}
}

func reloadOutboundTransaction(t *testing.T, db *gorm.DB, id uuid.UUID) models.OutboundTransaction {
	t.Helper()
	var row models.OutboundTransaction
	if err := db.First(&row, "id = ?", id).Error; err != nil {
		t.Fatalf("reload outbound %s: %v", id, err)
	}
	return row
}

func createOutboundTransactionForTest(t *testing.T, repo *OutboundTransactionRepo, ctx context.Context, resourceType string, chainID constants.ChainID) models.OutboundTransaction {
	t.Helper()
	params := outboundTransactionRepoTestParams(resourceType, chainID)
	params.IdempotencyKey += ":" + uuid.NewString()
	outbound, _, err := repo.Create(ctx, params)
	if err != nil {
		t.Fatalf("create outbound %s: %v", resourceType, err)
	}
	return *outbound
}

func outboundTransactionRepoTestParams(resourceType string, chainID constants.ChainID) OutboundTransactionCreate {
	domainID := uuid.New()
	symbol := "ETH"
	decimals := uint8(18)
	amount := "1000000000000000000"
	toAddress := "0xdestination"
	if chainID == constants.Bitcoin {
		symbol = "BTC"
		decimals = 8
		amount = "100000"
		toAddress = "bc1qdestination"
	}
	return OutboundTransactionCreate{
		IdempotencyKey: "outbound-test:" + resourceType + ":" + uuid.NewString(),
		ResourceType:   resourceType,
		ResourceID:     uuid.New(),
		MerchantID:     uuid.New(),
		DomainID:       &domainID,
		WalletID:       uuid.New(),
		ChainID:        chainID,
		ChainName:      constants.ChainName(chainID),
		Symbol:         symbol,
		Decimals:       decimals,
		AmountRaw:      amount,
		ToAddress:      toAddress,
		ActorID:        "admin@example.com",
		CorrelationID:  "corr-" + uuid.NewString(),
	}
}
