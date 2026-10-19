package middleware

import (
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v3"

	"core/localization"
)

// portalLocaleMaxAge is one year, matching the other portal cookies.
const portalLocaleMaxAge = 365 * 24 * 60 * 60

// IsPortalLocalePath reports whether path belongs to a surface that owns the language
// choice: the portal home, the merchant portal, the admin panel, invoices and — since
// 004 — the public buyer flow (payment-link landing, hosted checkout, pay page, payment
// result). Static assets and the public API are excluded, so machine responses never see
// this middleware.
func IsPortalLocalePath(path string) bool {
	switch {
	case path == "/":
		return true
	case path == "/merchant" || strings.HasPrefix(path, "/merchant/"):
		return true
	case path == "/admin" || strings.HasPrefix(path, "/admin/"):
		return true
	case strings.HasPrefix(path, "/invoice/"):
		return true
	case path == "/checkout" || strings.HasPrefix(path, "/checkout/"):
		return true
	case strings.HasPrefix(path, "/payment-links/"):
		return true
	default:
		return false
	}
}

// PortalLocale records an explicit language choice. Only a GET/HEAD request to a path in
// IsPortalLocalePath (portal home, merchant portal, admin panel, invoice, payment-link
// landing, hosted checkout and its result pages) that carries a *recognized* "lang" query
// value writes the shared language cookie (HTTPOnly, SameSite=Lax, one year, Secure following
// the real scheme) and redirects to the same path with every other query parameter preserved
// and the language parameter removed. Unrecognized values, POST requests, the public API and
// static assets pass through untouched — no cookie, no redirect.
func PortalLocale() fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return c.Next()
		}

		parsed, err := url.Parse(strings.TrimSpace(c.OriginalURL()))
		if err != nil || parsed.IsAbs() || parsed.Host != "" {
			return c.Next()
		}
		if !IsPortalLocalePath(parsed.Path) {
			return c.Next()
		}

		locale, ok := localization.Match(c.Query("lang"))
		if !ok {
			return c.Next()
		}

		query, err := url.ParseQuery(parsed.RawQuery)
		if err != nil {
			return c.Next()
		}
		target, ok := localization.StripLang(parsed.Path, query)
		if !ok {
			return c.Next()
		}

		c.Cookie(&fiber.Cookie{
			Name:     localization.CookieName,
			Value:    string(locale),
			Path:     "/",
			HTTPOnly: true,
			SameSite: "Lax",
			MaxAge:   portalLocaleMaxAge,
			Secure:   portalRequestIsHTTPS(c),
		})

		return c.Redirect().To(target)
	}
}

// portalRequestIsHTTPS mirrors the scheme detection used by the other portal cookies.
func portalRequestIsHTTPS(c fiber.Ctx) bool {
	return strings.EqualFold(c.Protocol(), "https") || strings.EqualFold(c.Get("X-Forwarded-Proto"), "https")
}
