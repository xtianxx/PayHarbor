package handlers

import (
	"html/template"
	"strings"
	"testing"

	"core/localization"
)

// Fix01C3 — the language-switch names must be shown in their own language in *both* locales
// (ui-guidance.md), and the switch metadata must not change with the page locale.
var switchNames = map[string]string{"zh-CN": "简体中文", "en": "English"}

type switchContext struct {
	name       string
	active     string
	hasSession bool
	adminPanel bool
}

func renderSwitchTemplate(t *testing.T, locale localization.Locale, ctx switchContext) string {
	t.Helper()
	engine, err := template.New("header.html").Funcs(template.FuncMap{"t": localization.T}).
		ParseFiles("../../views/dealer/partials/header.html")
	if err != nil {
		t.Fatalf("parse header partial: %v", err)
	}
	data := map[string]any{
		"Locale":      locale,
		"SwitchURLZH": "/merchant/login?lang=zh-CN",
		"SwitchURLEN": "/merchant/login?lang=en",
		"Active":      ctx.active,
		"HasSession":  ctx.hasSession,
		"AdminPanel":  ctx.adminPanel,
	}
	var out strings.Builder
	if err := engine.ExecuteTemplate(&out, "header.html", data); err != nil {
		t.Fatalf("render header partial (%s): %v", locale, err)
	}
	return out.String()
}

func TestFix01C3HeaderSwitchShowsEachLanguageName(t *testing.T) {
	// The header has three layouts (public, signed-in merchant, signed-in admin); every one
	// must show each language in its own language.
	contexts := []switchContext{
		{name: "public", active: "login", hasSession: false, adminPanel: false},
		{name: "merchant", active: "admin-dashboard", hasSession: true, adminPanel: false},
		{name: "admin", active: "admin-dashboard", hasSession: true, adminPanel: true},
	}
	for _, ctx := range contexts {
		for _, locale := range []localization.Locale{localization.ZHCN, localization.EN} {
			html := renderSwitchTemplate(t, locale, ctx)
			for tag, want := range switchNames {
				link := switchLinkFor(t, html, tag)
				if link == "" {
					t.Errorf("locale %s: no switch link for %s", locale, tag)
					continue
				}
				if !strings.Contains(link, ">"+want+"</a>") {
					t.Errorf("locale %s: %s link text is not %q: %s", locale, tag, want, link)
				}
				// the catalog is the single source of the Chinese name
				if want != localization.T(localization.Locale(tag), "Simplified Chinese") && want != localization.T(localization.Locale(tag), "English") {
					t.Errorf("name %q does not come from the catalog", want)
				}
				if !strings.Contains(link, `hreflang="`+tag+`"`) || !strings.Contains(link, `lang="`+tag+`"`) {
					t.Errorf("locale %s: %s link lost lang/hreflang: %s", locale, tag, link)
				}
				if !strings.Contains(link, `href="/merchant/login?lang=`) {
					t.Errorf("locale %s: %s link href changed: %s", locale, tag, link)
				}
			}
			// exactly one current marker, and it sits on the active locale link
			if got := strings.Count(html, `aria-current="true"`); got != 1 {
				t.Errorf("locale %s: expected exactly one aria-current, got %d", locale, got)
			}
			current := switchLinkFor(t, html, string(locale.HTMLTag()))
			if !strings.Contains(current, `aria-current="true"`) {
				t.Errorf("%s/%s: aria-current is not on the active link: %s", ctx.name, locale, current)
			}
			if strings.Contains(html, "<form") {
				t.Errorf("%s/%s: the language switch must not be inside a form", ctx.name, locale)
			}
		}
	}
}

func switchLinkFor(t *testing.T, html string, tag string) string {
	t.Helper()
	for _, link := range strings.Split(html, "<a ")[1:] {
		link = "<a " + link
		if strings.Contains(link, `hreflang="`+tag+`"`) {
			if end := strings.Index(link, "</a>"); end >= 0 {
				return link[:end+4]
			}
			return link
		}
	}
	return ""
}
