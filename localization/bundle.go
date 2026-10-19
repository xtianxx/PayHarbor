package localization

import (
	"bytes"
	"encoding/json"
	"html/template"
)

// BundleScriptElementID is the id of the non-executing JSON script element that carries
// the browser strings. The wiring stage renders JSBundle into
// `<script type="application/json" id="portal-i18n-data">…</script>`; the browser reads
// the payload once with JSON.parse and never executes it.
const BundleScriptElementID = "portal-i18n-data"

// bundlePayload is the trusted payload shipped to the browser.
type bundlePayload struct {
	Locale  string            `json:"locale"`
	Strings map[string]string `json:"strings"`
}

// JSBundle returns the browser payload for the locale as a JSON object with the
// canonical locale and the localized text for every JS key.
//
// The value is trusted catalog data only — never user input, and never HTML: it is
// marked template.JS solely so the JSON lands verbatim inside the script element, and
// the safety of that boundary comes from the escaping below, not from the caller.
func JSBundle(l Locale) template.JS {
	canonical := l.Canonical()
	texts := make(map[string]string, len(jsCatalogKeys))
	for _, key := range jsCatalogKeys {
		texts[key] = lookupText(canonical, key)
	}
	return marshalBundle(canonical, texts)
}

// marshalBundle serializes the payload and hardens it for inline embedding:
// encoding/json escapes <, > and & (so "</script>" can never terminate the element),
// and escapeLineSeparators removes the U+2028/U+2029 line separators that are valid in
// JSON strings but not in JavaScript string literals.
//
// The payload is a string-keyed map of strings, so json.Marshal cannot fail; the
// fallback still returns a valid, constant-shaped payload rather than invalid output.
func marshalBundle(l Locale, texts map[string]string) template.JS {
	payload, err := json.Marshal(bundlePayload{Locale: string(l.Canonical()), Strings: texts})
	if err != nil {
		return template.JS(`{"locale":"` + string(l.Canonical()) + `","strings":{}}`)
	}
	return template.JS(escapeLineSeparators(payload))
}

// escapeLineSeparators rewrites raw U+2028/U+2029 into their \u escapes. The check is
// cheap and keeps the guarantee independent of the encoding/json implementation.
func escapeLineSeparators(b []byte) []byte {
	if !bytes.Contains(b, []byte("\u2028")) && !bytes.Contains(b, []byte("\u2029")) {
		return b
	}
	b = bytes.ReplaceAll(b, []byte("\u2028"), []byte(`\u2028`))
	return bytes.ReplaceAll(b, []byte("\u2029"), []byte(`\u2029`))
}
