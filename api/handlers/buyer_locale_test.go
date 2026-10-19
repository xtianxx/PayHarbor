package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"core/asset"
	"core/constants"
	"core/models"
	"core/repositories"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// buyerFlowTurkishMarkers are the Turkish display words (with and without diacritics) that
// the pre-004 hosted checkout rendered. None of them may appear on a buyer page in either
// supported locale, together with the retired language metadata.
var buyerFlowTurkishMarkers = []string{
	"Türkçe", "Türkce", "tr.svg", `lang="tr"`, `hreflang="tr"`, "lang=tr",
	"Ödeme", "ödeme", "Odeme", "odeme",
	"Sipariş", "Siparis", "sipariş",
	"Kopyala", "Kopyalandı", "İptal", "Iptal", "iptal",
	"Güvenli", "guvenli", "Tutar", "tutar", "Seç", "seçin", "seç",
	"bekleniyor", "Bekleniyor", "Durum", "durum",
	"Yatırılan", "yatirilan", "Makbuz", "makbuz", "Dekont", "dekont",
	"Tamamlandı", "tamamlandi", "Başarısız", "basarisiz",
	"Eksik ödeme", "eksik odeme", "Fazla ödeme", "fazla odeme",
	"Kısmi ödeme", "kismi odeme", "Ağ seç", "ağ", "Adres", "adres",
	"Devam et", "devam et", "Tekrar dene", "tekrar dene",
	"İşlem detayları", "islem detaylari", "Cart", "Cüzdan", "cüzdan",
}

// assertNoTurkishBuyerCopy fails when a rendered buyer page still carries Turkish copy or
// retired language metadata.
func assertNoTurkishBuyerCopy(t *testing.T, page string, html string) {
	t.Helper()
	for _, marker := range buyerFlowTurkishMarkers {
		if strings.Contains(html, marker) {
			t.Fatalf("%s still renders Turkish marker %q: %s", page, marker, html)
		}
	}
}

// buyerLocaleTestApp wires the buyer flow (checkout, pay, result, status) over the given
// session repository.
func buyerLocaleTestApp(repo *fakePaymentSessionRepo, registry *asset.Registry) *fiber.App {
	app := fiber.New(fiber.Config{Views: newHandlerTestViews()})
	deps := PaymentHandlerDeps{PaymentRepo: repo, AssetRegistry: registry, PriceOracle: panicPriceOracle{}}
	app.Get("/checkout/:token", HandleCheckout(deps))
	app.Get("/checkout/:token/pay", HandleCheckoutPay(deps))
	app.Get("/checkout/:token/return/success", HandleCheckoutSuccessReturn(deps))
	return app
}

func buyerLocaleTestRegistry() *asset.Registry {
	registry := asset.NewRegistry()
	registry.Register(asset.NewEVMNative(constants.Ethereum, "ETH", "Ethereum", 18))
	registry.Register(asset.NewERC20(constants.Ethereum, "0x1111111111111111111111111111111111111111", "USDT", "Tether USD", 6))
	return registry
}

func buyerSelectSession(token string) *models.PaymentSession {
	future := time.Now().Add(10 * time.Minute)
	return &models.PaymentSession{
		ID:           uuid.New(),
		SessionToken: token,
		Status:       models.PaymentStatusPending,
		Amount:       "10",
		Currency:     "USD",
		ExpiresAt:    &future,
		Wallet: models.Wallet{
			EthereumAddress: "0x2222222222222222222222222222222222222222",
		},
	}
}

func buyerPaySession(token string) *models.PaymentSession {
	chainID := constants.Ethereum
	digits := uint8(6)
	future := time.Now().Add(10 * time.Minute)
	return &models.PaymentSession{
		ID:                uuid.New(),
		SessionToken:      token,
		Status:            models.PaymentStatusAwaitingPayment,
		Amount:            "10",
		Currency:          "USD",
		ExpiresAt:         &future,
		SelectedChainID:   &chainID,
		SelectedSymbol:    "USDT",
		SelectedDecimals:  digits,
		ExpectedAmountRaw: "1000000",
		DepositAddress:    "0x2222222222222222222222222222222222222222",
		Wallet: models.Wallet{
			EthereumAddress: "0x2222222222222222222222222222222222222222",
		},
	}
}

func buyerResultSession(token string) *models.PaymentSession {
	now := time.Now()
	chainID := constants.Ethereum
	session := buyerPaySession(token)
	session.Status = models.PaymentStatusPaid
	session.PaidAt = &now
	session.MatchedAmountRaw = "1000000"
	session.SelectedChainID = &chainID
	session.OrderID = "order-1"
	return session
}

func buyerGet(t *testing.T, app *fiber.App, target string, cookies map[string]string, headers map[string]string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for name, value := range cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// TestBuyerFlowDefaultsToSimplifiedChinese is evidence A: a client with no language cookie
// and no lang parameter gets zh-CN on the checkout, the pay page and the result page, even
// when the browser advertises English through Accept-Language.
func TestBuyerFlowDefaultsToSimplifiedChinese(t *testing.T) {
	registry := buyerLocaleTestRegistry()
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{
		buyerSelectSession("select-default"),
		buyerPaySession("pay-default"),
		buyerResultSession("result-default"),
	}}
	app := buyerLocaleTestApp(repo, registry)
	acceptEnglish := map[string]string{"Accept-Language": "en-US,en;q=0.9"}

	cases := []struct {
		page   string
		target string
		anchor string
	}{
		{"checkout", "/checkout/select-default", "安全结账"},
		{"pay", "/checkout/pay-default/pay", "请精确发送"},
		{"result", "/checkout/result-default/return/success", "支付状态"},
	}
	for _, tc := range cases {
		status, html := buyerGet(t, app, tc.target, nil, acceptEnglish)
		if status != fiber.StatusOK {
			t.Fatalf("%s status = %d", tc.page, status)
		}
		if !strings.Contains(html, `lang="zh-CN"`) {
			t.Fatalf("%s did not render lang=\"zh-CN\": %s", tc.page, html)
		}
		if !strings.Contains(html, tc.anchor) {
			t.Fatalf("%s did not render the Chinese anchor %q: %s", tc.page, tc.anchor, html)
		}
		assertNoTurkishBuyerCopy(t, tc.page, html)
	}
}

// TestBuyerFlowIgnoresRetiredLanguageInputs is the rest of evidence A/C: a legacy cookie, a
// retired query value and a legacy product language all resolve to the zh-CN default.
func TestBuyerFlowIgnoresRetiredLanguageInputs(t *testing.T) {
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{buyerSelectSession("select-legacy")}}
	app := buyerLocaleTestApp(repo, buyerLocaleTestRegistry())

	targets := []struct {
		name    string
		target  string
		cookies map[string]string
	}{
		{"retired legacy cookie", "/checkout/select-legacy", map[string]string{"gateway_lang": "tr"}},
		{"retired canonical cookie", "/checkout/select-legacy", map[string]string{"gateway_portal_lang": "tr"}},
		{"retired query", "/checkout/select-legacy?lang=tr", nil},
		{"retired query with english cookie", "/checkout/select-legacy?lang=tr", map[string]string{"gateway_portal_lang": "en"}},
	}
	for _, tc := range targets {
		status, html := buyerGet(t, app, tc.target, tc.cookies, nil)
		if status != fiber.StatusOK {
			t.Fatalf("%s status = %d", tc.name, status)
		}
		if tc.name == "retired query with english cookie" {
			if !strings.Contains(html, `lang="en"`) {
				t.Fatalf("%s: a retired query must not mask the recorded English choice: %s", tc.name, html)
			}
			continue
		}
		if !strings.Contains(html, `lang="zh-CN"`) {
			t.Fatalf("%s did not fall back to zh-CN: %s", tc.name, html)
		}
		assertNoTurkishBuyerCopy(t, tc.name, html)
	}
}

// TestBuyerFlowHonorsExplicitEnglishChoice is evidence B: a recorded English choice (cookie
// or recognized query) renders English on every buyer page and never leaks Turkish.
func TestBuyerFlowHonorsExplicitEnglishChoice(t *testing.T) {
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{
		buyerSelectSession("select-en"),
		buyerPaySession("pay-en"),
		buyerResultSession("result-en"),
	}}
	app := buyerLocaleTestApp(repo, buyerLocaleTestRegistry())
	english := map[string]string{"gateway_portal_lang": "en"}

	cases := []struct {
		page   string
		target string
		anchor string
	}{
		{"checkout", "/checkout/select-en", "Secure checkout"},
		{"pay", "/checkout/pay-en/pay", "Send exactly"},
		{"result", "/checkout/result-en/return/success", "Paid amount"},
	}
	for _, tc := range cases {
		status, html := buyerGet(t, app, tc.target, english, nil)
		if status != fiber.StatusOK {
			t.Fatalf("%s status = %d", tc.page, status)
		}
		if !strings.Contains(html, `lang="en"`) {
			t.Fatalf("%s did not render lang=\"en\": %s", tc.page, html)
		}
		if !strings.Contains(html, tc.anchor) {
			t.Fatalf("%s did not render the English anchor %q", tc.page, tc.anchor)
		}
		// The legacy cookie keeps a recorded English choice alive for existing visitors.
		statusLegacy, legacyHTML := buyerGet(t, app, tc.target, map[string]string{"gateway_lang": "en"}, nil)
		if statusLegacy != fiber.StatusOK || !strings.Contains(legacyHTML, `lang="en"`) {
			t.Fatalf("%s ignored the recorded legacy English choice (status=%d): %s", tc.page, statusLegacy, legacyHTML)
		}
		assertNoTurkishBuyerCopy(t, tc.page, html)
		assertNoTurkishBuyerCopy(t, tc.page+" (legacy cookie)", legacyHTML)
	}
}

// TestBuyerFlowRendersRecognizedQueryOverride proves the documented priority: a recognized
// query value wins over a recorded cookie for that response.
func TestBuyerFlowRendersRecognizedQueryOverride(t *testing.T) {
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{buyerSelectSession("select-override")}}
	app := buyerLocaleTestApp(repo, buyerLocaleTestRegistry())

	status, html := buyerGet(t, app, "/checkout/select-override?lang=en", map[string]string{"gateway_portal_lang": "zh-CN"}, nil)
	if status != fiber.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if !strings.Contains(html, `lang="en"`) {
		t.Fatalf("recognized lang query did not win over the cookie: %s", html)
	}
}

// TestBuyerFlowLocaleIsIsolatedAcrossSessions is evidence D: concurrent requests with
// different recorded locales render their own locale and never bleed into each other.
func TestBuyerFlowLocaleIsIsolatedAcrossSessions(t *testing.T) {
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{
		buyerSelectSession("select-concurrent"),
	}}
	app := buyerLocaleTestApp(repo, buyerLocaleTestRegistry())

	const iterations = 40
	var wg sync.WaitGroup
	errorsSeen := make(chan string, iterations*2)
	for range iterations {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, html := buyerGet(t, app, "/checkout/select-concurrent", map[string]string{"gateway_portal_lang": "zh-CN"}, nil)
			if !strings.Contains(html, `<html lang="zh-CN">`) || !strings.Contains(html, "安全结账") || strings.Contains(html, `<html lang="en">`) {
				errorsSeen <- "zh-CN session rendered another locale: " + html
			}
		}()
		go func() {
			defer wg.Done()
			_, html := buyerGet(t, app, "/checkout/select-concurrent", map[string]string{"gateway_portal_lang": "en"}, nil)
			if !strings.Contains(html, `<html lang="en">`) || !strings.Contains(html, "Secure checkout") || strings.Contains(html, "安全结账") {
				errorsSeen <- "en session rendered another locale: " + html
			}
		}()
	}
	wg.Wait()
	close(errorsSeen)
	for message := range errorsSeen {
		t.Fatal(message)
	}
}

// TestBuyerFlowSwitchLinksStayOnTheSamePath proves the language control keeps working: each
// page offers both locales through ?lang=… on its own path with the other query parameters
// preserved, and the active option is marked exactly once.
func TestBuyerFlowSwitchLinksStayOnTheSamePath(t *testing.T) {
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{buyerSelectSession("select-switch")}}
	app := buyerLocaleTestApp(repo, buyerLocaleTestRegistry())

	status, html := buyerGet(t, app, "/checkout/select-switch?asset=USDT", nil, nil)
	if status != fiber.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if !strings.Contains(html, `href="/checkout/select-switch?asset=USDT&amp;lang=en"`) {
		t.Fatalf("english switch link did not preserve the path and query: %s", html)
	}
	if !strings.Contains(html, `href="/checkout/select-switch?asset=USDT&amp;lang=zh-CN"`) {
		t.Fatalf("chinese switch link did not preserve the path and query: %s", html)
	}
	if strings.Count(html, `aria-current="page"`) != 1 {
		t.Fatalf("switch must mark exactly one active locale: %s", html)
	}
	if !strings.Contains(html, `hreflang="zh-CN"`) || !strings.Contains(html, `hreflang="en"`) {
		t.Fatalf("switch links must advertise the supported locales: %s", html)
	}
}

// TestBuyerPagesShipTheLocaleBundle proves the browser copy comes from the server payload:
// each buyer page embeds the JSON bundle of its locale and loads the shared runtime.
func TestBuyerPagesShipTheLocaleBundle(t *testing.T) {
	repo := &fakePaymentSessionRepo{sessions: []*models.PaymentSession{
		buyerSelectSession("select-bundle"),
		buyerPaySession("pay-bundle"),
		buyerResultSession("result-bundle"),
	}}
	app := buyerLocaleTestApp(repo, buyerLocaleTestRegistry())

	for _, target := range []string{
		"/checkout/select-bundle",
		"/checkout/pay-bundle/pay",
		"/checkout/result-bundle/return/success",
	} {
		status, html := buyerGet(t, app, target, nil, nil)
		if status != fiber.StatusOK {
			t.Fatalf("%s status = %d", target, status)
		}
		if !strings.Contains(html, `id="portal-i18n-data"`) {
			t.Fatalf("%s did not embed the i18n bundle element: %s", target, html)
		}
		if !strings.Contains(html, `"/assets/portal-i18n.js"`) {
			t.Fatalf("%s did not load the bundle runtime", target)
		}
		if !strings.Contains(html, `"locale":"zh-CN"`) {
			t.Fatalf("%s bundle did not carry the zh-CN locale", target)
		}
		if !strings.Contains(html, "已复制") || !strings.Contains(html, "选择币种") {
			t.Fatalf("%s bundle did not carry the translated browser strings", target)
		}
	}
}

// TestPaymentLinkErrorPageFollowsTheBuyerLocale proves the payment-link entry point
// renders its failure page in the buyer's locale: zh-CN by default (even with a browser
// that advertises English), English when the visitor recorded it, and never the retired
// Turkish copy. The entry point itself is exercised end to end against the offline stack;
// this covers the page it renders when the link cannot be resolved.
func TestPaymentLinkErrorPageFollowsTheBuyerLocale(t *testing.T) {
	deps := DealerDeps{
		ProductRepo: repositories.NewProductRepo(unreachableTestDB(t)),
		PaymentRepo: repositories.NewPaymentRepo(unreachableTestDB(t)),
		WalletRepo:  repositories.NewWalletRepo(repositories.NewDomainRepo(repositories.NewMerchantRepo(unreachableTestDB(t), nil))),
	}
	app := fiber.New(fiber.Config{Views: newHandlerTestViews()})
	app.Get("/payment-links/:token", HandlePaymentLink(deps))

	cases := []struct {
		name    string
		cookies map[string]string
		want    string
		title   string
	}{
		{"default chinese", nil, `lang="zh-CN"`, "支付链接不可用"},
		{"recorded english", map[string]string{"gateway_portal_lang": "en"}, `lang="en"`, "Payment link unavailable"},
		{"retired turkish cookie is ignored", map[string]string{"gateway_lang": "tr"}, `lang="zh-CN"`, "支付链接不可用"},
	}
	for _, tc := range cases {
		status, html := buyerGet(t, app, "/payment-links/missing-token", tc.cookies, map[string]string{"Accept-Language": "en-GB"})
		if status != fiber.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", tc.name, status)
		}
		if !strings.Contains(html, tc.want) {
			t.Fatalf("%s did not render %s: %s", tc.name, tc.want, html)
		}
		if !strings.Contains(html, tc.title) {
			t.Fatalf("%s did not render the localized title %q", tc.name, tc.title)
		}
		assertNoTurkishBuyerCopy(t, tc.name, html)
	}
}

// unreachableTestDB returns a gorm handle that fails on first use without dialling: the
// driver is opened with DisableAutomaticPing and the caller's context is already
// cancelled, so the handler under test takes its not-found path without a live database.
func unreachableTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  "host=127.0.0.1 port=1 user=test dbname=test sslmode=disable",
		PreferSimpleProtocol: true,
	}), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Discard,
	})
	if err != nil {
		t.Fatalf("open test handle: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return db.WithContext(ctx)
}
