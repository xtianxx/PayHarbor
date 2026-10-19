package main

import (
	"bytes"
	htmltemplate "html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"core/api/handlers"
	"core/localization"

	"github.com/gofiber/template/html/v3"
)

// turkishDiacritics are the characters that only appear in Turkish display copy.
// The English surfaces must not render any of them unless they come from operator
// data (the fixtures below deliberately contain none).
const turkishDiacritics = "ıİşŞğĞüÜöÖçÇ"

// englishDealerFixture is an all-English page fixture, so any Turkish left in the
// rendered output must come from the templates themselves. It only carries the chrome,
// URLs and identity every portal surface shares; page- and panel-specific data is
// attached by englishDashboardFixture and englishAdminFixture.
func englishDealerFixture() handlers.DealerPageData {
	return handlers.DealerPageData{
		Title:               "Merchant sign in",
		Active:              "login",
		Locale:              localization.EN,
		OIDCLoginURL:        "/auth/oidc/login",
		OIDCProvider:        "Corporate account",
		RegisterURL:         "/merchant/register",
		LoginURL:            "/merchant/login",
		OnboardingURL:       "/merchant/onboarding",
		DashboardURL:        "/merchant/dashboard",
		DomainsURL:          "/merchant/domains",
		LogoutURL:           "/merchant/logout",
		ActivityURL:         "/merchant/dashboard/activity",
		ActivityAuditURL:    "/merchant/dashboard/activity/audit",
		ActivityPaymentsURL: "/merchant/dashboard/activity/payments",
		ActivityDepositsURL: "/merchant/dashboard/activity/deposits",
		TreasuryURL:         "/merchant/dashboard/treasury",
		TransactionsURL:     "/merchant/dashboard/transactions",
		UsersURL:            "/merchant/dashboard/users",
		WithdrawalsURL:      "/merchant/dashboard/withdrawals",
		RescanURL:           "/merchant/dashboard/rescan",
		IntegrationsURL:     "/merchant/dashboard/domains",
		DomainsPanelURL:     "/merchant/dashboard/domains",
		ProductsURL:         "/merchant/dashboard/products",
		InvoicesURL:         "/merchant/dashboard/invoices",
		ProductsPanelURL:    "/merchant/dashboard/products/index",
		ProductsLinksURL:    "/merchant/dashboard/products/links",
		SettingsPanelURL:    "/merchant/dashboard/settings",
		MerchantID:          "merchant-id",
		MerchantName:        "Demo Merchant",
		MerchantEmail:       "demo@example.com",
		PaymentLinkURL:      "/checkout/demo-link",
		AssetCount:          1,
		NetworkCount:        3,
		WalletCount:         1,
		DomainCount:         1,
		ProductCount:        1,
		PaymentCount:        1,
		DepositCount:        1,
		ActivityCount:       1,
		WithdrawalCount:     1,

		AdminOverviewURL:       "/admin",
		AdminMerchantsURL:      "/admin/merchants",
		AdminVaultURL:          "/admin/vault",
		AdminAssetsURL:         "/admin/assets",
		AdminPaymentsURL:       "/admin/payments",
		AdminDepositsURL:       "/admin/deposits",
		AdminWithdrawalsURL:    "/admin/withdrawals",
		AdminWalletsURL:        "/admin/wallets",
		AdminActivityURL:       "/admin/activity",
		AdminSweepURL:          "/admin/sweep",
		AdminRecoverURL:        "/admin/recover",
		AdminLinksURL:          "/admin/links",
		AdminWebhooksURL:       "/admin/webhooks",
		AdminReconciliationURL: "/admin/reconciliation",
		AdminProviderHealthURL: "/admin/provider-health",
		AdminNetworksURL:       "/admin/networks",
		AdminRefundsURL:        "/admin/refunds",
		AdminReadinessURL:      "/admin/readiness",
		AdminMetricsURL:        "/admin/metrics",
		AdminRescanURL:         "/admin/rescan",
		AdminTestsURL:          "/admin/tests",
		AdminTestDepositURL:    "/admin/tests/deposit",
		AdminSecurityURL:       "/admin/security",
	}
}

// englishDomainViews returns the domain row the domains panel needs to render its
// credential stack (API key, key ID, secret rotation) and webhook evidence.
func englishDomainViews() []handlers.DealerDomainView {
	return []handlers.DealerDomainView{{
		ID:                  "domain-1",
		DomainURL:           "shop.example.test",
		NotificationMode:    "webhook",
		WebhookURL:          "https://merchant.example.test/webhook",
		WebhookSigningMode:  "HMAC-SHA256 signature",
		WebhookCatalogURL:   "/merchant/dashboard/webhook-catalog",
		WebhookDocsURL:      "/docs/webhooks",
		WebhookLastStatus:   "Delivered",
		WebhookLastEvent:    "payment.confirmed",
		WebhookLastAt:       "2026-06-02 12:00:00",
		WebhookLastAttempts: 1,
		KeyID:               "key-1",
		APIKey:              "gw_live_demo_key",
		APIScopes:           "checkout:write payments:read",
		APISecretStatus:     "Active",
		APISecretRotatedAt:  "2026-06-02 12:00:00",
		RotateConfirm:       "Rotate the API secret for shop.example.test?",
		SigningExample:      "curl -X POST https://gateway.example.test/payments/create",
		IdempotencyExample:  "idem_demo_key",
		CreatedAt:           "2026-06-02 12:00:00",
	}}
}

// englishWalletViews returns the wallet row the users panel and the withdrawal form render.
func englishWalletViews() []handlers.DealerWalletView {
	return []handlers.DealerWalletView{{
		ID:         "wallet-1",
		ShortID:    "wal-1",
		UserID:     "user-1",
		OwnerRef:   "customer-42",
		Domain:     "shop.example.test",
		WalletKind: "user",
		Label:      "Demo wallet",
		ProductID:  "-",
		CreatedAt:  "2026-06-02 12:00:00",
		Addresses:  []handlers.DealerAddressView{{Chain: "TRON", Address: "TXn9DemoAddress000000000000000000"}},
		Balances:   []handlers.DealerWalletBalanceRow{{AssetKey: "tron-usdt", Chain: "TRON", Symbol: "USDT", Available: "125.00", AvailableRaw: "125000000"}},
	}}
}

// englishWithdrawalViews returns the withdrawal row both the merchant withdrawals panel
// and the admin withdrawals panel render (the admin one is pending so its approve/reject
// actions render).
func englishWithdrawalViews() []handlers.DealerWithdrawalView {
	return []handlers.DealerWithdrawalView{{
		ID:              "withdrawal-1",
		ShortID:         "WD-1",
		MerchantID:      "merchant-id",
		ShortMerchantID: "merchant-id",
		MerchantName:    "Demo Merchant",
		WalletID:        "wallet-1",
		ShortWalletID:   "wal-1",
		Chain:           "TRON",
		ToAddress:       "TXn9DemoAddress000000000000000000",
		ToAddressShort:  "TXn9Demo...0000",
		AmountRaw:       "50000000",
		AmountDisplay:   "50.00",
		Symbol:          "USDT",
		Note:            "Supplier payout",
		Status:          "pending",
		StatusLabel:     "Pending review",
		StatusClass:     "is-warning",
		RequestedBy:     "demo@example.com",
		CreatedAt:       "2026-06-02 12:00:00",
		CreatedSort:     "2026-06-02 12:00:00",
		SearchText:      "Demo Merchant USDT pending",
	}}
}

// englishActivityViews returns the on-chain row the merchant transactions, merchant
// activity and admin overview panels render.
func englishActivityViews() []handlers.DealerActivityView {
	return []handlers.DealerActivityView{{
		ID:              "activity-1",
		ShortID:         "dep-1",
		Type:            "deposit",
		Chain:           "TRON",
		Symbol:          "USDT",
		AmountRaw:       "50000000",
		AmountDisplay:   "50.00 USDT",
		AmountSort:      "50000000",
		Status:          "confirmed",
		StatusLabel:     "Confirmed",
		StatusClass:     "is-success",
		Hash:            "0xdemo0000000000000000000000000000000000000000000000000000000000",
		HashShort:       "0xdemo...0000",
		FromAddress:     "TXn9Demo...0000",
		FromAddressFull: "TXn9DemoAddress000000000000000000",
		ToAddress:       "TXn9Demo...1111",
		ToAddressFull:   "TXn9DemoAddress111111111111111111",
		ProductID:       "product-1",
		UserID:          "user-1",
		WebhookStatus:   "Delivered",
		WebhookAttempts: 1,
		CreatedAt:       "2026-06-02 12:00:00",
		CreatedSort:     "2026-06-02 12:00:00",
		SearchText:      "0xdemo TRON USDT confirmed",
	}}
}

// englishPaymentViews returns the checkout row the products links tab and the merchant
// activity payments tab render.
func englishPaymentViews() []handlers.DealerPaymentView {
	return []handlers.DealerPaymentView{{
		ID:              "payment-1",
		ShortID:         "pay-1",
		OrderID:         "ORD-1001",
		ProductID:       "product-1",
		UserID:          "user-1",
		Domain:          "shop.example.test",
		LinkType:        "fixed",
		Amount:          "10.00",
		AmountSort:      "10.00",
		Currency:        "USD",
		AmountDisplay:   "10.00 USD",
		Status:          "paid",
		StatusLabel:     "Paid",
		StatusClass:     "is-success",
		WebhookStatus:   "delivered",
		WebhookAttempts: 1,
		CheckoutURL:     "https://shop.example.test/checkout/demo-link",
		InvoiceURL:      "https://shop.example.test/invoice/demo-link",
		SelectedAsset:   "USDT",
		SelectedChain:   "TRON",
		DepositAddress:  "TXn9DemoAddress000000000000000000",
		CreatedAt:       "2026-06-02 12:00:00",
		CreatedSort:     "2026-06-02 12:00:00",
		SearchText:      "ORD-1001 paid",
	}}
}

// englishDashboardFixture turns the base fixture into a signed-in merchant workspace and
// attaches the collections every dashboard panel ranges over, so the table, toolbar and
// detail branches render instead of their empty states.
func englishDashboardFixture(data *handlers.DealerPageData) {
	data.Active = "dashboard"
	data.HasSession = true

	data.TreasuryGroups = []handlers.DealerVaultAssetView{{
		ID:               "vault-usdt",
		Symbol:           "USDT",
		SearchText:       "USDT TRON",
		NetworkCount:     1,
		VariantCount:     1,
		AvailableDisplay: "125.00",
		AvailableSort:    "125.00",
		Details: []handlers.DealerVaultBalanceView{{
			Chain:            "TRON",
			Symbol:           "USDT",
			Token:            "TR7DemoTokenContract000000000000",
			DisplayToken:     "TR7DemoTokenContract000000000000",
			AvailableDisplay: "125.00",
			AvailableSort:    "125.00",
		}},
	}}
	data.ChainVaults = []handlers.DealerChainVaultView{{
		Chain:    "TRON",
		Deposits: 1,
		Assets:   []handlers.DealerBalanceView{{Chain: "TRON", Symbol: "USDT", AmountDisplay: "125.00"}},
	}}

	data.Wallets = englishWalletViews()
	data.WalletPage = handlers.DealerPaginationView{Page: 1, Limit: 25, Total: 1, TotalPages: 1, From: 1, To: 1}

	data.WithdrawalWallets = englishWalletViews()
	data.WithdrawalAssets = []handlers.DealerWithdrawalAssetView{{
		Value:            "tron-usdt",
		AssetKey:         "tron-usdt",
		Label:            "USDT on TRON",
		Chain:            "TRON",
		ChainLabel:       "TRON",
		Symbol:           "USDT",
		AvailableRaw:     "125000000",
		AvailableDisplay: "125.00 USDT",
		AvailableInput:   "125.00",
	}}
	data.Withdrawals = englishWithdrawalViews()

	data.Products = []handlers.DealerProductView{{
		ID:            "product-1",
		Name:          "Demo product",
		Description:   "Demo checkout description",
		LinkType:      "fixed",
		LinkTypeLabel: "Fixed amount",
		Amount:        "10.00",
		Currency:      "USD",
		AmountDisplay: "10.00 USD",
		Language:      "en",
		DomainID:      "domain-1",
		Domain:        "shop.example.test",
		PaymentURL:    "https://shop.example.test/checkout/demo-link",
		LogoURL:       "https://shop.example.test/logo.png",
		LogoText:      "DP",
		SuccessURL:    "https://shop.example.test/success",
		CancelURL:     "https://shop.example.test/cancel",
		CreatedAt:     "2026-06-02 12:00:00",
	}}
	data.PaymentAssets = []handlers.DealerAssetOption{{Value: "tron-usdt", Label: "TRON / USDT"}}
	data.Payments = englishPaymentViews()
	data.PaymentPage = handlers.DealerPaginationView{Page: 1, Limit: 25, Total: 1, TotalPages: 1, From: 1, To: 1}
	data.PaymentStats = map[string]int64{"paid": 1, "pending": 0, "awaiting_payment": 0, "expired": 0, "failed": 0, "underpaid": 0, "overpaid": 0, "partial_paid": 0}

	data.Activities = englishActivityViews()
	data.TransactionPage = handlers.DealerPaginationView{Page: 1, Limit: 25, Total: 1, TotalPages: 1, From: 1, To: 1}
	data.AuditLogs = []handlers.DealerAuditLogView{{
		ID:          "audit-1",
		Event:       "merchant.login",
		Status:      "success",
		Actor:       "demo@example.com",
		ActorRole:   "owner",
		Subject:     "merchant-id",
		Description: "Merchant signed in",
		IPAddress:   "203.0.113.10",
		UserAgent:   "Mozilla/5.0",
		Method:      "POST",
		Path:        "/merchant/login",
		CreatedAt:   "2026-06-02 12:00:00",
		CreatedSort: "2026-06-02 12:00:00",
		CreatedISO:  "2026-06-02T12:00:00Z",
	}}
	data.AuditPage = handlers.DealerPaginationView{Page: 1, Limit: 25, Total: 1, TotalPages: 1, From: 1, To: 1}
	data.ActivityPaymentPage = handlers.DealerPaginationView{Page: 1, Limit: 25, Total: 1, TotalPages: 1, From: 1, To: 1}
	data.ActivityDepositPage = handlers.DealerPaginationView{Page: 1, Limit: 25, Total: 1, TotalPages: 1, From: 1, To: 1}

	data.Domains = englishDomainViews()
	data.RescanChains = []handlers.DealerRescanChainOption{{Name: "tron", Label: "TRON", ChainID: "tron-mainnet", Meta: "TRON"}}

	data.SettingsVisibleChains = []handlers.DealerSettingsNetworkView{{Key: "tron", Chain: "TRON"}}
	data.SettingsHiddenChains = []handlers.DealerSettingsNetworkView{{Key: "base", Chain: "Base", ExplicitHidden: true}}
	data.HideTestnets = true
	data.HiddenChains = "base"
}

// englishAdminFixture turns the base fixture into a signed-in admin session on the given
// view/dealer/admin_dashboard.html AdminPanel value and attaches the collections that panel
// ranges over.
func englishAdminFixture(panel string) func(*handlers.DealerPageData) {
	return func(data *handlers.DealerPageData) {
		data.Active = "admin-dashboard"
		data.HasSession = true
		data.AdminPanel = panel
		data.AdminRole = "super-admin"
		data.AdminTOTPEnabled = true
		data.AdminLoginEmail = "admin@example.com"
		data.AdminPagination = handlers.DealerPaginationView{Page: 1, Limit: 25, Total: 1, TotalPages: 1, From: 1, To: 1}
		data.MerchantCount = 4
		data.WalletCountAll = 3
		data.RescanChains = []handlers.DealerRescanChainOption{{Name: "tron", Label: "TRON", ChainID: "tron-mainnet", Meta: "TRON"}}

		data.Withdrawals = englishWithdrawalViews()
		data.AdminDeposits = englishActivityViews()
		data.AdminRefunds = []handlers.DealerRefundView{{
			ID:          "refund-1",
			PaymentID:   "payment-1",
			MerchantID:  "merchant-id",
			DomainID:    "domain-1",
			AmountRaw:   "1000000",
			Reason:      "Customer requested a refund",
			Status:      "pending",
			RequestedBy: "demo@example.com",
			CreatedAt:   "2026-06-02 12:00:00",
		}}
		data.AdminSweepEligibleCount = 2
		data.AdminSweepStats = []handlers.DealerSweepStatusView{
			{Status: "pending", Label: "Pending", Count: 2},
			{Status: "succeeded", Label: "Succeeded", Count: 7},
		}
		data.AdminSweepJobs = []handlers.DealerSweepJobView{{
			ID:                    "sweep-1",
			ShortID:               "SW-1",
			TransactionUniqueHash: "0xdemo0000000000000000000000000000000000000000000000000000000000",
			WalletID:              "wallet-1",
			ShortWalletID:         "wal-1",
			MerchantID:            "merchant-id",
			ShortMerchantID:       "merchant-id",
			Chain:                 "TRON",
			Token:                 "TR7DemoTokenContract000000000000",
			Asset:                 "USDT",
			Status:                "pending",
			StatusLabel:           "Pending",
			Attempts:              1,
			MaxAttempts:           3,
			PrefundAttempts:       1,
			PrefundMaxAttempts:    3,
			CreatedAt:             "2026-06-02 12:00:00",
			UpdatedAt:             "2026-06-02 12:05:00",
		}}
		data.AdminOutboundPolicy = handlers.DealerOutboundPolicyView{
			ID:                   "policy-1",
			MaxAmountRaw:         "1000000000",
			VelocityLimitRaw:     "5000000000",
			VelocityWindowHours:  24,
			VelocityWindowLabel:  "24 hours",
			UpdatedBy:            "admin@example.com",
			UpdatedAt:            "2026-06-02 12:00:00",
			ConfigurationSummary: "Raw amount cap and rolling velocity window for outbound transfers.",
		}
		data.AdminOutboundWhitelist = []handlers.DealerOutboundWhitelistView{{
			ID:        "whitelist-1",
			Scope:     "global",
			Chain:     "TRON",
			Token:     "USDT",
			Address:   "TXn9DemoAddress000000000000000000",
			Label:     "Treasury vault",
			IsActive:  true,
			UpdatedBy: "admin@example.com",
			UpdatedAt: "2026-06-02 12:00:00",
		}}
	}
}

// englishRenderCase is one in-scope surface rendered with the English fixture. mutate
// applies the page- or panel-specific fields; anchors are copy and DOM hooks that only
// the surface under test renders, so a panel that stops rendering (or regresses to
// Turkish copy) fails by case name instead of passing on the shared chrome.
type englishRenderCase struct {
	name    string
	view    string
	mutate  func(*handlers.DealerPageData)
	anchors []string
}

// englishRenderCases is the English coverage matrix: every public merchant view, every
// merchant dashboard panel (plus the activity and products tab variants), and the admin
// dashboard panels that carry operator-facing copy.
func englishRenderCases() []englishRenderCase {
	merchantPanel := func(name, panel string, anchors ...string) englishRenderCase {
		return englishRenderCase{
			name: name,
			view: "dealer/dashboard",
			mutate: func(data *handlers.DealerPageData) {
				englishDashboardFixture(data)
				data.ActivePanel = panel
			},
			anchors: anchors,
		}
	}
	adminPanel := func(name, panel string, anchors ...string) englishRenderCase {
		return englishRenderCase{
			name:    name,
			view:    "dealer/admin_dashboard",
			mutate:  englishAdminFixture(panel),
			anchors: anchors,
		}
	}
	publicView := func(name, view, active string, anchors ...string) englishRenderCase {
		return englishRenderCase{
			name: name,
			view: view,
			mutate: func(data *handlers.DealerPageData) {
				data.Active = active
			},
			anchors: anchors,
		}
	}

	return []englishRenderCase{
		// Public merchant views.
		publicView("dealer-home", "dealer/home", "home",
			"Crypto payments that settle cleanly.", "Frequently asked questions"),
		publicView("dealer-login", "dealer/login", "login",
			"Sign in", "Sign in securely to manage payments.", "Merchant sign in", "Create a new merchant account"),
		publicView("dealer-register", "dealer/register", "register",
			"Create your merchant account", "Add crypto checkout to your store.", "Repeat password"),
		publicView("dealer-onboarding", "dealer/onboarding", "onboarding",
			"Your merchant account has been created.", "Registration complete", "Go to dashboard"),
		publicView("dealer-oidc-missing", "dealer/oidc_missing", "oidc-missing",
			"Identity provider connection is pending.", "OIDC settings missing", "Back to sign in"),
		publicView("dealer-admin-login", "dealer/admin_login", "admin-login",
			"Admin sign in", "Remember me", "Withdrawal approvals run through a controlled operations flow."),
		{
			name: "dealer-admin-2fa-setup",
			view: "dealer/admin_2fa_setup",
			mutate: func(data *handlers.DealerPageData) {
				data.Active = "admin-2fa-setup"
				data.TOTPSecret = "JBSWY3DPEHPK3PXP"
				data.TOTPQRDataURL = htmltemplate.URL("data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8AAAwAB/AGtE1qNAAAAAElFTkSuQmCC")
			},
			anchors: []string{
				"Secure sign in with Google Authenticator.",
				"Enable 2FA",
				"Show manual entry code",
				`alt="2FA QR Code"`,
			},
		},
		publicView("dealer-admin-2fa-verify", "dealer/admin_2fa_verify", "admin-2fa-verify",
			"Two-factor authentication.", "2FA Verification", "Enter the 6-digit code from your Google Authenticator app."),
		publicView("dealer-admin-2fa-disable", "dealer/admin_2fa_disable", "admin-2fa-disable",
			"Disable two-factor authentication.", "2FA Removal Confirmation", "Warning: once 2FA is disabled", "Disable 2FA"),

		// Merchant dashboard, one render per ActivePanel branch.
		merchantPanel("dealer-dashboard-overview-default", "",
			"Merchant operations overview", "Merchant ID", "Domain", "Create payment link"),
		merchantPanel("dealer-dashboard-overview", "overview",
			"Merchant operations overview", "Accumulated assets", "Copy"),
		merchantPanel("dealer-dashboard-treasury", "treasury",
			"Consolidated treasury view by coin with network breakdowns.",
			"Network vaults", "Treasury coverage and activity state of supported networks."),
		merchantPanel("dealer-dashboard-transactions", "transactions",
			"On-chain transaction records received by wallets, webhook and payment matching states.",
			"Search hash, customer, chain or status"),
		merchantPanel("dealer-dashboard-withdrawals", "withdrawals",
			"Track transfer requests from the reserve wallet to external addresses in a compact, readable feed.",
			"Admin approval", "Create withdrawal request"),
		{
			name: "dealer-dashboard-activity-deposits",
			view: "dealer/dashboard",
			mutate: func(data *handlers.DealerPageData) {
				englishDashboardFixture(data)
				data.ActivePanel = "activity"
				data.ActivityTab = "deposits"
			},
			anchors: []string{
				"Blockchain deposit flows, payment sessions, and audit records are tracked in a single controlled table.",
				"Newest deposits first",
				`id="merchant-activity-deposits-table"`,
			},
		},
		{
			name: "dealer-dashboard-activity-payments",
			view: "dealer/dashboard",
			mutate: func(data *handlers.DealerPageData) {
				englishDashboardFixture(data)
				data.ActivePanel = "activity"
				data.ActivityTab = "payments"
			},
			anchors: []string{
				"underpaid/overpaid/partial-paid are payment outcomes that require separate handling",
				`id="merchant-activity-payments-table"`,
			},
		},
		{
			name: "dealer-dashboard-activity-audit",
			view: "dealer/dashboard",
			mutate: func(data *handlers.DealerPageData) {
				englishDashboardFixture(data)
				data.ActivePanel = "activity"
				data.ActivityTab = "audit"
			},
			anchors: []string{
				"Newest records first",
				"Audit logger",
				`id="merchant-audit-table"`,
			},
		},
		merchantPanel("dealer-dashboard-users", "users",
			"Derived wallet addresses and domain scope for each user.",
			"Server search is done from the field above"),
		{
			name: "dealer-dashboard-products",
			view: "dealer/dashboard",
			mutate: func(data *handlers.DealerPageData) {
				englishDashboardFixture(data)
				data.ActivePanel = "products"
				data.ProductsTab = "products"
			},
			anchors: []string{
				"Manage domains, payment links, and invoice flows from a single workspace.",
				"Search product, domain or amount",
			},
		},
		{
			name: "dealer-dashboard-products-links",
			view: "dealer/dashboard",
			mutate: func(data *handlers.DealerPageData) {
				englishDashboardFixture(data)
				data.ActivePanel = "products"
				data.ProductsTab = "links"
			},
			anchors: []string{
				`data-products-panel="links"`,
				"Search order, customer or status",
				"Invoice",
			},
		},
		merchantPanel("dealer-dashboard-domains", "domains",
			"Webhook targets and access credentials.",
			"API key", "API Key", "Rotate secret", "Active webhook target", "Event catalog"),
		merchantPanel("dealer-dashboard-rescan", "rescan",
			"Idempotently replay an existing on-chain tx through the payment, ledger, and webhook flow.",
			"Select blockchain"),
		merchantPanel("dealer-dashboard-settings", "settings",
			"Manage dashboard and treasury coverage with drag and drop.",
			"Visible networks", "Hidden networks"),

		// Admin dashboard panels.
		adminPanel("dealer-admin-dashboard-overview", "overview",
			"Admin Panel", "Total merchants", "Deposit flow",
			"Move the on-chain balance in the selected wallet to another wallet or address."),
		adminPanel("dealer-admin-dashboard-rescan", "rescan",
			"Transaction rescan", "Re-process",
			"Re-reads the tx from the blockchain and re-sends it through the wallet match, payment, ledger and webhook flow."),
		adminPanel("dealer-admin-dashboard-security", "security",
			"Two-Factor Authentication", "Outbound policy", "Whitelist address",
			"/admin/security/2fa/disable"),
		adminPanel("dealer-admin-dashboard-withdrawals", "withdrawals",
			"Withdrawal requests", "Search merchant, wallet, address, tx or status",
			"Requested by", "/admin/withdrawals/withdrawal-1/approve"),
		adminPanel("dealer-admin-dashboard-refunds", "refunds",
			"Refund requests", "Rejection reason", "/admin/refunds/refund-1/approve"),
		adminPanel("dealer-admin-dashboard-sweep", "sweep",
			"Create missing sweep jobs", "Sweep task queue", "missing jobs"),
	}
}

// TestInScopeViewsRenderEnglishCopy renders every portal/admin surface that must default
// to English — each merchant dashboard panel, the admin panels that carry operator copy,
// and every auth view — and asserts the output is complete English HTML: non-empty,
// lang="en", no Turkish diacritics, and every case-specific anchor present. Replacing the
// earlier single-fixture pass with named cases keeps this the LANG-02 regression guard
// while making panel-level regressions fail with the panel name.
func TestInScopeViewsRenderEnglishCopy(t *testing.T) {
	engine := html.New("./views", ".html")
	engine.AddFunc("t", localization.T)
	for _, test := range englishRenderCases() {
		t.Run(test.name, func(t *testing.T) {
			data := englishDealerFixture()
			if test.mutate != nil {
				test.mutate(&data)
			}
			var buf bytes.Buffer
			if err := engine.Render(&buf, test.view, data, "dealer/layout"); err != nil {
				t.Fatalf("%s render failed: %v", test.view, err)
			}
			output := buf.String()
			if strings.TrimSpace(output) == "" {
				t.Fatalf("%s rendered empty output", test.view)
			}
			if !strings.Contains(output, `lang="en"`) {
				t.Fatalf("%s output lost its lang=\"en\" attribute", test.view)
			}
			if index := strings.IndexAny(output, turkishDiacritics); index >= 0 {
				t.Fatalf("%s renders Turkish copy near %q", test.view, snippetAround(output, index, 60))
			}
			for _, anchor := range test.anchors {
				if !strings.Contains(output, anchor) {
					t.Fatalf("%s output missing English anchor %q", test.view, anchor)
				}
			}
		})
	}
}

// TestInvoiceRendersEnglishCopy covers the invoice surface, whose handler-provided labels
// come from the invoice-only branch of api/handlers/payment.go.
func TestInvoiceRendersEnglishCopy(t *testing.T) {
	engine := html.New("./views", ".html")
	engine.AddFunc("t", localization.T)
	data := map[string]any{
		"Title":           "Invoice #ORDER-1",
		"Locale":          localization.EN,
		"StatusLabel":     "Completed",
		"StatusClass":     "is-paid",
		"ProductName":     "Demo product",
		"OrderID":         "ORDER-1",
		"DomainURL":       "demo.example.test",
		"Symbol":          "USDT",
		"ChainName":       "Ethereum",
		"CreatedAt":       "2026-06-02 12:00:00",
		"PaidAt":          "2026-06-02 12:05:00",
		"Amount":          "10.00",
		"Currency":        "USD",
		"FormattedAmount": "10.00 USD",
	}

	var buf bytes.Buffer
	if err := engine.Render(&buf, "invoice", data); err != nil {
		t.Fatalf("invoice render failed: %v", err)
	}
	output := buf.String()
	if index := strings.IndexAny(output, turkishDiacritics); index >= 0 {
		t.Fatalf("invoice renders Turkish copy near %q", snippetAround(output, index, 60))
	}
	for _, expected := range []string{"Invoice", "Completed", "USDT", "Ethereum", "ORDER-1"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("invoice output missing %q", expected)
		}
	}
	// Protocol data must survive untouched.
	for _, expected := range []string{`class="invoice-status`, "invoice-detail-item"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("invoice output missing structural hook %q", expected)
		}
	}
}

// TestCheckoutLanguageSwitchingCoversSupportedLocales proves the hosted checkout resolves
// its document language and copy from the request locale for both supported locales, and no
// longer carries the retired Turkish branch.
func TestCheckoutLanguageSwitchingCoversSupportedLocales(t *testing.T) {
	checkout, err := os.ReadFile("views/gateway/checkout.html")
	if err != nil {
		t.Fatal(err)
	}
	pay, err := os.ReadFile("views/gateway/pay.html")
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"checkout.html": string(checkout), "pay.html": string(pay)} {
		if !strings.Contains(source, `lang="{{.Locale.HTMLTag}}"`) {
			t.Fatalf("%s no longer uses the locale-driven language attribute", name)
		}
		for _, retired := range []string{"{{.Lang}}", "IsEnglish", "Türkçe", `hreflang="tr"`} {
			if strings.Contains(source, retired) {
				t.Fatalf("%s still carries the retired token %q", name, retired)
			}
		}
	}

	// payment_result.html renders both supported locales from the request locale.
	engine := html.New("./views", ".html")
	engine.AddFunc("t", localization.T)
	for _, test := range []struct {
		locale   localization.Locale
		expected string
		unwanted string
	}{
		{locale: localization.ZHCN, expected: "状态", unwanted: "Status"},
		{locale: localization.EN, expected: "Status", unwanted: "状态"},
	} {
		var buf bytes.Buffer
		data := map[string]any{
			"Locale":        test.locale,
			"I18NJSON":      localization.JSBundle(test.locale),
			"I18NElementID": localization.BundleScriptElementID,
			"SwitchURLZH":   "/checkout/token?lang=zh-CN",
			"SwitchURLEN":   "/checkout/token?lang=en",
			"StatusLabel":   "completed",
			"Title":         "Payment result",
			"Symbol":        "USDT",
			"ChainName":     "Ethereum",
			"Amount":        "10.00",
		}
		if err := engine.Render(&buf, "gateway/payment_result", data); err != nil {
			t.Fatalf("payment_result render failed (locale=%s): %v", test.locale, err)
		}
		output := buf.String()
		if !strings.Contains(output, `lang="`+test.locale.HTMLTag()+`"`) {
			t.Fatalf("payment_result locale=%s output lost its language attribute", test.locale)
		}
		if !strings.Contains(output, test.expected) {
			t.Fatalf("payment_result locale=%s output missing %q", test.locale, test.expected)
		}
		if strings.Contains(output, test.unwanted) {
			t.Fatalf("payment_result locale=%s output unexpectedly contains %q", test.locale, test.unwanted)
		}
	}
}

// snippetAround returns a bounded window around index so failure messages cannot panic
// on short outputs.
func snippetAround(text string, index, radius int) string {
	start := index - radius
	if start < 0 {
		start = 0
	}
	end := index + radius
	if end > len(text) {
		end = len(text)
	}
	return text[start:end]
}

// functionBody returns the source of the named JavaScript function up to its closing brace.
func functionBody(source, signature string) string {
	start := strings.Index(source, signature)
	if start < 0 {
		return ""
	}
	rest := source[start:]
	if end := strings.Index(rest, "\n}"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestAssetKeyNormalizationKeepsTechnicalLocale pins the one locale this change deliberately
// did NOT switch: normalizeAssetKey feeds ledger/balance lookups (data-asset-key /
// data-balance-key matching), so it keeps its original Turkish casing behaviour. Display
// filtering (normalize) and admin table sorting (localeCompare) are English.
func TestAssetKeyNormalizationKeepsTechnicalLocale(t *testing.T) {
	source, err := os.ReadFile("views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)

	if body := functionBody(text, "function normalizeAssetKey("); !strings.Contains(body, "toLocaleLowerCase('tr-TR')") {
		t.Fatalf("normalizeAssetKey must keep the technical tr-TR locale, got: %q", body)
	}
	if body := functionBody(text, "function normalize("); !strings.Contains(body, "toLocaleLowerCase('en-US')") {
		t.Fatalf("display normalize must use the English locale, got: %q", body)
	}
	if !strings.Contains(text, "localeCompare(normalize(rightValue), 'en'") {
		t.Fatal("admin table sorting must compare with the English locale")
	}
	for _, lookup := range []string{"data-asset-key", "data-balance-key"} {
		if !strings.Contains(text, lookup) {
			t.Fatalf("asset lookup attribute %q disappeared", lookup)
		}
	}
}

// TestPortalDomHooksAndProtocolTokensFrozen guards the attributes, ids, data hooks and
// protocol strings that translations must never touch.
func TestPortalDomHooksAndProtocolTokensFrozen(t *testing.T) {
	expectations := map[string][]string{
		"views/dealer/layout.html": {
			`src="/assets/dashboard.js"`,
			`/assets/tailwind.css`,
			`lang="{{.Locale.HTMLTag}}"`,
			`src="/assets/portal-i18n.js"`,
			`id="{{.I18NElementID}}"`,
		},
		"views/dealer/dashboard.html": {
			`id="apiKey`,
			`data-product-language`,
			// 004 narrowed the stored payment-link language to the two supported locales.
			`value="zh-CN"`,
			`value="en"`,
		},
		"views/dealer/admin_dashboard.html": {
			`data-admin-table-row`,
			`{{template "pg" .}}`,
		},
		"views/assets/dashboard.js": {
			`data-product-language`,
			// 004 fix: the dataset value is canonicalized before it reaches the
			// case-sensitive language select, and zh-CN stays the fallback.
			`function canonicalProductLanguage(value)`,
			`return 'zh-CN';`,
			`localeCompare`,
			`toLocaleLowerCase`,
		},
		"views/invoice.html": {
			`{{.Symbol}}`,
			`{{.ChainName}}`,
			`{{if .OrderID}}`,
			`lang="{{.Locale.HTMLTag}}"`,
			`id="{{.I18NElementID}}"`,
		},
	}
	for path, tokens := range expectations {
		source, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(source)
		for _, token := range tokens {
			if !strings.Contains(text, token) {
				t.Fatalf("%s lost frozen token %q", path, token)
			}
		}
	}
}
