package localization

import (
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// FIX-01A regression tests: public terminology, the six rich-select placeholders, the two
// inline confirmation dialogs and the invoice language switch. They are deliberately
// precise (exact attribute shapes) so the same file fails before the fix and passes after.

var reserveWalletKeys = []string{
	"Account was created but the reserve wallet could not be prepared. Please try again.",
	"Merchant reserve wallet could not be prepared: %s",
	"Reserve wallets could not be read: %s",
	"Sign-in completed but the reserve wallet could not be prepared. Please try again.",
	"Withdrawals can only be made from the merchant reserve wallet.",
}

func TestFix01AReserveWalletUsesApprovedTerm(t *testing.T) {
	for _, key := range reserveWalletKeys {
		got := T(ZHCN, key)
		if got == key {
			t.Errorf("no zh-CN entry for %q", key)
			continue
		}
		if strings.Contains(got, "备用钱包") {
			t.Errorf("%q still uses the un-approved term 备用钱包: %q", key, got)
		}
		if !strings.Contains(got, "储备钱包") {
			t.Errorf("%q does not use the approved term 储备钱包: %q", key, got)
		}
	}

	files, err := filepath.Glob("catalog_zh*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "备用钱包") {
			t.Errorf("%s still contains the un-approved term 备用钱包", file)
		}
	}
}

// placeholderExpectations maps each rich-select placeholder to the template that owns it.
var placeholderExpectations = map[string][]string{
	"../views/dealer/admin_dashboard.html": {
		"Select blockchain", "Select wallet", "Select asset", "Select chain", "Select target wallet",
	},
	"../views/dealer/dashboard.html": {
		"Select blockchain",
	},
}

var (
	// data-placeholder="Select blockchain" — a raw literal, i.e. un-instrumented.
	rawDataPlaceholder = regexp.MustCompile(`data-placeholder="([A-Z][^"{}]{3,})"`)
	// data-placeholder="{{ t .Locale "…" }}" — instrumented.
	tDataPlaceholder = regexp.MustCompile(`data-placeholder="\{\{\s*t\s+\$?\.Locale\s+"([^"]+)"\s*\}\}"`)
)

func TestFix01ARichSelectPlaceholdersAreLocalized(t *testing.T) {
	for file, literals := range placeholderExpectations {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)

		for _, literal := range literals {
			if !strings.Contains(text, `"`+literal+`"`) {
				t.Errorf("%s: expected literal %q was not found (fixture drift?)", file, literal)
				continue
			}
			if raw := rawDataPlaceholder.FindAllStringSubmatch(text, -1); len(raw) > 0 {
				for _, match := range raw {
					t.Errorf("%s: data-placeholder=%q is not localized", file, match[1])
				}
			}
		}

		for _, literal := range literals {
			// The literal must now be reachable through the translation helper.
			if got := T(ZHCN, literal); got == literal {
				t.Errorf("%s: %q has no zh-CN catalog entry (it would still render English)", file, literal)
			}
		}
	}
}

// TestFix01ATemplatePlaceholdersUseTrustedHelper pins the exact instrumented shape so the
// fix cannot regress into raw literals and so the value always comes from the catalog.
func TestFix01ATemplatePlaceholdersUseTrustedHelper(t *testing.T) {
	for file := range placeholderExpectations {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		instrumented := map[string]bool{}
		for _, match := range tDataPlaceholder.FindAllStringSubmatch(string(content), -1) {
			instrumented[match[1]] = true
		}
		if len(instrumented) == 0 {
			t.Errorf("%s: no data-placeholder uses {{ t .Locale … }}", file)
		}
	}
}

var (
	rawInlineConfirm    = regexp.MustCompile(`onsubmit="[^"]*confirm\('([^']{4,})'\)`)
	tInlineConfirm      = regexp.MustCompile(`onsubmit="[^"]*confirm\(\{\{\s*t\s+\$?\.Locale\s+"([^"]+)"\s*\}\}\)`)
	safeEscapeHatchUsed = regexp.MustCompile(`(safeJS|template\.HTML|noescape)`)
)

func TestFix01AInlineConfirmDialogsAreLocalized(t *testing.T) {
	const file = "../views/dealer/admin_dashboard.html"
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)

	expected := []string{"Update this network operational mode?", "Update the outbound policy?"}
	for _, literal := range expected {
		if !strings.Contains(text, literal) {
			t.Errorf("%s: expected confirmation %q was not found (fixture drift?)", file, literal)
		}
		if got := T(ZHCN, literal); got == literal {
			t.Errorf("%s: confirmation %q has no zh-CN catalog entry", file, literal)
		}
	}

	for _, match := range rawInlineConfirm.FindAllStringSubmatch(text, -1) {
		t.Errorf("%s: onsubmit confirm(%q) is not localized", file, match[1])
	}
	if len(tInlineConfirm.FindAllStringSubmatch(text, -1)) < len(expected) {
		t.Errorf("%s: fewer localized confirmations than expected (%d)", file, len(tInlineConfirm.FindAllStringSubmatch(text, -1)))
	}
	// The return-confirm contract must survive the change.
	if !strings.Contains(text, "return confirm(") {
		t.Error("the inline confirmations must keep the `return confirm(...)` form")
	}
	if safeEscapeHatchUsed.MatchString(text) {
		t.Error("admin_dashboard.html must not use safeJS/template.HTML/noescape")
	}
}

// invoiceFixture renders the invoice page with a minimal, credential-free fixture.
func invoiceFixture(locale Locale) map[string]any {
	zhURL := "/invoice/tok?lang=zh-CN"
	enURL := "/invoice/tok?lang=en"
	return map[string]any{
		"Locale":        locale,
		"I18NJSON":      template.JS(`{"locale":"zh-CN","strings":{}}`),
		"I18NElementID": BundleScriptElementID,
		"SwitchURLZH":   zhURL,
		"SwitchURLEN":   enURL,
		"Title":         T(locale, "Invoice #%s", "ORDER-1"),
		"OrderID":       "ORDER-1",
		"StatusLabel":   T(locale, "Completed"),
		"StatusClass":   "paid",
		"ProductName":   "Gateway Pay",
		"Amount":        "1.0",
		"Currency":      "USDT",
		"Symbol":        "USDT",
		"ChainName":     "TRON",
		"CreatedAt":     "2026-01-01",
		"DomainURL":     "https://shop.example",
		"CheckoutURL":   "/checkout/tok",
		"IsPaid":        true,
		"I18NElement":   BundleScriptElementID,
	}
}

func renderInvoice(t *testing.T, locale Locale) string {
	t.Helper()
	engine, err := template.New("invoice.html").Funcs(template.FuncMap{"t": T}).ParseFiles("../views/invoice.html")
	if err != nil {
		t.Fatalf("parse invoice.html: %v", err)
	}
	var out strings.Builder
	if err := engine.ExecuteTemplate(&out, "invoice.html", invoiceFixture(locale)); err != nil {
		t.Fatalf("render invoice.html: %v", err)
	}
	return out.String()
}

var (
	invoiceSwitchLink = regexp.MustCompile(`<a class="([^"]+)" href="([^"]+)"([^>]*)>`)
	ariaCurrentTrue   = regexp.MustCompile(`aria-current="true"`)
)

func TestFix01AInvoiceSwitchUsesExistingButtonClassAndLocaleAttributes(t *testing.T) {
	// Scope every assertion to the invoice header, where the switch lives. The rest of the
	// page legitimately uses invoice-button (the action row) and invoice-brand-sub (the
	// brand subline), so page-global counting would be meaningless.
	headerRegion := func(t *testing.T, page string) string {
		t.Helper()
		start := strings.Index(page, `<header class="invoice-header">`)
		end := strings.Index(page, `</header>`)
		if start < 0 || end <= start {
			t.Fatalf("invoice header region not found (start=%d end=%d)", start, end)
		}
		return page[start:end]
	}
	switchAnchors := regexp.MustCompile(`<a class="([^"]*)" href="([^"]*)"([^>]*)>`)

	for _, test := range []struct {
		locale   Locale
		marker   string
		otherURL string
	}{
		{ZHCN, "zh-CN", "/invoice/tok?lang=en"},
		{EN, "en", "/invoice/tok?lang=zh-CN"},
	} {
		header := headerRegion(t, renderInvoice(t, test.locale))

		switchLinks := make([][]string, 0, 2)
		for _, anchor := range switchAnchors.FindAllStringSubmatch(header, -1) {
			if strings.Contains(anchor[2], "lang=") {
				switchLinks = append(switchLinks, anchor)
			}
		}
		brandAnchors := 0
		for _, anchor := range switchAnchors.FindAllStringSubmatch(header, -1) {
			if !strings.Contains(anchor[2], "lang=") {
				brandAnchors++
				if anchor[1] != "invoice-brand" {
					t.Errorf("locale %s: unexpected non-switch header anchor %q", test.locale, anchor[0])
				}
			}
		}
		if brandAnchors != 1 {
			t.Errorf("locale %s: expected the single brand anchor to stay untouched, got %d", test.locale, brandAnchors)
		}
		if len(switchLinks) != 2 {
			t.Fatalf("locale %s: expected 2 switch anchors in the invoice header, got %d", test.locale, len(switchLinks))
		}
		for _, anchor := range switchLinks {
			class, href, attrs := anchor[1], anchor[2], anchor[3]
			if class != "invoice-button" {
				t.Errorf("locale %s: switch link class = %q, want invoice-button", test.locale, class)
			}
			if !strings.HasPrefix(href, "/invoice/") || !strings.Contains(href, "lang=") {
				t.Errorf("locale %s: switch link must be a local GET URL, got %q", test.locale, href)
			}
			for _, attr := range []string{`hreflang="`, `lang="`, `title="`} {
				if !strings.Contains(attrs, attr) {
					t.Errorf("locale %s: switch link %q is missing %s", test.locale, href, attr)
				}
			}
			active := strings.Contains(attrs, `aria-current="true"`)
			if active && !strings.Contains(href, "lang="+test.marker) {
				t.Errorf("locale %s: aria-current sits on the wrong link: %s", test.locale, href)
			}
			if !active && strings.Contains(href, "lang="+test.marker) {
				t.Errorf("locale %s: the active locale link %s must carry aria-current", test.locale, href)
			}
		}
		if got := len(ariaCurrentTrue.FindAllString(header, -1)); got != 1 {
			t.Errorf("locale %s: exactly one aria-current=true expected in the header, got %d", test.locale, got)
		}
		if !strings.Contains(header, `hreflang="zh-CN"`) || !strings.Contains(header, `hreflang="en"`) {
			t.Errorf("locale %s: both target languages must be declared", test.locale)
		}
		if !strings.Contains(header, test.otherURL) {
			t.Errorf("locale %s: the other language link %s is missing", test.locale, test.otherURL)
		}
		if strings.Contains(header, `<a class="invoice-brand-sub"`) {
			t.Errorf("locale %s: switch anchors must not use invoice-brand-sub", test.locale)
		}
		if strings.Contains(header, "<form") {
			t.Error("switch links must not be placed inside a form")
		}
	}
}

// TestFix01AInvoiceSwitchEscapesHostileURLs proves the switch hrefs are template-escaped
// rather than trusted markup: a javascript: URL must be neutralised by html/template.
func TestFix01AInvoiceSwitchEscapesHostileURLs(t *testing.T) {
	fixture := invoiceFixture(ZHCN)
	fixture["SwitchURLZH"] = "javascript:alert(1)"
	engine, err := template.New("invoice.html").Funcs(template.FuncMap{"t": T}).ParseFiles("../views/invoice.html")
	if err != nil {
		t.Fatalf("parse invoice.html: %v", err)
	}
	var out strings.Builder
	if err := engine.ExecuteTemplate(&out, "invoice.html", fixture); err != nil {
		t.Fatalf("render invoice.html: %v", err)
	}
	html := out.String()
	if strings.Contains(html, `href="javascript:alert(1)"`) {
		t.Error("a javascript: switch URL must not be emitted as-is")
	}
	if !strings.Contains(html, "ZgotmplZ") {
		t.Error("expected html/template to neutralise the javascript: URL (ZgotmplZ)")
	}
}
