package main

import (
	"bytes"
	"core/api/viewengine"
	"fmt"
	"github.com/gofiber/fiber/v3"
	"sync/atomic"
	"time"

	"core/api/handlers"
	"html/template"
	"strings"
	"sync"
	"testing"

	"core/localization"
)

// TEST-01C1 — 200 interleaved requests over one shared, pre-loaded engine must each render
// their own locale; a global page-locale would show up here immediately.

func TestConcurrentLocaleRenderingDoesNotBleed(t *testing.T) {
	engine, err := template.New("invoice.html").Funcs(template.FuncMap{"t": localization.T}).
		ParseFiles("views/invoice.html")
	if err != nil {
		t.Fatalf("parse invoice.html: %v", err)
	}
	fixture := func(locale localization.Locale) map[string]any {
		return map[string]any{
			"Locale": locale, "I18NJSON": localization.JSBundle(locale),
			"I18NElementID": localization.BundleScriptElementID,
			"SwitchURLZH":   "/invoice/tok?lang=zh-CN", "SwitchURLEN": "/invoice/tok?lang=en",
			"Title": "T", "OrderID": "ORDER-1", "ProductName": "Gateway Pay", "StatusLabel": "S",
			"StatusClass": "paid", "Amount": "1", "Currency": "USDT", "Symbol": "USDT",
			"ChainName": "TRON", "CreatedAt": "2026-01-01", "CheckoutURL": "/checkout/tok", "IsPaid": true,
		}
	}

	var wg sync.WaitGroup
	for i := range 200 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			locale := localization.ZHCN
			if i%2 == 1 {
				locale = localization.EN
			}
			var out strings.Builder
			if err := engine.ExecuteTemplate(&out, "invoice.html", fixture(locale)); err != nil {
				t.Errorf("iteration %d (%s): render failed: %v", i, locale, err)
				return
			}
			page := out.String()
			if !strings.Contains(page, `lang="`+locale.HTMLTag()+`"`) {
				t.Errorf("iteration %d: lang attribute is not %s", i, locale.HTMLTag())
			}
			anchor := localization.T(locale, "Total amount")
			if !strings.Contains(page, anchor) {
				t.Errorf("iteration %d (%s): localized anchor %q missing", i, locale, anchor)
			}
			other := localization.EN
			if locale == localization.EN {
				other = localization.ZHCN
			}
			if foreign := localization.T(other, "Total amount"); foreign != anchor && strings.Contains(page, foreign) {
				t.Errorf("iteration %d (%s): leaked the other locale's anchor %q", i, locale, foreign)
			}
			if !strings.Contains(page, `"locale":"`+string(locale)+`"`) {
				t.Errorf("iteration %d: i18n payload locale mismatch for %s", i, locale)
			}
		}(i)
	}
	wg.Wait()
}

// ENGINE-FIX: the production view engine (api/viewengine) serializes Load/Render around the
// upstream Fiber html engine, which is not concurrency-safe on its own. The batch starts 200
// goroutines at the same moment across mixed views (login, admin networks, invoice) with a
// per-request unique body marker, and asserts every page carries its own locale, its own
// marker and no other request's marker. Reload=false covers the real ./views templates (cold
// batch + repeated batch); the reload=true path runs against the small fixture templates.
func runConcurrentLocaleBatch(t *testing.T, engine fiber.Views, label string, invoiceOrder func(i int) string) {
	t.Helper()
	if err := engine.Load(); err != nil {
		t.Fatalf("%s: engine load: %v", label, err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var rendered int64
	begin := time.Now()
	for i := range 200 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			locale := localization.ZHCN
			if i%2 == 1 {
				locale = localization.EN
			}
			marker := fmt.Sprintf("REQ-%d-%s", i, locale)
			foreign := fmt.Sprintf("REQ-%d-%s", i+1, locale)
			anchor := localization.T(locale, "Sign in")
			<-start
			var out bytes.Buffer
			switch i % 3 {
			case 0:
				data := englishDealerFixture()
				data.Error = marker
				data.Locale = locale
				data.I18NJSON = localization.JSBundle(locale)
				data.I18NElementID = localization.BundleScriptElementID
				if err := engine.Render(&out, "dealer/login", data, "dealer/layout"); err != nil {
					t.Errorf("%s: iteration %d: %v", label, i, err)
					return
				}
			case 1:
				data := englishDealerFixture()
				englishAdminFixture("networks")(&data)
				data.AdminNetworkStates = []handlers.DealerNetworkOperationalStateView{{Chain: "TRON", ChainID: "tron", ChainSlug: "tron", Mode: "active", ModeLabel: localization.T(locale, "Active"), ModeClass: "badge-green", UpdatedBy: marker, UpdatedAt: marker}}
				data.Locale = locale
				data.I18NJSON = localization.JSBundle(locale)
				data.I18NElementID = localization.BundleScriptElementID
				anchor = localization.T(locale, "Sign out")
				if err := engine.Render(&out, "dealer/admin_dashboard", data, "dealer/layout"); err != nil {
					t.Errorf("%s: iteration %d: %v", label, i, err)
					return
				}
			default:
				orderID := invoiceOrder(i)
				data := map[string]any{
					"Locale": locale, "I18NJSON": localization.JSBundle(locale), "I18NElementID": localization.BundleScriptElementID,
					"SwitchURLZH": "/invoice/tok?lang=zh-CN", "SwitchURLEN": "/invoice/tok?lang=en",
					"Title": "T", "OrderID": orderID, "StatusLabel": localization.T(locale, "Completed"), "StatusClass": "paid",
					"Amount": "1", "Currency": "USDT", "AmountDisplay": "1 USDT", "Symbol": "USDT", "ChainName": "TRON",
					"CreatedAt": "2026-01-01", "CheckoutURL": "/checkout/tok", "IsPaid": true,
				}
				if err := engine.Render(&out, "invoice", data); err != nil {
					t.Errorf("%s: iteration %d: %v", label, i, err)
					return
				}
				marker = orderID
				foreign = invoiceOrder(i + 1)
				anchor = localization.T(locale, "Total amount")
			}
			page := out.String()
			atomic.AddInt64(&rendered, 1)
			if !strings.Contains(page, `lang="`+locale.HTMLTag()+`"`) {
				t.Errorf("%s: iteration %d: lang is not %s", label, i, locale.HTMLTag())
			}
			if !strings.Contains(page, `"locale":"`+string(locale)+`"`) {
				t.Errorf("%s: iteration %d: payload locale is not %s", label, i, locale)
			}
			if !strings.Contains(page, marker) {
				t.Errorf("%s: iteration %d: own marker %q is missing from the page", label, i, marker)
			}
			if strings.Contains(page, foreign) {
				t.Errorf("%s: iteration %d: page leaked another request's marker %q", label, i, foreign)
			}
			if !strings.Contains(page, anchor) {
				t.Errorf("%s: iteration %d: localized UI anchor %q missing", label, i, anchor)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	t.Logf("%s: %d/200 pages rendered in %s", label, atomic.LoadInt64(&rendered), time.Since(begin).Round(time.Millisecond))
}

func TestFiberEngineConcurrentLocaleRendering(t *testing.T) {
	production := viewengine.NewHTML("./views", false)
	invoiceMarker := func(i int) string { return fmt.Sprintf("REQ-%d-invoice", i) }
	runConcurrentLocaleBatch(t, production, "production-cold", invoiceMarker)
	runConcurrentLocaleBatch(t, production, "production-repeat", invoiceMarker)

	// reload=true re-parses on every request (development mode); the small fixture templates
	// keep that path covered at the same concurrency without pretending it is production.
	reload := viewengine.NewHTML("./api/viewengine/testdata", true)
	if err := reload.Load(); err != nil {
		t.Fatalf("reload engine load: %v", err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	begin := time.Now()
	for i := range 200 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			locale := "en"
			if i%2 == 1 {
				locale = "zh-CN"
			}
			marker := fmt.Sprintf("REQ-%d", i)
			<-start
			var out bytes.Buffer
			if err := reload.Render(&out, "probe", map[string]any{"Lang": locale, "I18N": locale, "Value": marker}); err != nil {
				t.Errorf("reload iteration %d: %v", i, err)
				return
			}
			page := out.String()
			if !strings.Contains(page, `lang="`+locale+`"`) || !strings.Contains(page, marker) {
				t.Errorf("reload iteration %d: page lost the request locale or marker: %s", i, page)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	t.Logf("reload=true: 200 fixture renders in %s (development re-parse cost, no production claim)", time.Since(begin).Round(time.Millisecond))
}
