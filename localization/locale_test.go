package localization

import (
	"sync"
	"testing"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		raw  string
		want Locale
		ok   bool
	}{
		{"zh", ZHCN, true},
		{"zh-CN", ZHCN, true},
		{"zh-cn", ZHCN, true},
		{"ZH_CN", ZHCN, true},
		{"  zh-Hans  ", ZHCN, true},
		{"zh-Hans-CN", ZHCN, true},
		{"zh-Hans-SG", ZHCN, true},
		{"zh-SG", ZHCN, true},
		{"en", EN, true},
		{"en-US", EN, true},
		{"en-GB", EN, true},
		{"EN", EN, true},
		{"en_us", EN, true},

		{"", "", false},
		{"   ", "", false},
		{"fr", "", false},
		{"de-DE", "", false},
		{"tr", "", false}, // checkout language; must never be accepted as a portal locale
		{"zh-Hant", "", false},
		{"zh-TW", "", false},
		{"zh-HK", "", false},
		{"zh-MO", "", false},
		{"zh-Hant-TW", "", false},
		{"enx", "", false},
		{"zhx", "", false},
		{"cn", "", false},
		{"en-", "", false},
		{"zh-", "", false},
		{"en-garbage", "", false},
		{"en-US-extra", "", false},
		{"zh-Hans-CN-x", "", false},
		{"en-<script>alert(1)</script>", "", false},
		{"en;DROP", "", false},
	}
	for _, test := range tests {
		got, ok := Match(test.raw)
		if ok != test.ok || got != test.want {
			t.Errorf("Match(%q) = (%q, %v), want (%q, %v)", test.raw, got, ok, test.want, test.ok)
		}
	}
}

func TestResolvePriority(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		cookie string
		want   Locale
	}{
		{"query wins over cookie", "en", "zh-CN", EN},
		{"query zh-CN wins over en cookie", "zh-CN", "en", ZHCN},
		{"unrecognized query falls back to cookie", "fr", "en", EN},
		{"garbage query falls back to cookie", "en-garbage", "zh-CN", ZHCN},
		{"unknown subtag query falls back to cookie", "zh-Hant", "en", EN},
		{"injection-shaped query falls back to cookie", "en-<script>alert(1)</script>", "zh-CN", ZHCN},
		{"empty query uses cookie", "", "en", EN},
		{"unrecognized cookie uses default", "", "fr", Default},
		{"nothing provided uses default", "", "", Default},
		{"traditional query falls back to cookie", "zh-Hant", "zh-CN", ZHCN},
		{"traditional query and cookie use default", "zh-Hant", "zh-TW", Default},
		{"checkout language is ignored everywhere", "tr", "tr", Default},
	}
	for _, test := range tests {
		if got := Resolve(test.query, test.cookie); got != test.want {
			t.Errorf("%s: Resolve(%q, %q) = %q, want %q", test.name, test.query, test.cookie, got, test.want)
		}
	}
}

func TestCanonicalAndHTMLTag(t *testing.T) {
	tests := []struct {
		raw  Locale
		want Locale
	}{
		{ZHCN, ZHCN},
		{EN, EN},
		{Locale("en-US"), EN},
		{Locale("zh-Hans"), ZHCN},
		{"", ZHCN},
		{Locale("fr"), ZHCN},
		{Locale("zh-Hant"), ZHCN}, // unsupported variant falls back to the default locale
		{Locale("<script>alert(1)</script>"), ZHCN},
	}
	for _, test := range tests {
		if got := test.raw.Canonical(); got != test.want {
			t.Errorf("Locale(%q).Canonical() = %q, want %q", test.raw, got, test.want)
		}
		if got := test.raw.HTMLTag(); got != string(test.want) {
			t.Errorf("Locale(%q).HTMLTag() = %q, want %q", test.raw, got, string(test.want))
		}
	}
}

// TestTNeverRendersRawInput pins the rule that an unknown locale is normalized to the
// default instead of leaking the client-supplied value into rendered output.
func TestTNeverRendersRawInput(t *testing.T) {
	for _, raw := range []string{"fr", "tr", "", "zh-Hant", "%s", "<script>alert(1)</script>"} {
		if got := T(Locale(raw), "Sign in"); got != "登录" {
			t.Errorf("T(%q, \"Sign in\") = %q, want the zh-CN default rendering", raw, got)
		}
	}
}

func TestTEnglishFallbackAndMissingKeys(t *testing.T) {
	const unknown = "A display string that is not in the catalog yet"

	if got := T(EN, unknown); got != unknown {
		t.Errorf("T(EN) of an unknown key = %q, want the key itself", got)
	}
	if got := T(ZHCN, unknown); got != unknown {
		t.Errorf("T(ZHCN) of an untranslated key = %q, want the English key as fallback", got)
	}
	if got := T(ZHCN, ""); got != "" {
		t.Errorf("T(ZHCN, \"\") = %q, want an empty string", got)
	}
	if got := T(EN, "Sign in"); got != "Sign in" {
		t.Errorf("T(EN, \"Sign in\") = %q, want the English source text", got)
	}
}

func TestTGlossaryTerms(t *testing.T) {
	tests := map[string]string{
		"merchant":               "商户",
		"domain":                 "域名",
		"Deposit":                "入账",
		"Top up":                 "充值",
		"Withdrawal":             "提现",
		"Refund":                 "退款",
		"Sweep":                  "归集",
		"balance":                "余额",
		"Payment link":           "支付链接",
		"Invoice":                "发票",
		"API key":                "API 密钥",
		"API secret":             "API 签名密钥",
		"Webhook signing secret": "Webhook 签名密钥",
		"Webhook":                "Webhook",
	}
	for key, want := range tests {
		if got := T(ZHCN, key); got != want {
			t.Errorf("T(ZHCN, %q) = %q, want %q", key, got, want)
		}
	}
}

func TestTFormatting(t *testing.T) {
	if got := T(EN, "Invoice #%s", "ORDER-9"); got != "Invoice #ORDER-9" {
		t.Errorf("english formatting = %q", got)
	}
	if got := T(ZHCN, "Invoice #%s", "ORDER-9"); got != "发票 #ORDER-9" {
		t.Errorf("zh formatting = %q", got)
	}
	if got := T(ZHCN, "%d of %d", 3, 9); got != "第 3 项，共 9 项" {
		t.Errorf("zh multi-argument order = %q", got)
	}
	if got := T(EN, "%d of %d", 3, 9); got != "3 of 9" {
		t.Errorf("english multi-argument order = %q", got)
	}

	// Arguments are values, never formats: replacements must not be reinterpreted.
	hostile := `$& $1 % %s $' </script><script>alert(1)</script>`
	wantEN := "Signed in as " + hostile
	if got := T(EN, "Signed in as %s", hostile); got != wantEN {
		t.Errorf("argument reinterpretation: got %q, want %q", got, wantEN)
	}
	if got := T(ZHCN, "Signed in as %s", hostile); got != "已登录："+hostile {
		t.Errorf("zh argument reinterpretation: got %q", got)
	}

	// No args means no formatting at all, so literal percent signs survive.
	if got := T(ZHCN, "100% reserved"); got != "100% 已预留" {
		t.Errorf("zero-argument percent = %q", got)
	}
	if got := T(EN, "100% reserved"); got != "100% reserved" {
		t.Errorf("zero-argument percent (en) = %q", got)
	}

	// html/template escapes rendered output; the package must hand over plain text so
	// nothing is double-escaped or marked safe here.
	if got := T(ZHCN, "Signed in as %s", "<b>merchant</b>"); got != "已登录：<b>merchant</b>" {
		t.Errorf("plain-text contract = %q", got)
	}
}

// TestConcurrentUse runs mixed locales in parallel; with -race it also proves the
// package keeps no mutable shared state, which is the concurrency half of the
// "no language bleeding between requests" requirement.
func TestConcurrentUse(t *testing.T) {
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			query, cookie, want := "en", "zh-CN", EN
			if i%2 == 1 {
				query, cookie, want = "zh-CN", "en", ZHCN
			}
			for j := range 100 {
				if got := Resolve(query, cookie); got != want {
					t.Errorf("iteration %d: Resolve = %q, want %q", j, got, want)
				}
				if got := T(want, "Invoice #%s", "X"); got == "" {
					t.Error("empty translation")
				}
				if got := string(JSBundle(want)); got == "" {
					t.Error("empty bundle")
				}
				_ = Keys()
				_ = JSKeys()
			}
		}(i)
	}
	wg.Wait()
}
