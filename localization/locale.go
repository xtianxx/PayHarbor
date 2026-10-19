// Package localization resolves the per-request UI locale for every user-visible surface —
// the public home page, the merchant portal, the dealer/admin panel, the hosted buyer flow
// (payment link, checkout, pay page, payment result) and the invoice — and renders the
// zh-CN copy for it.
//
// The English text introduced by the English-portal work is the catalog key, so the
// package needs exactly one translation table (zh-CN) and always falls back to the
// English source text when a key is missing. There is no package-level mutable state:
// every entry point is a pure function of its arguments, which is what keeps locales
// from bleeding between concurrent requests.
//
// Out of scope by design: stored/user data, machine values (routes, JSON keys, enums,
// asset keys), logs and webhook payloads.
package localization

import (
	"fmt"
	"strings"
)

// Locale is a canonical UI locale.
type Locale string

const (
	// ZHCN is Simplified Chinese (the portal default).
	ZHCN Locale = "zh-CN"
	// EN is English (the catalog key language, always available).
	EN Locale = "en"
)

const (
	// Default is used when a request carries no usable language preference.
	Default = ZHCN
	// CookieName is the single language cookie for the whole application: the portal
	// (home, merchant, admin, invoice) always used it, and since 004 the buyer flow
	// (payment link, checkout, pay page, payment result) records its choice here too.
	CookieName = "gateway_portal_lang"

	// LegacyCheckoutCookieName is the pre-004 hosted-checkout cookie. It is read-only
	// legacy input for the buyer flow: a recognized value still records that visitor's
	// choice, an unrecognized one (including the retired Turkish "tr") is ignored, and
	// the cookie is never written again.
	LegacyCheckoutCookieName = "gateway_lang"
)

// supportedAliases is the complete, explicit alias whitelist. Anything else — unknown
// languages, arbitrary subtags ("en-garbage", "zh-"), script tags or injected payloads —
// is rejected so the caller falls back instead of writing a cookie.
var supportedAliases = map[string]Locale{
	"en":    EN,
	"en-us": EN,
	"en-gb": EN,

	"zh":         ZHCN,
	"zh-cn":      ZHCN,
	"zh-hans":    ZHCN,
	"zh-hans-cn": ZHCN,
	"zh-sg":      ZHCN,
	"zh-hans-sg": ZHCN,
}

// Match reports whether raw is one of the supported aliases and returns its canonical
// locale. Comparison is case-insensitive and tolerates surrounding whitespace and
// underscore separators ("ZH_CN" == "zh-CN"); no other normalization is performed.
func Match(raw string) (Locale, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", false
	}
	if strings.IndexByte(v, '_') >= 0 {
		v = strings.ReplaceAll(v, "_", "-")
	}
	locale, ok := supportedAliases[v]
	return locale, ok
}

// Resolve applies the documented priority: an explicit, recognized query value wins,
// then the portal cookie, then the zh-CN default. Unrecognized values are ignored at
// every step, so nothing a client sends is ever rendered as a locale.
func Resolve(query, cookie string) Locale {
	if l, ok := Match(query); ok {
		return l
	}
	if l, ok := Match(cookie); ok {
		return l
	}
	return Default
}

// BuyerLocale resolves the locale of the public buyer flow (payment link, checkout, pay
// page, payment result, invoice) with the same rule the portal uses, plus the retired
// checkout cookie as a read-only hint:
//
//	recognized "lang" query → recognized gateway_portal_lang cookie →
//	recognized legacy gateway_lang cookie → Default
//
// Unrecognized values at every step are ignored, so a stored or linked "tr" (or any
// other third language) can never reach rendering and resolves to the zh-CN default.
// The function is pure: no cookie is written, nothing is read from the request.
func BuyerLocale(queryLang, portalCookie, legacyCookie string) Locale {
	if l, ok := Match(queryLang); ok {
		return l
	}
	if l, ok := Match(portalCookie); ok {
		return l
	}
	if l, ok := Match(legacyCookie); ok {
		return l
	}
	return Default
}

// BuyerLocaleForProduct resolves the locale at the entry of a buyer journey (the
// payment-link landing page). It extends BuyerLocale with the language the merchant
// stored on the link, which acts as the journey default:
//
//	recognized "lang" query → recognized gateway_portal_lang cookie →
//	recognized legacy gateway_lang cookie → recognized product language → Default
//
// The visitor's own recorded choice therefore always wins over the link's setting, and a
// retired or unsupported stored value (for example the historical "tr") falls through to
// the zh-CN default instead of being displayed.
func BuyerLocaleForProduct(queryLang, portalCookie, legacyCookie, productLanguage string) Locale {
	if l, ok := Match(queryLang); ok {
		return l
	}
	if l, ok := Match(portalCookie); ok {
		return l
	}
	if l, ok := Match(legacyCookie); ok {
		return l
	}
	if l, ok := Match(productLanguage); ok {
		return l
	}
	return Default
}

// NormalizeLanguage canonicalizes a stored or submitted non-UI language value (product
// and payment-link language, API request/response fields) to the supported set. Blank,
// retired ("tr") and unrecognized values become Default, so legacy rows keep working
// without a data migration and no unsupported value is ever echoed back.
func NormalizeLanguage(raw string) Locale {
	if l, ok := Match(raw); ok {
		return l
	}
	return Default
}

// Canonical maps any value — including unsupported or empty ones — to a supported
// locale. It is the single normalization point used by HTMLTag, T and JSBundle, so an
// unknown locale can never leak into rendered output.
func (l Locale) Canonical() Locale {
	if m, ok := Match(string(l)); ok {
		return m
	}
	return Default
}

// HTMLTag returns the value for the document language attribute ("zh-CN" or "en").
func (l Locale) HTMLTag() string {
	return string(l.Canonical())
}

// T returns the localized rendering of key for the locale. key is the English source
// text; a key without a zh-CN entry renders the key itself, which is what keeps English
// complete without a second catalog.
//
// When args are supplied the (trusted) catalog text is used as a format string, so
// translations must keep the argument order of the key; arguments are values only and
// are never re-interpreted. Without args no formatting is performed at all, so a
// literal "%" in a key or translation is preserved.
func T(l Locale, key string, args ...any) string {
	text := lookupText(l.Canonical(), key)
	if len(args) == 0 {
		return text
	}
	return fmt.Sprintf(text, args...)
}

// hasAnySubtag reports whether any "-"-separated part of v equals one of needles.
func hasAnySubtag(v string, needles []string) bool {
	for _, part := range strings.Split(v, "-") {
		for _, needle := range needles {
			if part == needle {
				return true
			}
		}
	}
	return false
}
