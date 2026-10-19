package handlers

import (
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"

	"core/localization"
)

// MerchantLocaleHeader is the private, canonical locale hint a merchant fast-navigation
// partial request carries: the locale of the document that is doing the fetching. It only
// selects the locale of that response's own chrome — it never persists a cookie, never
// grants access and never translates user data.
const MerchantLocaleHeader = "X-Merchant-Locale"

// isMerchantFastNavPartial reports whether the request is a merchant fast-navigation
// partial: a GET on the merchant dashboard/domains surfaces carrying the partial header.
// Only these requests may use MerchantLocaleHeader.
func isMerchantFastNavPartial(c fiber.Ctx) bool {
	if c.Method() != fiber.MethodGet {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(c.Get("X-Merchant-Navigation")), "partial") {
		return false
	}
	path := c.Path()
	return path == "/merchant/dashboard" || strings.HasPrefix(path, "/merchant/dashboard/") ||
		path == "/merchant/domains" || strings.HasPrefix(path, "/merchant/domains/")
}

// canonicalMerchantLocale returns the hint only when it is exactly a supported canonical
// tag ("en" or "zh-CN"); aliases such as "en-US" or "zh-Hans" are ignored so a partial can
// never render a locale the rest of the portal does not use.
func canonicalMerchantLocale(c fiber.Ctx) (localization.Locale, bool) {
	switch strings.TrimSpace(c.Get(MerchantLocaleHeader)) {
	case string(localization.EN):
		return localization.EN, true
	case string(localization.ZHCN):
		return localization.ZHCN, true
	}
	return localization.Locale(""), false
}

// merchantPageLocale resolves the locale for a merchant page or its fast-navigation
// partial. A partial request that carries a valid canonical MerchantLocaleHeader renders
// in that locale (the document context wins, so a shared cookie changed by another tab
// cannot flip this document's workspace); every other request keeps the normal order:
// explicit lang query, then the portal cookie, then the zh-CN default.
func merchantPageLocale(c fiber.Ctx) localization.Locale {
	if isMerchantFastNavPartial(c) {
		if loc, ok := canonicalMerchantLocale(c); ok {
			return loc
		}
	}
	return portalLocale(c)
}

// portalLocale resolves the portal locale for the current request: explicit lang query
// first, then the portal cookie, then the zh-CN default. Per request only — no globals.
func portalLocale(c fiber.Ctx) localization.Locale {
	return localization.Resolve(c.Query("lang"), c.Cookies(localization.CookieName))
}

// portalText localizes Go-owned display copy for the request's locale. Use it for page
// titles, field labels, flash/validation messages — never for routes, JSON keys, enum
// values, SQL, log fields or upstream error text.
func portalText(c fiber.Ctx, key string, args ...any) string {
	return localization.T(portalLocale(c), key, args...)
}

// portalChromeQuery rebuilds the current request's query parameters. fiber v3's Ctx.Query
// takes a key (there is no whole-query accessor), so the raw target is parsed instead.
func portalChromeQuery(c fiber.Ctx) url.Values {
	parsed, err := url.Parse(c.OriginalURL())
	if err != nil {
		return url.Values{}
	}
	return parsed.Query()
}

// applyPortalChrome attaches the per-request locale, the browser string bundle and the
// same-origin switch links to a page model.
func applyPortalChrome(c fiber.Ctx, loc localization.Locale, data *DealerPageData) {
	query := portalChromeQuery(c)
	data.Locale = loc
	data.I18NJSON = localization.JSBundle(loc)
	data.I18NElementID = localization.BundleScriptElementID
	data.SwitchURLZH, _ = localization.SwitchURL(c.Path(), query, localization.ZHCN)
	data.SwitchURLEN, _ = localization.SwitchURL(c.Path(), query, localization.EN)
}
