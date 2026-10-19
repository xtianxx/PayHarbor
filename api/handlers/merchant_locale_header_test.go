package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"core/localization"
	"core/models"
)

// newMerchantLocaleApp renders only the resolved locale, so the test pins the resolution
// rules without a database, session or template.
func newMerchantLocaleApp() *fiber.App {
	app := fiber.New()
	app.Get("/merchant/dashboard", func(c fiber.Ctx) error {
		loc := merchantPageLocale(c)
		c.Set("X-Test-Locale", string(loc.Canonical()))
		if isMerchantFastNavPartial(c) {
			c.Vary(MerchantLocaleHeader)
			c.Set(MerchantLocaleHeader, string(loc.Canonical()))
		}
		return c.SendString(string(loc.Canonical()))
	})
	app.Get("/api/v1/x", func(c fiber.Ctx) error { return c.SendString(string(portalLocale(c).Canonical())) })
	app.Get("/admin", func(c fiber.Ctx) error { return c.SendString(string(merchantPageLocale(c).Canonical())) })
	app.Get("/api/v1/merchant-helper", func(c fiber.Ctx) error { return c.SendString(string(merchantPageLocale(c).Canonical())) })
	app.Get("/merchant/dashboard/*", func(c fiber.Ctx) error { return c.SendString(string(merchantPageLocale(c).Canonical())) })
	app.Get("/merchant/domains/*", func(c fiber.Ctx) error { return c.SendString(string(merchantPageLocale(c).Canonical())) })
	app.Get("/merchant/dashboardx", func(c fiber.Ctx) error { return c.SendString(string(merchantPageLocale(c).Canonical())) })
	app.Get("/checkout/*", func(c fiber.Ctx) error { return c.SendString(string(merchantPageLocale(c).Canonical())) })
	app.Get("/merchant/domains", func(c fiber.Ctx) error {
		loc := merchantPageLocale(c)
		if isMerchantFastNavPartial(c) {
			c.Set(MerchantLocaleHeader, string(loc.Canonical()))
		}
		return c.SendString(string(loc.Canonical()))
	})
	app.Post("/merchant/dashboard", func(c fiber.Ctx) error {
		c.Set("X-Test-Locale", string(merchantPageLocale(c).Canonical()))
		return c.SendString(string(merchantPageLocale(c).Canonical()))
	})
	return app
}

func TestMerchantPartialLocaleHeaderWinsOverCookie(t *testing.T) {
	app := newMerchantLocaleApp()
	cases := []struct {
		name    string
		path    string
		header  map[string]string
		cookie  string
		want    string
		partial bool
	}{
		{"partial header en with zh cookie renders en", "/merchant/dashboard", map[string]string{MerchantLocaleHeader: "en", "X-Merchant-Navigation": "partial"}, "zh-CN", "en", true},
		{"partial header zh-CN with en cookie renders zh-CN", "/merchant/dashboard", map[string]string{MerchantLocaleHeader: "zh-CN", "X-Merchant-Navigation": "partial"}, "en", "zh-CN", true},
		{"invalid header falls back to the cookie", "/merchant/dashboard", map[string]string{MerchantLocaleHeader: "fr", "X-Merchant-Navigation": "partial"}, "en", "en", true},
		{"missing header falls back to the cookie", "/merchant/dashboard", map[string]string{"X-Merchant-Navigation": "partial"}, "en", "en", true},
		{"normal request ignores the header", "/merchant/dashboard", map[string]string{MerchantLocaleHeader: "en"}, "zh-CN", "zh-CN", false},
		{"lang query wins over the cookie on a normal request", "/merchant/dashboard?lang=en", nil, "zh-CN", "en", false},
		{"no header and no cookie defaults to zh-CN", "/merchant/dashboard", nil, "", "zh-CN", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		for k, v := range tc.header {
			req.Header.Set(k, v)
		}
		if tc.cookie != "" {
			req.AddCookie(&http.Cookie{Name: localization.CookieName, Value: tc.cookie})
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		body := make([]byte, 16)
		n, _ := resp.Body.Read(body)
		if got := string(body[:n]); got != tc.want {
			t.Errorf("%s: locale = %q, want %q", tc.name, got, tc.want)
		}
		if tc.partial && resp.Header.Get(MerchantLocaleHeader) != tc.want {
			t.Errorf("%s: response %s = %q, want %q", tc.name, MerchantLocaleHeader, resp.Header.Get(MerchantLocaleHeader), tc.want)
		}
	}
}

// The header must never leak into API surfaces.
func TestMerchantPartialLocaleHeaderDoesNotAffectAPI(t *testing.T) {
	app := newMerchantLocaleApp()
	req := httptest.NewRequest("GET", "/api/v1/x", nil)
	req.Header.Set(MerchantLocaleHeader, "en")
	req.Header.Set("X-Merchant-Navigation", "partial")
	req.AddCookie(&http.Cookie{Name: localization.CookieName, Value: "zh-CN"})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 16)
	n, _ := resp.Body.Read(body)
	if got := string(body[:n]); got != "zh-CN" {
		t.Errorf("API locale = %q, want the cookie locale zh-CN (header must not apply)", got)
	}
}

// The private hint is canonical-exact and scoped: aliases, non-GET methods and
// non-merchant surfaces must all fall back to the normal resolver.
func TestMerchantLocaleHeaderScopeAndCanonical(t *testing.T) {
	app := newMerchantLocaleApp()
	partial := map[string]string{"X-Merchant-Navigation": "partial"}
	req := func(method, path string, hdr map[string]string, cookie string) (string, string) {
		r := httptest.NewRequest(method, path, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: localization.CookieName, Value: cookie})
		}
		resp, err := app.Test(r)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		body := make([]byte, 16)
		n, _ := resp.Body.Read(body)
		return string(body[:n]), resp.Header.Get("Set-Cookie")
	}
	cases := []struct {
		name, method, path, header, cookie, want string
		hdr                                      map[string]string
	}{
		{"alias en-US is rejected, cookie wins", "GET", "/merchant/dashboard", "en-US", "zh-CN", "zh-CN", partial},
		{"alias zh-Hans is rejected, cookie wins", "GET", "/merchant/dashboard", "zh-Hans", "en", "en", partial},
		{"exact en is honoured", "GET", "/merchant/dashboard", "en", "zh-CN", "en", partial},
		{"exact zh-CN is honoured", "GET", "/merchant/domains", "zh-CN", "en", "zh-CN", partial},
		{"POST ignores the hint", "POST", "/merchant/dashboard", "en", "zh-CN", "zh-CN", partial},
		{"admin surface ignores the hint", "GET", "/admin", "en", "zh-CN", "zh-CN", partial},
		{"api surface ignores the hint", "GET", "/api/v1/x", "en", "zh-CN", "zh-CN", partial},
		{"hint without the partial header is ignored", "GET", "/merchant/dashboard", "en", "zh-CN", "zh-CN", nil},
		{"nested activity/payments honours the hint", "GET", "/merchant/dashboard/activity/payments", "en", "zh-CN", "en", partial},
		{"nested users honours the hint", "GET", "/merchant/dashboard/users", "zh-CN", "en", "zh-CN", partial},
		{"nested domains path honours the hint", "GET", "/merchant/domains/foo", "en", "zh-CN", "en", partial},
		{"prefix boundary /merchant/dashboardx ignores the hint", "GET", "/merchant/dashboardx", "en", "zh-CN", "zh-CN", partial},
		{"checkout path ignores the hint", "GET", "/checkout/tok-1", "en", "zh-CN", "zh-CN", partial},
		{"API path through the merchant helper ignores the hint", "GET", "/api/v1/merchant-helper", "en", "zh-CN", "zh-CN", partial},
	}
	for _, tc := range cases {
		got, setCookie := req(tc.method, tc.path, func() map[string]string {
			h := map[string]string{MerchantLocaleHeader: tc.header}
			for k, v := range tc.hdr {
				h[k] = v
			}
			return h
		}(), tc.cookie)
		if got != tc.want {
			t.Errorf("%s: locale = %q, want %q", tc.name, got, tc.want)
		}
		if setCookie != "" {
			t.Errorf("%s: response must not write a language cookie, got %q", tc.name, setCookie)
		}
	}
	// HEAD must never receive the partial locale metadata for the hint
	hr := httptest.NewRequest("HEAD", "/merchant/dashboard", nil)
	hr.Header.Set(MerchantLocaleHeader, "en")
	hr.Header.Set("X-Merchant-Navigation", "partial")
	hr.AddCookie(&http.Cookie{Name: localization.CookieName, Value: "zh-CN"})
	hresp, err := app.Test(hr)
	if err != nil {
		t.Fatal(err)
	}
	if hresp.StatusCode != 200 {
		t.Errorf("HEAD status = %d, want 200", hresp.StatusCode)
	}
	if got := hresp.Header.Get("X-Test-Locale"); got != "zh-CN" {
		t.Errorf("HEAD resolved locale = %q, want zh-CN (the hint must not apply)", got)
	}
	if v := hresp.Header.Get(MerchantLocaleHeader); v != "" {
		t.Errorf("HEAD must not emit %s, got %q", MerchantLocaleHeader, v)
	}
	if sc := hresp.Header.Values("Set-Cookie"); len(sc) != 0 {
		t.Errorf("HEAD must not write cookies, got %v", sc)
	}
	// query priority on a normal (non-partial) request is unchanged
	gotQuery, queryCookie := req("GET", "/merchant/dashboard?lang=en", nil, "zh-CN")
	if gotQuery != "en" {
		t.Errorf("query priority regressed: %q", gotQuery)
	}
	if queryCookie != "" {
		t.Errorf("query selection must not write a language cookie, got %q", queryCookie)
	}
}

// The row labels produced by the merchant builders must follow the document locale the
// request carries, not the shared portal cookie, while raw data stays untouched.
func TestMerchantPartialBuilderLabelsFollowTheDocumentLocale(t *testing.T) {
	app := fiber.New()
	app.Get("/merchant/dashboard", func(c fiber.Ctx) error {
		loc := merchantPageLocale(c)
		products := []models.Product{{Name: "Demo product", LinkType: "fixed", Amount: "10.00", Currency: "USD", Language: "en"}}
		payments := []models.PaymentSession{{Status: models.PaymentStatusPaid, Amount: "10.00", Currency: "USD", SessionToken: "tok-1", LinkType: "fixed"}}
		pv := dealerProductViews(c, loc, products)
		pav := dealerPaymentViews(c, loc, payments)
		return c.JSON(fiber.Map{
			"linkType": pv[0].LinkTypeLabel, "amountDisplay": pv[0].AmountDisplay,
			"statusLabel": pav[0].StatusLabel, "statusClass": pav[0].StatusClass,
			"rawAmount": pav[0].Amount, "rawProductAmount": pv[0].Amount, "productName": pv[0].Name,
		})
	})
	fetch := func(header, cookie string) map[string]string {
		r := httptest.NewRequest("GET", "/merchant/dashboard", nil)
		r.Header.Set(MerchantLocaleHeader, header)
		r.Header.Set("X-Merchant-Navigation", "partial")
		r.AddCookie(&http.Cookie{Name: localization.CookieName, Value: cookie})
		resp, err := app.Test(r)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if sc := resp.Header.Values("Set-Cookie"); len(sc) != 0 {
			t.Errorf("partial response must not write cookies, got %v", sc)
		}
		return out
	}
	en := fetch("en", "zh-CN") // English document, Chinese cookie
	zh := fetch("zh-CN", "en") // Chinese document, English cookie
	wantENFixed := localization.T(localization.EN, "Fixed amount")
	wantZHFixed := localization.T(localization.ZHCN, "Fixed amount")
	wantENPaid := localization.T(localization.EN, "Paid")
	wantZHPaid := localization.T(localization.ZHCN, "Paid")
	if en["linkType"] != wantENFixed || en["statusLabel"] != wantENPaid {
		t.Errorf("English document labels = %q / %q, want %q / %q", en["linkType"], en["statusLabel"], wantENFixed, wantENPaid)
	}
	if zh["linkType"] != wantZHFixed || zh["statusLabel"] != wantZHPaid {
		t.Errorf("Chinese document labels = %q / %q, want %q / %q", zh["linkType"], zh["statusLabel"], wantZHFixed, wantZHPaid)
	}
	if wantENFixed == wantZHFixed || wantENPaid == wantZHPaid {
		t.Fatal("catalog values are identical across locales; the assertion cannot detect a locale mix")
	}
	for _, got := range []map[string]string{en, zh} {
		if got["rawAmount"] != "10.00" || got["rawProductAmount"] != "10.00" || got["productName"] != "Demo product" || got["statusClass"] != "is-success" {
			t.Errorf("raw data must stay untouched, got %v", got)
		}
	}
}
