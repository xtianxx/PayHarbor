package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	apiHandlers "core/api/handlers"
	"core/api/routes"
	coreApplication "core/application"
	"core/constants"
	"core/helpers"
	"core/models"
	"core/repositories"

	fiber "github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// A7-R1 HTTP evidence: the v1 refund endpoint must answer a busy ledger asset with
// a retryable 503, keep the idempotency key usable so the caller can repeat the
// same request, and keep the existing 400 over-limit response.

type v1RefundHTTPFixture struct {
	db            *gorm.DB
	app           *fiber.App
	merchantID    uuid.UUID
	domainID      uuid.UUID
	apiKey        string
	apiSecret     string
	paymentID     uuid.UUID
	holderSess    uuid.UUID
	nextTimestamp int64
}

func seedV1RefundHTTPFixture(t *testing.T) *v1RefundHTTPFixture {
	t.Helper()
	db := openOutboundGrantPostgresTestDB(t)
	useTestRouterForOutbound(t, db)
	router := coreApplication.CORE.Router
	ctx := context.Background()

	merchantID, domainID, walletID := uuid.New(), uuid.New(), uuid.New()
	apiKey := "a7r1-key-" + uuid.NewString()
	apiSecret := "a7r1-secret-" + uuid.NewString()
	hashedSecret, err := helpers.HMACSecret(apiSecret)
	if err != nil {
		t.Fatalf("hash api secret: %v", err)
	}
	hdAccountID := uint32(domainID[0])<<24 | uint32(domainID[1])<<16 | uint32(domainID[2])<<8 | uint32(domainID[3])
	if hdAccountID == 0 {
		hdAccountID = 1
	}
	suffix := strings.ReplaceAll(walletID.String(), "-", "")
	merchant := models.Merchant{ID: merchantID, Name: "A7R1 Merchant", Email: "a7r1-" + merchantID.String() + "@example.com", IsActive: true}
	domain := models.Domain{
		ID:          domainID,
		MerchantID:  merchantID,
		DomainURL:   "https://" + domainID.String() + ".example.com",
		APIKey:      apiKey,
		APISecret:   hashedSecret,
		APIScopes:   models.DefaultDomainAPIScopesCSV(),
		HDAccountID: hdAccountID,
	}
	wallet := models.Wallet{
		ID:          walletID,
		MerchantID:  merchantID,
		DomainID:    domainID,
		HDAccountID: hdAccountID,
		HDAddressId: 1,
	}
	seedUniqueWalletAddressesForTest(&wallet, "a7r1", suffix)
	// A merchant reserve domain + wallet so the handler does not create one.
	reserveDomainID, reserveWalletID := uuid.New(), uuid.New()
	reserveDomain := models.Domain{ID: reserveDomainID, MerchantID: merchantID, DomainURL: "_reserve_", APIKey: "reserve-" + reserveDomainID.String(), APISecret: "reserve-secret", HDAccountID: hdAccountID + 1}
	reserveSuffix := strings.ReplaceAll(reserveWalletID.String(), "-", "")
	reserveWallet := models.Wallet{ID: reserveWalletID, MerchantID: merchantID, DomainID: reserveDomainID, HDAccountID: hdAccountID, HDAddressId: 0}
	seedUniqueWalletAddressesForTest(&reserveWallet, "a7r1reserve", reserveSuffix)
	for name, value := range []any{&merchant, &domain, &wallet, &reserveDomain, &reserveWallet} {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("seed fixture row %d: %v", name, err)
		}
	}

	chainID := constants.Ethereum
	paymentID := uuid.New()
	newPaidSession := func(id uuid.UUID) models.PaymentSession {
		return models.PaymentSession{
			ID:                id,
			SessionToken:      "a7r1-" + uuid.NewString(),
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
	}
	session := newPaidSession(paymentID)
	holderSession := newPaidSession(uuid.New())
	if err := db.Create(&session).Error; err != nil {
		t.Fatalf("seed paid session: %v", err)
	}
	if err := db.Create(&holderSession).Error; err != nil {
		t.Fatalf("seed holder session: %v", err)
	}

	ledger := repositories.NewLedgerRepo(db)
	deposit := models.Transaction{
		ID:            uuid.New(),
		ChainID:       chainID,
		UniqueHash:    "a7r1-deposit-" + uuid.NewString(),
		Hash:          "a7r1-hash-" + uuid.NewString(),
		BlockNumber:   "1",
		BlockHash:     "a7r1-block-" + uuid.NewString(),
		Symbol:        "ETH",
		Decimals:      18,
		FromAddress:   "0xfrom",
		ToAddress:     "0xto",
		Amount:        "1000",
		Status:        models.TransactionStatusConfirmed,
		MerchantID:    &merchantID,
		DomainID:      &domainID,
		WalletID:      &walletID,
		Confirmations: 12,
	}
	if err := ledger.PostStandaloneDepositAvailable(ctx, deposit); err != nil {
		t.Fatalf("seed available balance: %v", err)
	}

	deps := v1RefundDeps(router)
	app := fiber.New()
	app.Post("/api/v1/refund/create", apiHandlers.HandleV1RefundCreate(deps))
	return &v1RefundHTTPFixture{db: db, app: app, merchantID: merchantID, domainID: domainID, apiKey: apiKey, apiSecret: apiSecret, paymentID: paymentID, holderSess: holderSession.ID, nextTimestamp: time.Now().Unix()}
}

// seedUniqueWalletAddressesForTest fills every uniquely indexed wallet address
// column so two fixture wallets in one schema cannot collide on an empty value.
func seedUniqueWalletAddressesForTest(wallet *models.Wallet, prefix, suffix string) {
	wallet.BitcoinAddress = prefix + "-btc-" + suffix
	wallet.EthereumAddress = "0x" + prefix + suffix
	wallet.AvalancheAddress = prefix + "-avax-" + suffix
	wallet.BinanceAddress = prefix + "-bnb-" + suffix
	wallet.BaseAddress = prefix + "-base-" + suffix
	wallet.ArbitrumAddress = prefix + "-arb-" + suffix
	wallet.UnichainAddress = prefix + "-uni-" + suffix
	wallet.TronAddress = prefix + "-tron-" + suffix
	wallet.SolanaAddress = prefix + "-sol-" + suffix
	wallet.ChilizAddress = prefix + "-chz-" + suffix
	wallet.ChilizSpicyAddress = prefix + "-spicy-" + suffix
}

// v1RefundDeps wires the refund-create handler the same way the real router does,
// minus the webhook delivery repo (the lifecycle delivery enqueue is a no-op then).
func v1RefundDeps(router *routes.Router) apiHandlers.V1APIDeps {
	return apiHandlers.V1APIDeps{
		DomainRepo:                  router.DomainRepo,
		WalletRepo:                  router.WalletRepo,
		PaymentRepo:                 router.PaymentRepo,
		WithdrawalRepo:              router.WithdrawalRepo,
		RefundRepo:                  router.RefundRepo,
		LedgerRepo:                  router.LedgerRepo,
		TransactionRepo:             router.TransactionRepo,
		MoneyEventOutboxRepo:        router.MoneyEventOutboxRepo,
		OutboundRepo:                router.OutboundTransactionRepo,
		ReconciliationRepo:          router.ReconciliationRepo,
		ActivityLogRepo:             router.ActivityLogRepo,
		OutboundPolicyRepo:          router.OutboundPolicyRepo,
		NetworkOperationalStateRepo: router.NetworkOperationalStateRepo,
		AssetRegistry:               router.AssetRegistry(),
		IdempotencyRepo:             router.IdempotencyRepo,
	}
}

func (f *v1RefundHTTPFixture) post(t *testing.T, idempotencyKey string, amountRaw string) (int, string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"payment_id": f.paymentID.String(), "amount_raw": amountRaw})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	body := bytes.NewReader(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/refund/create", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", f.apiKey)
	req.Header.Set("X-API-Secret", f.apiSecret)
	// Each request needs its own signature: the replay guard rejects a repeated
	// (timestamp, signature) pair, and a fresh key retry reuses the same body.
	f.nextTimestamp++
	timestamp := strconv.FormatInt(f.nextTimestamp, 10)
	req.Header.Set("X-Gateway-Timestamp", timestamp)
	req.Header.Set("X-Gateway-Signature", helpers.GenerateRequestSignature(f.apiSecret, http.MethodPost, "/api/v1/refund/create", timestamp, payload))
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := f.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return response.StatusCode, string(raw)
}

func (f *v1RefundHTTPFixture) refundCount(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := f.db.Model(&models.Refund{}).Where("payment_id = ?", f.paymentID).Count(&count).Error; err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	return count
}

// TestV1RefundCreateStoresCanonicalAmountAndKeepsCapAcrossSpellings drives the real
// signed HTTP handler with a "+60" amount: the stored refund must be the canonical
// decimal 60 (so the active-total rule counts it), a second +60 with a new key must
// be refused with the existing over-limit message, and replaying the first key must
// not create another refund.
func TestV1RefundCreateStoresCanonicalAmountAndKeepsCapAcrossSpellings(t *testing.T) {
	fixture := seedV1RefundHTTPFixture(t)

	status, body := fixture.post(t, "a7r2-canonical-key", "+60")
	if status != http.StatusCreated {
		t.Fatalf("first +60 request = %d (%s), want 201", status, body)
	}
	var refunds []models.Refund
	if err := fixture.db.Where("payment_id = ?", fixture.paymentID).Find(&refunds).Error; err != nil {
		t.Fatalf("list refunds: %v", err)
	}
	if len(refunds) != 1 {
		t.Fatalf("refunds after the first +60 = %d, want 1", len(refunds))
	}
	if refunds[0].AmountRaw != "60" || refunds[0].Status != models.RefundStatusPending {
		t.Fatalf("stored refund = %#v, want the canonical 60 pending row", refunds[0])
	}

	status, body = fixture.post(t, "a7r2-canonical-next", "+60")
	if status != http.StatusBadRequest || !strings.Contains(body, "refund amount exceeds refundable payment amount") {
		t.Fatalf("second +60 with a new key = %d (%s), want the existing 400 over-limit message", status, body)
	}
	if count := fixture.refundCount(t); count != 1 {
		t.Fatalf("refunds after the refused +60 = %d, want still 1", count)
	}

	status, body = fixture.post(t, "a7r2-canonical-key", "+60")
	if status != http.StatusOK {
		t.Fatalf("replayed request = %d (%s), want the stored 200 response", status, body)
	}
	if count := fixture.refundCount(t); count != 1 {
		t.Fatalf("refunds after the replay = %d, want still 1", count)
	}
}

// TestV1RefundCreateReturns503WhileAssetBusyAndRetriesWithSameKey holds the exact
// ledger asset lock from a realistic concurrent transaction (a refund hold on the
// same merchant/domain/asset), then drives the real HTTP handler.
func TestV1RefundCreateReturns503WhileAssetBusyAndRetriesWithSameKey(t *testing.T) {
	fixture := seedV1RefundHTTPFixture(t)

	holder := fixture.db.Begin()
	if holder.Error != nil {
		t.Fatalf("begin asset holder: %v", holder.Error)
	}
	var holderSession models.PaymentSession
	if err := holder.First(&holderSession, "id = ?", fixture.holderSess).Error; err != nil {
		t.Fatalf("load holder session: %v", err)
	}
	holdRefund := models.Refund{ID: uuid.New(), MerchantID: fixture.merchantID, DomainID: fixture.domainID, PaymentID: holderSession.ID, AmountRaw: "10", Status: models.RefundStatusPending}
	if err := repositories.NewLedgerRepo(holder).CreateRefundHoldWithDB(context.Background(), holder, holdRefund, holderSession); err != nil {
		_ = holder.Rollback().Error
		t.Fatalf("hold the ledger asset from a concurrent transaction: %v", err)
	}

	status, body := fixture.post(t, "a7r1-busy-key", "60")
	if status != http.StatusServiceUnavailable {
		_ = holder.Rollback().Error
		t.Fatalf("busy response = %d (%s), want 503", status, body)
	}
	if !strings.Contains(strings.ToLower(body), "busy") {
		t.Fatalf("busy body = %s, want a retryable busy message", body)
	}
	if count := fixture.refundCount(t); count != 0 {
		t.Fatalf("refunds for the payment after the busy response = %d, want 0", count)
	}

	if err := holder.Rollback().Error; err != nil {
		t.Fatalf("release asset holder: %v", err)
	}

	// The same Idempotency-Key must remain usable once the asset is free.
	status, body = fixture.post(t, "a7r1-busy-key", "60")
	if status != http.StatusCreated {
		t.Fatalf("retry with the same Idempotency-Key = %d (%s), want 201", status, body)
	}
	if count := fixture.refundCount(t); count != 1 {
		t.Fatalf("refunds after the retry = %d, want exactly 1", count)
	}
	var refunds []models.Refund
	if err := fixture.db.Where("payment_id = ?", fixture.paymentID).Find(&refunds).Error; err != nil {
		t.Fatalf("list refunds: %v", err)
	}
	if refunds[0].AmountRaw != "60" || refunds[0].Status != models.RefundStatusPending {
		t.Fatalf("refund = %#v, want the retried 60 pending refund", refunds[0])
	}
	var outboxCount int64
	if err := fixture.db.Model(&models.MoneyEventOutbox{}).Where("event_id = ?", refunds[0].ID.String()+":"+constants.WebhookEventRefundRequestedV1).Count(&outboxCount).Error; err != nil {
		t.Fatalf("count requested events: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("requested lifecycle events = %d, want exactly 1", outboxCount)
	}

	// Over-limit keeps the existing 400 response and message.
	status, body = fixture.post(t, "a7r1-over-key", "100")
	if status != http.StatusBadRequest || !strings.Contains(body, "refund amount exceeds refundable payment amount") {
		t.Fatalf("over-limit response = %d (%s), want the existing 400 over-limit message", status, body)
	}
	if count := fixture.refundCount(t); count != 1 {
		t.Fatalf("refunds after the over-limit request = %d, want still 1", count)
	}
}
