package localization

import "testing"

// TestBuyerLocalePriority pins the buyer-flow resolution order: a recognized "lang" query
// wins, then the canonical cookie, then the retired checkout cookie, then zh-CN. Turkish
// and any other third language are ignored at every step, so a stored or linked "tr"
// resolves to the default instead of being rendered.
func TestBuyerLocalePriority(t *testing.T) {
	cases := []struct {
		name         string
		query        string
		portalCookie string
		legacyCookie string
		want         Locale
	}{
		{"no input defaults to zh-CN", "", "", "", ZHCN},
		{"query wins over both cookies", "en", "zh-CN", "zh-CN", EN},
		{"query wins when only legacy is set", "en", "", "zh-CN", EN},
		{"canonical cookie wins over legacy", "zh-CN", "zh-CN", "en", ZHCN},
		{"legacy cookie is honored when nothing else is set", "", "", "en", EN},
		{"retired turkish query is ignored", "tr", "en", "", EN},
		{"retired turkish query falls back to default", "tr", "", "", ZHCN},
		{"retired turkish cookie is ignored", "", "tr", "tr", ZHCN},
		{"retired turkish legacy cookie is ignored", "", "", "tr", ZHCN},
		{"third language query is ignored", "de", "en", "", EN},
		{"third language query alone falls back to default", "fr-CA", "", "", ZHCN},
		{"script-shaped query is ignored", "en-<script>alert(1)</script>", "", "en", EN},
		{"whitespace query is ignored", "   ", "en", "", EN},
		{"aliases canonicalize", "ZH_CN", "", "", ZHCN},
		{"english aliases canonicalize", "en-GB", "", "", EN},
		{"hans alias canonicalizes", "zh-Hans", "", "", ZHCN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuyerLocale(tc.query, tc.portalCookie, tc.legacyCookie); got != tc.want {
				t.Fatalf("BuyerLocale(%q, %q, %q) = %q, want %q", tc.query, tc.portalCookie, tc.legacyCookie, got, tc.want)
			}
		})
	}
}

// TestBuyerLocaleForProductPriority pins the journey-entry order: the visitor's own choice
// (query, then cookies) always beats the language stored on the payment link, and a link
// language that is retired or unsupported falls through to the zh-CN default.
func TestBuyerLocaleForProductPriority(t *testing.T) {
	cases := []struct {
		name            string
		query           string
		portalCookie    string
		legacyCookie    string
		productLanguage string
		want            Locale
	}{
		{"link language is the journey default", "", "", "", "en", EN},
		{"no input and no link language defaults to zh-CN", "", "", "", "", ZHCN},
		{"visitor cookie beats the link language", "", "zh-CN", "", "en", ZHCN},
		{"visitor query beats the link language", "zh-CN", "", "", "en", ZHCN},
		{"legacy cookie beats the link language", "", "", "zh-CN", "en", ZHCN},
		{"retired link language falls through to default", "", "", "", "tr", ZHCN},
		{"unsupported link language falls through to default", "", "", "", "de-DE", ZHCN},
		{"retired link language does not mask a visitor choice", "en", "", "", "tr", EN},
		{"link aliases canonicalize", "", "", "", "zh-Hans", ZHCN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuyerLocaleForProduct(tc.query, tc.portalCookie, tc.legacyCookie, tc.productLanguage)
			if got != tc.want {
				t.Fatalf("BuyerLocaleForProduct(%q, %q, %q, %q) = %q, want %q",
					tc.query, tc.portalCookie, tc.legacyCookie, tc.productLanguage, got, tc.want)
			}
		})
	}
}

// TestNormalizeLanguageCanonicalizesStoredValues pins the stored-value normalization used
// by the product/payment-link language and the API fields: only the supported aliases
// survive, everything else (blank, retired "tr", third languages, junk) becomes the
// zh-CN default so a legacy row can never render an unsupported language.
func TestNormalizeLanguageCanonicalizesStoredValues(t *testing.T) {
	cases := map[string]Locale{
		"":           ZHCN,
		"   ":        ZHCN,
		"en":         EN,
		"EN-us":      EN,
		"zh":         ZHCN,
		"zh-CN":      ZHCN,
		"zh-Hans":    ZHCN,
		"tr":         ZHCN,
		"tr-TR":      ZHCN,
		"de":         ZHCN,
		"en-garbage": ZHCN,
		"<script>":   ZHCN,
	}
	for raw, want := range cases {
		if got := NormalizeLanguage(raw); got != want {
			t.Fatalf("NormalizeLanguage(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestBuyerLocaleIgnoresBrowserPreference documents that no buyer entry point consults the
// browser's Accept-Language: the resolver takes exactly three explicit inputs and a
// missing/forged one can only fall back to the default.
func TestBuyerLocaleNeverUsesBrowserPreference(t *testing.T) {
	if got := BuyerLocale("", "", ""); got != Default {
		t.Fatalf("empty resolution = %q, want default %q", got, Default)
	}
	if Default != ZHCN {
		t.Fatalf("buyer default = %q, want zh-CN", Default)
	}
}

// TestBuyerBrowserKeysAreCatalogued guards the bundle: every buyer string the browser asks
// for must have a zh-CN rendering, otherwise a Chinese buyer page would silently show
// English.
func TestBuyerBrowserKeysAreCatalogued(t *testing.T) {
	for _, key := range []string{
		"Copied",
		"Choose coin",
		"Waiting for coin",
		"Select network",
		"Payment confirmed",
		"Redirecting to the success page.",
		"Payment confirming",
		"A transaction was detected and is waiting for final settlement.",
		"Waiting for payment",
		"Choose the amount in your wallet and send it to the address below.",
		"Send the exact amount to the address below.",
		"Partial payment received",
		"The received amount is being added to this checkout. Send the remaining amount to complete the payment.",
		"Payment",
		"Waiting for payment...",
		"Live connection is active. Send the amount you choose now.",
		"Live connection is active. Send the exact amount now.",
		"Live connection reconnecting",
		"Polling fallback is still checking this payment.",
	} {
		if _, ok := catalogZH[key]; !ok {
			t.Fatalf("buyer browser key %q has no zh-CN catalog entry", key)
		}
		if T(ZHCN, key) == key {
			t.Fatalf("buyer browser key %q renders English in zh-CN", key)
		}
	}
}

// TestBuyerKeysRenderChinese ensures the buyer surfaces are actually translated rather than
// falling back to the English source text in zh-CN.
func TestBuyerKeysRenderChinese(t *testing.T) {
	for _, key := range []string{
		"Secure checkout",
		"Checkout progress",
		"Checkout language",
		"Choose a language",
		"Close language selection",
		"Available languages",
		"Payment method",
		"Choose coin",
		"Search coin",
		"Network",
		"Select network",
		"Waiting for token",
		"Waiting for coin",
		"Continue",
		"Select",
		"Try again",
		"Address unavailable",
		"Unavailable",
		"Change asset",
		"Price source",
		"Not used",
		"Gateway pricing",
		"Payer decides",
		"Amount you want to deposit",
		"You pay",
		"Set in wallet",
		"No expiry",
		"Send your chosen amount",
		"Send exactly",
		"Chosen amount",
		"Wallet address",
		"Exact amount",
		"Payment URI",
		"Copy address",
		"Expected amount",
		"Order",
		"Order ID",
		"Track ID",
		"Status",
		"Total",
		"Amount",
		"Copy",
		"Cancel",
		"Change",
		"Payment status",
		"Completed",
		"Canceled",
		"Action required",
		"Paid amount",
		"Settled with",
		"Transaction details",
		"Receipt references",
		"Show",
		"Print receipt",
		"Back to checkout",
		"Paid",
		"Expired",
		"Failed",
		"Underpaid",
		"Overpaid",
		"Partial payment",
		"Partial payment received",
		"Payment confirmed",
		"Payment confirming",
		"Waiting for payment",
		"Payment unavailable",
		"Payment canceled",
		"Payment complete",
		"Payment link unavailable",
	} {
		if _, ok := catalogZH[key]; !ok {
			t.Fatalf("buyer key %q has no zh-CN catalog entry", key)
		}
		if text := T(ZHCN, key); text == key {
			t.Fatalf("buyer key %q renders English in zh-CN", key)
		}
	}
}
