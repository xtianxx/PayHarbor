package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"core/localization"
	"core/models"

	"github.com/gofiber/fiber/v3"
)

// storedProductLanguages are the values a payment-link row can hold today: the two supported
// values, the case/spelling aliases merchants or older builds wrote, the retired "tr" and an
// unrelated language. Every one of them must reach the edit form as a value its language
// select actually offers.
var storedProductLanguages = []struct {
	stored string
	want   string
}{
	{stored: "en", want: "en"},
	{stored: "EN", want: "en"},
	{stored: "en-US", want: "en"},
	{stored: "zh-CN", want: "zh-CN"},
	{stored: "ZH_CN", want: "zh-CN"},
	{stored: "zh", want: "zh-CN"},
	{stored: "tr", want: "zh-CN"},
	{stored: "TR", want: "zh-CN"},
	{stored: "fr", want: "zh-CN"},
	{stored: "", want: "zh-CN"},
}

func productLanguageFixtureProducts() []models.Product {
	products := make([]models.Product, 0, len(storedProductLanguages))
	for index, entry := range storedProductLanguages {
		products = append(products, models.Product{
			Name:        "product-" + strconv.Itoa(index),
			Description: "fixture",
			LinkType:    models.PaymentLinkTypeFixed,
			Amount:      "10",
			Currency:    "USD",
			Language:    entry.stored,
		})
	}
	return products
}

// buildProductViews runs the real builder through a request, exactly like the merchant
// dashboard does.
func buildProductViews(t *testing.T, locale localization.Locale, products []models.Product) []DealerProductView {
	t.Helper()
	app := fiber.New()
	var views []DealerProductView
	app.Get("/probe", func(c fiber.Ctx) error {
		views = dealerProductViews(c, locale, products)
		return c.SendString("ok")
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/probe", nil))
	if err != nil {
		t.Fatalf("probe request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK || len(views) != len(products) {
		t.Fatalf("probe returned %d views (status=%d), want %d", len(views), resp.StatusCode, len(products))
	}
	return views
}

// TestProductViewLanguageIsCanonicalForEveryStoredValue covers the first half of the edit-form
// path (stored value -> view): the machine value the template puts into
// data-product-language must be one of the two option values, otherwise the case-sensitive
// language select cannot select it, submits an empty value and silently rewrites the stored
// choice (for example an English link saved as Chinese).
func TestProductViewLanguageIsCanonicalForEveryStoredValue(t *testing.T) {
	views := buildProductViews(t, localization.ZHCN, productLanguageFixtureProducts())
	for index, entry := range storedProductLanguages {
		view := views[index]
		if view.Language != entry.want {
			t.Fatalf("stored %q produced machine language %q, want %q", entry.stored, view.Language, entry.want)
		}
		if view.Language != "zh-CN" && view.Language != "en" {
			t.Fatalf("stored %q produced %q, which is not a language select option value", entry.stored, view.Language)
		}
		wantLabel := localization.T(localization.ZHCN, "Simplified Chinese")
		if entry.want == "en" {
			wantLabel = localization.T(localization.ZHCN, "English")
		}
		if view.LanguageLabel != wantLabel {
			t.Fatalf("stored %q produced label %q, want %q", entry.stored, view.LanguageLabel, wantLabel)
		}
	}
}

// TestProductViewLanguageLabelFollowsTheRequestLocale proves the displayed label is localized
// while the machine value stays canonical.
func TestProductViewLanguageLabelFollowsTheRequestLocale(t *testing.T) {
	views := buildProductViews(t, localization.EN, productLanguageFixtureProducts())
	if got := views[0].LanguageLabel; got != "English" {
		t.Fatalf("english label = %q, want English", got)
	}
	if got := views[3].LanguageLabel; got != "Simplified Chinese" {
		t.Fatalf("chinese label in the english locale = %q, want Simplified Chinese", got)
	}
	if got := views[6].Language; got != "zh-CN" {
		t.Fatalf("retired stored language machine value = %q, want zh-CN", got)
	}
}

// TestProductEditFormTemplateCarriesCanonicalLanguageOptions covers the second half of the path
// (view -> dataset -> select): the rendered merchant dashboard row exposes the canonical
// language in data-product-language and the edit form only offers the two canonical options,
// so the dataset value always finds a matching option.
func TestProductEditFormTemplateCarriesCanonicalLanguageOptions(t *testing.T) {
	views := buildProductViews(t, localization.ZHCN, productLanguageFixtureProducts())
	data := DealerPageData{
		Locale:      localization.ZHCN,
		Title:       "products",
		Active:      "products",
		ActivePanel: "products",
		Products:    views,
		Domains:     []DealerDomainView{{ID: "domain-1", DomainURL: "shop.test"}},
	}
	app := fiber.New(fiber.Config{Views: newHandlerTestViews()})
	app.Get("/merchant/dashboard", func(c fiber.Ctx) error {
		return c.Render("dealer/dashboard", data, "dealer/layout")
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/merchant/dashboard", nil))
	if err != nil {
		t.Fatalf("dashboard render failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("dashboard status = %d, body=%s", resp.StatusCode, page[:minInt(len(page), 600)])
	}

	for index, entry := range storedProductLanguages {
		name := "product-" + strconv.Itoa(index)
		marker := `data-product-language="` + entry.want + `"`
		if !strings.Contains(page, marker) {
			t.Fatalf("product %q (stored %q) did not render %s", name, entry.stored, marker)
		}
	}
	for _, retired := range []string{`data-product-language="TR"`, `data-product-language="EN"`, `data-product-language="ZH-CN"`, `data-product-language=""`} {
		if strings.Contains(page, retired) {
			t.Fatalf("dashboard still renders a non-canonical language dataset %s", retired)
		}
	}

	optionLine := optionValuesIn(page, `id="product_edit_language"`)
	if optionLine != "zh-CN,en" {
		t.Fatalf("edit language select options = %q, want zh-CN,en (page: modal=%d editSelects=%d createSelects=%d rows=%d)",
			optionLine,
			strings.Count(page, `data-dashboard-modal="payment-link-edit"`),
			strings.Count(page, `id="product_edit_language"`),
			strings.Count(page, `id="product_language"`),
			strings.Count(page, `data-product-language=`))
	}
	createLine := optionValuesIn(page, `id="product_language"`)
	if createLine != "zh-CN,en" {
		t.Fatalf("create language select options = %q, want zh-CN,en", createLine)
	}
}

// optionValuesIn extracts the option values of the select that carries idMarker, in document
// order, so a test can compare them with the values the dataset is allowed to contain.
func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func optionValuesIn(page string, idMarker string) string {
	start := strings.Index(page, idMarker)
	if start < 0 {
		return ""
	}
	segment := page[start:]
	if end := strings.Index(segment, "</select>"); end >= 0 {
		segment = segment[:end]
	}
	values := make([]string, 0, 2)
	rest := segment
	for {
		valueIndex := strings.Index(rest, `value="`)
		if valueIndex < 0 {
			break
		}
		rest = rest[valueIndex+len(`value="`):]
		end := strings.Index(rest, `"`)
		if end < 0 {
			break
		}
		values = append(values, rest[:end])
		rest = rest[end:]
	}
	return strings.Join(values, ",")
}

// TestProductLanguageEditPathIsCanonicalizedInTheShippedScript guards the browser half of the
// path: the script that copies data-product-language into the form must canonicalize it, and it
// must not fall back to a bare dataset read that can leave the select empty.
func TestProductLanguageEditPathIsCanonicalizedInTheShippedScript(t *testing.T) {
	body, err := os.ReadFile("../../views/assets/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	for _, token := range []string{
		"function canonicalProductLanguage(value)",
		"language: canonicalProductLanguage(button.getAttribute('data-product-language')),",
		"return 'zh-CN';",
	} {
		if !strings.Contains(script, token) {
			t.Fatalf("dashboard.js lost the canonical product-language token %q", token)
		}
	}
	if strings.Contains(script, "language: (button.getAttribute('data-product-language') || 'zh-CN'),") {
		t.Fatal("dashboard.js still assigns the raw dataset value to the language select")
	}
}
