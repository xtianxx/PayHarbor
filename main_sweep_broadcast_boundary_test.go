package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	coreApplication "core/application"
	"core/blockchain"
	"core/constants"
	"core/models"
	"core/repositories"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// A10: a sweep hash is broadcast evidence, not finality. These tests run the real
// sweep worker against PostgreSQL with a local fake chain (no RPC), and assert the
// hold, the ledger and the send fences.

type fakeSweepChain struct {
	blockchain.Chain
	withdrawHash string
	withdrawErr  error
	withdraws    int
	hdCalls      int
}

func (f *fakeSweepChain) ChainID() constants.ChainID { return constants.Ethereum }
func (f *fakeSweepChain) Name() string               { return constants.ChainName(constants.Ethereum) }

func (f *fakeSweepChain) CreateHDWallet(context.Context, int, int) (*blockchain.WalletDetails, error) {
	f.hdCalls++
	return &blockchain.WalletDetails{Address: "0xuseraddress"}, nil
}

func (f *fakeSweepChain) Withdraw(_ context.Context, _ blockchain.WalletDetails, _, _ string) (*blockchain.TransactionResult, error) {
	f.withdraws++
	if f.withdrawErr != nil {
		return nil, f.withdrawErr
	}
	return &blockchain.TransactionResult{TxHash: f.withdrawHash, Success: true}, nil
}

func installFakeSweepChain(t *testing.T, chain *fakeSweepChain) {
	t.Helper()
	factory := coreApplication.CORE.Router.Blockchains()
	name := constants.ChainName(constants.Ethereum)
	factory.UnregisterChain(name)
	if err := factory.RegisterChain(name, chain); err != nil {
		t.Fatalf("register fake sweep chain: %v", err)
	}
}

type sweepWorkerFixture struct {
	db      *gorm.DB
	job     models.SweepJob
	txModel models.Transaction
	chain   *fakeSweepChain
}

func seedSweepWorkerFixture(t *testing.T, chain *fakeSweepChain) *sweepWorkerFixture {
	t.Helper()
	db := openOutboundGrantPostgresTestDB(t)
	useTestRouterForOutbound(t, db)
	installFakeSweepChain(t, chain)
	ctx := context.Background()

	merchantID, domainID, walletID := uuid.New(), uuid.New(), uuid.New()
	hdAccountID := uint32(domainID[0])<<24 | uint32(domainID[1])<<16 | uint32(domainID[2])<<8 | uint32(domainID[3])
	if hdAccountID == 0 {
		hdAccountID = 1
	}
	merchant := models.Merchant{ID: merchantID, Name: "A10 Sweep Merchant", Email: "a10-" + merchantID.String() + "@example.com", IsActive: true}
	domain := models.Domain{ID: domainID, MerchantID: merchantID, DomainURL: "https://" + domainID.String() + ".example.com", APIKey: "a10-key-" + domainID.String(), APISecret: "a10-secret", HDAccountID: hdAccountID}
	userWallet := models.Wallet{ID: walletID, MerchantID: merchantID, DomainID: domainID, HDAccountID: hdAccountID, HDAddressId: 1}
	seedUniqueWalletAddressesForTest(&userWallet, "a10user", strings.ReplaceAll(walletID.String(), "-", ""))
	reserveDomainID, reserveWalletID := uuid.New(), uuid.New()
	reserveDomain := models.Domain{ID: reserveDomainID, MerchantID: merchantID, DomainURL: "_reserve_", APIKey: "a10-reserve-" + reserveDomainID.String(), APISecret: "a10-reserve-secret", HDAccountID: hdAccountID + 1}
	reserveWallet := models.Wallet{ID: reserveWalletID, MerchantID: merchantID, DomainID: reserveDomainID, HDAccountID: hdAccountID + 1, HDAddressId: 0}
	seedUniqueWalletAddressesForTest(&reserveWallet, "a10reserve", strings.ReplaceAll(reserveWalletID.String(), "-", ""))
	for _, value := range []any{&merchant, &domain, &userWallet, &reserveDomain, &reserveWallet} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("seed sweep fixture row: %v", err)
		}
	}

	ledger := repositories.NewLedgerRepo(db)
	source := models.Transaction{
		ID:            uuid.New(),
		ChainID:       constants.Ethereum,
		UniqueHash:    "a10-sweep-" + uuid.NewString(),
		Hash:          "a10hash" + uuid.NewString()[:8],
		BlockNumber:   "1",
		BlockHash:     "a10block",
		Symbol:        "ETH",
		Decimals:      18,
		FromAddress:   "0xuseraddress",
		ToAddress:     "0xreserveaddress",
		Amount:        "100",
		Status:        models.TransactionStatusConfirmed,
		MerchantID:    &merchantID,
		DomainID:      &domainID,
		WalletID:      &walletID,
		Confirmations: 12,
	}
	finalizedAt := time.Now().Add(-time.Minute)
	source.FinalizedAt = &finalizedAt
	if err := db.Create(&source).Error; err != nil {
		t.Fatalf("seed sweep source transaction: %v", err)
	}
	if err := ledger.PostStandaloneDepositAvailable(ctx, source); err != nil {
		t.Fatalf("seed sweep source balance: %v", err)
	}
	job, queued, err := repositories.NewSweepJobRepo(db).EnqueueForTransaction(ctx, source)
	if err != nil || !queued {
		t.Fatalf("enqueue sweep job: %v (queued=%v)", err, queued)
	}
	if err := db.Model(&models.SweepJob{}).Where("id = ?", job.ID).Updates(map[string]any{"next_run_at": time.Now().Add(-time.Minute)}).Error; err != nil {
		t.Fatalf("make sweep job due: %v", err)
	}
	return &sweepWorkerFixture{db: db, job: *job, txModel: source, chain: chain}
}

func (f *sweepWorkerFixture) reloadJob(t *testing.T) models.SweepJob {
	t.Helper()
	var job models.SweepJob
	if err := f.db.First(&job, "id = ?", f.job.ID).Error; err != nil {
		t.Fatalf("reload sweep job: %v", err)
	}
	return job
}

func (f *sweepWorkerFixture) outboundRow(t *testing.T) models.OutboundTransaction {
	t.Helper()
	var row models.OutboundTransaction
	if err := f.db.First(&row, "resource_type = ? AND resource_id = ?", models.OutboundResourceSweepJob, f.job.ID).Error; err != nil {
		t.Fatalf("load sweep outbound: %v", err)
	}
	return row
}

func (f *sweepWorkerFixture) reservationCounts(t *testing.T) (reserved, released, consumed int64) {
	t.Helper()
	count := func(status string) int64 {
		var total int64
		if err := f.db.Model(&models.OutboundChainResourceReservation{}).Where("status = ?", status).Count(&total).Error; err != nil {
			t.Fatalf("count %s reservations: %v", status, err)
		}
		return total
	}
	return count(models.OutboundResourceReservationReserved), count(models.OutboundResourceReservationReleased), count(models.OutboundResourceReservationConsumed)
}

func (f *sweepWorkerFixture) ledgerCounts(t *testing.T) (holdPending, releases int64) {
	t.Helper()
	if err := f.db.Model(&models.LedgerEntry{}).Where("sweep_job_id = ? AND entry_type = ? AND status = ?", f.job.ID, models.LedgerEntryTypeSweepHold, models.LedgerStatusPending).Count(&holdPending).Error; err != nil {
		t.Fatalf("count pending sweep holds: %v", err)
	}
	if err := f.db.Model(&models.LedgerEntry{}).Where("sweep_job_id = ? AND entry_type = ?", f.job.ID, models.LedgerEntryTypeSweepRelease).Count(&releases).Error; err != nil {
		t.Fatalf("count sweep releases: %v", err)
	}
	return holdPending, releases
}

func (f *sweepWorkerFixture) occurrenceCount(t *testing.T, eventType string) int64 {
	t.Helper()
	var count int64
	if err := f.db.Model(&models.MoneyEventOutbox{}).Where("aggregate_id = ? AND event_type = ?", f.job.ID.String(), eventType).Count(&count).Error; err != nil {
		t.Fatalf("count %s occurrences: %v", eventType, err)
	}
	return count
}

func TestSweepBroadcastObservationIsNotFinality(t *testing.T) {
	chain := &fakeSweepChain{withdrawHash: "0xsweepobserved"}
	fixture := seedSweepWorkerFixture(t, chain)

	processSweepJobs(context.Background())

	if chain.withdraws != 1 {
		t.Fatalf("chain withdraws = %d, want exactly one broadcast", chain.withdraws)
	}
	job := fixture.reloadJob(t)
	if job.Status != models.SweepJobStatusDeadLetter || job.SweepTxHash != "0xsweepobserved" ||
		job.OperatorAction != models.SweepOperatorActionReconcileBroadcast ||
		job.FailureCategory != models.SweepFailureCategoryBroadcastUncertain {
		t.Fatalf("job = %#v, want broadcast-observed reconcile, not success", job)
	}
	outbound := fixture.outboundRow(t)
	if outbound.Status != models.OutboundStatusBroadcasted || outbound.TxHash != "0xsweepobserved" || outbound.FinalizedAt != nil {
		t.Fatalf("outbound = %#v, want broadcasted without finality", outbound)
	}
	reserved, released, consumed := fixture.reservationCounts(t)
	if consumed != 1 || reserved != 0 || released != 0 {
		t.Fatalf("reservations reserved/released/consumed = %d/%d/%d, want 0/0/1", reserved, released, consumed)
	}
	holdPending, releases := fixture.ledgerCounts(t)
	if holdPending == 0 || releases != 0 {
		t.Fatalf("sweep hold pending = %d, release legs = %d; the hold must stay until finality", holdPending, releases)
	}
	if got := fixture.occurrenceCount(t, constants.WebhookEventSweepSucceededV1); got != 0 {
		t.Fatalf("succeeded occurrences = %d, want none for a broadcast-only sweep", got)
	}
	if got := fixture.occurrenceCount(t, constants.WebhookEventSweepDeadLetteredV1); got != 1 {
		t.Fatalf("dead-lettered occurrences = %d, want 1", got)
	}

	// The recorded broadcast fences every later automatic attempt.
	processSweepJobs(context.Background())
	if chain.withdraws != 1 {
		t.Fatalf("chain withdraws after a second worker pass = %d, want the recorded sweep to stay fenced", chain.withdraws)
	}
	if err := repositories.NewSweepJobRepo(fixture.db).RecordOperatorRecovery(context.Background(), fixture.job.ID, models.SweepRecoveryActionRetry, "operator retry", "", nil); !errors.Is(err, repositories.ErrSweepBroadcastEvidencePresent) {
		t.Fatalf("retry after a broadcast error = %v, want ErrSweepBroadcastEvidencePresent", err)
	}
}

func TestSweepPostSendUnknownFailureKeepsHoldAndBlocksRetry(t *testing.T) {
	chain := &fakeSweepChain{withdrawErr: errors.New("rpc connection reset by peer")}
	fixture := seedSweepWorkerFixture(t, chain)

	processSweepJobs(context.Background())

	if chain.withdraws != 1 {
		t.Fatalf("chain withdraws = %d, want 1", chain.withdraws)
	}
	outbound := fixture.outboundRow(t)
	if outbound.Status != models.OutboundStatusNeedsOperatorAction || outbound.BroadcastAttemptedAt == nil {
		t.Fatalf("outbound = %#v, want attempt evidence preserved for operator reconciliation", outbound)
	}
	job := fixture.reloadJob(t)
	if job.Status != models.SweepJobStatusDeadLetter || job.OperatorAction != models.SweepOperatorActionReconcileBroadcast {
		t.Fatalf("job = %#v, want a reconcile marker instead of a plain retry", job)
	}
	reserved, released, _ := fixture.reservationCounts(t)
	if reserved != 1 || released != 0 {
		t.Fatalf("reservations reserved/released = %d/%d, want the attempt's reservation kept", reserved, released)
	}
	holdPending, releases := fixture.ledgerCounts(t)
	if holdPending == 0 || releases != 0 {
		t.Fatalf("sweep hold pending = %d, release legs = %d; a post-send failure must keep the hold", holdPending, releases)
	}
	if err := repositories.NewSweepJobRepo(fixture.db).RecordOperatorRecovery(context.Background(), fixture.job.ID, models.SweepRecoveryActionRetry, "operator retry", "", nil); !errors.Is(err, repositories.ErrSweepBroadcastEvidencePresent) {
		t.Fatalf("retry after an unknown send outcome = %v, want ErrSweepBroadcastEvidencePresent", err)
	}
	processSweepJobs(context.Background())
	if chain.withdraws != 1 {
		t.Fatalf("chain withdraws after a second worker pass = %d, want no automatic re-send", chain.withdraws)
	}
}

func TestSweepHoldReleaseRequiresAbsenceOfBroadcastEvidence(t *testing.T) {
	chain := &fakeSweepChain{withdrawHash: "0xnever"}
	fixture := seedSweepWorkerFixture(t, chain)
	ctx := context.Background()

	// A dead-letter job whose outbound already carries attempt evidence must keep the
	// hold even when the error text looks pre-broadcast.
	attempted := fixture.job
	attempted.Status = models.SweepJobStatusDeadLetter
	attemptedAt := time.Now()
	if err := fixture.db.Create(&models.OutboundTransaction{
		ID:                   uuid.New(),
		IdempotencyKey:       "a10-attempt-" + uuid.NewString(),
		ResourceType:         models.OutboundResourceSweepJob,
		ResourceID:           fixture.job.ID,
		MerchantID:           fixture.job.MerchantID,
		WalletID:             fixture.job.WalletID,
		ChainID:              constants.Ethereum,
		ChainName:            constants.ChainName(constants.Ethereum),
		Symbol:               "ETH",
		Decimals:             18,
		AmountRaw:            "100",
		ToAddress:            "0xreserveaddress",
		Status:               models.OutboundStatusBroadcastAttempted,
		BroadcastAttemptedAt: &attemptedAt,
		CorrelationID:        "a10",
	}).Error; err != nil {
		t.Fatalf("seed attempted sweep outbound: %v", err)
	}
	if err := fixture.db.Model(&models.LedgerEntry{}).Where("sweep_job_id = ?", fixture.job.ID).Count(new(int64)).Error; err != nil {
		t.Fatalf("touch ledger: %v", err)
	}
	releaseSweepHoldOnPreBroadcastDeadLetter(ctx, attempted, &fixture.txModel, errors.New("reserve wallet has no address for chain 1"))
	if _, releases := fixture.ledgerCounts(t); releases != 0 {
		t.Fatalf("release legs = %d, want the hold kept while broadcast evidence exists", releases)
	}
	var reconciliationRows int64
	if err := fixture.db.Model(&models.ReconciliationJob{}).Count(&reconciliationRows).Error; err != nil {
		t.Fatalf("count reconciliation jobs: %v", err)
	}
	if reconciliationRows == 0 {
		t.Fatal("an evidence-fenced hold release must open reconciliation")
	}

	// A clean dead-letter job without any attempt evidence still releases per the
	// existing pre-broadcast rules.
	clean := seedSweepWorkerFixture(t, &fakeSweepChain{withdrawHash: "0xclean"})
	if err := clean.db.Model(&models.SweepJob{}).Where("id = ?", clean.job.ID).Updates(map[string]any{"status": models.SweepJobStatusDeadLetter, "next_run_at": nil}).Error; err != nil {
		t.Fatalf("dead-letter clean job: %v", err)
	}
	cleanJob := clean.reloadJob(t)
	if err := repositories.NewLedgerRepo(clean.db).CreateSweepHold(ctx, cleanJob, clean.txModel); err != nil {
		t.Fatalf("create clean sweep hold: %v", err)
	}
	holdPendingBefore, _ := clean.ledgerCounts(t)
	if holdPendingBefore == 0 {
		t.Fatal("clean fixture must have a pending sweep hold")
	}
	releaseSweepHoldOnPreBroadcastDeadLetter(ctx, cleanJob, &clean.txModel, errors.New("reserve wallet has no address for chain 1"))
	if _, releases := clean.ledgerCounts(t); releases == 0 {
		t.Fatalf("a pre-broadcast failure without evidence must still release the sweep hold")
	}
	if hasEvidence, reason, err := repositories.NewSweepJobRepo(clean.db).SweepBroadcastEvidence(ctx, clean.job.ID); err != nil || hasEvidence {
		t.Fatalf("clean job evidence probe = %v/%q/%v, want no evidence", hasEvidence, reason, err)
	}
}
