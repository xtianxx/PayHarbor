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
	"gorm.io/gorm/clause"
)

// A7: same-payment refund creation must be serialised so two concurrent requests
// cannot both fit under one refundable limit. These tests run the real repository
// against PostgreSQL.

type refundGuardFixture struct {
	db         *gorm.DB
	ctx        context.Context
	repo       *RefundRepo
	ledger     *LedgerRepo
	merchantID uuid.UUID
	domainID   uuid.UUID
	walletID   uuid.UUID
	sessionID  uuid.UUID
}

func seedRefundGuardFixture(t *testing.T, creditRaw string) refundGuardFixture {
	t.Helper()
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.PaymentSession{}, &models.Transaction{}, &models.Refund{}, &models.LedgerEntry{}, &models.MoneyEventOutbox{}); err != nil {
		t.Fatalf("automigrate refund guard tables: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(4)
	}
	ctx := context.Background()
	merchantID, domainID, walletID := seedWithdrawalOwner(t, db)
	chainID := constants.Ethereum
	session := models.PaymentSession{
		ID:                uuid.New(),
		SessionToken:      "refund-guard-" + uuid.NewString(),
		MerchantID:        merchantID,
		DomainID:          domainID,
		WalletID:          walletID,
		OrderID:           "order-" + uuid.NewString(),
		LinkType:          "fixed",
		Amount:            "100",
		Currency:          "USD",
		SettlementPolicy:  "single",
		Status:            models.PaymentStatusPaid,
		SelectedChainID:   &chainID,
		SelectedSymbol:    "ETH",
		SelectedDecimals:  18,
		ExpectedAmountRaw: "100",
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("seed paid session: %v", err)
	}
	ledger := NewLedgerRepo(db)
	if creditRaw != "" {
		deposit := ledgerTestTransaction(merchantID, domainID, walletID, "refund-guard-"+uuid.NewString(), creditRaw)
		if err := ledger.PostStandaloneDepositAvailable(ctx, deposit); err != nil {
			t.Fatalf("seed available balance: %v", err)
		}
	}
	return refundGuardFixture{
		db: db, ctx: ctx, repo: NewRefundRepo(db), ledger: ledger,
		merchantID: merchantID, domainID: domainID, walletID: walletID, sessionID: session.ID,
	}
}

func (f refundGuardFixture) request(amountRaw, idempotencyKey string) RefundCreateGuardedRequest {
	walletID := f.walletID
	return RefundCreateGuardedRequest{
		Refund: &models.Refund{
			ID:             uuid.New(),
			MerchantID:     f.merchantID,
			DomainID:       f.domainID,
			PaymentID:      f.sessionID,
			WalletID:       &walletID,
			AmountRaw:      amountRaw,
			Reason:         "test refund",
			Status:         models.RefundStatusPending,
			RequestedBy:    "api",
			IdempotencyKey: idempotencyKey,
			CorrelationID:  "corr-" + uuid.NewString(),
		},
		MerchantID: f.merchantID,
		DomainID:   f.domainID,
		PaymentID:  f.sessionID,
	}
}

func (f refundGuardFixture) refundCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := f.db.Model(&models.Refund{}).Where("payment_id = ?", f.sessionID).Count(&count).Error; err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	return count
}

func (f refundGuardFixture) activeTotalRaw(t *testing.T) string {
	t.Helper()
	total, err := f.repo.ActiveTotalRawByPayment(f.ctx, f.sessionID)
	if err != nil {
		t.Fatalf("active total: %v", err)
	}
	return total.String()
}

func TestRefundRepoCreateWithHoldGuardedSerializesConcurrentSamePayment(t *testing.T) {
	fixture := seedRefundGuardFixture(t, "1000")

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, key := range []string{"race-a", "race-b"} {
		request := fixture.request("60", key)
		go func(req RefundCreateGuardedRequest) {
			<-start
			results <- fixture.repo.CreateWithHoldGuarded(fixture.ctx, req, fixture.ledger)
		}(request)
	}
	close(start)

	succeeded := 0
	limitExceeded := 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrRefundLimitExceeded):
			limitExceeded++
		default:
			t.Fatalf("concurrent refund creation error = %v, want nil or ErrRefundLimitExceeded", err)
		}
	}
	if succeeded != 1 || limitExceeded != 1 {
		t.Fatalf("outcomes = %d succeeded / %d limit-exceeded, want exactly one of each", succeeded, limitExceeded)
	}
	if count := fixture.refundCount(t); count != 1 {
		t.Fatalf("refund rows = %d, want exactly one", count)
	}
	if total := fixture.activeTotalRaw(t); total != "60" {
		t.Fatalf("active refund total = %s, want 60 (cap is 100)", total)
	}
	var refunds []models.Refund
	if err := fixture.db.Where("payment_id = ?", fixture.sessionID).Find(&refunds).Error; err != nil {
		t.Fatalf("list refunds: %v", err)
	}
	var holdTotal int64
	if err := fixture.db.Model(&models.LedgerEntry{}).Where("entry_type = ?", models.LedgerEntryTypeRefundHold).Count(&holdTotal).Error; err != nil {
		t.Fatalf("count hold entries: %v", err)
	}
	var winnerHolds int64
	if err := fixture.db.Model(&models.LedgerEntry{}).Where("refund_id = ? AND entry_type = ?", refunds[0].ID, models.LedgerEntryTypeRefundHold).Count(&winnerHolds).Error; err != nil {
		t.Fatalf("count winner hold entries: %v", err)
	}
	if winnerHolds == 0 || holdTotal != winnerHolds {
		t.Fatalf("hold entries = %d total / %d for the winning refund, want only the winner's legs", holdTotal, winnerHolds)
	}
	requirePostgresCount(t, fixture.db, &models.MoneyEventOutbox{}, "event_id = ?", refunds[0].ID.String()+":"+constants.WebhookEventRefundRequestedV1, 1)
}

func TestRefundRepoCreateWithHoldGuardedEnforcesCapBoundaries(t *testing.T) {
	fixture := seedRefundGuardFixture(t, "1000")

	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("60", "boundary-60"), fixture.ledger); err != nil {
		t.Fatalf("create 60: %v", err)
	}
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("40", "boundary-40"), fixture.ledger); err != nil {
		t.Fatalf("create 40: %v", err)
	}
	if total := fixture.activeTotalRaw(t); total != "100" {
		t.Fatalf("active total at the cap = %s, want 100", total)
	}
	err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("1", "boundary-over"), fixture.ledger)
	if !errors.Is(err, ErrRefundLimitExceeded) {
		t.Fatalf("one unit over the cap error = %v, want ErrRefundLimitExceeded", err)
	}
	if count := fixture.refundCount(t); count != 2 {
		t.Fatalf("refund rows = %d, want 2 after the rejected third request", count)
	}

	// A replayed request (same refund row) must not add a second hold or outbox
	// event, and must not be able to double-spend refundable amount.
	replay := fixture.request("40", "boundary-replay")
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, replay, fixture.ledger); err != nil {
		if !errors.Is(err, ErrRefundLimitExceeded) {
			t.Fatalf("replay error = %v, want ErrRefundLimitExceeded (cap is already consumed)", err)
		}
	} else {
		t.Fatalf("replay created a refund although the cap was already consumed")
	}
	if count := fixture.refundCount(t); count != 2 {
		t.Fatalf("refund rows after replay = %d, want 2", count)
	}
}

func TestRefundRepoCreateWithHoldGuardedActiveStatusSetAndPaymentScope(t *testing.T) {
	fixture := seedRefundGuardFixture(t, "1000")

	// Terminal-negative statuses do not consume refundable amount.
	for index, status := range []string{models.RefundStatusFailed, models.RefundStatusRejected} {
		refund := models.Refund{
			ID:             uuid.New(),
			MerchantID:     fixture.merchantID,
			DomainID:       fixture.domainID,
			PaymentID:      fixture.sessionID,
			AmountRaw:      "80",
			Status:         status,
			IdempotencyKey: status + "-" + uuid.NewString(),
			CorrelationID:  "corr-" + uuid.NewString(),
		}
		if err := fixture.db.Create(&refund).Error; err != nil {
			t.Fatalf("seed %s refund %d: %v", status, index, err)
		}
	}
	if total := fixture.activeTotalRaw(t); total != "0" {
		t.Fatalf("active total with failed/rejected rows = %s, want 0", total)
	}
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("100", "status-set-allowed"), fixture.ledger); err != nil {
		t.Fatalf("full refund after terminal-negative rows: %v", err)
	}

	// processing/succeeded rows do consume it.
	second := seedRefundGuardFixture(t, "1000")
	for _, status := range []string{models.RefundStatusProcessing, models.RefundStatusSucceeded} {
		refund := models.Refund{
			ID:             uuid.New(),
			MerchantID:     second.merchantID,
			DomainID:       second.domainID,
			PaymentID:      second.sessionID,
			AmountRaw:      "40",
			Status:         status,
			IdempotencyKey: status + "-" + uuid.NewString(),
			CorrelationID:  "corr-" + uuid.NewString(),
		}
		if err := second.db.Create(&refund).Error; err != nil {
			t.Fatalf("seed %s refund: %v", status, err)
		}
	}
	if total := second.activeTotalRaw(t); total != "80" {
		t.Fatalf("active total with processing/succeeded rows = %s, want 80", total)
	}
	if err := second.repo.CreateWithHoldGuarded(second.ctx, second.request("30", "status-set-rejected"), second.ledger); !errors.Is(err, ErrRefundLimitExceeded) {
		t.Fatalf("30 over an 80-active payment error = %v, want ErrRefundLimitExceeded", err)
	}

	// Different payments must not be serialised against each other.
	third := seedRefundGuardFixture(t, "1000")
	fourth := seedRefundGuardFixture(t, "1000")
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		results <- third.repo.CreateWithHoldGuarded(third.ctx, third.request("60", "other-payment-a"), third.ledger)
	}()
	go func() {
		<-start
		results <- fourth.repo.CreateWithHoldGuarded(fourth.ctx, fourth.request("60", "other-payment-b"), fourth.ledger)
	}()
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("refund creation for an unrelated payment failed: %v", err)
		}
	}
}

func TestRefundRepoCreateWithHoldGuardedRejectsInvalidScopeAndAmount(t *testing.T) {
	fixture := seedRefundGuardFixture(t, "1000")

	mismatch := fixture.request("10", "scope-mismatch")
	mismatch.MerchantID = uuid.New()
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, mismatch, fixture.ledger); !errors.Is(err, ErrRefundPaymentNotRefundable) {
		t.Fatalf("foreign merchant error = %v, want ErrRefundPaymentNotRefundable", err)
	}
	domainMismatch := fixture.request("10", "scope-domain")
	domainMismatch.DomainID = uuid.New()
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, domainMismatch, fixture.ledger); !errors.Is(err, ErrRefundPaymentNotRefundable) {
		t.Fatalf("foreign domain error = %v, want ErrRefundPaymentNotRefundable", err)
	}
	if err := fixture.db.Model(&models.PaymentSession{}).Where("id = ?", fixture.sessionID).Update("status", models.PaymentStatusPending).Error; err != nil {
		t.Fatalf("unpay session: %v", err)
	}
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("10", "scope-unpaid"), fixture.ledger); !errors.Is(err, ErrRefundPaymentNotRefundable) {
		t.Fatalf("unpaid payment error = %v, want ErrRefundPaymentNotRefundable", err)
	}
	if err := fixture.db.Model(&models.PaymentSession{}).Where("id = ?", fixture.sessionID).Update("status", models.PaymentStatusPaid).Error; err != nil {
		t.Fatalf("re-pay session: %v", err)
	}

	for _, amount := range []string{"0", "-5", "1.5", "abc", " ", "0x10"} {
		err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request(amount, "amount-"+amount), fixture.ledger)
		if !errors.Is(err, ErrRefundAmountInvalid) {
			t.Fatalf("amount %q error = %v, want ErrRefundAmountInvalid", amount, err)
		}
	}
	if count := fixture.refundCount(t); count != 0 {
		t.Fatalf("refund rows after rejected requests = %d, want 0", count)
	}
	requireLedgerCount(t, fixture.db, 0, "entry_type = ?", models.LedgerEntryTypeRefundHold)
	requirePostgresCount(t, fixture.db, &models.MoneyEventOutbox{}, "event_type = ?", constants.WebhookEventRefundRequestedV1, 0)
}

func TestRefundRepoCreateWithHoldGuardedRollsBackWhenOutboxFails(t *testing.T) {
	fixture := seedRefundGuardFixture(t, "1000")

	if err := fixture.db.Exec(`CREATE FUNCTION fail_refund_requested_outbox() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RAISE EXCEPTION ''synthetic outbox failure''; END'`).Error; err != nil {
		t.Fatalf("create failing outbox trigger function: %v", err)
	}
	if err := fixture.db.Exec(`CREATE TRIGGER fail_refund_requested_outbox BEFORE INSERT ON money_event_outboxes FOR EACH ROW EXECUTE FUNCTION fail_refund_requested_outbox()`).Error; err != nil {
		t.Fatalf("create failing outbox trigger: %v", err)
	}

	err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("50", "outbox-failure"), fixture.ledger)
	if err == nil {
		t.Fatal("refund creation must fail when the requested outbox event cannot be written")
	}
	if count := fixture.refundCount(t); count != 0 {
		t.Fatalf("refund rows after outbox failure = %d, want 0 (rolled back)", count)
	}
	requireLedgerCount(t, fixture.db, 0, "entry_type = ?", models.LedgerEntryTypeRefundHold)
	if total := fixture.activeTotalRaw(t); total != "0" {
		t.Fatalf("active total after outbox failure = %s, want 0", total)
	}
}

// TestRefundRepoCreateWithHoldGuardedReturnsAssetBusyWithoutWaiting reproduces the
// A7-R1 lock graph: another transaction (the deposit/reorg order) holds the exact
// ledger asset lock while the guard holds the payment row. The guard must refuse
// immediately instead of queueing behind the asset, its rollback must release the
// payment row, and the asset-first path must then be able to take the payment row
// without a deadlock.
func TestRefundRepoCreateWithHoldGuardedReturnsAssetBusyWithoutWaiting(t *testing.T) {
	fixture := seedRefundGuardFixture(t, "1000")
	assetKey := ledgerAssetLockMapKey(fixture.merchantID, &fixture.domainID, constants.Ethereum, nil)

	holder := fixture.db.Begin()
	if holder.Error != nil {
		t.Fatalf("begin asset holder: %v", holder.Error)
	}
	committed := false
	defer func() {
		if !committed {
			_ = holder.Rollback().Error
		}
	}()
	var acquired bool
	if err := holder.Raw("SELECT pg_try_advisory_xact_lock(hashtext(?))", assetKey).Row().Scan(&acquired); err != nil {
		t.Fatalf("hold asset lock: %v", err)
	}
	if !acquired {
		t.Fatal("asset lock was already held before the test started")
	}

	started := time.Now()
	err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("60", "asset-busy"), fixture.ledger)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrRefundAssetBusy) {
		t.Fatalf("guard error with a held asset lock = %v, want ErrRefundAssetBusy", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("busy refusal took %s; the guard must not wait for the asset lock", elapsed)
	}
	if count := fixture.refundCount(t); count != 0 {
		t.Fatalf("refund rows after the busy refusal = %d, want 0", count)
	}
	requireLedgerCount(t, fixture.db, 0, "entry_type = ?", models.LedgerEntryTypeRefundHold)
	requirePostgresCount(t, fixture.db, &models.MoneyEventOutbox{}, "event_type = ?", constants.WebhookEventRefundRequestedV1, 0)

	// The refused transaction released the payment row.
	var sessionID string
	if err := fixture.db.Raw("SELECT id::text FROM payment_sessions WHERE id = ? FOR UPDATE NOWAIT", fixture.sessionID).Scan(&sessionID).Error; err != nil {
		t.Fatalf("payment row must be free after the busy rollback: %v", err)
	}

	// The asset-first order (the shape the deposit/reorg correction path uses)
	// completes in one transaction while the asset lock is still held here.
	started = time.Now()
	var lockedSession models.PaymentSession
	if err := holder.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedSession, "id = ?", fixture.sessionID).Error; err != nil {
		t.Fatalf("asset-first path could not lock the payment row after the busy rollback: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("asset-first path waited %s for the payment row", time.Since(started))
	}
	if err := holder.Commit().Error; err != nil {
		t.Fatalf("commit asset-first path: %v", err)
	}
	committed = true
}

// TestRefundRepoCreateWithHoldGuardedRetryAfterBusyReReadsState proves that a retry
// after a busy refusal is a fresh transaction: it re-reads the payment state, the
// related transaction (a reorg-style change of the actual amount) and the active
// refund totals instead of reusing the earlier limit.
func TestRefundRepoCreateWithHoldGuardedRetryAfterBusyReReadsState(t *testing.T) {
	fixture := seedRefundGuardFixture(t, "1000")
	assetKey := ledgerAssetLockMapKey(fixture.merchantID, &fixture.domainID, constants.Ethereum, nil)

	holder := fixture.db.Begin()
	if holder.Error != nil {
		t.Fatalf("begin asset holder: %v", holder.Error)
	}
	defer func() { _ = holder.Rollback().Error }()
	var acquired bool
	if err := holder.Raw("SELECT pg_try_advisory_xact_lock(hashtext(?))", assetKey).Row().Scan(&acquired); err != nil || !acquired {
		t.Fatalf("hold asset lock: %v (acquired=%v)", err, acquired)
	}
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("60", "retry-after-busy"), fixture.ledger); !errors.Is(err, ErrRefundAssetBusy) {
		t.Fatalf("first attempt error = %v, want ErrRefundAssetBusy", err)
	}

	// While the caller backs off, the payment changes underneath it: a reorg-style
	// correction links a transaction whose actual amount is larger than expected.
	uniqueHash := "refund-retry-" + uuid.NewString()
	related := ledgerTestTransaction(fixture.merchantID, fixture.domainID, fixture.walletID, uniqueHash, "150")
	if err := fixture.db.WithContext(fixture.ctx).Create(&related).Error; err != nil {
		t.Fatalf("seed related transaction: %v", err)
	}
	if err := fixture.db.Model(&models.PaymentSession{}).Where("id = ?", fixture.sessionID).Updates(map[string]any{
		"tx_unique_hash": uniqueHash,
		"status":         models.PaymentStatusPending,
	}).Error; err != nil {
		t.Fatalf("mutate payment during backoff: %v", err)
	}
	if err := holder.Commit().Error; err != nil {
		t.Fatalf("release asset lock: %v", err)
	}

	// The retry must observe the un-paid state rather than a cached answer.
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("60", "retry-after-busy"), fixture.ledger); !errors.Is(err, ErrRefundPaymentNotRefundable) {
		t.Fatalf("retry against an un-paid payment error = %v, want ErrRefundPaymentNotRefundable", err)
	}
	if err := fixture.db.Model(&models.PaymentSession{}).Where("id = ?", fixture.sessionID).Update("status", models.PaymentStatusPaid).Error; err != nil {
		t.Fatalf("re-pay session: %v", err)
	}
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("120", "retry-after-busy"), fixture.ledger); err != nil {
		t.Fatalf("retry after the reorg-style actual amount must use the fresh limit: %v", err)
	}
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("40", "retry-after-busy-over"), fixture.ledger); !errors.Is(err, ErrRefundLimitExceeded) {
		t.Fatalf("fresh active total must still cap the next refund: error = %v, want ErrRefundLimitExceeded", err)
	}
	if total := fixture.activeTotalRaw(t); total != "120" {
		t.Fatalf("active total after the retry = %s, want 120 (cap from the reorg actual 150)", total)
	}
}

// TestRefundRepoCreateWithHoldGuardedRejectsAssetMismatchAndNonPendingStatus covers
// the same-transaction asset consistency checks: a request may fill missing asset
// fields from the session but may not disagree with it, and creation must start in
// pending (a failed-status row must not slip past the active cap).
func TestRefundRepoCreateWithHoldGuardedRejectsAssetMismatchAndNonPendingStatus(t *testing.T) {
	fixture := seedRefundGuardFixture(t, "1000")

	mismatches := []func(*RefundCreateGuardedRequest){
		func(req *RefundCreateGuardedRequest) { req.Refund.Chain = "tron" },
		func(req *RefundCreateGuardedRequest) { req.Refund.Symbol = "BTC" },
		func(req *RefundCreateGuardedRequest) { req.Refund.Decimals = 8 },
		func(req *RefundCreateGuardedRequest) { other := "0xothertoken"; req.Refund.Token = &other },
	}
	for index, mutate := range mismatches {
		request := fixture.request("10", "asset-mismatch-"+uuid.NewString())
		mutate(&request)
		if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, request, fixture.ledger); !errors.Is(err, ErrRefundAssetMismatch) {
			t.Fatalf("asset mismatch %d error = %v, want ErrRefundAssetMismatch", index, err)
		}
	}
	if count := fixture.refundCount(t); count != 0 {
		t.Fatalf("refund rows after mismatched requests = %d, want 0", count)
	}

	// Empty asset fields are filled from the locked session.
	filled := fixture.request("50", "asset-filled")
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, filled, fixture.ledger); err != nil {
		t.Fatalf("refund with empty asset fields: %v", err)
	}
	stored := reloadRefundForTest(t, fixture.db, filled.Refund.ID)
	if !strings.EqualFold(stored.Chain, constants.ChainName(constants.Ethereum)) || stored.Symbol != "ETH" || stored.Decimals != 18 {
		t.Fatalf("stored refund asset = %s/%s/%d, want the session asset", stored.Chain, stored.Symbol, stored.Decimals)
	}

	// A non-pending creation status is refused and cannot bypass the active cap.
	active := fixture.request("50", "status-active")
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, active, fixture.ledger); err != nil {
		t.Fatalf("second active refund: %v", err)
	}
	for _, status := range []string{models.RefundStatusFailed, models.RefundStatusSucceeded, models.RefundStatusProcessing} {
		request := fixture.request("100", "status-"+status+"-"+uuid.NewString())
		request.Refund.Status = status
		if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, request, fixture.ledger); !errors.Is(err, ErrRefundCreateStatusInvalid) {
			t.Fatalf("creation with status %q error = %v, want ErrRefundCreateStatusInvalid", status, err)
		}
	}
	if count := fixture.refundCount(t); count != 2 {
		t.Fatalf("refund rows = %d, want only the two pending creates", count)
	}
}

// TestRefundRepoCreateWithHoldGuardedStoresCanonicalValues covers the A7-R2
// canonicalisation: a signed or zero-padded amount is stored as plain decimal so
// the active-total rule counts it, and a padded or blank creation status becomes
// pending so it cannot slip past the cap.
func TestRefundRepoCreateWithHoldGuardedStoresCanonicalValues(t *testing.T) {
	fixture := seedRefundGuardFixture(t, "1000")

	first := fixture.request("+60", "canonical-plus")
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, first, fixture.ledger); err != nil {
		t.Fatalf("create with a signed amount: %v", err)
	}
	if stored := reloadRefundForTest(t, fixture.db, first.Refund.ID); stored.AmountRaw != "60" {
		t.Fatalf("stored amount = %q, want the canonical 60", stored.AmountRaw)
	}
	if total := fixture.activeTotalRaw(t); total != "60" {
		t.Fatalf("active total after a signed amount = %s, want 60 (the row must be counted)", total)
	}
	second := fixture.request("+60", "canonical-plus-second")
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, second, fixture.ledger); !errors.Is(err, ErrRefundLimitExceeded) {
		t.Fatalf("second signed amount error = %v, want ErrRefundLimitExceeded", err)
	}

	third := seedRefundGuardFixture(t, "1000")
	leading := third.request("  0060  ", "canonical-leading-zero")
	if err := third.repo.CreateWithHoldGuarded(third.ctx, leading, third.ledger); err != nil {
		t.Fatalf("create with leading zeros: %v", err)
	}
	if stored := reloadRefundForTest(t, third.db, leading.Refund.ID); stored.AmountRaw != "60" {
		t.Fatalf("stored amount = %q, want 60", stored.AmountRaw)
	}
	if total := third.activeTotalRaw(t); total != "60" {
		t.Fatalf("active total with leading zeros = %s, want 60", total)
	}
	if err := third.repo.CreateWithHoldGuarded(third.ctx, third.request("40", "canonical-leading-40"), third.ledger); err != nil {
		t.Fatalf("second refund within the cap: %v", err)
	}
	over := third.request("1", "canonical-leading-over")
	if err := third.repo.CreateWithHoldGuarded(third.ctx, over, third.ledger); !errors.Is(err, ErrRefundLimitExceeded) {
		t.Fatalf("over-cap error = %v, want ErrRefundLimitExceeded", err)
	}

	fourth := seedRefundGuardFixture(t, "1000")
	if err := fourth.db.Model(&models.PaymentSession{}).Where("id = ?", fourth.sessionID).Update("expected_amount_raw", "200").Error; err != nil {
		t.Fatalf("raise the session cap: %v", err)
	}
	for _, status := range []string{" pending ", "   "} {
		request := fourth.request("60", "canonical-status-"+uuid.NewString())
		request.Refund.Status = status
		if err := fourth.repo.CreateWithHoldGuarded(fourth.ctx, request, fourth.ledger); err != nil {
			t.Fatalf("create with status %q: %v", status, err)
		}
		if stored := reloadRefundForTest(t, fourth.db, request.Refund.ID); stored.Status != models.RefundStatusPending {
			t.Fatalf("stored status for %q = %q, want pending", status, stored.Status)
		}
	}
	if total := fourth.activeTotalRaw(t); total != "120" {
		t.Fatalf("active total with status variants = %s, want 120 (both counted)", total)
	}
	overStatus := fourth.request("81", "canonical-status-over")
	if err := fourth.repo.CreateWithHoldGuarded(fourth.ctx, overStatus, fourth.ledger); !errors.Is(err, ErrRefundLimitExceeded) {
		t.Fatalf("over-cap after status variants error = %v, want ErrRefundLimitExceeded", err)
	}
}

// TestRefundRepoCreateWithHoldGuardedCanonicalizesTokens covers the token rules: a
// non-empty token must match the session exactly (base58 identifiers are
// case-sensitive), an empty pointer inherits the session token, and the recorded
// value is always the server-owned session token.
func TestRefundRepoCreateWithHoldGuardedCanonicalizesTokens(t *testing.T) {
	fixture := seedRefundGuardFixture(t, "1000")
	sessionToken := "So1MixedCaseTokenAbCdEf"
	if err := fixture.db.Model(&models.PaymentSession{}).Where("id = ?", fixture.sessionID).Update("selected_token", sessionToken).Error; err != nil {
		t.Fatalf("set session token: %v", err)
	}
	tokenDeposit := ledgerTestTransaction(fixture.merchantID, fixture.domainID, fixture.walletID, "refund-token-"+uuid.NewString(), "1000")
	tokenDeposit.Token = &sessionToken
	if err := fixture.ledger.PostStandaloneDepositAvailable(fixture.ctx, tokenDeposit); err != nil {
		t.Fatalf("seed token balance: %v", err)
	}

	wrongCase := fixture.request("10", "token-wrong-case")
	lower := strings.ToLower(sessionToken)
	wrongCase.Refund.Token = &lower
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, wrongCase, fixture.ledger); !errors.Is(err, ErrRefundAssetMismatch) {
		t.Fatalf("lowercased token error = %v, want ErrRefundAssetMismatch", err)
	}
	other := fixture.request("10", "token-other")
	otherToken := "OtherToken123"
	other.Refund.Token = &otherToken
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, other, fixture.ledger); !errors.Is(err, ErrRefundAssetMismatch) {
		t.Fatalf("foreign token error = %v, want ErrRefundAssetMismatch", err)
	}
	if count := fixture.refundCount(t); count != 0 {
		t.Fatalf("refund rows after rejected tokens = %d, want 0", count)
	}

	empty := fixture.request("40", "token-empty")
	emptyString := ""
	empty.Refund.Token = &emptyString
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, empty, fixture.ledger); err != nil {
		t.Fatalf("empty token pointer: %v", err)
	}
	if stored := reloadRefundForTest(t, fixture.db, empty.Refund.ID); stored.Token == nil || *stored.Token != sessionToken {
		t.Fatalf("stored token = %v, want the session token %q", stored.Token, sessionToken)
	}
	exact := fixture.request("10", "token-exact")
	exactToken := sessionToken
	exact.Refund.Token = &exactToken
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, exact, fixture.ledger); err != nil {
		t.Fatalf("exact token: %v", err)
	}
	if stored := reloadRefundForTest(t, fixture.db, exact.Refund.ID); stored.Token == nil || *stored.Token != sessionToken {
		t.Fatalf("stored exact token = %v, want %q", stored.Token, sessionToken)
	}

	native := seedRefundGuardFixture(t, "1000")
	nativeRequest := native.request("10", "token-native")
	nilToken := ""
	nativeRequest.Refund.Token = &nilToken
	if err := native.repo.CreateWithHoldGuarded(native.ctx, nativeRequest, native.ledger); err != nil {
		t.Fatalf("native token: %v", err)
	}
	if stored := reloadRefundForTest(t, native.db, nativeRequest.Refund.ID); stored.Token != nil {
		t.Fatalf("stored native token = %v, want nil", stored.Token)
	}
	if stored := reloadRefundForTest(t, native.db, nativeRequest.Refund.ID); stored.Chain != constants.ChainName(constants.Ethereum) || stored.Symbol != "ETH" || stored.Decimals != 18 {
		t.Fatalf("stored canonical asset = %s/%s/%d, want the session asset", stored.Chain, stored.Symbol, stored.Decimals)
	}
}

func reloadRefundForTest(t *testing.T, db *gorm.DB, id uuid.UUID) models.Refund {
	t.Helper()
	var refund models.Refund
	if err := db.First(&refund, "id = ?", id).Error; err != nil {
		t.Fatalf("reload refund %s: %v", id, err)
	}
	return refund
}

func TestRefundLimitRawRuleMatchesLegacyBranches(t *testing.T) {
	limit, err := RefundLimitRaw(RefundCapFacts{DonationLink: true, MatchedAmountRaw: "70", ExpectedAmountRaw: "0"})
	if err != nil || limit.String() != "70" {
		t.Fatalf("donation matched limit = %v/%v, want 70", limit, err)
	}
	if _, err := RefundLimitRaw(RefundCapFacts{DonationLink: true, ExpectedAmountRaw: "0", MatchedAmountRaw: "0"}); !errors.Is(err, ErrRefundExpectedAmountInvalid) {
		t.Fatalf("donation without any usable amount error = %v, want ErrRefundExpectedAmountInvalid", err)
	}
	limit, err = RefundLimitRaw(RefundCapFacts{ExpectedAmountRaw: "100"})
	if err != nil || limit.String() != "100" {
		t.Fatalf("expected limit = %v/%v, want 100", limit, err)
	}
	limit, err = RefundLimitRaw(RefundCapFacts{ExpectedAmountRaw: "100", TxUniqueHash: "0xabc", TxResolved: true, TxAmountRaw: "150"})
	if err != nil || limit.String() != "150" {
		t.Fatalf("larger related transaction limit = %v/%v, want 150", limit, err)
	}
	limit, err = RefundLimitRaw(RefundCapFacts{ExpectedAmountRaw: "100", TxUniqueHash: "0xabc", TxResolved: true, TxAmountRaw: "40"})
	if err != nil || limit.String() != "100" {
		t.Fatalf("smaller related transaction limit = %v/%v, want 100", limit, err)
	}
	limit, err = RefundLimitRaw(RefundCapFacts{ExpectedAmountRaw: "100", TxUniqueHash: "0xabc"})
	if err != nil || limit.String() != "100" {
		t.Fatalf("unresolvable related transaction limit = %v/%v, want the expected 100", limit, err)
	}
	if _, err = RefundLimitRaw(RefundCapFacts{ExpectedAmountRaw: "not-a-number"}); !errors.Is(err, ErrRefundExpectedAmountInvalid) {
		t.Fatalf("invalid expected amount error = %v, want ErrRefundExpectedAmountInvalid", err)
	}

	// The guarded path gathers those facts from the persisted transaction row: a
	// related transaction larger than the expected amount raises the cap.
	fixture := seedRefundGuardFixture(t, "1000")
	uniqueHash := "refund-cap-" + uuid.NewString()
	related := ledgerTestTransaction(fixture.merchantID, fixture.domainID, fixture.walletID, uniqueHash, "150")
	if err := fixture.db.WithContext(fixture.ctx).Create(&related).Error; err != nil {
		t.Fatalf("seed related transaction: %v", err)
	}
	if err := fixture.db.Model(&models.PaymentSession{}).Where("id = ?", fixture.sessionID).Update("tx_unique_hash", uniqueHash).Error; err != nil {
		t.Fatalf("link related transaction: %v", err)
	}
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("120", "cap-from-transaction"), fixture.ledger); err != nil {
		t.Fatalf("refund above the expected amount but below the actual amount: %v", err)
	}
	if err := fixture.repo.CreateWithHoldGuarded(fixture.ctx, fixture.request("40", "cap-from-transaction-over"), fixture.ledger); !errors.Is(err, ErrRefundLimitExceeded) {
		t.Fatalf("refund above the actual amount error = %v, want ErrRefundLimitExceeded", err)
	}
}
