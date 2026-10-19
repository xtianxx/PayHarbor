package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"core/api/routes"
	coreApplication "core/application"
	"core/blockchain"
	"core/constants"
	"core/models"
	"core/repositories"
	"core/types"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A6-R1 behavioural tests for the atomic send grant. They run the real executor
// against PostgreSQL with the signer/chain boundary replaced, so "the signer was
// called" is observed directly (call count and the exact persisted snapshot)
// instead of being inferred from source structure.

func openOutboundGrantPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("OUTBOX_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("MONEY_OUTBOX_TEST_DATABASE_URL")
	}
	if dsn == "" {
		dsn = os.Getenv("TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set OUTBOX_TEST_DATABASE_URL to run outbound send-grant tests")
	}
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect test postgres: %v", err)
	}
	if err := adminDB.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error; err != nil {
		t.Fatalf("enable uuid extension: %v", err)
	}
	schemaName := "outbound_send_test_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	quotedSchema := `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `"`
	if err := adminDB.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	if err := adminDB.Exec(
		"CREATE FUNCTION " + quotedSchema + ".uuid_generate_v4() RETURNS uuid LANGUAGE SQL VOLATILE PARALLEL SAFE AS 'SELECT public.uuid_generate_v4()'",
	).Error; err != nil {
		t.Fatalf("create schema-local uuid function: %v", err)
	}
	db, err := gorm.Open(postgres.Open(outboundGrantDSNWithSearchPath(dsn, schemaName)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect schema-scoped test postgres: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get test sql db: %v", err)
	}
	// The race tests need genuinely concurrent transactions.
	sqlDB.SetMaxOpenConns(4)
	if err := db.AutoMigrate(
		&models.Merchant{},
		&models.Domain{},
		&models.Wallet{},
		&models.WithdrawalRequest{},
		&models.Refund{},
		&models.OutboundTransaction{},
		&models.OutboundChainResourceReservation{},
		&models.MoneyEventOutbox{},
		&models.ReconciliationJob{},
		&models.NetworkOperationalState{},
		&models.LedgerEntry{},
		&models.PaymentSession{},
		&models.Transaction{},
		&models.IdempotencyKey{},
		&models.OutboundPolicySetting{},
		&models.OutboundAddressWhitelist{},
		&models.ActivityLog{},
		&models.APISignedRequestReplay{},
		&models.SweepJob{},
		&models.WorkerLease{},
	); err != nil {
		t.Fatalf("automigrate outbound send-grant schema: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
		_ = adminDB.Exec("DROP SCHEMA IF EXISTS " + quotedSchema + " CASCADE").Error
		if adminSQL, adminErr := adminDB.DB(); adminErr == nil {
			_ = adminSQL.Close()
		}
	})
	return db
}

func outboundGrantDSNWithSearchPath(dsn, schemaName string) string {
	if strings.Contains(dsn, "://") {
		if parsed, err := url.Parse(dsn); err == nil {
			query := parsed.Query()
			query.Set("search_path", schemaName)
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}
	return strings.TrimSpace(dsn) + " search_path=" + schemaName
}

func useTestRouterForOutbound(t *testing.T, db *gorm.DB) {
	t.Helper()
	previous := coreApplication.CORE
	coreApplication.CORE = &coreApplication.App{DB: db, Router: routes.NewRouter(db)}
	t.Cleanup(func() { coreApplication.CORE = previous })
}

type outboundSendObservation struct {
	params types.TransferParams
}

// stubOutboundSend replaces the signer/chain boundary and records every call.
func stubOutboundSend(t *testing.T, result *blockchain.TransactionResult, sendErr error) *[]outboundSendObservation {
	t.Helper()
	var mu sync.Mutex
	observations := make([]outboundSendObservation, 0, 4)
	previous := outboundSendFunc
	outboundSendFunc = func(_ *repositories.WalletRepo, _ *blockchain.ChainFactory, params types.TransferParams, sweep bool) (*blockchain.TransactionResult, error) {
		if sweep {
			t.Error("outbound broadcast must not use the sweep transfer path")
		}
		mu.Lock()
		observations = append(observations, outboundSendObservation{params: params})
		mu.Unlock()
		return result, sendErr
	}
	t.Cleanup(func() { outboundSendFunc = previous })
	return &observations
}

type outboundSendFixture struct {
	merchantID   uuid.UUID
	domainID     uuid.UUID
	walletID     uuid.UUID
	withdrawalID uuid.UUID
	outbound     models.OutboundTransaction
}

func seedOutboundSendFixture(t *testing.T, db *gorm.DB, repo *repositories.OutboundTransactionRepo) outboundSendFixture {
	t.Helper()
	ctx := context.Background()
	merchantID, domainID, walletID := uuid.New(), uuid.New(), uuid.New()
	hdAccountID := uint32(domainID[0])<<24 | uint32(domainID[1])<<16 | uint32(domainID[2])<<8 | uint32(domainID[3])
	if hdAccountID == 0 {
		hdAccountID = 1
	}
	suffix := strings.ReplaceAll(walletID.String(), "-", "")
	merchant := models.Merchant{ID: merchantID, Name: "Send Grant Merchant", Email: "merchant-" + merchantID.String() + "@example.com", IsActive: true}
	domain := models.Domain{ID: domainID, MerchantID: merchantID, DomainURL: "https://" + domainID.String() + ".example.com", APIKey: "key-" + domainID.String(), APISecret: "secret", HDAccountID: hdAccountID}
	wallet := models.Wallet{
		ID:                 walletID,
		MerchantID:         merchantID,
		DomainID:           domainID,
		HDAccountID:        hdAccountID,
		HDAddressId:        0,
		EthereumAddress:    "0x" + suffix,
		BitcoinAddress:     "btc-" + suffix,
		TronAddress:        "tron-" + suffix,
		SolanaAddress:      "sol-" + suffix,
		AvalancheAddress:   "avax-" + suffix,
		BinanceAddress:     "bnb-" + suffix,
		BaseAddress:        "base-" + suffix,
		ArbitrumAddress:    "arb-" + suffix,
		UnichainAddress:    "uni-" + suffix,
		ChilizAddress:      "chz-" + suffix,
		ChilizSpicyAddress: "spicy-" + suffix,
	}
	if err := db.Create(&merchant).Error; err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	if err := db.Create(&domain).Error; err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if err := db.Create(&wallet).Error; err != nil {
		t.Fatalf("seed wallet: %v", err)
	}

	withdrawalID := uuid.New()
	request := models.WithdrawalRequest{
		ID:             withdrawalID,
		MerchantID:     merchantID,
		DomainID:       &domainID,
		WalletID:       walletID,
		Chain:          constants.ChainName(constants.Ethereum),
		Symbol:         "ETH",
		Decimals:       18,
		ToAddress:      "0xdestination",
		AmountRaw:      "1000000000000000000",
		Status:         models.WithdrawalStatusProcessing,
		RequestedBy:    "admin@example.com",
		IdempotencyKey: "withdrawal-" + uuid.NewString(),
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatalf("seed processing withdrawal: %v", err)
	}

	outbound, _, err := repo.Create(ctx, repositories.OutboundTransactionCreate{
		IdempotencyKey: "outbound-send-grant:" + uuid.NewString(),
		ResourceType:   models.OutboundResourceWithdrawal,
		ResourceID:     withdrawalID,
		MerchantID:     merchantID,
		DomainID:       &domainID,
		WalletID:       walletID,
		ChainID:        constants.Ethereum,
		ChainName:      constants.ChainName(constants.Ethereum),
		Symbol:         "ETH",
		Decimals:       18,
		AmountRaw:      "1000000000000000000",
		ToAddress:      "0xdestination",
		ActorID:        "admin@example.com",
		CorrelationID:  "corr-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create outbound row: %v", err)
	}
	return outboundSendFixture{merchantID: merchantID, domainID: domainID, walletID: walletID, withdrawalID: withdrawalID, outbound: *outbound}
}

func loadOutboundSendRow(t *testing.T, db *gorm.DB, id uuid.UUID) models.OutboundTransaction {
	t.Helper()
	var row models.OutboundTransaction
	if err := db.First(&row, "id = ?", id).Error; err != nil {
		t.Fatalf("reload outbound %s: %v", id, err)
	}
	return row
}

func loadWithdrawalSendRow(t *testing.T, db *gorm.DB, id uuid.UUID) models.WithdrawalRequest {
	t.Helper()
	var row models.WithdrawalRequest
	if err := db.First(&row, "id = ?", id).Error; err != nil {
		t.Fatalf("reload withdrawal %s: %v", id, err)
	}
	return row
}

func loadOutboundSendReservations(t *testing.T, db *gorm.DB, outboundID uuid.UUID) []models.OutboundChainResourceReservation {
	t.Helper()
	var rows []models.OutboundChainResourceReservation
	if err := db.Where("outbound_transaction_id = ?", outboundID).Find(&rows).Error; err != nil {
		t.Fatalf("list reservations for %s: %v", outboundID, err)
	}
	return rows
}

func countOutboundSendRows(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

// TestOutboundExecutionSendsPersistedGrantSnapshot proves the executor broadcasts
// the persisted grant snapshot and never the stale claimed row it was handed.
func TestOutboundExecutionSendsPersistedGrantSnapshot(t *testing.T) {
	db := openOutboundGrantPostgresTestDB(t)
	useTestRouterForOutbound(t, db)
	repo := coreApplication.CORE.Router.OutboundTransactionRepo
	fixture := seedOutboundSendFixture(t, db, repo)
	observations := stubOutboundSend(t, &blockchain.TransactionResult{TxHash: "0xgranted", Success: true}, nil)

	staleToken := "0xstaletoken"
	stale := fixture.outbound
	stale.WalletID = uuid.New()
	stale.AmountRaw = "1"
	stale.ToAddress = "0xstale"
	stale.Token = &staleToken
	stale.ActorID = "stale@example.com"

	if err := executeOutboundTransaction(context.Background(), stale); err != nil {
		t.Fatalf("execute outbound: %v", err)
	}
	if len(*observations) != 1 {
		t.Fatalf("signer calls = %d, want exactly 1", len(*observations))
	}
	params := (*observations)[0].params
	if params.WalletID == nil || *params.WalletID != fixture.walletID.String() {
		t.Fatalf("signer wallet = %v, want the persisted wallet %s", params.WalletID, fixture.walletID)
	}
	if params.AmountRaw == nil || *params.AmountRaw != fixture.outbound.AmountRaw {
		t.Fatalf("signer amount = %v, want the persisted amount %s", params.AmountRaw, fixture.outbound.AmountRaw)
	}
	if params.ToAddress == nil || *params.ToAddress != fixture.outbound.ToAddress {
		t.Fatalf("signer destination = %v, want the persisted destination %s", params.ToAddress, fixture.outbound.ToAddress)
	}
	if params.ActorID != fixture.outbound.ActorID {
		t.Fatalf("signer actor = %q, want the persisted actor %q", params.ActorID, fixture.outbound.ActorID)
	}
	if params.Token != nil {
		t.Fatalf("signer token = %v, want the persisted nil token", params.Token)
	}
	if params.JobID != fixture.outbound.ID.String() {
		t.Fatalf("signer job = %q, want outbound id %s", params.JobID, fixture.outbound.ID)
	}

	row := loadOutboundSendRow(t, db, fixture.outbound.ID)
	if row.Status != models.OutboundStatusBroadcasted || row.TxHash != "0xgranted" || row.BroadcastAttemptedAt == nil {
		t.Fatalf("outbound row = %#v, want one broadcasted attempt with the observed hash", row)
	}
	reservations := loadOutboundSendReservations(t, db, fixture.outbound.ID)
	if len(reservations) != 1 || reservations[0].Status != models.OutboundResourceReservationConsumed || reservations[0].TxHash != "0xgranted" {
		t.Fatalf("reservations = %#v, want one consumed chain resource", reservations)
	}
	withdrawal := loadWithdrawalSendRow(t, db, fixture.withdrawalID)
	if withdrawal.TxHash != "0xgranted" || withdrawal.Status != models.WithdrawalStatusProcessing {
		t.Fatalf("withdrawal = %#v, want the broadcast recorded while still processing", withdrawal)
	}
}

// TestOutboundExecutionBlockedStatesNeverReachTheSigner proves that every blocked
// state is invisible to the signer and leaves hold, ledger and chain resources
// untouched.
func TestOutboundExecutionBlockedStatesNeverReachTheSigner(t *testing.T) {
	db := openOutboundGrantPostgresTestDB(t)
	useTestRouterForOutbound(t, db)
	repo := coreApplication.CORE.Router.OutboundTransactionRepo
	observations := stubOutboundSend(t, &blockchain.TransactionResult{TxHash: "0xnever"}, nil)
	now := time.Now()

	cases := []struct {
		name      string
		mutations map[string]any
	}{
		{name: "prepared row with terminal category", mutations: map[string]any{"error_category": "terminal_failed"}},
		{name: "signed row with terminal category", mutations: map[string]any{"status": models.OutboundStatusSigned, "error_category": "broadcast_uncertain"}},
		{name: "finalized row", mutations: map[string]any{"status": models.OutboundStatusFinalized, "finalized_at": &now}},
		{name: "already broadcasted row", mutations: map[string]any{"status": models.OutboundStatusBroadcasted, "tx_hash": "0xprior", "broadcasted_at": &now}},
		{name: "attempted row with hash", mutations: map[string]any{"status": models.OutboundStatusBroadcastAttempted, "tx_hash": "0xprior", "broadcast_attempted_at": &now}},
		{name: "operator action row", mutations: map[string]any{"status": models.OutboundStatusNeedsOperatorAction}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := seedOutboundSendFixture(t, db, repo)
			if err := db.Model(&models.OutboundTransaction{}).Where("id = ?", fixture.outbound.ID).Updates(testCase.mutations).Error; err != nil {
				t.Fatalf("seed blocked state: %v", err)
			}
			ledgerBefore := countOutboundSendRows(t, db, &models.LedgerEntry{})
			outboxBefore := countOutboundSendRows(t, db, &models.MoneyEventOutbox{})
			callsBefore := len(*observations)

			_ = executeOutboundTransaction(context.Background(), fixture.outbound)

			if len(*observations) != callsBefore {
				t.Fatalf("signer calls = %d, want %d for a blocked outbound", len(*observations), callsBefore)
			}
			if reservations := loadOutboundSendReservations(t, db, fixture.outbound.ID); len(reservations) != 0 {
				t.Fatalf("blocked outbound created chain reservations: %#v", reservations)
			}
			if ledgerAfter := countOutboundSendRows(t, db, &models.LedgerEntry{}); ledgerAfter != ledgerBefore {
				t.Fatalf("ledger rows changed for a blocked outbound: %d -> %d", ledgerBefore, ledgerAfter)
			}
			if outboxAfter := countOutboundSendRows(t, db, &models.MoneyEventOutbox{}); outboxAfter != outboxBefore {
				t.Fatalf("money outbox rows changed for a blocked outbound: %d -> %d", outboxBefore, outboxAfter)
			}
			withdrawal := loadWithdrawalSendRow(t, db, fixture.withdrawalID)
			if withdrawal.TxHash != "" || withdrawal.Status != models.WithdrawalStatusProcessing || withdrawal.FinalizedAt != nil {
				t.Fatalf("blocked outbound mutated the hold: %#v", withdrawal)
			}
		})
	}

	// A resource that already settled carries finality evidence, so it is a hard
	// block: the row must stay untouched (never overwritten with an operator task)
	// and the signer must never be reached.
	settled := seedOutboundSendFixture(t, db, repo)
	if err := db.Model(&models.WithdrawalRequest{}).Where("id = ?", settled.withdrawalID).Updates(map[string]any{"status": models.WithdrawalStatusFinalized, "finalized_at": &now}).Error; err != nil {
		t.Fatalf("settle withdrawal: %v", err)
	}
	callsBefore := len(*observations)
	if err := executeOutboundTransaction(context.Background(), settled.outbound); err != nil {
		t.Fatalf("settled-resource execution returned error: %v", err)
	}
	if len(*observations) != callsBefore {
		t.Fatalf("signer was called for a settled resource: %d -> %d", callsBefore, len(*observations))
	}
	if row := loadOutboundSendRow(t, db, settled.outbound.ID); row.Status != models.OutboundStatusPrepared || row.ErrorCategory != "" || row.NextRunAt != nil {
		t.Fatalf("settled-resource row = %#v, want the untouched prepared row", row)
	}
	if reservations := loadOutboundSendReservations(t, db, settled.outbound.ID); len(reservations) != 0 {
		t.Fatalf("settled-resource outbound created chain reservations: %#v", reservations)
	}
	if reconciliations := countOutboundSendRows(t, db, &models.ReconciliationJob{}); reconciliations < 1 {
		t.Fatal("a hard resource block must open a reconciliation job")
	}

	// A resource that merely failed (no hash, no broadcast, no finality) is a soft
	// block: the existing rules route it to the operator queue and the signer is
	// still never reached.
	failed := seedOutboundSendFixture(t, db, repo)
	if err := db.Model(&models.WithdrawalRequest{}).Where("id = ?", failed.withdrawalID).Update("status", models.WithdrawalStatusFailed).Error; err != nil {
		t.Fatalf("fail withdrawal: %v", err)
	}
	callsBefore = len(*observations)
	if err := executeOutboundTransaction(context.Background(), failed.outbound); err != nil {
		t.Fatalf("failed-resource execution returned error: %v", err)
	}
	if len(*observations) != callsBefore {
		t.Fatalf("signer was called for a failed resource: %d -> %d", callsBefore, len(*observations))
	}
	if row := loadOutboundSendRow(t, db, failed.outbound.ID); row.Status != models.OutboundStatusNeedsOperatorAction || row.ErrorCategory != "outbound_execution_blocked" {
		t.Fatalf("failed-resource row = %#v, want an operator action with the blocked category", row)
	}
	if reservations := loadOutboundSendReservations(t, db, failed.outbound.ID); len(reservations) != 0 {
		t.Fatalf("failed-resource outbound created chain reservations: %#v", reservations)
	}
}

// TestOutboundExecutionConcurrentWorkersBroadcastOnce replays the production race:
// two workers execute the same claimed outbound row concurrently, and the signer
// must observe exactly one broadcast while the losing worker holds no chain
// resource it could release.
func TestOutboundExecutionConcurrentWorkersBroadcastOnce(t *testing.T) {
	db := openOutboundGrantPostgresTestDB(t)
	useTestRouterForOutbound(t, db)
	repo := coreApplication.CORE.Router.OutboundTransactionRepo
	fixture := seedOutboundSendFixture(t, db, repo)
	observations := stubOutboundSend(t, &blockchain.TransactionResult{TxHash: "0xonce", Success: true}, nil)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			errs[index] = executeOutboundTransaction(context.Background(), fixture.outbound)
		}(i)
	}
	close(start)
	wg.Wait()

	if len(*observations) != 1 {
		t.Fatalf("signer calls = %d, want exactly one broadcast for two workers", len(*observations))
	}
	if params := (*observations)[0].params; params.AmountRaw == nil || *params.AmountRaw != fixture.outbound.AmountRaw {
		t.Fatalf("signer amount = %v, want the persisted amount %s", params.AmountRaw, fixture.outbound.AmountRaw)
	}
	row := loadOutboundSendRow(t, db, fixture.outbound.ID)
	if row.Status != models.OutboundStatusBroadcasted || row.TxHash != "0xonce" || row.Attempts != 1 {
		t.Fatalf("outbound row = %#v, want exactly one broadcasted attempt", row)
	}
	reservations := loadOutboundSendReservations(t, db, fixture.outbound.ID)
	if len(reservations) != 1 {
		t.Fatalf("reservations = %#v, want exactly one chain resource", reservations)
	}
	if reservations[0].Status != models.OutboundResourceReservationConsumed || reservations[0].TxHash != "0xonce" {
		t.Fatalf("reservation = %#v, want the winner's resource consumed with the broadcast hash", reservations[0])
	}
	withdrawal := loadWithdrawalSendRow(t, db, fixture.withdrawalID)
	if withdrawal.TxHash != "0xonce" || withdrawal.Status != models.WithdrawalStatusProcessing {
		t.Fatalf("withdrawal = %#v, want exactly one recorded broadcast", withdrawal)
	}
	outboxAfter := countOutboundSendRows(t, db, &models.MoneyEventOutbox{})
	if outboxAfter != 1 {
		t.Fatalf("payout broadcast events = %d, want exactly one lifecycle event", outboxAfter)
	}
}

// TestOutboundExecutionPreBroadcastFailureReleasesOnlyItsOwnGrant covers the
// definite (non-broadcast) failure path: the grant holder releases the chain
// resource it holds, the resource is marked failed by the existing rules and no
// retry is scheduled.
func TestOutboundExecutionPreBroadcastFailureReleasesOnlyItsOwnGrant(t *testing.T) {
	db := openOutboundGrantPostgresTestDB(t)
	useTestRouterForOutbound(t, db)
	repo := coreApplication.CORE.Router.OutboundTransactionRepo
	fixture := seedOutboundSendFixture(t, db, repo)
	observations := stubOutboundSend(t, nil, errors.New("signer unavailable"))

	err := executeOutboundTransaction(context.Background(), fixture.outbound)
	if err == nil {
		t.Fatal("pre-broadcast failure must be reported")
	}
	if len(*observations) != 1 {
		t.Fatalf("signer calls = %d, want 1", len(*observations))
	}
	reservations := loadOutboundSendReservations(t, db, fixture.outbound.ID)
	if len(reservations) != 1 || reservations[0].Status != models.OutboundResourceReservationReleased {
		t.Fatalf("reservations = %#v, want the grant holder's resource released", reservations)
	}
	row := loadOutboundSendRow(t, db, fixture.outbound.ID)
	if row.Status != models.OutboundStatusNeedsOperatorAction || row.ErrorCategory != "broadcast_failed_before_network" || row.NextRunAt != nil {
		t.Fatalf("outbound row = %#v, want an operator action without a scheduled retry", row)
	}
	withdrawal := loadWithdrawalSendRow(t, db, fixture.withdrawalID)
	if withdrawal.Status != models.WithdrawalStatusFailed || withdrawal.TxHash != "" {
		t.Fatalf("withdrawal = %#v, want the resource failed without a broadcast hash", withdrawal)
	}
}
