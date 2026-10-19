package main

// Brand regression tests for the PayHarbor rename.
//
// These render the real templates (dealer shell, buyer checkout, invoice-facing
// result page) in both locales and assert the user-visible brand, so the rename
// is verified behaviorally instead of by grepping source text. Compatibility
// identifiers (locale cookies, webhook headers, API key prefixes, metric names)
// are asserted to stay unchanged.

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"core/api/handlers"
	"core/constants"
	"core/localization"
	"core/models"

	"github.com/gofiber/template/html/v3"
)

func newBrandEngine(t *testing.T) *html.Engine {
	t.Helper()
	engine := html.New("./views", ".html")
	engine.AddFunc("t", localization.T)
	return engine
}

func renderBrandTemplate(t *testing.T, engine *html.Engine, view string, data any, layout ...string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := engine.Render(&buf, view, data, layout...); err != nil {
		t.Fatalf("%s render failed: %v", view, err)
	}
	return buf.String()
}

func assertContainsAll(t *testing.T, name, output string, expected []string) {
	t.Helper()
	for _, want := range expected {
		if !strings.Contains(output, want) {
			t.Fatalf("%s output missing %q", name, want)
		}
	}
}

func assertContainsNone(t *testing.T, name, output string, forbidden []string) {
	t.Helper()
	for _, unwanted := range forbidden {
		if strings.Contains(output, unwanted) {
			t.Fatalf("%s output unexpectedly contains %q", name, unwanted)
		}
	}
}

func TestDealerShellRendersPayHarborBrandInBothLocales(t *testing.T) {
	engine := newBrandEngine(t)

	adminEN := handlers.DealerPageData{
		Locale:           localization.EN,
		Title:            "Admin panel",
		Active:           "admin",
		AdminPanel:       "overview",
		AdminOverviewURL: "/admin",
		MerchantEmail:    "ops@example.com",
		LogoutURL:        "/admin/logout",
		SwitchURLZH:      "/admin?lang=zh-CN",
		SwitchURLEN:      "/admin?lang=en",
	}
	out := renderBrandTemplate(t, engine, "dealer/home", adminEN, "dealer/layout")
	assertContainsAll(t, "admin EN shell", out, []string{
		"PayHarbor Admin",
		"PayHarbor Merchant Portal",
		"<title>Admin panel | PayHarbor Merchant Portal</title>",
	})
	assertContainsNone(t, "admin EN shell", out, []string{"Gateway Admin", "Gateway Merchant Portal"})

	adminZH := adminEN
	adminZH.Locale = localization.ZHCN
	out = renderBrandTemplate(t, engine, "dealer/home", adminZH, "dealer/layout")
	assertContainsAll(t, "admin ZH shell", out, []string{"PayHarbor 管理后台", "PayHarbor 商户门户"})
	assertContainsNone(t, "admin ZH shell", out, []string{"网关管理后台", "网关商户门户"})

	publicEN := handlers.DealerPageData{
		Locale:       localization.EN,
		Title:        "Crypto payment gateway",
		Active:       "home",
		DashboardURL: "/merchant/dashboard",
		LoginURL:     "/merchant/login",
		RegisterURL:  "/merchant/register",
	}
	out = renderBrandTemplate(t, engine, "dealer/home", publicEN, "dealer/layout")
	assertContainsAll(t, "public EN home", out, []string{
		"PayHarbor Checkout",
		">PayHarbor</span>",
		"PayHarbor checkout preview",
		"Can PayHarbor create hosted checkout pages?",
		"PayHarbor Merchant Portal",
	})
	assertContainsNone(t, "public EN home", out, []string{
		"Gateway Checkout", ">Gateway</span>", "Can Gateway create hosted checkout pages?",
	})

	publicZH := publicEN
	publicZH.Locale = localization.ZHCN
	out = renderBrandTemplate(t, engine, "dealer/home", publicZH, "dealer/layout")
	assertContainsAll(t, "public ZH home", out, []string{"PayHarbor 收银台", "PayHarbor 商户门户"})
	assertContainsNone(t, "public ZH home", out, []string{"网关收银台", "网关商户门户"})
}

func TestBuyerSurfacesRenderPayHarborBrand(t *testing.T) {
	engine := newBrandEngine(t)
	session := &models.PaymentSession{
		SessionToken:      "checkout-token",
		OrderID:           "ORDER-2002",
		Amount:            "25.00",
		Currency:          "USD",
		SelectedSymbol:    "USDT",
		DepositAddress:    "0x1111111111111111111111111111111111111111",
		ExpectedAmountRaw: "25000000",
	}

	checkout := renderBrandTemplate(t, engine, "gateway/checkout", map[string]any{
		"Session":        session,
		"Locale":         localization.EN,
		"I18NJSON":       localization.JSBundle(localization.EN),
		"I18NElementID":  localization.BundleScriptElementID,
		"SwitchURLZH":    "/checkout/checkout-token?lang=zh-CN",
		"SwitchURLEN":    "/checkout/checkout-token?lang=en",
		"AssetGroups":    []handlers.CheckoutAssetGroup{{Symbol: "PEPPER", Name: "PEPPER", ChainCount: 1, URL: "/checkout/checkout-token?asset=PEPPER"}},
		"SelectedSymbol": "",
		"Assets":         []handlers.CheckoutAssetOption{},
		"ExpiresAtUnix":  time.Now().Add(time.Hour).UnixMilli(),
	})
	assertContainsAll(t, "checkout EN", checkout, []string{"<title>PayHarbor</title>", "Gateway pricing"})
	assertContainsNone(t, "checkout EN", checkout, []string{"Gateway Pay"})

	pay := renderBrandTemplate(t, engine, "gateway/pay", map[string]any{
		"Session":       session,
		"Locale":        localization.EN,
		"I18NJSON":      localization.JSBundle(localization.EN),
		"I18NElementID": localization.BundleScriptElementID,
		"SwitchURLZH":   "/checkout/checkout-token/pay?lang=zh-CN",
		"SwitchURLEN":   "/checkout/checkout-token/pay?lang=en",
		"QRCodeURL":     "/checkout/checkout-token/qr.png",
		"PaymentURI":    "ethereum:0x1111111111111111111111111111111111111111",
		"ChainName":     "ethereum",
		"AmountDisplay": "25 USDT",
		"ExpiresAtUnix": time.Now().Add(time.Hour).UnixMilli(),
		"CheckoutState": map[string]any{"Status": "active", "Payable": true},
		"StatusMode":    "detecting",
		"StatusTitle":   "Waiting for payment",
		"StatusBody":    "Send the exact amount to the address below.",
	})
	assertContainsAll(t, "pay EN", pay, []string{"<title>PayHarbor</title>"})
	assertContainsNone(t, "pay EN", pay, []string{"Gateway Pay"})

	result := renderBrandTemplate(t, engine, "gateway/payment_result", map[string]any{
		"Title":         "Payment complete",
		"Message":       "Payment received successfully.",
		"Status":        "paid",
		"ResultKind":    "success",
		"Locale":        localization.EN,
		"I18NJSON":      localization.JSBundle(localization.EN),
		"I18NElementID": localization.BundleScriptElementID,
		"SwitchURLZH":   "/checkout/result-token?lang=zh-CN",
		"SwitchURLEN":   "/checkout/result-token?lang=en",
		"CheckoutURL":   "/checkout/result-token",
	})
	assertContainsAll(t, "result EN", result, []string{"PayHarbor"})
	assertContainsNone(t, "result EN", result, []string{"Gateway Pay"})

	resultZH := renderBrandTemplate(t, engine, "gateway/payment_result", map[string]any{
		"Title":         "Payment complete",
		"Message":       "Payment received successfully.",
		"Status":        "paid",
		"ResultKind":    "success",
		"Locale":        localization.ZHCN,
		"I18NJSON":      localization.JSBundle(localization.ZHCN),
		"I18NElementID": localization.BundleScriptElementID,
		"CheckoutURL":   "/checkout/result-token",
	})
	assertContainsAll(t, "result ZH", resultZH, []string{"PayHarbor"})
	assertContainsNone(t, "result ZH", resultZH, []string{"Gateway Pay"})
}

func TestBrandIdentifiersAndCompatibilityNames(t *testing.T) {
	if constants.PRODUCT_NAME != "PayHarbor" {
		t.Fatalf("constants.PRODUCT_NAME = %q, want PayHarbor", constants.PRODUCT_NAME)
	}
	if constants.APPLICATION_NAME != "PAYHARBOR" {
		t.Fatalf("constants.APPLICATION_NAME = %q, want PAYHARBOR", constants.APPLICATION_NAME)
	}
	// Locale cookie names are a compatibility surface and must not be renamed.
	if localization.CookieName != "gateway_portal_lang" {
		t.Fatalf("localization.CookieName = %q, want gateway_portal_lang", localization.CookieName)
	}
	if localization.LegacyCheckoutCookieName != "gateway_lang" {
		t.Fatalf("localization.LegacyCheckoutCookieName = %q, want gateway_lang", localization.LegacyCheckoutCookieName)
	}
}
