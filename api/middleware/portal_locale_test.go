package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"core/localization"
)

// TEST-01C1: request-boundary behaviour of the portal locale middleware, driven through
// Fiber's app.Test — no server socket is opened, no database is touched.

func newPortalLocaleApp() *fiber.App {
	app := fiber.New()
	app.Use(PortalLocale())
	app.Use(func(c fiber.Ctx) error {
		return c.Status(fiber.StatusOK).SendString("PASS:" + c.Path())
	})
	return app
}

func do(t *testing.T, app *fiber.App, method, target string, cookies ...string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for _, cookie := range cookies {
		// AddCookie keeps every cookie; Header.Set would silently drop the earlier ones.
		req.AddCookie(&http.Cookie{Name: strings.SplitN(cookie, "=", 2)[0], Value: strings.SplitN(cookie, "=", 2)[1]})
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, target, err)
	}
	return resp, resp.Header.Get("Set-Cookie")
}

func TestPortalLocaleSetsCookieAndStripsLang(t *testing.T) {
	app := newPortalLocaleApp()
	for _, tc := range []struct{ target, path, canonical string }{
		{"/merchant/login?lang=en", "/merchant/login", "en"},
		{"/merchant/login?lang=en-US", "/merchant/login", "en"},
		{"/merchant/login?lang=ZH_CN", "/merchant/login", "zh-CN"},
		{"/admin/security?lang=zh-Hans", "/admin/security", "zh-CN"},
		{"/invoice/tok?lang=zh", "/invoice/tok", "zh-CN"},
		{"/?lang=en", "/", "en"},
		{"/merchant/login?lang=en&a=1&a=2&b=x", "/merchant/login?a=1&a=2&b=x", "en"},
		// Since 004 the buyer flow records its choice through the same middleware.
		{"/checkout/tok?lang=en", "/checkout/tok", "en"},
		{"/checkout/tok?lang=zh-Hans&asset=USDT", "/checkout/tok?asset=USDT", "zh-CN"},
		{"/checkout/tok/return/success?lang=zh", "/checkout/tok/return/success", "zh-CN"},
		{"/payment-links/tok?lang=en&user_id=1", "/payment-links/tok?user_id=1", "en"},
	} {
		resp, setCookie := do(t, app, http.MethodGet, tc.target)
		if resp.StatusCode != fiber.StatusSeeOther {
			t.Errorf("%s: status = %d, want 303 (Fiber Redirect().To default)", tc.target, resp.StatusCode)
			continue
		}
		if got := resp.Header.Get("Location"); got != tc.path {
			t.Errorf("%s: Location = %q, want %q", tc.target, got, tc.path)
		}
		if !strings.Contains(setCookie, "gateway_portal_lang="+tc.canonical) {
			t.Errorf("%s: Set-Cookie %q missing the canonical value %q", tc.target, setCookie, tc.canonical)
		}
		for _, part := range []string{"path=/", "httponly", "samesite=lax", "max-age=31536000"} {
			if !strings.Contains(strings.ToLower(setCookie), part) {
				t.Errorf("%s: Set-Cookie %q missing %q", tc.target, setCookie, part)
			}
		}
		if strings.Contains(strings.ToLower(setCookie), "secure") {
			t.Errorf("%s: plain-HTTP request must not set Secure: %q", tc.target, setCookie)
		}
		if strings.Contains(setCookie, "gateway_lang") {
			t.Errorf("%s: checkout cookie was touched: %q", tc.target, setCookie)
		}
	}
}

func TestPortalLocaleIgnoresInvalidOrExcludedRequests(t *testing.T) {
	app := newPortalLocaleApp()
	for _, target := range []string{
		// invalid values
		"/merchant/login?lang=tr", "/merchant/login?lang=fr", "/merchant/login?lang=en-garbage",
		"/checkout/tok?lang=tr", "/payment-links/tok?lang=tr", "/invoice/tok?lang=tr",
		"/merchant/login?lang=zh-", "/merchant/login?lang=%3Cscript%3E", "/merchant/login?lang=en%0d%0aX-Injected:1",
		// scope exclusions
		"/pay/tok?lang=en", "/assets/app.js?lang=en", "/static/x.css?lang=en",
		"/api/v1/common/readiness?lang=en", "/docs/index?lang=en",
		"/merchantx?lang=en", "/adminx?lang=en", "/invoice?lang=en", "//evil.test/x?lang=en",
		"/merchant/../admin?lang=en",
	} {
		resp, setCookie := do(t, app, http.MethodGet, target)
		if resp.StatusCode != fiber.StatusOK {
			t.Errorf("%s: status = %d, want 200 (pass-through)", target, resp.StatusCode)
		}
		if setCookie != "" {
			t.Errorf("%s: must not write any cookie, got %q", target, setCookie)
		}
		if body := resp.Header.Get("Location"); body != "" {
			t.Errorf("%s: must not redirect, got Location=%q", target, body)
		}
	}
}

func TestPortalLocaleLeavesMutatingRequestsAlone(t *testing.T) {
	app := newPortalLocaleApp()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		resp, setCookie := do(t, app, method, "/merchant/login?lang=en")
		if resp.StatusCode != fiber.StatusOK || setCookie != "" {
			t.Errorf("%s: status=%d cookie=%q, want a plain pass-through", method, resp.StatusCode, setCookie)
		}
	}
	// the checkout language cookie must survive untouched
	resp, setCookie := do(t, app, http.MethodGet, "/merchant/login?lang=en", "gateway_lang=tr")
	if resp.StatusCode != fiber.StatusSeeOther {
		t.Fatalf("portal switch no longer redirects: %d", resp.StatusCode)
	}
	if strings.Contains(setCookie, "gateway_lang") {
		t.Errorf("checkout cookie modified by the portal middleware: %q", setCookie)
	}
}

func TestIsPortalLocalePath(t *testing.T) {
	// Since 004 the buyer flow owns the portal language cookie as well: checkout,
	// payment-link and result paths record a recognized choice exactly like the portal.
	allowed := []string{
		"/", "/merchant", "/merchant/login", "/admin", "/admin/x/y", "/invoice/tok",
		"/checkout", "/checkout/tok", "/checkout/tok/pay", "/checkout/tok/return/success",
		"/payment-links/tok",
	}
	blocked := []string{"", "/merchantx", "/adminx", "/invoice", "/checkoutx", "/pay/x", "/assets/x", "/static/x", "/api/v1/x", "/docs/x", "//evil.test/x"}
	// "/merchant/../admin" is prefix-matched true by design; the middleware still refuses to
	// redirect it because StripLang rejects parent segments (asserted in the pass-through test).
	for _, path := range allowed {
		if !IsPortalLocalePath(path) {
			t.Errorf("IsPortalLocalePath(%q) = false, want true", path)
		}
	}
	for _, path := range blocked {
		if IsPortalLocalePath(path) {
			t.Errorf("IsPortalLocalePath(%q) = true, want false", path)
		}
	}
	if !IsPortalLocalePath("/merchant/../admin") {
		t.Error("prefix matching changed for parent segments; update the traversal expectation")
	}
}

func TestPortalLocaleHeadBehavesLikeGet(t *testing.T) {
	app := newPortalLocaleApp()
	resp, setCookie := do(t, app, http.MethodHead, "/merchant/login?lang=en")
	if resp.StatusCode != fiber.StatusSeeOther || !strings.Contains(setCookie, localization.CookieName+"=en") {
		t.Errorf("HEAD: status=%d cookie=%q", resp.StatusCode, setCookie)
	}
}

// A hostile "returnTo"-style parameter must not turn the switch into an open redirect: the
// Location stays a same-origin path with the parameter preserved verbatim.
func TestPortalLocaleNeverRedirectsOffOrigin(t *testing.T) {
	app := newPortalLocaleApp()
	resp, _ := do(t, app, http.MethodGet, "/merchant/login?returnTo=https://evil.test&lang=en")
	if resp.StatusCode != fiber.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "/merchant/login?") || strings.HasPrefix(location, "//") ||
		strings.Contains(location, "://") || strings.Contains(location, "lang=") {
		t.Errorf("unsafe or lang-carrying Location: %q", location)
	}
	if !strings.Contains(location, "https%3A%2F%2Fevil.test") {
		t.Errorf("the unrelated parameter was not preserved verbatim: %q", location)
	}
}

// TEST-01C2 additions: precedence, cookie fallbacks, Secure under HTTPS, mutating bodies,
// repeated parameters and hostile Host/absolute-URL inputs.
func TestPortalLocalePrecedenceAndCookieFallbacks(t *testing.T) {
	app := newPortalLocaleApp()

	// valid query wins over an existing valid cookie (and rewrites it)
	resp, setCookie := do(t, app, http.MethodGet, "/merchant/login?lang=en", "gateway_portal_lang=zh-CN")
	if resp.StatusCode != fiber.StatusSeeOther || !strings.Contains(setCookie, "gateway_portal_lang=en") {
		t.Errorf("query did not win over the cookie: status=%d cookie=%q", resp.StatusCode, setCookie)
	}

	// invalid query falls back to the valid cookie: the page is rendered with that locale and
	// no new cookie/redirect is produced by the middleware
	for _, target := range []string{"/merchant/login?lang=tr", "/merchant/login?lang=en-garbage", "/merchant/login"} {
		resp, setCookie := do(t, app, http.MethodGet, target, "gateway_portal_lang=en")
		if resp.StatusCode != fiber.StatusOK || setCookie != "" {
			t.Errorf("%s with a valid cookie: status=%d cookie=%q, want a pass-through", target, resp.StatusCode, setCookie)
		}
	}

	// repeated lang: fiber.Query returns the first value; every lang entry is stripped
	resp, _ = do(t, app, http.MethodGet, "/merchant/login?lang=en&lang=zh-CN&keep=1")
	location := resp.Header.Get("Location")
	if strings.Contains(location, "lang=") || !strings.Contains(location, "keep=1") {
		t.Errorf("repeated lang handling: %q", location)
	}
	// repeated non-lang parameters survive
	resp, _ = do(t, app, http.MethodGet, "/merchant/login?lang=en&x=1&x=2")
	if got := resp.Header.Get("Location"); got != "/merchant/login?x=1&x=2" {
		t.Errorf("repeated parameters lost: %q", got)
	}
}

// The Secure flag has two sources: a real TLS connection (c.Protocol() == "https") and the
// X-Forwarded-Proto proxy header. app.Test has no TLS socket, so the proxy branch is asserted
// behaviourally here and the TLS branch stays for the native/real-TLS phase (ZH-04); the
// helper is pinned by the source contract so the branch cannot silently disappear.
func TestPortalLocaleSecureCookieUnderHTTPS(t *testing.T) {
	source, err := os.ReadFile("portal_locale.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{`strings.EqualFold(c.Protocol(), "https")`, `strings.EqualFold(c.Get("X-Forwarded-Proto"), "https")`} {
		if !strings.Contains(string(source), token) {
			t.Errorf("Secure-flag contract lost %q", token)
		}
	}
	app := newPortalLocaleApp()
	// the existing X-Forwarded-Proto contract is unchanged
	req2 := httptest.NewRequest(http.MethodGet, "/merchant/login?lang=en", nil)
	req2.Header.Set("X-Forwarded-Proto", "https")
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatal(err)
	}
	if got := resp2.Header.Get("Set-Cookie"); !strings.Contains(strings.ToLower(got), "secure") {
		t.Errorf("X-Forwarded-Proto=https must set Secure: %q", got)
	}
}

func TestPortalLocaleMutatingRequestReachesHandlerUnchanged(t *testing.T) {
	app := fiber.New()
	app.Use(PortalLocale())
	var seen struct{ method, path, body, cookie string }
	app.Post("/merchant/domains", func(c fiber.Ctx) error {
		seen.method, seen.path, seen.body, seen.cookie = c.Method(), c.Path(), string(c.Body()), c.Cookies("gateway_lang")
		return c.Status(fiber.StatusCreated).SendString("created")
	})
	req := httptest.NewRequest(http.MethodPost, "/merchant/domains?lang=en", strings.NewReader("name=shop&x=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "gateway_lang", Value: "tr"})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusCreated {
		t.Errorf("status = %d, want 201 (no redirect)", resp.StatusCode)
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Errorf("mutating request must not write a language cookie: %q", resp.Header.Get("Set-Cookie"))
	}
	if seen.method != http.MethodPost || seen.path != "/merchant/domains" || seen.body != "name=shop&x=1" || seen.cookie != "tr" {
		t.Errorf("handler saw %+v, want the original request untouched", seen)
	}
}

func TestPortalLocaleHostileHostCannotRedirectOffOrigin(t *testing.T) {
	app := newPortalLocaleApp()
	req := httptest.NewRequest(http.MethodGet, "/merchant/login?lang=en", nil)
	req.Host = "evil.test"
	req.Header.Set("X-Forwarded-Host", "evil.test")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	location := resp.Header.Get("Location")
	if !strings.HasPrefix(location, "/merchant/login") || strings.Contains(location, "evil.test") || strings.Contains(location, "://") {
		t.Errorf("host-influenced Location: %q", location)
	}
}
