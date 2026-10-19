package main

import (
	"encoding/json"
	"html"
	"html/template"
	"strings"
	"testing"

	"core/api/handlers"
	"core/localization"
)

// TEST-01C1 — real render-time escaping: hostile user values must never appear as raw markup,
// and the trusted i18n payload must stay a separate, parseable JSON element.

var hostileValues = []string{
	`<script>alert(1)</script>`,
	`</script><script>alert(2)</script>`,
	`<img src=x onerror=alert(1)>`,
	`"quoted' & <b>bold</b>`,
	`$&$1%s`,
	`Active`,
	`https://ok.example/?q=1&x=2`,
}

func renderInvoiceForSecurity(t *testing.T, locale localization.Locale, hostile string) string {
	t.Helper()
	engine, err := template.New("invoice.html").Funcs(template.FuncMap{"t": localization.T}).
		ParseFiles("views/invoice.html")
	if err != nil {
		t.Fatalf("parse invoice.html: %v", err)
	}
	data := map[string]any{
		"Locale": locale, "I18NJSON": localization.JSBundle(locale), "I18NElementID": localization.BundleScriptElementID,
		"SwitchURLZH": "/invoice/tok?lang=zh-CN", "SwitchURLEN": "/invoice/tok?lang=en",
		"Title":       localization.T(locale, "Invoice #%s", hostile),
		"OrderID":     hostile,
		"ProductName": hostile, "ProductDescription": hostile, "DomainURL": hostile,
		"StatusLabel": hostile, "StatusClass": "paid", "Amount": hostile, "Currency": hostile,
		"Symbol": hostile, "ChainName": hostile, "CreatedAt": hostile, "TxHash": hostile,
		"CheckoutURL": "/checkout/tok?next=" + hostile, "IsPaid": true,
	}
	var out strings.Builder
	if err := engine.ExecuteTemplate(&out, "invoice.html", data); err != nil {
		t.Fatalf("render invoice.html (%s): %v", locale, err)
	}
	return out.String()
}

func TestInvoiceEscapesHostileUserValues(t *testing.T) {
	for _, locale := range []localization.Locale{localization.ZHCN, localization.EN} {
		for _, hostile := range hostileValues {
			page := renderInvoiceForSecurity(t, locale, hostile)
			for _, raw := range []string{"<script>alert(1)</script>", "</script><script>", "<img src=x onerror", "<b>bold</b>"} {
				if strings.Contains(page, raw) {
					t.Errorf("%s/%q: raw markup %q survived escaping", locale, hostile, raw)
				}
			}
			// the escaped text must decode back to the original user value
			title := page[strings.Index(page, "<title>")+len("<title>"):]
			title = title[:strings.Index(title, "</title>")]
			if !strings.Contains(html.UnescapeString(title), hostile) {
				t.Errorf("%s: the escaped title does not round-trip to %q (got %q)", locale, hostile, title)
			}
		}
	}
}

func TestInvoiceI18NPayloadIsASeparateTrustedJSONElement(t *testing.T) {
	for _, locale := range []localization.Locale{localization.ZHCN, localization.EN} {
		page := renderInvoiceForSecurity(t, locale, hostileValues[0])
		marker := `id="` + localization.BundleScriptElementID + `"`
		index := strings.Index(page, marker)
		if index < 0 {
			t.Fatalf("%s: the i18n script element is missing", locale)
		}
		start := strings.Index(page[index:], ">")
		end := strings.Index(page[index:], "</script>")
		if start < 0 || end < 0 || end <= start {
			t.Fatalf("%s: malformed i18n element", locale)
		}
		body := page[index+start+1 : index+end]
		var payload struct {
			Locale  string            `json:"locale"`
			Strings map[string]string `json:"strings"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("%s: i18n payload is not valid JSON: %v", locale, err)
		}
		if payload.Locale != string(locale) {
			t.Errorf("%s: payload locale = %q", locale, payload.Locale)
		}
		if len(payload.Strings) == 0 {
			t.Errorf("%s: payload carries no strings", locale)
		}
		if strings.Contains(body, "<") || strings.Contains(body, "</script") {
			t.Errorf("%s: raw markup inside the JSON payload: %q", locale, body[:min(80, len(body))])
		}
		// user data never leaks into the trusted bundle
		for _, hostile := range hostileValues {
			if strings.Contains(body, hostile) {
				t.Errorf("%s: user value %q leaked into the i18n bundle", locale, hostile)
			}
		}
		// and the element is a non-executing script element
		if !strings.Contains(page[index-40:index], `type="application/json"`) {
			t.Errorf("%s: element is not declared as application/json", locale)
		}
	}
}

// A template function returning a hostile string must still be escaped by the engine when it
// lands in an attribute's JS context (the hostiles are supplied by a test stub, never stored
// in the catalog).
func TestInlineConfirmTextIsContextEscaped(t *testing.T) {
	hostile := `"; alert(1); //`
	funcs := template.FuncMap{"t": func(_ any, _ string, _ ...any) string { return hostile }}
	engine, err := template.New("admin_dashboard.html").Funcs(funcs).
		ParseFiles("views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatalf("parse admin_dashboard.html: %v", err)
	}
	data := englishDealerFixture()
	englishAdminFixture("security")(&data)
	var out strings.Builder
	if err := engine.ExecuteTemplate(&out, "admin_dashboard.html", data); err != nil {
		t.Fatalf("render admin_dashboard.html: %v", err)
	}
	page := out.String()
	if strings.Contains(page, `alert(1); //`) && !strings.Contains(page, `\u0022`) && !strings.Contains(page, "&#34;") {
		t.Error("attribute JS context did not escape the injected quote")
	}
	if !strings.Contains(page, "return confirm(") {
		t.Error("the confirm contract was lost")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = handlers.DealerPageData{}

// TEST-01C2 — the merchant and admin surfaces with non-empty rows: hostile user data in
// concrete row fields must be escaped, round-trip as plain text, and stay out of the bundle.
func TestMerchantAndAdminRowsEscapeHostileUserData(t *testing.T) {
	hostile := `<img src=x onerror=alert(1)>"$&$1%s已翻译`
	render := func(name string, locale localization.Locale, data handlers.DealerPageData) string {
		engine, err := template.New(name).Funcs(template.FuncMap{"t": localization.T}).
			ParseFiles("views/dealer/" + name)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		data.Locale = locale
		data.I18NJSON = localization.JSBundle(locale)
		data.I18NElementID = localization.BundleScriptElementID
		var out strings.Builder
		if err := engine.ExecuteTemplate(&out, name, data); err != nil {
			t.Fatalf("render %s (%s): %v", name, locale, err)
		}
		return out.String()
	}

	for _, locale := range []localization.Locale{localization.ZHCN, localization.EN} {
		merchant := englishDealerFixture()
		englishDashboardFixture(&merchant)
		merchant.Error = hostile
		merchant.Success = hostile
		merchant.Payments = []handlers.DealerPaymentView{{
			OrderID: hostile, Merchant: hostile, StatusLabel: hostile, Status: "pending",
			Amount: "10", Currency: "USDT", CreatedAt: "2026-01-01",
		}}
		page := render("dashboard.html", locale, merchant)
		if strings.Contains(page, "<img src=x onerror") {
			t.Errorf("%s: merchant row leaked raw markup", locale)
		}
		if !strings.Contains(html.UnescapeString(page), "已翻译") {
			t.Errorf("%s: merchant row user text did not round-trip", locale)
		}
		// The JSON element lives in layout.html (Fiber renders it around this page), so here the
		// isolation is asserted as: no user data inside any inline JSON payload of this page, and
		// none inside the trusted bundle the layout will inject.
		if strings.Contains(page, `type="application/json"`) && strings.Contains(page, "onerror") {
			t.Errorf("%s: merchant inline JSON payload carries user data", locale)
		}
		if bundle := string(localization.JSBundle(locale)); strings.Contains(bundle, hostile) || strings.Contains(bundle, "onerror") {
			t.Errorf("%s: merchant user data leaked into the trusted bundle", locale)
		}

		admin := englishDealerFixture()
		englishAdminFixture("security")(&admin)
		admin.AdminLoginEmail = hostile
		admin.AdminOutboundWhitelist = []handlers.DealerOutboundWhitelistView{{
			ID: "whitelist-1", Scope: "global", Chain: "TRON", Token: "USDT",
			Address: hostile, Label: hostile, IsActive: true,
		}}
		adminPage := render("admin_dashboard.html", locale, admin)
		if strings.Contains(adminPage, "<img src=x onerror") {
			t.Errorf("%s: admin whitelist row leaked raw markup", locale)
		}
		if !strings.Contains(html.UnescapeString(adminPage), "已翻译") {
			t.Errorf("%s: admin row user text did not round-trip", locale)
		}
		if strings.Contains(adminPage, `type="application/json"`) && strings.Contains(adminPage, "onerror") {
			t.Errorf("%s: admin inline JSON payload carries user data", locale)
		}
		if bundle := string(localization.JSBundle(locale)); strings.Contains(bundle, hostile) || strings.Contains(bundle, "onerror") {
			t.Errorf("%s: admin user data leaked into the trusted bundle", locale)
		}
		if !strings.Contains(adminPage, "return confirm(") {
			t.Errorf("%s: the admin confirm contract regressed", locale)
		}
	}
}

func i18nBodyOf(t *testing.T, page string) string {
	t.Helper()
	marker := `id="` + localization.BundleScriptElementID + `"`
	index := strings.Index(page, marker)
	if index < 0 {
		t.Fatalf("i18n element missing")
	}
	start := strings.Index(page[index:], ">")
	end := strings.Index(page[index:], "</script>")
	if start < 0 || end <= start {
		t.Fatalf("malformed i18n element")
	}
	return page[index+start+1 : index+end]
}
