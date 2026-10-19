package localization

import (
	"net/url"
	"strings"
	"testing"
)

func TestSwitchURLBuildsSamePathLink(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		query string
		want  Locale
		out   string
	}{
		{name: "plain path", path: "/merchant/login", want: EN, out: "/merchant/login?lang=en"},
		{name: "switches to chinese", path: "/merchant/login", want: ZHCN, out: "/merchant/login?lang=zh-CN"},
		{name: "keeps other parameters", path: "/merchant/dashboard", query: "tab=products&q=abc", want: EN, out: "/merchant/dashboard?lang=en&q=abc&tab=products"},
		{name: "replaces existing lang", path: "/merchant/dashboard", query: "lang=en&tab=products", want: ZHCN, out: "/merchant/dashboard?lang=zh-CN&tab=products"},
		{name: "duplicate lang entries collapse", path: "/merchant/dashboard", query: "lang=en&lang=zh-CN", want: EN, out: "/merchant/dashboard?lang=en"},
		{name: "duplicate parameters survive", path: "/admin", query: "id=1&id=2", want: EN, out: "/admin?id=1&id=2&lang=en"},
		{name: "escapes parameter values", path: "/admin", query: "next=/x?a=b&c=%26d", want: EN, out: "/admin?c=%26d&lang=en&next=%2Fx%3Fa%3Db"},
		{name: "root path", path: "/", want: ZHCN, out: "/?lang=zh-CN"},
		{name: "invoice path", path: "/invoice/token-1", want: EN, out: "/invoice/token-1?lang=en"},
		{name: "unicode path", path: "/merchant/发票", want: ZHCN, out: "/merchant/%E5%8F%91%E7%A5%A8?lang=zh-CN"},
		{name: "alias target works", path: "/", want: Locale("zh-Hans"), out: "/?lang=zh-CN"},
	}
	for _, test := range tests {
		query, err := url.ParseQuery(test.query)
		if err != nil {
			t.Fatalf("%s: bad fixture query: %v", test.name, err)
		}
		got, ok := SwitchURL(test.path, query, test.want)
		if !ok {
			t.Errorf("%s: SwitchURL rejected a local path", test.name)
			continue
		}
		if got != test.out {
			t.Errorf("%s: SwitchURL = %q, want %q", test.name, got, test.out)
		}
		if !strings.HasPrefix(got, "/") || strings.HasPrefix(got, "//") {
			t.Errorf("%s: not a local path: %q", test.name, got)
		}
	}
}

// TestSwitchURLRejectsForeignTargets pins the open-redirect guard: nothing that could
// send the visitor to another origin (or split a response header) may be accepted, and
// the Host/returnTo style inputs are simply never consulted.
func TestSwitchURLRejectsForeignTargets(t *testing.T) {
	hostile := []string{
		"",
		"//evil.example.com/merchant/login",
		"https://evil.example.com/merchant/login",
		"http://evil.example.com",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"merchant/login",
		"/merchant/login\r\nSet-Cookie: a=b",
		"/merchant/login\nX-Injected: 1",
		"/merchant/\\evil",
		"/merchant/../admin",
		"/merchant/../../etc/passwd",
		"/merchant/login\x00",
	}
	for _, path := range hostile {
		if got, ok := SwitchURL(path, url.Values{}, EN); ok {
			t.Errorf("SwitchURL(%q) = %q, accepted a non-local path", path, got)
		}
	}
}

// TestSwitchURLRejectsUnknownLocale keeps the helper and the resolver consistent: an
// unsupported target produces no link at all rather than a link with a junk value.
func TestSwitchURLRejectsUnknownLocale(t *testing.T) {
	for _, target := range []Locale{"fr", "en-garbage", "zh-", "zh-Hant", ""} {
		if got, ok := SwitchURL("/merchant/login", url.Values{}, target); ok {
			t.Errorf("SwitchURL(target=%q) = %q, want no link", target, got)
		}
	}
}

func TestStripLangKeepsOtherParameters(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		query string
		out   string
	}{
		{name: "drops lang only", path: "/merchant/login", query: "lang=en", out: "/merchant/login"},
		{name: "keeps the rest in order", path: "/merchant/dashboard", query: "lang=zh-CN&tab=products&q=a+b", out: "/merchant/dashboard?q=a+b&tab=products"},
		{name: "duplicates survive", path: "/admin", query: "id=1&lang=en&id=2", out: "/admin?id=1&id=2"},
		{name: "no query", path: "/", out: "/"},
	}
	for _, test := range tests {
		query, err := url.ParseQuery(test.query)
		if err != nil {
			t.Fatalf("%s: bad fixture query: %v", test.name, err)
		}
		got, ok := StripLang(test.path, query)
		if !ok {
			t.Errorf("%s: StripLang rejected a local path", test.name)
			continue
		}
		if got != test.out {
			t.Errorf("%s: StripLang = %q, want %q", test.name, got, test.out)
		}
		if strings.Contains(got, "lang=") {
			t.Errorf("%s: StripLang kept a lang entry: %q", test.name, got)
		}
	}
}

func TestStripLangRejectsForeignPaths(t *testing.T) {
	for _, path := range []string{"", "//evil.example.com", "https://evil.example.com", "/a\r\nb", "/../x", "relative"} {
		if got, ok := StripLang(path, url.Values{}); ok {
			t.Errorf("StripLang(%q) = %q, want rejection", path, got)
		}
	}
}

func TestIsLocalPath(t *testing.T) {
	valid := []string{"/", "/merchant/login", "/admin/2fa/setup", "/invoice/abc", "/a%20b", "/a.b/c-d_e"}
	for _, path := range valid {
		if !isLocalPath(path) {
			t.Errorf("isLocalPath(%q) = false, want true", path)
		}
	}
}
