package localization

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSBundlePayload(t *testing.T) {
	for _, l := range []Locale{ZHCN, EN} {
		var payload bundlePayload
		if err := json.Unmarshal([]byte(string(JSBundle(l))), &payload); err != nil {
			t.Fatalf("JSBundle(%q) is not valid JSON: %v", l, err)
		}
		if payload.Locale != string(l) {
			t.Errorf("JSBundle(%q) locale = %q", l, payload.Locale)
		}
		keys := JSKeys()
		if len(payload.Strings) != len(keys) {
			t.Errorf("JSBundle(%q) carries %d strings, want %d", l, len(payload.Strings), len(keys))
		}
		for _, key := range keys {
			if got, want := payload.Strings[key], lookupText(l, key); got != want {
				t.Errorf("JSBundle(%q)[%q] = %q, want %q", l, key, got, want)
			}
		}
	}
}

func TestJSBundleEnglishIsTheSourceText(t *testing.T) {
	var payload bundlePayload
	if err := json.Unmarshal([]byte(string(JSBundle(EN))), &payload); err != nil {
		t.Fatal(err)
	}
	for key, value := range payload.Strings {
		if value != key {
			t.Errorf("english bundle[%q] = %q, want the key itself", key, value)
		}
	}
}

// TestJSBundleUnknownLocaleUsesDefault pins that the bundle can never carry an
// unsupported locale or untranslated placeholder text.
func TestJSBundleUnknownLocaleUsesDefault(t *testing.T) {
	for _, raw := range []Locale{"fr", "", "zh-Hant", Locale("<script>")} {
		var payload bundlePayload
		if err := json.Unmarshal([]byte(string(JSBundle(raw))), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Locale != string(Default) {
			t.Errorf("JSBundle(%q) locale = %q, want %q", raw, payload.Locale, Default)
		}
		if payload.Strings["Copied"] != "已复制" {
			t.Errorf("JSBundle(%q) lost the default translation: %q", raw, payload.Strings["Copied"])
		}
	}
}

// TestMarshalBundleEscapesUntrustedShapedValues drives the embedding boundary with
// hostile shapes: the payload must stay parseable JSON with no raw markup, no script
// terminator and no raw line separators, and must round-trip byte-exact.
func TestMarshalBundleEscapesUntrustedShapedValues(t *testing.T) {
	hostile := map[string]string{
		"script terminator": `</script><script>alert(1)</script>`,
		"html entities":     `<>&`,
		"line separators":   "a\u2028b\u2029c",
		"quotes & escapes":  `"quoted" \back\ \u0000`,
		"percent & verbs":   `%s %d %% %[2]s`,
		"cjk & emoji":       "商户 🔐",
		"control chars":     "line1\nline2\ttab",
	}

	raw := string(marshalBundle(ZHCN, hostile))
	for _, forbidden := range []string{"<", ">", "&", "</script", "\u2028", "\u2029"} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("bundle contains raw %q", forbidden)
		}
	}
	if !strings.Contains(raw, `\u003c`) {
		t.Error("bundle does not escape < as \\u003c")
	}

	var payload bundlePayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("hardened bundle is no longer valid JSON: %v", err)
	}
	if payload.Locale != string(ZHCN) {
		t.Errorf("locale = %q, want %q", payload.Locale, ZHCN)
	}
	for key, want := range hostile {
		if got := payload.Strings[key]; got != want {
			t.Errorf("round trip changed %q: got %q, want %q", key, got, want)
		}
	}
}

// TestJSBundleIsDeterministic guards the JSON encoding choice (sorted map keys): a
// hand-rolled encoder or a reordered slice would make caching/ETag style comparisons
// unstable.
func TestJSBundleIsDeterministic(t *testing.T) {
	first, second := string(JSBundle(ZHCN)), string(JSBundle(ZHCN))
	if first != second {
		t.Fatalf("JSBundle(ZHCN) is not deterministic:\n%s\n%s", first, second)
	}
	if first == string(JSBundle(EN)) {
		t.Fatal("zh-CN and en bundles are identical")
	}
}
