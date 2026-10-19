package handlers

import (
	"core/constants"
	"github.com/gofiber/fiber/v3"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	configurations "core/application/configuration"
	"core/localization"
	"core/models"
)

// Fix01C3R — the last confirmed Go display families. Part A exercises entry points that are
// ALREADY locale-aware, so a failure here is a genuinely missing translation, not a
// signature change.

func TestFix01C3ROutboundPolicyHoursFollowLocale(t *testing.T) {
	setting := &models.OutboundPolicySetting{VelocityWindowSecs: 48 * 3600}
	zh := dealerOutboundPolicyView(localization.ZHCN, setting)
	en := dealerOutboundPolicyView(localization.EN, setting)
	if en.VelocityWindowLabel != "48 hours" {
		t.Errorf("english hours label = %q, want the original wording", en.VelocityWindowLabel)
	}
	if zh.VelocityWindowLabel == en.VelocityWindowLabel || !hasCJK(zh.VelocityWindowLabel) {
		t.Errorf("hours label is not localized: zh=%q en=%q", zh.VelocityWindowLabel, en.VelocityWindowLabel)
	}
	// machine fields stay identical
	if zh.VelocityWindowHours != en.VelocityWindowHours || zh.ID != en.ID {
		t.Errorf("policy machine fields changed: zh=%+v en=%+v", zh, en)
	}
}

func TestFix01C3RAssetOptionLabelsFollowLocale(t *testing.T) {
	registry := configurations.NewAssetRegistry()
	zhOptions := dealerAssetOptions(localization.ZHCN, registry)
	enOptions := dealerAssetOptions(localization.EN, registry)
	if len(enOptions) == 0 {
		t.Fatal("asset registry produced no options; the fixture premise is wrong")
	}
	enByValue := make(map[string]string, len(enOptions))
	for _, option := range enOptions {
		if _, dup := enByValue[option.Value]; dup {
			t.Fatalf("duplicate asset option value %q", option.Value)
		}
		enByValue[option.Value] = option.Label
	}
	localized := 0
	for _, option := range zhOptions {
		enLabel, ok := enByValue[option.Value]
		if !ok {
			t.Errorf("asset option %q exists only in zh", option.Value)
			continue
		}
		if option.Label != enLabel {
			localized++
		}
		if option.Value == "" || !strings.Contains(option.Label, " ") {
			t.Errorf("asset option label lost its shape: %+v", option)
		}
	}
	if localized == 0 {
		t.Errorf("no asset option label is localized: sample zh=%q en=%q",
			zhOptions[0].Label, enByValue[zhOptions[0].Value])
	}
}

func hasCJK(value string) bool {
	for _, r := range value {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// Fix01C3R-CORRECT: the sweep job status label and the remaining reconciliation evidence
// branches are localized too. These call already locale-aware builders, so a failure is a
// real missing translation (RED), never a signature change.

func TestFix01C3RSweepJobStatusLabelsFollowLocale(t *testing.T) {
	for _, tc := range []struct{ status, en, zh string }{
		{"pending", "pending", "待处理"},
		{"processing", "processing", "处理中"},
		{"failed", "failed", "失败"},
		{"dead_letter", "dead letter", "死信"},
		{"succeeded", "succeeded", "已成功"},
	} {
		job := models.SweepJob{Status: tc.status}
		zh := dealerSweepJobViews(localization.ZHCN, []models.SweepJob{job})
		en := dealerSweepJobViews(localization.EN, []models.SweepJob{job})
		if len(zh) != 1 || len(en) != 1 {
			t.Fatalf("%s: unexpected view count zh=%d en=%d", tc.status, len(zh), len(en))
		}
		if en[0].StatusLabel != tc.en {
			t.Errorf("%s: english label = %q, want the legacy %q", tc.status, en[0].StatusLabel, tc.en)
		}
		if zh[0].StatusLabel != tc.zh {
			t.Errorf("%s: zh label = %q, want %q", tc.status, zh[0].StatusLabel, tc.zh)
		}
		if zh[0].Status != en[0].Status || zh[0].Status != tc.status {
			t.Errorf("%s: machine status changed: zh=%q en=%q", tc.status, zh[0].Status, en[0].Status)
		}
	}
	// unknown statuses keep the legacy underscore→space fallback in BOTH locales and are never
	// used as catalog keys or format strings.
	for _, raw := range []string{"vendor_state", "%s", "<script>x</script>", "weird_state_2"} {
		for _, loc := range []localization.Locale{localization.ZHCN, localization.EN} {
			views := dealerSweepJobViews(loc, []models.SweepJob{{Status: raw}})
			want := strings.ReplaceAll(raw, "_", " ")
			if views[0].StatusLabel != want {
				t.Errorf("%s: unknown status %q -> %q, want %q", loc, raw, views[0].StatusLabel, want)
			}
		}
	}
	// row-adjacent fields stay verbatim, and the native fallback keeps its lowercase EN form
	token := "USDT"
	rich := models.SweepJob{Status: "failed", Token: &token, OperatorAction: "manual-check", LastError: "boom"}
	zh := dealerSweepJobViews(localization.ZHCN, []models.SweepJob{rich})[0]
	en := dealerSweepJobViews(localization.EN, []models.SweepJob{rich})[0]
	if zh.LastError != en.LastError || zh.OperatorAction != en.OperatorAction || zh.OperatorAction != "manual-check" {
		t.Errorf("row diagnostics changed: zh=%+v en=%+v", zh, en)
	}
	plain := dealerSweepJobViews(localization.EN, []models.SweepJob{{Status: "pending"}})[0]
	if plain.Asset != "native" {
		t.Errorf("english native asset label = %q, want the legacy lowercase form", plain.Asset)
	}
}

func TestFix01C3RReconciliationEvidenceAllGroups(t *testing.T) {
	positive := []struct {
		group string
		row   models.ReconciliationJob
	}{
		{"chain", models.ReconciliationJob{ChainID: 1}},
		{"ledger", models.ReconciliationJob{EvidenceJSON: `{"ledger":"x"}`}},
		{"lifecycle", models.ReconciliationJob{ResourceType: "payment_session"}},
		{"webhook", models.ReconciliationJob{EvidenceJSON: `{"webhook":"x"}`}},
		{"broadcast", models.ReconciliationJob{EvidenceJSON: `{"outbound":"x"}`}},
	}
	for _, tc := range positive {
		zh := reconciliationEvidenceAvailability(localization.ZHCN, tc.row, tc.group)
		en := reconciliationEvidenceAvailability(localization.EN, tc.row, tc.group)
		if en != "available" {
			t.Errorf("%s: english positive = %q, want the original wording", tc.group, en)
		}
		if zh != "可用" {
			t.Errorf("%s: zh positive = %q, want 可用", tc.group, zh)
		}
	}
	negative := []struct {
		group string
		row   models.ReconciliationJob
	}{
		{"chain", models.ReconciliationJob{}},
		{"ledger", models.ReconciliationJob{}},
		{"lifecycle", models.ReconciliationJob{}},
		{"webhook", models.ReconciliationJob{}},
		{"broadcast", models.ReconciliationJob{}},
		{"unknown", models.ReconciliationJob{}},
	}
	for _, tc := range negative {
		if got := reconciliationEvidenceAvailability(localization.EN, tc.row, tc.group); got != "evidence unavailable" {
			t.Errorf("%s: english negative = %q", tc.group, got)
		}
		if got := reconciliationEvidenceAvailability(localization.ZHCN, tc.row, tc.group); got != "证据不可用" {
			t.Errorf("%s: zh negative = %q, want 证据不可用", tc.group, got)
		}
	}
}

func TestFix01C3RProviderIndicatorsFollowLocale(t *testing.T) {
	rows := []models.ProviderHealthSnapshot{
		{ChainID: 1, ProviderLabel: "rpc-a", Status: "healthy", LagFromReference: 5},
		{ChainID: 1, ProviderLabel: "rpc-b", Status: "unhealthy"},
		{ChainID: 1, ProviderLabel: "rpc-c", Status: "healthy", Selected: true},
		{ChainID: 1, ProviderLabel: "rpc-d", Status: "healthy", FailoverReason: "%s keep-me"},
	}
	zh := dealerProviderHealthViews(localization.ZHCN, rows)
	en := dealerProviderHealthViews(localization.EN, rows)
	if len(zh) != len(en) || len(zh) != 4 {
		t.Fatalf("view count mismatch: zh=%d en=%d", len(zh), len(en))
	}
	if en[0].StaleIndicator != "lag 5 blocks" {
		t.Errorf("english lag indicator = %q", en[0].StaleIndicator)
	}
	if zh[0].StaleIndicator == en[0].StaleIndicator || !hasCJK(zh[0].StaleIndicator) {
		t.Errorf("zh lag indicator not localized: %q", zh[0].StaleIndicator)
	}
	if zh[1].StaleIndicator == en[1].StaleIndicator {
		t.Errorf("zh stale indicator not localized: %q", zh[1].StaleIndicator)
	}
	if en[2].FallbackPolicy != "selected provider" || zh[2].FallbackPolicy == en[2].FallbackPolicy {
		t.Errorf("selected-provider policy: zh=%q en=%q", zh[2].FallbackPolicy, en[2].FallbackPolicy)
	}
	if !strings.Contains(zh[3].FallbackPolicy, "%s keep-me") {
		t.Errorf("failover reason must stay verbatim: %q", zh[3].FallbackPolicy)
	}
	for i := range rows {
		if zh[i].Status != en[i].Status || zh[i].ProviderLabel != en[i].ProviderLabel {
			t.Errorf("row %d machine fields changed: zh=%+v en=%+v", i, zh[i], en[i])
		}
	}
}

func TestFix01C3RPolicySummaryAndHoursLocale(t *testing.T) {
	setting := &models.OutboundPolicySetting{
		EmergencyFrozen: true, WhitelistRequired: true,
		MaxAmountRaw: "1000", VelocityLimitRaw: "500", VelocityWindowSecs: 0, UpdatedBy: "admin@example.com",
	}
	zh := dealerOutboundPolicyView(localization.ZHCN, setting)
	en := dealerOutboundPolicyView(localization.EN, setting)
	if zh.VelocityWindowHours != 24 || en.VelocityWindowHours != 24 {
		t.Errorf("fallback hours changed: zh=%d en=%d", zh.VelocityWindowHours, en.VelocityWindowHours)
	}
	if !strings.Contains(en.ConfigurationSummary, "freeze active") || !strings.Contains(en.ConfigurationSummary, "whitelist required") ||
		!strings.Contains(en.ConfigurationSummary, "max 1000") || !strings.Contains(en.ConfigurationSummary, "velocity 500") {
		t.Errorf("english summary lost its legacy shape: %q", en.ConfigurationSummary)
	}
	for _, want := range []string{"已冻结", "需要白名单", "1000", "500"} {
		if !strings.Contains(zh.ConfigurationSummary, want) {
			t.Errorf("zh summary %q missing %q", zh.ConfigurationSummary, want)
		}
	}
	if zh.UpdatedBy != "admin@example.com" || en.UpdatedBy != "admin@example.com" {
		t.Errorf("UpdatedBy must stay verbatim: zh=%q en=%q", zh.UpdatedBy, en.UpdatedBy)
	}
	// zero/`nil` fallback path
	empty := dealerOutboundPolicyView(localization.ZHCN, nil)
	if empty.VelocityWindowHours != 24 || empty.ConfigurationSummary == "Default-off" {
		t.Errorf("nil-setting fallback not localized: %+v", empty)
	}
}

func TestFix01C3RPreviewFallbacksFollowLocale(t *testing.T) {
	for _, empty := range []string{"No active error", "Evidence unavailable", "Affected resources unavailable", "Payload preview unavailable"} {
		for _, blank := range []string{"", "{}", "[]"} {
			zh := dealerPreviewText(localization.ZHCN, blank, empty)
			if zh == empty || !hasCJK(zh) {
				t.Errorf("preview fallback %q for %q not localized: %q", empty, blank, zh)
			}
			if en := dealerPreviewText(localization.EN, blank, empty); en != empty {
				t.Errorf("english preview fallback changed: %q", en)
			}
		}
	}
	// real content is never translated, even when it happens to equal a catalog key
	for _, value := range []string{"Active", "%s", "证据"} {
		for _, loc := range []localization.Locale{localization.ZHCN, localization.EN} {
			if got := dealerPreviewText(loc, value, "No active error"); got != value {
				t.Errorf("%s: preview value %q was altered: %q", loc, value, got)
			}
		}
	}
}

// C3RC remaining gap: the reconciliation timeline/next-action and the real product / test
// payment builders, driven through a temporary Fiber handler (fixture data only, no routes
// from the production router, no database, no fund action).

func TestFix01C3RReconciliationTimelineAndNextAction(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := created.Add(2 * time.Hour)
	resolved := updated.Add(time.Hour)
	row := models.ReconciliationJob{
		CreatedAt: created, UpdatedAt: updated, ResolvedAt: &resolved, Attempts: 3,
		Status: models.ReconciliationStatusResolved,
	}
	en := reconciliationAuditTimeline(localization.EN, row)
	zh := reconciliationAuditTimeline(localization.ZHCN, row)
	if !strings.Contains(en, "opened") || !strings.Contains(en, "updated") || !strings.Contains(en, "resolved") || !strings.Contains(en, "3 attempts") {
		t.Errorf("english timeline lost its legacy wording: %q", en)
	}
	if zh == en || !hasCJK(zh) {
		t.Errorf("zh timeline not localized: %q", zh)
	}
	// dates and counts survive verbatim in both locales
	enDate := strings.FieldsFunc(en, func(r rune) bool { return r == ' ' || r == '·' })[1]
	if !strings.Contains(zh, enDate) || !strings.Contains(zh, "3") {
		t.Errorf("zh timeline lost the timestamp/count: %q (expected %q)", zh, enDate)
	}

	for _, tc := range []struct {
		status, en, zh string
	}{
		{models.ReconciliationStatusResolved, "No action", localization.T(localization.ZHCN, "No action")},
		{models.ReconciliationStatusRetryScheduled, "Wait for scheduled retry", "等待计划重试"},
		{models.ReconciliationStatusNeedsOperatorAction, "Operator review required", "需要人工复核"},
		{models.ReconciliationStatusFailed, "Escalate with evidence", "携证据升级"},
		{"unexpected", "Inspect evidence and resolve, defer, or escalate", "检查证据并解决、延后或升级"},
	} {
		got := reconciliationNextAction(localization.EN, models.ReconciliationJob{Status: tc.status})
		if got != tc.en {
			t.Errorf("EN next action for %q = %q, want %q", tc.status, got, tc.en)
		}
		zhGot := reconciliationNextAction(localization.ZHCN, models.ReconciliationJob{Status: tc.status})
		if zhGot != tc.zh {
			t.Errorf("ZH next action for %q = %q, want %q", tc.status, zhGot, tc.zh)
		}
		// the status value itself is never translated
		if strings.Contains(zhGot, tc.status) {
			t.Errorf("next action leaked the raw status: %q", zhGot)
		}
	}
}

func TestFix01C3RProductAndTestPaymentBuildersViaRequest(t *testing.T) {
	products := []models.Product{
		{Name: "Payment", Description: "desc", LinkType: models.PaymentLinkTypeFixed, Amount: "10", Currency: "USDT", Language: "en"},
		{Name: "Donation", Description: "desc", LinkType: models.PaymentLinkTypeDonation, Amount: "", Currency: "USD", Language: "tr"},
	}
	chainID := constants.ChainID(1)
	sessions := []models.PaymentSession{{
		OrderID: "ORDER-1", ProductID: "prod-1", LinkType: models.PaymentLinkTypeFixed,
		Amount: "5", Currency: "USDT", Status: "pending", SelectedSymbol: "USDT", ExpectedAmountRaw: "5000000",
		SelectedChainID: &chainID,
	}}

	type capture struct {
		products []DealerProductView
		payments []DealerTestPaymentView
	}
	for _, tc := range []struct {
		locale, fixedLabel, donationLabel, anyAmount, paymentLabel string
	}{
		{localization.ZHCN.HTMLTag(), "固定金额", "捐赠", "任意金额", "支付"},
		{localization.EN.HTMLTag(), "Fixed amount", "Donation", "Any amount", "Payment"},
	} {
		app := fiber.New()
		var got capture
		app.Get("/probe", func(c fiber.Ctx) error {
			got.products = dealerProductViews(c, portalLocale(c), products)
			got.payments = dealerTestPaymentViews(c, sessions)
			return c.SendString("ok")
		})
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/probe?lang="+tc.locale, nil))
		if err != nil || resp.StatusCode != fiber.StatusOK {
			t.Fatalf("%s: probe request failed: %v (%d)", tc.locale, err, resp.StatusCode)
		}
		fixed, donation := got.products[0], got.products[1]
		if fixed.LinkTypeLabel != tc.fixedLabel {
			t.Errorf("%s: fixed link type label = %q, want %q", tc.locale, fixed.LinkTypeLabel, tc.fixedLabel)
		}
		if donation.LinkTypeLabel != tc.donationLabel {
			t.Errorf("%s: donation link type label = %q, want %q", tc.locale, donation.LinkTypeLabel, tc.donationLabel)
		}
		// a user product literally named "Payment" must NOT be translated, and the raw amount
		// concatenation stays byte-identical
		if fixed.Name != "Payment" || fixed.Description != "desc" || fixed.Amount != "10" || fixed.Currency != "USDT" || !strings.EqualFold(fixed.Language, "en") {
			t.Errorf("%s: fixed product user data changed: %+v", tc.locale, fixed)
		}
		if fixed.AmountDisplay != "10 USDT" {
			t.Errorf("%s: fixed amount display = %q, want the raw concatenation", tc.locale, fixed.AmountDisplay)
		}
		if donation.AmountDisplay != tc.anyAmount {
			t.Errorf("%s: donation amount display = %q, want %q", tc.locale, donation.AmountDisplay, tc.anyAmount)
		}
		// 004: the view's machine language value is canonical (legacy "tr" resolves to the
		// zh-CN default) so the edit form's case-sensitive select always has a matching option;
		// the product's own data (name, description) stays untouched.
		if donation.Name != "Donation" || donation.Language != localization.ZHCN.HTMLTag() {
			t.Errorf("%s: donation view language = %q, want the canonical default", tc.locale, donation.Language)
		}
		if len(got.products) != 2 || len(got.payments) != 1 {
			t.Fatalf("%s: builder fixture premise failed: products=%d payments=%d", tc.locale, len(got.products), len(got.payments))
		}
		payment := got.payments[0]
		if payment.LinkTypeLabel != tc.paymentLabel {
			t.Errorf("%s: test payment link type label = %q, want %q", tc.locale, payment.LinkTypeLabel, tc.paymentLabel)
		}
		if payment.OrderID != "ORDER-1" || payment.LinkType != models.PaymentLinkTypeFixed {
			t.Errorf("%s: test payment machine fields changed: %+v", tc.locale, payment)
		}
	}
}
