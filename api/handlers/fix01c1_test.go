package handlers

import (
	"context"
	"strings"
	"sync"
	"testing"

	"core/localization"
)

// Fix01C1 — readiness actual-view localization and the payment/withdrawal display labels.
// Part 1 (RED before the production change): the readiness view is already locale-aware, so
// these assertions fail on the current English labels; the catalog keys for the payment and
// withdrawal labels are asserted here too (they do not exist yet).

func TestFix01C1ReadinessViewLocalizesLevelsAndItems(t *testing.T) {
	_, _, zhLevels, zhRaw := dealerAdminReadinessView(localization.ZHCN, context.Background(), DealerDeps{})
	_, _, enLevels, _ := dealerAdminReadinessView(localization.EN, context.Background(), DealerDeps{})

	if len(zhLevels) != 4 || len(enLevels) != 4 {
		t.Fatalf("expected the four readiness levels, got zh=%d en=%d", len(zhLevels), len(enLevels))
	}

	// Level titles/summaries are own trusted literals and must follow the request locale.
	enTitles := map[string]string{}
	for _, level := range enLevels {
		enTitles[level.Key] = level.Title
	}
	englishLeak := 0
	for _, level := range zhLevels {
		if level.Title == enTitles[level.Key] {
			englishLeak++
			t.Errorf("level %q title is still English: %q", level.Key, level.Title)
		}
		if strings.TrimSpace(level.Summary) == "" || level.Summary == enSummaryFor(enLevels, level.Key) {
			t.Errorf("level %q summary is still English: %q", level.Key, level.Summary)
		}
		// Logic/state fields must not be translated.
		enLevel := enLevelFor(enLevels, level.Key)
		if level.Status != enLevel.Status || level.StatusClass != enLevel.StatusClass || level.Key != enLevel.Key {
			t.Errorf("level %q changed its machine fields: status=%q/%q class=%q/%q", level.Key, level.Status, enLevel.Status, level.StatusClass, enLevel.StatusClass)
		}
	}
	if englishLeak == 4 {
		t.Error("no readiness level title was localized")
	}

	// Item labels are the labels the actual view displays (the item() literal, not
	// readinessCheckLabel) — they must be localized too.
	for _, item := range zhLevels[0].Items {
		if item.Label == "" {
			continue
		}
		if item.Label == "Database access" || item.Label == "Migration evidence" || item.Label == "Portal session secret" {
			t.Errorf("item %q label is still the English literal: %q", item.Name, item.Label)
		}
		if item.Status != "" && item.Status != "ready" && item.Status != "blocked" {
			t.Errorf("item %q status changed: %q", item.Name, item.Status)
		}
	}
	if len(zhRaw) > 0 {
		// raw diagnostic entries keep Name/Status/Details verbatim.
		for _, raw := range zhRaw {
			if raw.Name == "" && raw.Status == "" {
				t.Error("raw readiness entries lost their machine fields")
			}
		}
	}

	// English stays exactly as before.
	if got := levelTitle(enLevels, "controlled-beta"); got != "Controlled beta" {
		t.Errorf("english level title = %q", got)
	}
}

func enLevelFor(levels []DealerReadinessLevelView, key string) DealerReadinessLevelView {
	for _, level := range levels {
		if level.Key == key {
			return level
		}
	}
	return DealerReadinessLevelView{}
}

func levelTitle(levels []DealerReadinessLevelView, key string) string {
	return enLevelFor(levels, key).Title
}
func enSummaryFor(levels []DealerReadinessLevelView, key string) string {
	return enLevelFor(levels, key).Summary
}

// Fix01C1 catalog contract: the payment/withdrawal display labels must be translated with the
// approved wording (Underpaid=不足, Overpaid=超额, Partial/Partially=部分支付, Rejected=已拒绝, …)
// and must never be confused with each other.
func TestFix01C1StatusLabelCatalogContract(t *testing.T) {
	tests := map[string]string{
		"Paid":             "已支付",
		"Awaiting payment": "等待支付",
		"Awaiting asset":   "等待资产",
		"Partially paid":   "部分支付",
		"Underpaid":        "不足",
		"Overpaid":         "超额",
		"Canceled":         "已取消",
		"Expired":          "已过期",
		"Failed":           "失败",
		"Pending":          "待处理",
		"Processing":       "处理中",
		"Approved":         "已批准",
		"Finalized":        "已结算",
		"Rejected":         "已拒绝",
	}
	for key, want := range tests {
		if got := localization.T(localization.ZHCN, key); got != want {
			t.Errorf("T(zh-CN, %q) = %q, want %q", key, got, want)
		}
		if got := localization.T(localization.EN, key); got != key {
			t.Errorf("T(en, %q) = %q, want the English source text", key, got)
		}
	}
}

// Fix01C1 part 2: the presentation builders take the locale explicitly and keep every
// non-display field verbatim (unknown values are never used as catalog keys or formats).
func TestFix01C1PaymentStatusPresentationBehaviour(t *testing.T) {
	tests := []struct{ status, en, zh, class string }{
		{"paid", "Paid", "已支付", "is-success"},
		{"awaiting_payment", "Awaiting payment", "等待支付", "is-info"},
		{"pending", "Awaiting asset", "等待资产", "is-warning"},
		{"partial_paid", "Partially paid", "部分支付", "is-warning"},
		{"underpaid", "Underpaid", "不足", "is-warning"},
		{"overpaid", "Overpaid", "超额", "is-warning"},
		{"canceled", "Canceled", "已取消", "is-danger"},
		{"expired", "Expired", "已过期", "is-danger"},
		{"failed", "Failed", "失败", "is-danger"},
	}
	for _, tc := range tests {
		enLabel, enClass := paymentStatusPresentation(localization.EN, tc.status)
		zhLabel, zhClass := paymentStatusPresentation(localization.ZHCN, tc.status)
		if enLabel != tc.en || enClass != tc.class {
			t.Errorf("EN %s = (%q, %q), want (%q, %q)", tc.status, enLabel, enClass, tc.en, tc.class)
		}
		if zhLabel != tc.zh || zhClass != tc.class {
			t.Errorf("ZH %s = (%q, %q), want (%q, %q)", tc.status, zhLabel, zhClass, tc.zh, tc.class)
		}
	}
	// unknown values stay verbatim in both locales (never a key, never a format string)
	for _, raw := range []string{"Active", "%s", `<script>alert(1)</script>`, "vendor_state"} {
		for _, loc := range []localization.Locale{localization.ZHCN, localization.EN} {
			if label, class := paymentStatusPresentation(loc, raw); label != raw || class != "" {
				t.Errorf("%s: unknown status %q -> (%q, %q), want verbatim", loc, raw, label, class)
			}
		}
	}
	// empty status localizes only the placeholder
	if label, _ := paymentStatusPresentation(localization.ZHCN, ""); label != "未知" {
		t.Errorf("empty status zh = %q, want the localized Unknown", label)
	}
	if label, _ := paymentStatusPresentation(localization.EN, ""); label != "Unknown" {
		t.Errorf("empty status en = %q", label)
	}
}

func TestFix01C1WithdrawalStatusPresentationBehaviour(t *testing.T) {
	tests := []struct{ status, en, zh, class string }{
		{"pending", "Pending", "待处理", "is-warning"},
		{"processing", "Processing", "处理中", "is-info"},
		{"approved", "Approved", "已批准", "is-info"},
		{"finalized", "Finalized", "已结算", "is-success"},
		{"rejected", "Rejected", "已拒绝", "is-danger"},
		{"failed", "Failed", "失败", "is-danger"},
	}
	for _, tc := range tests {
		enLabel, enClass := withdrawalStatusPresentation(localization.EN, tc.status)
		zhLabel, zhClass := withdrawalStatusPresentation(localization.ZHCN, tc.status)
		if enLabel != tc.en || enClass != tc.class {
			t.Errorf("EN %s = (%q, %q), want (%q, %q)", tc.status, enLabel, enClass, tc.en, tc.class)
		}
		if zhLabel != tc.zh || zhClass != tc.class {
			t.Errorf("ZH %s = (%q, %q), want (%q, %q)", tc.status, zhLabel, zhClass, tc.zh, tc.class)
		}
	}
	for _, raw := range []string{"operator_hold", "%s", "<script>x</script>"} {
		for _, loc := range []localization.Locale{localization.ZHCN, localization.EN} {
			if label, class := withdrawalStatusPresentation(loc, raw); label != raw || class != "" {
				t.Errorf("%s: unknown withdrawal status %q -> (%q, %q)", loc, raw, label, class)
			}
		}
	}
}

// Two concurrent "requests" must never see each other's language.
func TestFix01C1RequestLocaleIsolation(t *testing.T) {
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			loc := localization.ZHCN
			want := "部分支付"
			if i%2 == 1 {
				loc, want = localization.EN, "Partially paid"
			}
			for range 50 {
				if got, _ := paymentStatusPresentation(loc, "partial_paid"); got != want {
					t.Errorf("locale %s: partial_paid = %q, want %q", loc, got, want)
				}
			}
		}(i)
	}
	wg.Wait()
}
