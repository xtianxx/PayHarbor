package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"core/constants"
	"core/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// A10: a sweep hash is broadcast evidence, not finality. These tests cover the
// claim/retry/attempt fences, the single-transaction broadcast record, its
// rollback points and the sweep send grant.

type sweepBoundaryFixture struct {
	db         *gorm.DB
	ctx        context.Context
	jobs       *SweepJobRepo
	outbound   *OutboundTransactionRepo
	ledger     *LedgerRepo
	merchantID uuid.UUID
	domainID   uuid.UUID
	walletID   uuid.UUID
	txModel    models.Transaction
	job        models.SweepJob
	outboundTx models.OutboundTransaction
}

func seedSweepBoundaryFixture(t *testing.T) *sweepBoundaryFixture {
	t.Helper()
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(
		&models.Merchant{}, &models.Domain{}, &models.Wallet{},
		&models.Transaction{}, &models.SweepJob{},
		&models.OutboundTransaction{}, &models.OutboundChainResourceReservation{},
		&models.LedgerEntry{}, &models.MoneyEventOutbox{}, &models.ReconciliationJob{},
	); err != nil {
		t.Fatalf("automigrate sweep boundary tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	ledger := NewLedgerRepo(db)
	source := ledgerTestTransaction(merchantID, domainID, walletID, "sweep-boundary-"+uuid.NewString(), "1000")
	finalizedAt := time.Now().Add(-time.Minute)
	source.FinalizedAt = &finalizedAt
	if err := db.Create(&source).Error; err != nil {
		t.Fatalf("seed source transaction: %v", err)
	}
	if err := ledger.PostStandaloneDepositAvailable(ctx, source); err != nil {
		t.Fatalf("seed available balance: %v", err)
	}
	jobs := NewSweepJobRepo(db)
	job, queued, err := jobs.EnqueueForTransaction(ctx, source)
	if err != nil || !queued {
		t.Fatalf("enqueue sweep job: %v (queued=%v)", err, queued)
	}
	outboundRepo := NewOutboundTransactionRepo(db)
	outboundTx, _, err := outboundRepo.CreateForSweepJob(ctx, *job, source, "0xreserve")
	if err != nil {
		t.Fatalf("create sweep outbound: %v", err)
	}
	return &sweepBoundaryFixture{
		db: db, ctx: ctx, jobs: jobs, outbound: outboundRepo, ledger: ledger,
		merchantID: merchantID, domainID: domainID, walletID: walletID,
		txModel: source, job: *job, outboundTx: *outboundTx,
	}
}

// claim takes the job exactly as the worker does, so the send grant can prove the
// caller's lease.
func (f *sweepBoundaryFixture) claim(t *testing.T) models.SweepJob {
	t.Helper()
	mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"status": models.SweepJobStatusPending, "next_run_at": time.Now().Add(-time.Minute), "locked_until": nil})
	claimed, err := f.jobs.ClaimDue(f.ctx, 10, 2*time.Minute)
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	for _, job := range claimed {
		if job.ID == f.job.ID {
			f.job = job
			return job
		}
	}
	t.Fatalf("sweep job was not claimable: %#v", claimed)
	return models.SweepJob{}
}

func (f *sweepBoundaryFixture) attempt(t *testing.T) *OutboundSendGrant {
	t.Helper()
	if f.job.LockedUntil == nil {
		f.claim(t)
	}
	grant, block, err := f.outbound.AcquireSweepSendGrant(f.ctx, f.job, f.outboundTx.ID, "0xsource", 30*time.Second)
	if err != nil {
		t.Fatalf("acquire sweep send grant: %v", err)
	}
	if grant == nil {
		t.Fatalf("sweep send grant refused: %#v", block)
	}
	return grant
}

func mustUpdateSweepJobRow(t *testing.T, f *sweepBoundaryFixture, id uuid.UUID, updates map[string]any) {
	t.Helper()
	if err := f.db.Model(&models.SweepJob{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		t.Fatalf("update sweep job: %v", err)
	}
}

func mustUpdateSweepOutboundRow(t *testing.T, f *sweepBoundaryFixture, id uuid.UUID, updates map[string]any) {
	t.Helper()
	if err := f.db.Model(&models.OutboundTransaction{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		t.Fatalf("update sweep outbound: %v", err)
	}
}

func reloadSweepJobRow(t *testing.T, f *sweepBoundaryFixture, id uuid.UUID) models.SweepJob {
	t.Helper()
	var job models.SweepJob
	if err := f.db.First(&job, "id = ?", id).Error; err != nil {
		t.Fatalf("reload sweep job: %v", err)
	}
	return job
}

func reloadSweepOutboundRow(t *testing.T, f *sweepBoundaryFixture, id uuid.UUID) models.OutboundTransaction {
	t.Helper()
	var row models.OutboundTransaction
	if err := f.db.First(&row, "id = ?", id).Error; err != nil {
		t.Fatalf("reload sweep outbound: %v", err)
	}
	return row
}

func reloadReservationRow(t *testing.T, f *sweepBoundaryFixture, id uuid.UUID) models.OutboundChainResourceReservation {
	t.Helper()
	var row models.OutboundChainResourceReservation
	if err := f.db.First(&row, "id = ?", id).Error; err != nil {
		t.Fatalf("reload reservation: %v", err)
	}
	return row
}

func countSweepOccurrences(t *testing.T, f *sweepBoundaryFixture, eventType string) int64 {
	t.Helper()
	var count int64
	if err := f.db.Model(&models.MoneyEventOutbox{}).
		Where("aggregate_id = ? AND event_type = ?", f.job.ID.String(), eventType).
		Count(&count).Error; err != nil {
		t.Fatalf("count %s occurrences: %v", eventType, err)
	}
	return count
}

func TestSweepJobRepoClaimDueFencesBroadcastEvidence(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, f *sweepBoundaryFixture)
	}{
		{name: "job broadcast hash recorded", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"sweep_tx_hash": "0xrecorded"})
		}},
		{name: "job reconcile marker", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"operator_action": models.SweepOperatorActionReconcileBroadcast})
		}},
		{name: "job broadcast uncertain category", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"failure_category": models.SweepFailureCategoryBroadcastUncertain})
		}},
		{name: "outbound attempt evidence", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			f.attempt(t)
		}},
		{name: "outbound hash evidence", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			now := time.Now()
			mustUpdateSweepOutboundRow(t, f, f.outboundTx.ID, map[string]any{"status": models.OutboundStatusBroadcasted, "tx_hash": "0xout", "broadcasted_at": &now})
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := seedSweepBoundaryFixture(t)
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"status": models.SweepJobStatusFailed, "next_run_at": time.Now().Add(-time.Minute)})
			testCase.mutate(t, f)

			claimed, err := f.jobs.ClaimDue(f.ctx, 10, 2*time.Minute)
			if err != nil {
				t.Fatalf("claim due: %v", err)
			}
			for _, job := range claimed {
				if job.ID == f.job.ID {
					t.Fatal("a job with broadcast evidence must never be re-claimed for an automatic send")
				}
			}
		})
	}

	t.Run("clean pre-broadcast failure stays claimable", func(t *testing.T) {
		f := seedSweepBoundaryFixture(t)
		mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"status": models.SweepJobStatusFailed, "next_run_at": time.Now().Add(-time.Minute), "last_error": "no reserve wallet"})
		claimed, err := f.jobs.ClaimDue(f.ctx, 10, 2*time.Minute)
		if err != nil {
			t.Fatalf("claim due: %v", err)
		}
		for _, job := range claimed {
			if job.ID == f.job.ID {
				return
			}
		}
		t.Fatal("a pre-broadcast failure without attempt evidence must stay claimable")
	})
}

func TestSweepJobRepoRetryRefusesBroadcastEvidence(t *testing.T) {
	blocked := []struct {
		name   string
		mutate func(t *testing.T, f *sweepBoundaryFixture)
	}{
		{name: "job hash", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"sweep_tx_hash": "0xrecorded"})
		}},
		{name: "reconcile marker", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"operator_action": models.SweepOperatorActionReconcileBroadcast})
		}},
		{name: "outbound attempt", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			now := time.Now()
			mustUpdateSweepOutboundRow(t, f, f.outboundTx.ID, map[string]any{"status": models.OutboundStatusBroadcastAttempted, "broadcast_attempted_at": &now})
		}},
	}
	for _, testCase := range blocked {
		t.Run(testCase.name, func(t *testing.T) {
			f := seedSweepBoundaryFixture(t)
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"status": models.SweepJobStatusDeadLetter, "next_run_at": nil})
			testCase.mutate(t, f)

			err := f.jobs.RecordOperatorRecovery(f.ctx, f.job.ID, models.SweepRecoveryActionRetry, "operator retry", "", nil)
			if !errors.Is(err, ErrSweepBroadcastEvidencePresent) {
				t.Fatalf("retry error = %v, want ErrSweepBroadcastEvidencePresent", err)
			}
			job := reloadSweepJobRow(t, f, f.job.ID)
			if job.Status == models.SweepJobStatusFailed || job.NextRunAt != nil {
				t.Fatalf("refused retry re-queued the job: %#v", job)
			}
		})
	}

	t.Run("clean failure can retry", func(t *testing.T) {
		f := seedSweepBoundaryFixture(t)
		mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"status": models.SweepJobStatusDeadLetter, "next_run_at": nil, "last_error": "reservation rejected"})
		if err := f.jobs.RecordOperatorRecovery(f.ctx, f.job.ID, models.SweepRecoveryActionRetry, "operator retry", "", nil); err != nil {
			t.Fatalf("clean retry: %v", err)
		}
		job := reloadSweepJobRow(t, f, f.job.ID)
		if job.Status != models.SweepJobStatusFailed || job.NextRunAt == nil {
			t.Fatalf("clean retry job = %#v, want a scheduled retry", job)
		}
	})
}

func TestSweepJobRepoRecordSweepBroadcastObservedIsAtomicAndNotFinality(t *testing.T) {
	f := seedSweepBoundaryFixture(t)
	if err := f.ledger.CreateSweepHold(f.ctx, f.job, f.txModel); err != nil {
		t.Fatalf("create sweep hold: %v", err)
	}
	var holdBefore int64
	if err := f.db.Model(&models.LedgerEntry{}).Where("sweep_job_id = ? AND entry_type = ?", f.job.ID, models.LedgerEntryTypeSweepHold).Count(&holdBefore).Error; err != nil {
		t.Fatalf("count sweep hold legs: %v", err)
	}
	grant := f.attempt(t)

	if err := f.jobs.RecordSweepBroadcastObserved(f.ctx, f.job.ID, f.outboundTx.ID, grant.Reservation.ID, "0xsweep"); err != nil {
		t.Fatalf("record sweep broadcast: %v", err)
	}

	job := reloadSweepJobRow(t, f, f.job.ID)
	if job.Status != models.SweepJobStatusDeadLetter || job.SweepTxHash != "0xsweep" ||
		job.OperatorAction != models.SweepOperatorActionReconcileBroadcast ||
		job.FailureCategory != models.SweepFailureCategoryBroadcastUncertain {
		t.Fatalf("job = %#v, want a dead_letter reconcile marker with the observed hash", job)
	}
	outbound := reloadSweepOutboundRow(t, f, f.outboundTx.ID)
	if outbound.Status != models.OutboundStatusBroadcasted || outbound.TxHash != "0xsweep" || outbound.FinalizedAt != nil {
		t.Fatalf("outbound = %#v, want broadcasted without finality", outbound)
	}
	reservation := reloadReservationRow(t, f, grant.Reservation.ID)
	if reservation.Status != models.OutboundResourceReservationConsumed || reservation.TxHash != "0xsweep" {
		t.Fatalf("reservation = %#v, want the granted reservation consumed with the hash", reservation)
	}

	var holdAfter int64
	if err := f.db.Model(&models.LedgerEntry{}).Where("sweep_job_id = ? AND entry_type = ?", f.job.ID, models.LedgerEntryTypeSweepHold).Count(&holdAfter).Error; err != nil {
		t.Fatalf("count sweep hold legs after: %v", err)
	}
	if holdAfter != holdBefore || holdAfter == 0 {
		t.Fatalf("sweep hold legs = %d (before %d), want them untouched", holdAfter, holdBefore)
	}
	requireLedgerCount(t, f.db, 0, "sweep_job_id = ? AND entry_type = ?", f.job.ID, models.LedgerEntryTypeSweepRelease)
	if got := countSweepOccurrences(t, f, constants.WebhookEventSweepDeadLetteredV1); got != 1 {
		t.Fatalf("dead-lettered occurrences = %d, want 1", got)
	}
	if got := countSweepOccurrences(t, f, constants.WebhookEventSweepSucceededV1); got != 0 {
		t.Fatalf("succeeded occurrences = %d, want none for a broadcast-only record", got)
	}

	if err := f.jobs.RecordSweepBroadcastObserved(f.ctx, f.job.ID, f.outboundTx.ID, grant.Reservation.ID, "0xsweep"); err != nil {
		t.Fatalf("idempotent repeat: %v", err)
	}
	if got := countSweepOccurrences(t, f, constants.WebhookEventSweepDeadLetteredV1); got != 1 {
		t.Fatalf("dead-lettered occurrences after repeat = %d, want 1", got)
	}
	if err := f.jobs.RecordSweepBroadcastObserved(f.ctx, f.job.ID, f.outboundTx.ID, grant.Reservation.ID, "0xother"); !errors.Is(err, ErrSweepJobStateConflict) {
		t.Fatalf("conflicting hash error = %v, want ErrSweepJobStateConflict", err)
	}
}

func TestSweepJobRepoRecordSweepBroadcastObservedRollsBack(t *testing.T) {
	run := func(t *testing.T, setup func(t *testing.T, f *sweepBoundaryFixture), reservationID func(grant *OutboundSendGrant) uuid.UUID) {
		f := seedSweepBoundaryFixture(t)
		if err := f.ledger.CreateSweepHold(f.ctx, f.job, f.txModel); err != nil {
			t.Fatalf("create sweep hold: %v", err)
		}
		grant := f.attempt(t)
		if setup != nil {
			setup(t, f)
		}
		expected := grant.Reservation.ID
		if reservationID != nil {
			expected = reservationID(grant)
		}

		if err := f.jobs.RecordSweepBroadcastObserved(f.ctx, f.job.ID, f.outboundTx.ID, expected, "0xsweep"); err == nil {
			t.Fatal("the record must fail when one of its writes cannot commit")
		}
		job := reloadSweepJobRow(t, f, f.job.ID)
		if job.SweepTxHash != "" || job.OperatorAction == models.SweepOperatorActionReconcileBroadcast || job.Status == models.SweepJobStatusDeadLetter {
			t.Fatalf("partially recorded job = %#v", job)
		}
		outbound := reloadSweepOutboundRow(t, f, f.outboundTx.ID)
		if outbound.Status != models.OutboundStatusBroadcastAttempted || outbound.TxHash != "" {
			t.Fatalf("partially recorded outbound = %#v", outbound)
		}
		reservation := reloadReservationRow(t, f, grant.Reservation.ID)
		if reservation.Status != models.OutboundResourceReservationReserved {
			t.Fatalf("reservation = %#v, want it still reserved", reservation)
		}
		if got := countSweepOccurrences(t, f, constants.WebhookEventSweepDeadLetteredV1); got != 0 {
			t.Fatalf("dead-lettered occurrences = %d, want none after a rollback", got)
		}
		requireLedgerCount(t, f.db, 0, "sweep_job_id = ? AND entry_type = ?", f.job.ID, models.LedgerEntryTypeSweepRelease)
	}

	t.Run("outbox failure rolls the record back", func(t *testing.T) {
		run(t, func(t *testing.T, f *sweepBoundaryFixture) {
			if err := f.db.Exec(`CREATE FUNCTION fail_sweep_occurrence() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RAISE EXCEPTION ''synthetic outbox failure''; END'`).Error; err != nil {
				t.Fatalf("create outbox failure function: %v", err)
			}
			if err := f.db.Exec(`CREATE TRIGGER fail_sweep_occurrence BEFORE INSERT ON money_event_outboxes FOR EACH ROW EXECUTE FUNCTION fail_sweep_occurrence()`).Error; err != nil {
				t.Fatalf("create outbox failure trigger: %v", err)
			}
		}, nil)
	})

	t.Run("outbound write failure rolls the record back", func(t *testing.T) {
		run(t, func(t *testing.T, f *sweepBoundaryFixture) {
			if err := f.db.Exec(`CREATE FUNCTION skip_sweep_broadcasted() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NULL; END'`).Error; err != nil {
				t.Fatalf("create skip function: %v", err)
			}
			if err := f.db.Exec(`CREATE TRIGGER skip_sweep_broadcasted BEFORE UPDATE ON outbound_transactions FOR EACH ROW WHEN (NEW.status = 'broadcasted') EXECUTE FUNCTION skip_sweep_broadcasted()`).Error; err != nil {
				t.Fatalf("create skip trigger: %v", err)
			}
		}, nil)
	})

	t.Run("reconciliation failure rolls the record back", func(t *testing.T) {
		run(t, func(t *testing.T, f *sweepBoundaryFixture) {
			if err := f.db.Exec(`CREATE FUNCTION fail_sweep_reconciliation() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RAISE EXCEPTION ''synthetic reconciliation failure''; END'`).Error; err != nil {
				t.Fatalf("create reconciliation failure function: %v", err)
			}
			if err := f.db.Exec(`CREATE TRIGGER fail_sweep_reconciliation BEFORE INSERT ON reconciliation_jobs FOR EACH ROW EXECUTE FUNCTION fail_sweep_reconciliation()`).Error; err != nil {
				t.Fatalf("create reconciliation failure trigger: %v", err)
			}
		}, nil)
	})

	t.Run("reservation consume failure rolls the record back", func(t *testing.T) {
		run(t, nil, func(*OutboundSendGrant) uuid.UUID { return uuid.New() })
	})
}

func TestOutboundTransactionRepoAcquireSweepSendGrantRequiresLiveClaimAndCanonicalSource(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		setup  func(t *testing.T, f *sweepBoundaryFixture)
	}{
		{name: "unclaimed job", reason: "sweep_job_not_claimed", setup: func(t *testing.T, f *sweepBoundaryFixture) {
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"status": models.SweepJobStatusPending, "locked_until": nil})
			f.job.Status = models.SweepJobStatusPending
			f.job.LockedUntil = nil
		}},
		{name: "different lease owner", reason: "sweep_job_claim_mismatch", setup: func(t *testing.T, f *sweepBoundaryFixture) {
			f.claim(t)
			other := time.Now().Add(5 * time.Minute)
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"locked_until": &other})
		}},
		{name: "expired lease", reason: "sweep_job_claim_expired", setup: func(t *testing.T, f *sweepBoundaryFixture) {
			f.claim(t)
			past := time.Now().Add(-time.Second)
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"locked_until": &past})
			f.job.LockedUntil = &past
		}},
		{name: "dead letter job", reason: "sweep_job_not_claimed", setup: func(t *testing.T, f *sweepBoundaryFixture) {
			f.claim(t)
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"status": models.SweepJobStatusDeadLetter})
		}},
		{name: "reorged source", reason: "sweep_source_not_canonical", setup: func(t *testing.T, f *sweepBoundaryFixture) {
			f.claim(t)
			now := time.Now()
			if err := f.db.Model(&models.Transaction{}).Where("unique_hash = ?", f.txModel.UniqueHash).Updates(map[string]any{"reorged_at": &now}).Error; err != nil {
				t.Fatalf("reorg the source transaction: %v", err)
			}
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := seedSweepBoundaryFixture(t)
			testCase.setup(t, f)
			grant, block, err := f.outbound.AcquireSweepSendGrant(f.ctx, f.job, f.outboundTx.ID, "0xsource", 30*time.Second)
			if err != nil {
				t.Fatalf("acquire sweep send grant: %v", err)
			}
			if grant != nil || block == nil || block.Reason != testCase.reason || !block.HardEvidence {
				t.Fatalf("grant=%v block=%#v, want the %q refusal", grant, block, testCase.reason)
			}
			var reservations int64
			if err := f.db.Model(&models.OutboundChainResourceReservation{}).Where("outbound_transaction_id = ?", f.outboundTx.ID).Count(&reservations).Error; err != nil {
				t.Fatalf("count reservations: %v", err)
			}
			if reservations != 0 {
				t.Fatal("a refused sweep grant must not reserve the chain resource")
			}
		})
	}
}

func TestSweepJobRepoClaimDueIsAtomicUnderConcurrency(t *testing.T) {
	f := seedSweepBoundaryFixture(t)
	if sqlDB, err := f.db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(4)
	}
	mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"status": models.SweepJobStatusPending, "next_run_at": time.Now().Add(-time.Minute), "locked_until": nil})

	start := make(chan struct{})
	results := make(chan []models.SweepJob, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			jobs, err := f.jobs.ClaimDue(f.ctx, 10, 2*time.Minute)
			results <- jobs
			errs <- err
		}()
	}
	close(start)

	claims := 0
	var winnerLease *time.Time
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("claim due: %v", err)
		}
		for _, job := range <-results {
			if job.ID == f.job.ID {
				claims++
				winnerLease = job.LockedUntil
			}
		}
	}
	if claims != 1 {
		t.Fatalf("concurrent claims = %d, want exactly one claim for the job", claims)
	}
	persisted := reloadSweepJobRow(t, f, f.job.ID)
	if winnerLease == nil || persisted.LockedUntil == nil ||
		!winnerLease.Truncate(time.Microsecond).Equal(persisted.LockedUntil.Truncate(time.Microsecond)) {
		t.Fatalf("returned lease %v does not match the persisted lease %v", winnerLease, persisted.LockedUntil)
	}
	if persisted.Status != models.SweepJobStatusProcessing {
		t.Fatalf("claimed job status = %q, want processing", persisted.Status)
	}
}

func TestSweepJobRepoRecordSweepBroadcastObservedKeepsTerminalDecisions(t *testing.T) {
	f := seedSweepBoundaryFixture(t)
	if err := f.ledger.CreateSweepHold(f.ctx, f.job, f.txModel); err != nil {
		t.Fatalf("create sweep hold: %v", err)
	}
	grant := f.attempt(t)
	recoveredAt := time.Now()
	mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{
		"status":          models.SweepJobStatusSucceeded,
		"sweep_tx_hash":   "0xhuman",
		"recovery_action": models.SweepRecoveryActionMarkSuccess,
		"recovered_at":    &recoveredAt,
		"operator_action": "",
	})

	err := f.jobs.RecordSweepBroadcastObserved(f.ctx, f.job.ID, f.outboundTx.ID, grant.Reservation.ID, "0xlate")
	if !errors.Is(err, ErrSweepTerminalDecisionKept) {
		t.Fatalf("late hash error = %v, want ErrSweepTerminalDecisionKept", err)
	}
	job := reloadSweepJobRow(t, f, f.job.ID)
	if job.Status != models.SweepJobStatusSucceeded || job.SweepTxHash != "0xhuman" ||
		job.OperatorAction == models.SweepOperatorActionReconcileBroadcast {
		t.Fatalf("job = %#v, want the human decision preserved", job)
	}
	if outbound := reloadSweepOutboundRow(t, f, f.outboundTx.ID); outbound.TxHash == "0xlate" || outbound.FinalizedAt != nil {
		t.Fatalf("outbound = %#v, want no late hash written", outbound)
	}
	requireLedgerCount(t, f.db, 0, "sweep_job_id = ? AND entry_type = ?", f.job.ID, models.LedgerEntryTypeSweepRelease)
	// The refused record rolls back, so the worker's fallback (its own transaction)
	// persists the evidence and the reconciliation without touching the decision.
	if err := f.jobs.RecordSweepBroadcastEvidenceFallback(f.ctx, f.job.ID, f.outboundTx.ID, grant.Reservation.ID, "0xlate"); err != nil {
		t.Fatalf("fallback evidence record: %v", err)
	}
	var reconciliations int64
	if err := f.db.Model(&models.ReconciliationJob{}).Where("resource_id = ?", f.job.ID.String()).Count(&reconciliations).Error; err != nil {
		t.Fatalf("count reconciliations: %v", err)
	}
	if reconciliations != 1 {
		t.Fatalf("active reconciliations = %d, want exactly one", reconciliations)
	}
	if kept := reloadSweepJobRow(t, f, f.job.ID); kept.Status != models.SweepJobStatusSucceeded || kept.SweepTxHash != "0xhuman" {
		t.Fatalf("job after the fallback = %#v, want the human decision preserved", kept)
	}

	// A replay of the hash the decision already records is a no-op.
	if err := f.jobs.RecordSweepBroadcastObserved(f.ctx, f.job.ID, f.outboundTx.ID, grant.Reservation.ID, "0xhuman"); err != nil {
		t.Fatalf("matching hash replay on a terminal job: %v", err)
	}
	if got := reloadSweepJobRow(t, f, f.job.ID); got.Status != models.SweepJobStatusSucceeded || got.SweepTxHash != "0xhuman" {
		t.Fatalf("job after replay = %#v", got)
	}
}

func TestSweepJobRepoRecordSweepBroadcastObservedRejectsWrongIdentityAndHash(t *testing.T) {
	cases := []struct {
		name      string
		reserveID func(t *testing.T, f *sweepBoundaryFixture, grant *OutboundSendGrant) uuid.UUID
		outbound  func(t *testing.T, f *sweepBoundaryFixture, grant *OutboundSendGrant) uuid.UUID
		hash      string
		prepare   func(t *testing.T, f *sweepBoundaryFixture, grant *OutboundSendGrant)
	}{
		{name: "foreign outbound", outbound: func(t *testing.T, f *sweepBoundaryFixture, grant *OutboundSendGrant) uuid.UUID {
			other := ledgerTestTransaction(f.merchantID, f.domainID, f.walletID, "sweep-foreign-"+uuid.NewString(), "10")
			if err := f.db.Create(&other).Error; err != nil {
				t.Fatalf("seed foreign transaction: %v", err)
			}
			foreignJob, queued, err := f.jobs.EnqueueForTransaction(f.ctx, other)
			if err != nil || !queued {
				t.Fatalf("enqueue foreign job: %v (queued=%v)", err, queued)
			}
			foreignOutbound, _, err := f.outbound.CreateForSweepJob(f.ctx, *foreignJob, other, "0xreserve")
			if err != nil {
				t.Fatalf("create foreign outbound: %v", err)
			}
			return foreignOutbound.ID
		}},
		{name: "foreign reservation", reserveID: func(t *testing.T, f *sweepBoundaryFixture, grant *OutboundSendGrant) uuid.UUID {
			return uuid.New()
		}},
		{name: "prepared outbound without attempt", prepare: func(t *testing.T, f *sweepBoundaryFixture, grant *OutboundSendGrant) {
			now := time.Now().Add(-time.Minute)
			mustUpdateSweepOutboundRow(t, f, f.outboundTx.ID, map[string]any{"status": models.OutboundStatusPrepared, "broadcast_attempted_at": nil, "broadcasted_at": nil, "tx_hash": ""})
			_ = now
		}},
		{name: "different hash already recorded", prepare: func(t *testing.T, f *sweepBoundaryFixture, grant *OutboundSendGrant) {
			now := time.Now()
			mustUpdateSweepOutboundRow(t, f, f.outboundTx.ID, map[string]any{"tx_hash": "0xprior", "broadcasted_at": &now})
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			f := seedSweepBoundaryFixture(t)
			grant := f.attempt(t)
			outboundID := f.outboundTx.ID
			if testCase.outbound != nil {
				outboundID = testCase.outbound(t, f, grant)
			}
			reservationID := grant.Reservation.ID
			if testCase.reserveID != nil {
				reservationID = testCase.reserveID(t, f, grant)
			}
			if testCase.prepare != nil {
				testCase.prepare(t, f, grant)
			}
			err := f.jobs.RecordSweepBroadcastObserved(f.ctx, f.job.ID, outboundID, reservationID, "0xsweep")
			if !errors.Is(err, ErrSweepJobStateConflict) {
				t.Fatalf("record error = %v, want ErrSweepJobStateConflict", err)
			}
			job := reloadSweepJobRow(t, f, f.job.ID)
			if job.SweepTxHash != "" || job.Status == models.SweepJobStatusDeadLetter {
				t.Fatalf("job = %#v, want no partial record", job)
			}
			if got := reloadReservationRow(t, f, grant.Reservation.ID); got.Status != models.OutboundResourceReservationReserved {
				t.Fatalf("reservation = %#v, want it untouched", got)
			}
			if got := countSweepOccurrences(t, f, constants.WebhookEventSweepDeadLetteredV1); got != 0 {
				t.Fatalf("dead-lettered occurrences = %d, want none", got)
			}
		})
	}
}

func TestOutboundTransactionRepoAcquireSweepSendGrantSerializesAndFences(t *testing.T) {
	t.Run("two workers on one job: exactly one grant", func(t *testing.T) {
		f := seedSweepBoundaryFixture(t)
		f.claim(t)
		if sqlDB, err := f.db.DB(); err == nil {
			sqlDB.SetMaxOpenConns(4)
		}
		start := make(chan struct{})
		type outcome struct {
			grant *OutboundSendGrant
			block *OutboundSendBlock
			err   error
		}
		results := make(chan outcome, 2)
		for range 2 {
			go func() {
				<-start
				grant, block, err := f.outbound.AcquireSweepSendGrant(f.ctx, f.job, f.outboundTx.ID, "0xsource", 30*time.Second)
				results <- outcome{grant: grant, block: block, err: err}
			}()
		}
		close(start)

		var winner *OutboundSendGrant
		var loser *OutboundSendBlock
		for range 2 {
			res := <-results
			if res.err != nil {
				t.Fatalf("acquire sweep send grant: %v", res.err)
			}
			if res.grant != nil {
				if winner != nil {
					t.Fatal("two workers received a sweep send grant for one job")
				}
				winner = res.grant
				continue
			}
			loser = res.block
		}
		if winner == nil {
			t.Fatal("no worker received a sweep send grant")
		}
		if winner.Outbound.Status != models.OutboundStatusBroadcastAttempted || winner.Outbound.Attempts != 1 {
			t.Fatalf("granted snapshot = %#v", winner.Outbound)
		}
		if loser == nil || loser.Reason == "" || !loser.HardEvidence {
			t.Fatalf("loser block = %#v, want a hard-evidence refusal", loser)
		}

		var released int64
		if err := f.db.Model(&models.OutboundChainResourceReservation{}).Where("status = ?", models.OutboundResourceReservationReleased).Count(&released).Error; err != nil {
			t.Fatalf("count released reservations: %v", err)
		}
		if released != 0 {
			t.Fatalf("released reservations = %d, want none after a losing worker", released)
		}
		reservation := reloadReservationRow(t, f, winner.Reservation.ID)
		if reservation.Status != models.OutboundResourceReservationReserved {
			t.Fatalf("reservation = %#v, want the winner's reservation still reserved", reservation)
		}

		// A third intent for the same wallet/chain cannot occupy the sequence resource.
		other := ledgerTestTransaction(f.merchantID, f.domainID, f.walletID, "sweep-third-"+uuid.NewString(), "10")
		other.Symbol = "ETH"
		if err := f.db.Create(&other).Error; err != nil {
			t.Fatalf("seed third intent transaction: %v", err)
		}
		thirdJob, queued, err := f.jobs.EnqueueForTransaction(f.ctx, other)
		if err != nil || !queued {
			t.Fatalf("enqueue third job: %v (queued=%v)", err, queued)
		}
		thirdOutbound, _, err := f.outbound.CreateForSweepJob(f.ctx, *thirdJob, other, "0xreserve")
		if err != nil {
			t.Fatalf("create third outbound: %v", err)
		}
		if _, _, err := f.outbound.ReserveSequence(f.ctx, *thirdOutbound, "0xsource", 30*time.Second); !errors.Is(err, ErrOutboundResourceAlreadyReserved) {
			t.Fatalf("third intent reservation error = %v, want ErrOutboundResourceAlreadyReserved", err)
		}
	})

	fenced := []struct {
		name   string
		mutate func(t *testing.T, f *sweepBoundaryFixture)
	}{
		{name: "job hash recorded", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"sweep_tx_hash": "0xrecorded"})
		}},
		{name: "job reconcile marker", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			mustUpdateSweepJobRow(t, f, f.job.ID, map[string]any{"operator_action": models.SweepOperatorActionReconcileBroadcast})
		}},
		{name: "stale claim after an attempt", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			now := time.Now()
			mustUpdateSweepOutboundRow(t, f, f.outboundTx.ID, map[string]any{"status": models.OutboundStatusBroadcastAttempted, "broadcast_attempted_at": &now})
		}},
		{name: "outbound already broadcasted", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			now := time.Now()
			mustUpdateSweepOutboundRow(t, f, f.outboundTx.ID, map[string]any{"status": models.OutboundStatusBroadcasted, "tx_hash": "0xout", "broadcasted_at": &now})
		}},
		{name: "outbound finalized", mutate: func(t *testing.T, f *sweepBoundaryFixture) {
			now := time.Now()
			mustUpdateSweepOutboundRow(t, f, f.outboundTx.ID, map[string]any{"status": models.OutboundStatusFinalized, "finalized_at": &now})
		}},
	}
	for _, testCase := range fenced {
		t.Run("refuses "+testCase.name, func(t *testing.T) {
			f := seedSweepBoundaryFixture(t)
			f.claim(t)
			testCase.mutate(t, f)
			grant, block, err := f.outbound.AcquireSweepSendGrant(f.ctx, f.job, f.outboundTx.ID, "0xsource", 30*time.Second)
			if err != nil {
				t.Fatalf("acquire sweep send grant: %v", err)
			}
			if grant != nil || block == nil || !block.HardEvidence {
				t.Fatalf("grant=%v block=%#v, want a hard-evidence refusal", grant, block)
			}
			var reservations int64
			if err := f.db.Model(&models.OutboundChainResourceReservation{}).Where("outbound_transaction_id = ?", f.outboundTx.ID).Count(&reservations).Error; err != nil {
				t.Fatalf("count reservations: %v", err)
			}
			if reservations != 0 {
				t.Fatalf("reservations = %d, want none for a refused sweep grant", reservations)
			}
		})
	}
}
