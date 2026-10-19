package main

import (
	"html/template"
	"strings"
	"testing"

	"core/api/handlers"
	"core/localization"
)

// renderAdminDashboardStdlib executes views/dealer/admin_dashboard.html with the standard
// library html/template — NOT Fiber's Render — so the template's own range/scope resolution
// is tested in isolation from the handler layer. (The Fiber engine only adds the layout.)
func renderAdminDashboardStdlib(t *testing.T, locale localization.Locale, panel string) string {
	t.Helper()
	engine, err := template.New("admin_dashboard.html").Funcs(template.FuncMap{"t": localization.T}).
		ParseFiles("views/dealer/admin_dashboard.html")
	if err != nil {
		t.Fatalf("parse admin_dashboard.html: %v", err)
	}
	data := englishDealerFixture()
	englishAdminFixture(panel)(&data)
	data.Locale = locale
	data.AdminOutboundWhitelist = []handlers.DealerOutboundWhitelistView{{
		ID: "whitelist-1", Scope: "global", Chain: "TRON", Token: "USDT", Address: "TWhitelistAddress1", Label: "global · TRON · USDT", IsActive: true,
	}}
	data.AdminNetworkStates = []handlers.DealerNetworkOperationalStateView{{
		Chain: "TRON", ChainID: "tron", ChainSlug: "tron", Mode: "active", ModeLabel: "Active",
		ModeClass: "badge-green", UpdatedBy: "admin@example.com", UpdatedAt: "2026-06-02 12:00:00",
	}}
	var out strings.Builder
	if err := engine.ExecuteTemplate(&out, "admin_dashboard.html", data); err != nil {
		t.Fatalf("stdlib execute (%s, panel %s) failed: %v", locale, panel, err)
	}
	return out.String()
}

// TestPortalRangeLocale pins the two confirmation dialogs that live INSIDE {{range}} blocks:
// they must read the page locale from the root data ($.Locale), not from the ranged row.
func TestPortalRangeLocale(t *testing.T) {
	tests := []struct {
		locale           localization.Locale
		networkConfirm   string
		whitelistConfirm string
		whitelistHeading string
	}{
		{localization.ZHCN, "更新该网络的运营模式？", "更新白名单状态？", localization.T(localization.ZHCN, "Whitelist address")},
		{localization.EN, "Update this network operational mode?", "Update the whitelist status?", "Whitelist address"},
	}

	for _, tc := range tests {
		networks := renderAdminDashboardStdlib(t, tc.locale, "networks")
		if !strings.Contains(networks, tc.networkConfirm) {
			t.Errorf("locale %s: network-mode confirm dialog missing %q", tc.locale, tc.networkConfirm)
		}
		for _, anchor := range []string{`action="/admin/networks/state"`, "return confirm("} {
			if !strings.Contains(networks, anchor) {
				t.Errorf("locale %s: networks panel lost %q", tc.locale, anchor)
			}
		}

		security := renderAdminDashboardStdlib(t, tc.locale, "security")
		if !strings.Contains(security, tc.whitelistConfirm) {
			t.Errorf("locale %s: whitelist confirm dialog missing %q", tc.locale, tc.whitelistConfirm)
		}
		for _, anchor := range []string{
			`action="/admin/security/outbound-whitelist/whitelist-1/toggle"`,
			tc.whitelistHeading, // the root-scope heading *after* the ranged row (localized)
			`action="/admin/security/outbound-whitelist"`,
			`action="/admin/security/outbound-policy"`,
			"return confirm(",
		} {
			if !strings.Contains(security, anchor) {
				t.Errorf("locale %s: security panel lost %q", tc.locale, anchor)
			}
		}
		// the whitelist form must render completely (its tail proves execution reached the end)
		if !strings.Contains(security, "</form>") {
			t.Errorf("locale %s: security panel rendered no complete form", tc.locale)
		}
	}
}
