package localization

import (
	"net/url"
	"strings"
)

// SwitchURL builds the same-origin language link for the current portal path.
//
// The path must be a local absolute path: anything that could redirect elsewhere — a
// protocol-relative value ("//host"), an absolute URL, backslashes, control characters
// (CRLF header injection) or parent segments — is rejected and the caller renders no
// link. Query parameters are copied verbatim, duplicates included, and every "lang"
// entry is replaced by the requested locale, so the link both switches and (through the
// middleware) remembers the choice.
func SwitchURL(path string, query url.Values, target Locale) (string, bool) {
	locale, ok := Match(string(target))
	if !ok || !isLocalPath(path) {
		return "", false
	}

	values := url.Values{}
	for key, list := range query {
		if key == "lang" {
			continue
		}
		for _, value := range list {
			values.Add(key, value)
		}
	}
	values.Set("lang", string(locale))

	return (&url.URL{Path: path, RawQuery: values.Encode()}).String(), true
}

// StripLang returns the same-origin URL with every "lang" entry removed while all other
// parameters (duplicates included) are kept. It is the redirect target used after a
// language choice has been recorded in the cookie, so the switch does not stay in the
// address bar and cannot redirect again.
func StripLang(path string, query url.Values) (string, bool) {
	if !isLocalPath(path) {
		return "", false
	}
	values := url.Values{}
	for key, list := range query {
		if key == "lang" {
			continue
		}
		for _, value := range list {
			values.Add(key, value)
		}
	}
	target := &url.URL{Path: path}
	if encoded := values.Encode(); encoded != "" {
		target.RawQuery = encoded
	}
	return target.String(), true
}

// isLocalPath reports whether path is a safe same-origin path.
func isLocalPath(path string) bool {
	if path == "" || path[0] != '/' {
		return false
	}
	if strings.HasPrefix(path, "//") || strings.Contains(path, "\\") {
		return false
	}
	if strings.Contains(path, "://") {
		return false
	}
	if strings.ContainsAny(path, "\r\n\x00\t") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}
