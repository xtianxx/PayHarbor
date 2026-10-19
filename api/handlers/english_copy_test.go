package handlers

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Turkish display copy must not come back to the handler package after LANG-02/004: the
// buyer flow resolves its copy through the localization catalog as well.
// These are display-string invariants, not style tests: they read the package sources and
// look for Turkish copy inside string literals, which is the only place this package can
// carry user-visible text.
var (
	turkishDiacriticPattern = regexp.MustCompile(`[ıİşŞğĞüÜöÖçÇ]`)
	// ASCII-Turkish words that must not appear in handler display copy. Machine values
	// (route paths, scope values, enums such as "tr"/"en") are unaffected by this list.
	asciiTurkishPattern = regexp.MustCompile(`(?i)\b(durum|tutar|tarih|giris|kullanici|kaydet|tumu|iptal|duzenle|ayarlar|cikis|siparis|adres|cuzdan|islem|bildirim|bakiye|beklemede|tamamlandi|basarisiz|silindi|olustur|guncelle|yukleniyor|gonderiliyor|lutfen|zorunlu|gecerli|odeme|fatura|supurme|uye|isyeri|kayit|sifre|eposta)\b`)
)

func TestHandlerSourcesCarryNoTurkishDisplayCopy(t *testing.T) {
	for _, path := range []string{"dealer.go", "txrescan.go", "payment.go"} {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for lineNumber, line := range strings.Split(string(source), "\n") {
			if !strings.Contains(line, `"`) {
				continue
			}
			if turkishDiacriticPattern.MatchString(line) {
				t.Fatalf("%s:%d still carries Turkish diacritic copy: %s", path, lineNumber+1, strings.TrimSpace(line))
			}
			if asciiTurkishPattern.MatchString(line) {
				t.Fatalf("%s:%d still carries ASCII-Turkish display copy: %s", path, lineNumber+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestInvoiceHandlerLabelsAreEnglishAndCheckoutCopyIsLocalized pins the buyer-flow scope
// after 004: the hosted checkout resolves its copy through the localization catalog in both
// supported locales, and no payer-facing string may fall back to the retired Turkish copy.
func TestInvoiceHandlerLabelsAreEnglishAndCheckoutCopyIsLocalized(t *testing.T) {
	source, err := os.ReadFile("payment.go")
	if err != nil {
		t.Fatalf("read payment.go: %v", err)
	}
	text := string(source)

	start := strings.Index(text, "// HandlePaymentInvoice renders")
	if start < 0 {
		t.Fatal("HandlePaymentInvoice not found in payment.go")
	}
	invoiceBranch := text[start:]

	if strings.Contains(invoiceBranch, "Fatura #") {
		t.Fatal("invoice handler still builds a Turkish invoice title")
	}
	if !strings.Contains(invoiceBranch, "Invoice #") {
		t.Fatal("invoice handler does not build an English invoice title")
	}
	for _, turkishLabel := range []string{"Tamamlandı", "İptal edildi", "Beklemede", "Başarısız", "Ödendi"} {
		if strings.Contains(invoiceBranch, turkishLabel) {
			t.Fatalf("invoice status labels still contain Turkish %q", turkishLabel)
		}
	}

	// The checkout resolves copy through the catalog: the retired Turkish fields, the
	// Turkish status text and the checkout cookie plumbing must be gone, while the
	// English source keys and the locale resolution stay in place.
	for _, retired := range []string{"TitleTR", "BodyTR", `"Ödendi"`, `"gateway_lang"`, "normalizeLanguage"} {
		if strings.Contains(text, retired) {
			t.Fatalf("payment.go still carries retired language plumbing %q", retired)
		}
	}
	for _, preserved := range []string{
		"state.Title = \"Paid\"",
		"state.Body = \"Payment received successfully.\"",
		"func buyerLocale(c fiber.Ctx) localization.Locale",
		"localization.LegacyCheckoutCookieName",
		"checkoutResultStatusLabel(locale localization.Locale",
	} {
		if !strings.Contains(text, preserved) {
			t.Fatalf("payment.go lost buyer locale token %q", preserved)
		}
	}
}
