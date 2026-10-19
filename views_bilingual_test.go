package main

import (
	"bytes"
	"fmt"
	htmlTemplate "html/template"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"core/api/handlers"
	"core/localization"
	"github.com/gofiber/template/html/v3"
	xhtml "golang.org/x/net/html"
)

// TEST-02 (T501, corrected) — the frozen English matrix rendered in BOTH locales with a
// POSITIVE Chinese assertion per original UI anchor. The English case list
// (`englishRenderCases()`: names, views, mutates, anchors) is never modified; every anchor is
// classified explicitly (machine / pure UI copy / technical own key) and an anchor that is
// neither an exact catalog key nor a technical token requires a mapping in
// `anchorKeyOverrides` — a silent fallback is a failure, not a pass.

const frozenRenderCaseCount = 29

// anchorKeyOverrides maps frozen anchors that are fragments/attributes of a longer source key
// to that full key. Everything not listed must already be an exact catalog key.
var anchorKeyOverrides = map[string]string{
	`Warning: once 2FA is disabled`: "Warning: once 2FA is disabled, your account will be protected only by email and password.",
	`Status copy: awaiting_payment`: "Status copy: awaiting_payment awaits payment, pending/confirming awaits finality or selection, paid is accepted, expired/failed are terminal, underpaid/overpaid/partial-paid are payment outcomes that require separate handling.",
	`alt="2FA QR Code"`:             "2FA QR Code",
	`underpaid/overpaid/partial-paid are payment outcomes that require separate handling`: "Status copy: awaiting_payment awaits payment, pending/confirming awaits finality or selection, paid is accepted, expired/failed are terminal, underpaid/overpaid/partial-paid are payment outcomes that require separate handling.",
	`Users &amp; Wallets`: "Users & Wallets",
}

type bilingualEngine struct{ engine *html.Engine }

func newBilingualEngine() *bilingualEngine {
	engine := html.New("./views", ".html")
	engine.AddFunc("t", localization.T)
	return &bilingualEngine{engine: engine}
}

func (b *bilingualEngine) render(t *testing.T, view string, data handlers.DealerPageData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := b.engine.Render(&buf, view, data, "dealer/layout"); err != nil {
		t.Fatalf("render %s: %v", view, err)
	}
	return buf.String()
}

// withLocale clones the fixture for the request locale (Locale, trusted bundle, switch URLs)
// and mirrors the Go builders for the row display fields the real handlers localize.
func withLocale(t *testing.T, data handlers.DealerPageData, locale localization.Locale) handlers.DealerPageData {
	t.Helper()
	data.Locale = locale
	if data.Title != "" {
		data.Title = localization.T(locale, data.Title)
	}
	data.I18NJSON = localization.JSBundle(locale)
	data.I18NElementID = localization.BundleScriptElementID
	data.SwitchURLZH, _ = localization.SwitchURL(localization.ZHCN.HTMLTag(), url.Values{}, localization.ZHCN)
	data.SwitchURLEN, _ = localization.SwitchURL("/admin", url.Values{}, localization.EN)

	localize := func(value string) string {
		if value == "" {
			return value
		}
		if rendering := localization.T(locale, value); rendering != value {
			return rendering
		}
		return value
	}
	for i := range data.Payments {
		data.Payments[i].StatusLabel = localize(data.Payments[i].StatusLabel)
	}
	for i := range data.Products {
		data.Products[i].LinkTypeLabel = localize(data.Products[i].LinkTypeLabel)
	}
	for i := range data.Withdrawals {
		data.Withdrawals[i].StatusLabel = localize(data.Withdrawals[i].StatusLabel)
	}
	for i := range data.Activities {
		data.Activities[i].StatusLabel = localize(data.Activities[i].StatusLabel)
	}
	for i := range data.AdminSweepStats {
		data.AdminSweepStats[i].Label = localize(data.AdminSweepStats[i].Label)
	}
	for i := range data.AdminSweepJobs {
		data.AdminSweepJobs[i].StatusLabel = localize(data.AdminSweepJobs[i].StatusLabel)
	}
	if strings.Contains(data.AdminOutboundPolicy.ConfigurationSummary, "Raw amount cap") {
		data.AdminOutboundPolicy.ConfigurationSummary = localization.T(locale, "Default-off")
	}
	return data
}

// visibleText returns the rendered DOM text plus human-readable attributes, with script,
// style, JSON payloads and comments excluded by the HTML parser.
func visibleText(t *testing.T, page string) string {
	t.Helper()
	doc, err := xhtml.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse rendered HTML: %v", err)
	}
	var out strings.Builder
	var walk func(*xhtml.Node, bool)
	walk = func(node *xhtml.Node, hidden bool) {
		if node.Type == xhtml.ElementNode {
			switch node.Data {
			case "script", "style", "noscript":
				hidden = true
			}
			for _, attr := range node.Attr {
				switch attr.Key {
				case "alt", "title", "placeholder", "aria-label", "data-placeholder":
					if !hidden {
						out.WriteString(" " + attr.Val + " ")
					}
				}
			}
		}
		if node.Type == xhtml.TextNode && !hidden {
			out.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, hidden)
		}
	}
	walk(doc, false)
	return out.String()
}

// isMachineAnchor reports whether an anchor asserts machine structure rather than copy.
func isMachineAnchor(anchor string) bool {
	// routes, query strings and bare paths are machine structure, not copy
	if strings.HasPrefix(anchor, "/") || strings.HasPrefix(anchor, "?") || strings.HasPrefix(anchor, "#") {
		return true
	}
	for _, marker := range []string{"data-", "action=", "name=", "id=", "href=", "value=", "colspan", "{{", "class=", "<span", "<div", "<a "} {
		if strings.Contains(anchor, marker) {
			return true
		}
	}
	return false
}

// resolveSourceKey returns the trusted catalog key behind a frozen anchor.
func resolveSourceKey(anchor string, catalog map[string]bool) (string, bool) {
	decoded := xhtml.UnescapeString(anchor)
	if catalog[decoded] {
		return decoded, true
	}
	for fragment, key := range anchorKeyOverrides {
		if strings.Contains(anchor, fragment) || strings.Contains(decoded, xhtml.UnescapeString(fragment)) {
			if catalog[key] {
				return key, true
			}
		}
	}
	return "", false
}

// standaloneIndex finds key as a standalone phrase (not part of a longer identifier such as a
// data value "domain-1"): that is what a visible-copy leak actually looks like.
func standaloneIndex(haystack, key string) int {
	pattern := regexp.MustCompile(`(?:^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(key) + `(?:$|[^A-Za-z0-9_-])`)
	if loc := pattern.FindStringIndex(haystack); loc != nil {
		return loc[0]
	}
	return -1
}

func containsHanText(value string) bool {
	for _, r := range value {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// assertCaseLocale performs the per-case, per-locale assertions and returns the counted
// anchor classes, so a meta-test can verify that a missing heading or row is detected.
// collectCaseErrors is the pure assertion core: it returns the problems and the anchor class
// counts, so tests and the meta-test can inspect them without a probe *testing.T.
func collectCaseErrors(name, locale, page, visible string, anchors []string, catalog map[string]bool) (problems []string, ui, machine, technical int) {
	for _, anchor := range anchors {
		if isMachineAnchor(anchor) {
			machine++
			if !strings.Contains(page, anchor) {
				problems = append(problems, fmt.Sprintf("%s/%s: machine anchor %q lost", name, locale, anchor))
			}
			continue
		}
		key, ok := resolveSourceKey(anchor, catalog)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s/%s: anchor %q has no trusted source key mapping (add it to anchorKeyOverrides or make it an exact catalog key)", name, locale, anchor))
			continue
		}
		if locale == localization.EN.HTMLTag() {
			key, _ := resolveSourceKey(anchor, catalog)
			if localization.T(localization.ZHCN, key) == key {
				technical++
			} else {
				ui++
			}
			if !strings.Contains(page, anchor) {
				problems = append(problems, fmt.Sprintf("%s/%s: english anchor %q lost from the frozen case", name, locale, anchor))
			}
			continue
		}
		rendering := localization.T(localization.ZHCN, key)
		if rendering == key {
			technical++
			if !strings.Contains(visible, key) {
				problems = append(problems, fmt.Sprintf("%s/zh-CN: technical key %q is not rendered", name, key))
			}
			continue
		}
		ui++
		if !strings.Contains(visible, rendering) {
			problems = append(problems, fmt.Sprintf("%s/zh-CN: translated copy %q (source key %q) is missing from visible text", name, rendering, key))
		}
		if at := standaloneIndex(visible, key); at >= 0 {
			problems = append(problems, fmt.Sprintf("%s/zh-CN: english copy %q still visible near %q", name, key, snippetAround(visible, at, 40)))
		}
	}
	return problems, ui, machine, technical
}

// assertCaseLocale reports the pure helper's problems through the real *testing.T.
func assertCaseLocale(t *testing.T, name, locale string, page string, anchors []string, catalog map[string]bool) (ui, machine, technical int) {
	t.Helper()
	problems, ui, machine, technical := collectCaseErrors(name, locale, page, visibleText(t, page), anchors, catalog)
	for _, problem := range problems {
		t.Error(problem)
	}
	return ui, machine, technical
}

func TestBilingualPortalViews(t *testing.T) {
	cases := englishRenderCases()
	if len(cases) != frozenRenderCaseCount {
		t.Fatalf("englishRenderCases() has %d cases, the frozen matrix has %d: extending is fine, silently shrinking is not", len(cases), frozenRenderCaseCount)
	}
	catalog := map[string]bool{}
	for _, key := range localization.Keys() {
		catalog[key] = true
	}
	engine := newBilingualEngine()
	totalUI, totalMachine, totalTechnical := 0, 0, 0
	for _, tc := range cases {
		for _, locale := range []localization.Locale{localization.ZHCN, localization.EN} {
			t.Run(tc.name+"/"+locale.HTMLTag(), func(t *testing.T) {
				data := englishDealerFixture()
				if tc.mutate != nil {
					tc.mutate(&data)
				}
				data = withLocale(t, data, locale)
				page := engine.render(t, tc.view, data)

				if !strings.Contains(page, `lang="`+locale.HTMLTag()+`"`) {
					t.Errorf("missing lang=%s", locale.HTMLTag())
				}
				if !strings.Contains(page, `"locale":"`+string(locale)+`"`) {
					t.Errorf("i18n payload locale does not match the request")
				}
				ui, machine, technical := assertCaseLocale(t, tc.name, locale.HTMLTag(), page, tc.anchors, catalog)
				if locale == localization.ZHCN {
					totalUI += ui
					totalMachine += machine
					totalTechnical += technical
					t.Logf("%s: ui=%d machine=%d technical=%d", tc.name, ui, machine, technical)
				}
			})
		}
	}
	t.Logf("frozen matrix totals (zh side): ui=%d machine=%d technical=%d across %d cases", totalUI, totalMachine, totalTechnical, len(cases))
}

// TestBilingualMatrixDetectsMissingCopy is the meta-test: the assertion helper must report an
// error when a required translation or a machine anchor disappears from the output.
func TestBilingualMatrixDetectsMissingCopy(t *testing.T) {
	catalog := map[string]bool{}
	for _, key := range localization.Keys() {
		catalog[key] = true
	}
	cases := englishRenderCases()
	if len(cases) == 0 {
		t.Fatal("no frozen cases")
	}
	engine := newBilingualEngine()
	tc := cases[0]
	for _, locale := range []localization.Locale{localization.ZHCN, localization.EN} {
		data := englishDealerFixture()
		if tc.mutate != nil {
			tc.mutate(&data)
		}
		data = withLocale(t, data, locale)
		page := engine.render(t, tc.view, data)

		healthyErrors, ui, machine, _ := collectCaseErrors(tc.name, locale.HTMLTag(), page, visibleText(t, page), tc.anchors, catalog)
		if len(healthyErrors) > 0 {
			t.Fatalf("%s/%s: the assertion helper reports problems on a healthy render: %v", tc.name, locale.HTMLTag(), healthyErrors)
		}
		if ui+machine == 0 {
			t.Fatalf("%s/%s: the helper classified no anchor", tc.name, locale.HTMLTag())
		}
		// remove a machine anchor and a translated phrase: both must be detected
		brokenPage := strings.ReplaceAll(page, `data-`, `data-x-`)
		brokenErrors, _, _, _ := collectCaseErrors(tc.name, locale.HTMLTag(), brokenPage, visibleText(t, brokenPage), tc.anchors, catalog)
		if len(brokenErrors) == 0 && machine > 0 {
			t.Errorf("%s/%s: a missing machine anchor was not detected", tc.name, locale.HTMLTag())
		}
		if locale == localization.ZHCN && ui > 0 {
			brokenVisible := page
			for _, key := range tc.anchors {
				if resolved, ok := resolveSourceKey(key, catalog); ok {
					if rendering := localization.T(localization.ZHCN, resolved); rendering != resolved {
						brokenVisible = strings.ReplaceAll(brokenVisible, rendering, "")
					}
				}
			}
			copyErrors, _, _, _ := collectCaseErrors(tc.name, locale.HTMLTag(), brokenVisible, visibleText(t, brokenVisible), tc.anchors, catalog)
			if len(copyErrors) == 0 {
				t.Errorf("%s: removing the translated copy was not detected", tc.name)
			}
		}
	}
}

// bilingualExtraCase: each panel carries a non-empty typed row, its own static heading key
// (never the shared navigation), and explicit expectations for BOTH locales.
type bilingualExtraCase struct {
	name        string
	panel       string
	rowSentinel string
	headingKey  string
	rowChecks   []struct{ en, zh string }
	mutate      func(*handlers.DealerPageData, localization.Locale)
}

func bilingualExtraCases() []bilingualExtraCase {
	checks := func(pairs ...[2]string) []struct{ en, zh string } {
		out := make([]struct{ en, zh string }, 0, len(pairs))
		for _, pair := range pairs {
			out = append(out, struct{ en, zh string }{en: pair[0], zh: localization.T(localization.ZHCN, pair[0])})
		}
		return out
	}
	return []bilingualExtraCase{
		{name: "networks", panel: "networks", rowSentinel: "tron-network-1",
			headingKey: "Centrally enable or disable new deposit and withdrawal entries for each network. The state is kept in the database; no restart or environment change is required.",
			rowChecks:  checks([2]string{"Deposits off", ""}),
			mutate: func(d *handlers.DealerPageData, loc localization.Locale) {
				d.AdminNetworkStates = []handlers.DealerNetworkOperationalStateView{{
					Chain: "TRON", ChainID: "tron-network-1", ChainSlug: "tron", Mode: "deposits_off",
					ModeLabel: localization.T(loc, "Deposits off"), ModeClass: "badge-amber", Reason: "maintenance",
					UpdatedBy: "admin@example.com", UpdatedAt: "2026-06-02 12:00:00",
				}}
			}},
		{name: "readiness", panel: "readiness", rowSentinel: "check-database-1",
			headingKey: "Controlled beta, real-funds production, wallet-provider custody and exchange-grade tracking claims are evaluated with separate evidence and blocker lists.",
			rowChecks:  checks([2]string{"Controlled beta", ""}, [2]string{"Database access", ""}),
			mutate: func(d *handlers.DealerPageData, loc localization.Locale) {
				d.AdminReadiness = []handlers.DealerReadinessLevelView{{
					Key: "controlled-beta", Title: localization.T(loc, "Controlled beta"), Summary: localization.T(loc, "Controlled beta"),
					Status: "blocked", StatusClass: "badge-red",
					Items: []handlers.DealerReadinessCheckView{{Name: "database", Label: localization.T(loc, "Database access"), Status: "blocked", StatusClass: "badge-red", Owner: "Platform Ops", EvidenceURL: "/admin", EvidenceLabel: localization.T(loc, "Evidence"), Blocking: true}},
				}}
				d.AdminReadinessRaw = []handlers.DealerReadinessCheckView{{Name: "check-database-1", Label: localization.T(loc, "Database access"), Status: "blocked", StatusClass: "badge-red", Details: "raw detail"}}
			}},
		{name: "metrics", panel: "metrics", rowSentinel: "gateway_metrics_collection_error",
			headingKey: "Collector errors",
			rowChecks:  checks([2]string{"Collector errors", ""}, [2]string{"Metrics collector", ""}),
			mutate: func(d *handlers.DealerPageData, loc localization.Locale) {
				d.AdminMetricsSummary = handlers.DealerMetricsSummaryView{Headline: localization.T(loc, "Metrics require action"), Description: localization.T(loc, "Metrics require action"), Status: "critical", StatusClass: "badge-red", Tone: "critical", TotalSeries: 2, TotalGroups: 1, CollectionErrors: 1}
				d.AdminMetricsGroups = []handlers.DealerMetricGroupView{{
					Key: "collectors", Title: localization.T(loc, "Collector errors"), Summary: localization.T(loc, "Collector errors"), Status: "critical", StatusClass: "badge-red",
					Items: []handlers.DealerMetricView{{Name: "gateway_metrics_collection_error", DisplayName: localization.T(loc, "Metrics collector"), Value: "1", Status: "critical", StatusClass: "badge-red", Description: localization.T(loc, "Metrics require action"), Action: localization.T(loc, "Metrics require action"), IsProblem: true}},
				}}
				d.AdminMetricAlerts = []handlers.DealerMetricAlertView{{Title: localization.T(loc, "Metrics collector"), Detail: localization.T(loc, "Metrics require action"), Action: localization.T(loc, "Metrics require action"), Metric: "gateway_metrics_collection_error", Labels: `collector="x"`, Value: "1", Group: localization.T(loc, "Collector errors"), Status: "critical", StatusClass: "badge-red", Rank: 2}}
				d.AdminMetricTabs = []handlers.DealerMetricTabView{{Key: "collectors", Label: localization.T(loc, "Collector errors"), URL: "/admin/metrics?tab=collectors", Count: 1, Status: "critical", StatusClass: "badge-red", Active: true}}
				d.AdminMetricsRaw = "gateway_metrics_collection_error 1"
				d.AdminMetricsActiveTab = "collectors"
			}},
		{name: "webhooks", panel: "webhooks", rowSentinel: "webhook-row-1",
			headingKey: "Latency evidence unavailable",
			rowChecks:  checks([2]string{"Latency evidence unavailable", ""}),
			mutate: func(d *handlers.DealerPageData, loc localization.Locale) {
				d.AdminWebhooks = []handlers.DealerWebhookDeliveryView{{ID: "webhook-row-1", EventType: "payment.created", EventVersion: "v1", MerchantID: "merchant-1", Status: "dead_letter", Attempts: 2, LatencyEvidence: localization.T(loc, "Latency evidence unavailable"), NextAction: localization.T(loc, "Retry")}}
			}},
		{name: "provider-health", panel: "provider-health", rowSentinel: "provider-row-1",
			headingKey: "Provider health",
			rowChecks:  checks([2]string{"selected provider", ""}, [2]string{"head current", ""}),
			mutate: func(d *handlers.DealerPageData, loc localization.Locale) {
				d.AdminProviderHealth = []handlers.DealerProviderHealthView{{Chain: "TRON", ChainID: "tron", ProviderLabel: "provider-row-1", Status: "healthy", StatusClass: "badge-green", Reachable: true, LatestHeight: 10, LatencyMS: 5, Lag: 0, StaleIndicator: localization.T(loc, "head current"), FallbackPolicy: localization.T(loc, "selected provider")}}
			}},
		{name: "assets", panel: "assets", rowSentinel: "asset-row-1",
			headingKey: "Active chain/asset definitions in the registry. Technical identifier values are shown in the row details.",
			rowChecks:  checks([2]string{"Asset type", ""}, [2]string{"Native", ""}),
			mutate: func(d *handlers.DealerPageData, loc localization.Locale) {
				d.AdminAssets = []handlers.DealerAssetOption{{Value: "asset-row-1", AssetKey: "tron:native", Label: localization.T(loc, "%s / %s / %s / %d decimals", "TRON", "TRX", "native", 6), Chain: "TRON", Symbol: "TRX", Name: "TRX", ChainID: "tron", IsNative: true, TypeLabel: localization.T(loc, "Native")}}
			}},
		{name: "wallets", panel: "wallets", rowSentinel: "wallet-row-1",
			headingKey: "Wallet %s",
			rowChecks:  checks([2]string{"Wallet", ""}),
			mutate: func(d *handlers.DealerPageData, loc localization.Locale) {
				d.AdminWallets = []handlers.DealerWalletView{{
					ID: "wallet-row-1", ShortID: "wallet-row-1", MerchantID: "merchant-1", Merchant: "Shop",
					Label: localization.T(loc, "Wallet %s", "wallet-row-1"), WalletKind: "deposit",
					AddressPreview: "TXyz…1234", BalanceCount: 1, BalancePreview: "1 TRX",
					Balances: []handlers.DealerWalletBalanceRow{{AssetKey: "tron:native", Symbol: "TRX", Chain: "TRON", AvailableRaw: "5"}},
				}}
			}},
		{name: "recover", panel: "recover", rowSentinel: "recover-row-1",
			headingKey: "Select chain",
			rowChecks:  checks([2]string{"Select chain", ""}),
			mutate: func(d *handlers.DealerPageData, loc localization.Locale) {
				d.AdminRecoverChains = []handlers.DealerRecoverChainOption{{ChainID: "recover-row-1", Chain: "TRON", AssetCount: 2, LogoURL: "/static/tron.svg"}}
				d.AdminVaults = []handlers.DealerVaultAssetView{{ID: "recover-row-1", Symbol: "TRX", SearchText: "TRON TRX", NetworkCount: 1, VariantCount: 1, VaultRaw: "5", VaultDisplay: "5 TRX"}}
			}},
		{name: "reconciliation", panel: "reconciliation", rowSentinel: "job-row-1",
			headingKey: "Chain, ledger, lifecycle, webhook and broadcast evidence groups are reviewed without destructive edit.",
			rowChecks:  checks([2]string{"available", ""}),
			mutate: func(d *handlers.DealerPageData, loc localization.Locale) {
				d.AdminReconciliationJobs = []handlers.DealerReconciliationJobView{{ID: "job-row-1", ShortID: "job-row-1", Status: "resolved", StatusClass: "badge-green", Reason: "auto", EvidencePreview: localization.T(loc, "available")}}
			}},
	}
}

func TestBilingualAdminPanelsExtraCases(t *testing.T) {
	engine := newBilingualEngine()
	for _, tc := range bilingualExtraCases() {
		for _, locale := range []localization.Locale{localization.ZHCN, localization.EN} {
			t.Run(tc.name+"/"+locale.HTMLTag(), func(t *testing.T) {
				data := englishDealerFixture()
				englishAdminFixture(tc.panel)(&data)
				tc.mutate(&data, locale)
				data = withLocale(t, data, locale)
				page := engine.render(t, "dealer/admin_dashboard", data)

				if !strings.Contains(page, `lang="`+locale.HTMLTag()+`"`) {
					t.Errorf("missing lang=%s", locale.HTMLTag())
				}
				if !strings.Contains(page, `"locale":"`+string(locale)+`"`) {
					t.Errorf("i18n payload locale does not match the request")
				}
				if !strings.Contains(page, tc.rowSentinel) {
					t.Errorf("row sentinel %q is missing from the %s render: the fixture does not reach a visible field", tc.rowSentinel, locale)
				}
				visible := visibleText(t, page)
				heading := localization.T(locale, tc.headingKey)
				if tc.headingKey == "Wallet %s" {
					heading = localization.T(locale, "Wallet %s", tc.rowSentinel)
				}
				if !strings.Contains(visible, heading) && !strings.Contains(page, heading) {
					t.Errorf("%s/%s: panel-exclusive heading %q is missing", tc.name, locale, heading)
				}
				for _, check := range tc.rowChecks {
					want := check.en
					if locale == localization.ZHCN {
						want = check.zh
					}
					if want == "" {
						t.Errorf("%s: row check has no expectation for %s", tc.name, locale)
						continue
					}
					if !strings.Contains(visible, want) && !strings.Contains(page, want) {
						t.Errorf("%s/%s: expected localized row copy %q is missing", tc.name, locale, want)
					}
				}
				if locale == localization.ZHCN && !containsHanText(visible) {
					t.Errorf("zh %s panel shows no Chinese copy at all", tc.name)
				}
			})
		}
	}
}

// TestBilingualInvoice asserts both locales of the invoice page, including the status label in
// visible text, the raw machine values and the two switch links with their own language name,
// href and the single current marker.
func TestBilingualInvoice(t *testing.T) {
	for _, tc := range []struct{ locale localization.Locale }{
		{localization.ZHCN}, {localization.EN},
	} {
		t.Run("invoice/"+tc.locale.HTMLTag(), func(t *testing.T) {
			engine, err := htmlTemplate.New("invoice.html").Funcs(htmlTemplate.FuncMap{"t": localization.T}).
				ParseFiles("views/invoice.html")
			if err != nil {
				t.Fatalf("parse invoice.html: %v", err)
			}
			statusLabel := localization.T(tc.locale, "Completed")
			data := map[string]any{
				"Locale": tc.locale, "I18NJSON": localization.JSBundle(tc.locale),
				"I18NElementID": localization.BundleScriptElementID,
				"SwitchURLZH":   "/invoice/tok?lang=zh-CN", "SwitchURLEN": "/invoice/tok?lang=en",
				"Title": localization.T(tc.locale, "Invoice #%s", "ORDER-42"), "OrderID": "ORDER-42",
				"StatusLabel": statusLabel, "StatusClass": "paid", "IsPaid": true,
				"ProductName": "Gateway Pay", "Amount": "10", "Currency": "USDT", "AmountDisplay": "10 USDT",
				"Symbol": "USDT", "ChainName": "TRON", "CreatedAt": "2026-01-02 03:04", "TxHash": "0xabc123",
				"CheckoutURL": "/checkout/tok",
			}
			var out bytes.Buffer
			if err := engine.ExecuteTemplate(&out, "invoice.html", data); err != nil {
				t.Fatalf("render invoice (%s): %v", tc.locale, err)
			}
			page := out.String()
			visible := visibleText(t, page)

			if !strings.Contains(page, `lang="`+tc.locale.HTMLTag()+`"`) {
				t.Errorf("invoice lang attribute is not %s", tc.locale.HTMLTag())
			}
			if !strings.Contains(page, `"locale":"`+string(tc.locale)+`"`) {
				t.Errorf("invoice payload locale is not %s", tc.locale)
			}
			if !strings.Contains(visible, statusLabel) {
				t.Errorf("invoice status label %q is not in the visible text (locale %s)", statusLabel, tc.locale)
			}
			if !strings.Contains(visible, localization.T(tc.locale, "Total amount")) {
				t.Errorf("invoice is missing the localized total-amount label")
			}
			if !strings.Contains(visible, localization.T(tc.locale, "Invoice details")) {
				t.Errorf("invoice is missing the localized details heading")
			}
			for _, raw := range []string{"10 USDT", "ORDER-42", "0xabc123", "USDT", "TRON", "2026-01-02 03:04"} {
				if !strings.Contains(page, raw) {
					t.Errorf("invoice lost the raw value %q", raw)
				}
			}
			// both switch links are present, each named in its own language, with the current
			// marker on the active one and no cross-locale leakage of the other language's copy
			for _, link := range []struct {
				tag, name, href string
			}{
				{"zh-CN", localization.T(localization.ZHCN, "Simplified Chinese"), "/invoice/tok?lang=zh-CN"},
				{"en", "English", "/invoice/tok?lang=en"},
			} {
				anchor := switchAnchorWithHref(t, page, link.href)
				if !strings.Contains(anchor, ">"+link.name+"<") {
					t.Errorf("locale %s: switch link for %s does not show %q: %s", tc.locale, link.tag, link.name, anchor)
				}
				current := strings.Contains(anchor, `aria-current="true"`)
				if current != (link.tag == tc.locale.HTMLTag()) {
					t.Errorf("locale %s: aria-current placement wrong on the %s link", tc.locale, link.tag)
				}
			}
			if tc.locale == localization.EN {
				if strings.Contains(visible, localization.T(localization.ZHCN, "Total amount")) {
					t.Errorf("english invoice leaks the Chinese copy for Total amount")
				}
			}
		})
	}
}

func switchAnchorWithHref(t *testing.T, page, href string) string {
	t.Helper()
	for _, fragment := range strings.Split(page, "<a ") {
		if strings.Contains(fragment, `href="`+href+`"`) {
			if end := strings.Index(fragment, "</a>"); end >= 0 {
				return fragment[:end+4]
			}
			return fragment
		}
	}
	t.Fatalf("no switch anchor with href %q", href)
	return ""
}
