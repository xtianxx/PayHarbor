package handlers

import (
	"strings"
	"sync"
	"testing"

	"core/asset"
	"core/localization"
	"core/models"
)

// FIX-01B focused tests: the trusted display builders must take the request locale
// explicitly, render zh-CN labels for their own literals, and keep user/history data,
// machine values, URLs and unknown fallbacks verbatim.

type nativeAssetStub struct{ asset.Asset }

func (nativeAssetStub) IsNative() bool { return true }

func TestFix01BReadinessCheckLabelsFollowLocale(t *testing.T) {
	if got := readinessCheckLabel(localization.EN, "database"); got != "Database access" {
		t.Errorf("EN label = %q, want the English source text", got)
	}
	if got := readinessCheckLabel(localization.ZHCN, "database"); got == "Database access" || got == "" {
		t.Errorf("ZHCN label = %q, want a localized label", got)
	}
	if got := readinessCheckLabel(localization.ZHCN, "sweep.job_backlog"); got == "Sweep backlog" {
		t.Errorf("ZHCN label for sweep.job_backlog is still English: %q", got)
	}

	// Unknown check names are transformed only, never treated as a catalog key.
	if got := readinessCheckLabel(localization.ZHCN, "vendor.custom.check"); got != "vendor / custom / check" {
		t.Errorf("unknown check name = %q, want the dotted name preserved as segments", got)
	}
	if got := readinessCheckLabel(localization.ZHCN, "100%s.check"); got != "100%s / check" {
		t.Errorf("unknown check name with a verb = %q, must not be format-interpreted", got)
	}
}

func TestFix01BReadinessEvidenceAndBlockingLabels(t *testing.T) {
	if got := readinessEvidenceLabel(localization.EN, ""); got != "Evidence" {
		t.Errorf("EN evidence label = %q", got)
	}
	if got := readinessEvidenceLabel(localization.ZHCN, ""); got == "Evidence" || got == "" {
		t.Errorf("ZHCN evidence label = %q, want a localized label", got)
	}
	// URLs are machine values: verbatim in both locales.
	for _, locale := range []localization.Locale{localization.ZHCN, localization.EN} {
		if got := readinessEvidenceLabel(locale, "/admin/webhooks"); got != "/admin/webhooks" {
			t.Errorf("locale %s: URL = %q, must stay verbatim", locale, got)
		}
	}

	if got := readinessBlockingLabel(localization.EN, true); got != "blocking" {
		t.Errorf("EN blocking label = %q", got)
	}
	if got := readinessBlockingLabel(localization.EN, false); got != "non-blocking" {
		t.Errorf("EN non-blocking label = %q", got)
	}
	if got := readinessBlockingLabel(localization.ZHCN, true); got == "blocking" {
		t.Errorf("ZHCN blocking label is still English: %q", got)
	}
	if got := readinessBlockingLabel(localization.ZHCN, false); got == "non-blocking" {
		t.Errorf("ZHCN non-blocking label is still English: %q", got)
	}
}

func TestFix01BAdminAssetTypeLabels(t *testing.T) {
	if got := adminAssetTypeLabel(localization.ZHCN, nil); got != "unknown" {
		t.Errorf("nil asset fallback = %q, want the untouched fallback", got)
	}
	if got := adminAssetTypeLabel(localization.EN, nativeAssetStub{}); got != "Native" {
		t.Errorf("EN native label = %q", got)
	}
	if got := adminAssetTypeLabel(localization.ZHCN, nativeAssetStub{}); got == "Native" {
		t.Errorf("ZHCN native label is still English: %q", got)
	}
}

func TestFix01BNetworkModeLabelsKeepMachineClass(t *testing.T) {
	label, class := networkOperationalModePresentation(localization.EN, models.NetworkOperationalModeDepositsOff)
	if label != "Deposits off" || class != "badge-amber" {
		t.Errorf("EN presentation = (%q, %q)", label, class)
	}
	zhLabel, zhClass := networkOperationalModePresentation(localization.ZHCN, models.NetworkOperationalModeDepositsOff)
	if zhLabel == "Deposits off" {
		t.Errorf("ZHCN mode label is still English: %q", zhLabel)
	}
	if zhClass != "badge-amber" {
		t.Errorf("badge class must stay a machine value, got %q", zhClass)
	}
}

func TestFix01BWalletLabelKeepsIdentifiersVerbatim(t *testing.T) {
	wallet := models.Wallet{ProductID: "P-100", UserID: "U-200"}
	for _, locale := range []localization.Locale{localization.ZHCN, localization.EN} {
		if got := walletLabel(locale, wallet); got != "P-100 · U-200" {
			t.Errorf("locale %s: wallet with product/user IDs = %q, must stay verbatim", locale, got)
		}
	}

	anonymous := models.Wallet{}
	if got := walletLabel(localization.EN, anonymous); !strings.HasPrefix(got, "Wallet ") {
		t.Errorf("EN anonymous wallet = %q, want the Wallet <id> form", got)
	}
	zh := walletLabel(localization.ZHCN, anonymous)
	if strings.HasPrefix(zh, "Wallet ") || zh == walletLabel(localization.EN, anonymous) {
		t.Errorf("ZHCN anonymous wallet = %q, want a localized prefix", zh)
	}
	if !strings.Contains(zh, strings.TrimSpace(strings.TrimPrefix(walletLabel(localization.EN, anonymous), "Wallet "))) {
		t.Errorf("ZHCN anonymous wallet %q lost the wallet id", zh)
	}
}

func TestFix01BMetricTabLabelsAndFallback(t *testing.T) {
	if got := dealerMetricTabLabel(localization.EN, "runtime", "Fallback Title"); got != "Runtime" {
		t.Errorf("EN tab label = %q", got)
	}
	if got := dealerMetricTabLabel(localization.ZHCN, "runtime", "Fallback Title"); got == "Runtime" {
		t.Errorf("ZHCN tab label is still English: %q", got)
	}
	// Unknown keys fall back to upstream text, verbatim — never as a format string.
	for _, fallback := range []string{"Upstream Title", "100%s off", `<script>alert(1)</script>`, "vendor:title"} {
		if got := dealerMetricTabLabel(localization.ZHCN, "unknown-key", fallback); got != fallback {
			t.Errorf("fallback = %q, want verbatim %q", got, fallback)
		}
	}
}

func TestFix01BReconciliationScopeKeepsIdentifiersVerbatim(t *testing.T) {
	row := models.ReconciliationJob{ScopeKey: "merchant:42"}
	got := reconciliationScopeLabel(localization.ZHCN, row)
	if !strings.Contains(got, "merchant:42") {
		t.Errorf("scope value must stay verbatim, got %q", got)
	}
	if got == reconciliationScopeLabel(localization.EN, row) {
		t.Errorf("ZHCN scope label must differ from the English one: %q", got)
	}

	blocks := models.ReconciliationJob{FromBlock: 5, ToBlock: 9}
	zhBlocks := reconciliationScopeLabel(localization.ZHCN, blocks)
	enBlocks := reconciliationScopeLabel(localization.EN, blocks)
	if !strings.Contains(zhBlocks, "5") || !strings.Contains(zhBlocks, "9") {
		t.Errorf("block range numbers must survive, got %q", zhBlocks)
	}
	if zhBlocks == enBlocks {
		t.Errorf("block range prefix must be localized, got %q", zhBlocks)
	}
	if !strings.Contains(enBlocks, "5") || !strings.Contains(enBlocks, "9") {
		t.Errorf("english block range lost its numbers: %q", enBlocks)
	}
}

// TestFix01BNoCrossRequestLocaleBleed runs the builders for two locales interleaved and
// concurrently: labels must always match the locale passed in, which is what keeps
// concurrent requests from showing each other's language.
func TestFix01BNoCrossRequestLocaleBleed(t *testing.T) {
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			locale := localization.ZHCN
			wantEnglish := false
			if i%2 == 1 {
				locale, wantEnglish = localization.EN, true
			}
			for range 50 {
				if got := readinessCheckLabel(locale, "database"); (got == "Database access") != wantEnglish {
					t.Errorf("locale %s: label = %q", locale, got)
				}
				if _, class := networkOperationalModePresentation(locale, models.NetworkOperationalModeActive); class != "badge-green" {
					t.Errorf("locale %s: badge class changed: %q", locale, class)
				}
				if got := walletLabel(locale, models.Wallet{}); strings.HasPrefix(got, "Wallet ") != wantEnglish {
					t.Errorf("locale %s: wallet label = %q", locale, got)
				}
			}
		}(i)
	}
	wg.Wait()
}
