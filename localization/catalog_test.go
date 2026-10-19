package localization

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// TestKeysReturnsCopy pins the public API: callers get sorted copies and cannot mutate
// the process-wide catalog through them.
func TestKeysReturnsCopy(t *testing.T) {
	first, second := Keys(), Keys()
	if len(first) == 0 {
		t.Fatal("Keys() returned nothing")
	}
	if !slices.Equal(first, second) {
		t.Fatalf("Keys() is not stable: %v vs %v", first, second)
	}
	if !sort.StringsAreSorted(first) {
		t.Fatalf("Keys() must be sorted: %v", first)
	}
	first[0] = "MUTATED"
	if Keys()[0] == "MUTATED" {
		t.Fatal("Keys() exposes the internal catalog map")
	}
}

// TestJSKeysReturnsCopyAndIsCoveredByCatalog keeps the browser contract honest: every
// key shipped to the browser must have a zh-CN rendering, otherwise the Chinese UI would
// silently show English.
func TestJSKeysReturnsCopyAndIsCoveredByCatalog(t *testing.T) {
	keys := JSKeys()
	if len(keys) == 0 {
		t.Fatal("JSKeys() returned nothing")
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("JSKeys() must be sorted: %v", keys)
	}
	keys[0] = "MUTATED"
	if JSKeys()[0] == "MUTATED" {
		t.Fatal("JSKeys() exposes the internal key slice")
	}

	catalog := make(map[string]bool, len(catalogZH))
	for key := range catalogZH {
		catalog[key] = true
	}
	for _, key := range JSKeys() {
		if !catalog[key] {
			t.Errorf("JS key %q has no zh-CN entry", key)
		}
		if got := T(ZHCN, key); got == key {
			t.Errorf("JS key %q falls back to English for zh-CN", key)
		}
	}
}

var verbPattern = regexp.MustCompile(`%(\[[0-9]+\])?([a-zA-Z%])`)

// verbs lists the format verbs of a catalog string in order. "%%" is a literal percent
// sign, not a verb. Explicit argument indexes are accepted by Go but are not used by this
// catalog; if one ever appears, only the verb letters are compared.
func verbs(text string) []string {
	var out []string
	for _, match := range verbPattern.FindAllStringSubmatch(text, -1) {
		if match[2] == "%" {
			continue
		}
		out = append(out, match[2])
	}
	return out
}

// TestCatalogVerbParity guards the placeholders: a translation must keep the same verbs
// in the same order as its English key, because T formats the translation with the same
// argument list.
func TestCatalogVerbParity(t *testing.T) {
	for key, text := range catalogZH {
		want, got := verbs(key), verbs(text)
		if !slices.Equal(want, got) {
			t.Errorf("key %q has verbs %v but its zh-CN text %q has %v", key, want, text, got)
		}
	}
}

// TestCatalogConcurrentReadersRunRaceFree exercises the whole read surface in parallel;
// with -race it proves no reader mutates shared state.
func TestCatalogConcurrentReadersRunRaceFree(t *testing.T) {
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				for _, key := range Keys() {
					if got := lookupText(ZHCN, key); got == "" {
						t.Errorf("empty translation for %q", key)
					}
				}
			}
		}()
	}
	wg.Wait()
}

// TestCatalogSurfacesHaveNoConflictingDuplicates enforces what catalog_zh.go documents: the
// same English key may appear in several surface maps, but every copy must carry the same
// translation, otherwise the merged runtime value depends on file order.
func TestCatalogSurfacesHaveNoConflictingDuplicates(t *testing.T) {
	surfaces := map[string]map[string]string{
		"coreZH": coreZH, "shellZH": shellZH, "dashboardZH": dashboardZH, "adminZH": adminZH,
		"invoiceZH": invoiceZH, "handlersZH": handlersZH, "jsZH": jsZH,
	}
	values := map[string]map[string]string{}
	for name, entries := range surfaces {
		for key, value := range entries {
			if values[key] == nil {
				values[key] = map[string]string{}
			}
			values[key][name] = value
		}
	}
	conflicts := 0
	for key, perSurface := range values {
		first := ""
		for _, value := range perSurface {
			if first == "" {
				first = value
				continue
			}
			if value != first {
				conflicts++
				t.Errorf("key %q has conflicting translations: %v (merged runtime value comes from the last surface in mergeCatalogs order)", key, perSurface)
				break
			}
		}
	}
	if conflicts > 0 {
		t.Logf("%d conflicting duplicate keys must be synchronised to the merged canonical value", conflicts)
	}
}

// TestCatalogRespectsApprovedGlossary pins the supervisor-approved terminology.
func TestCatalogRespectsApprovedGlossary(t *testing.T) {
	required := map[string]string{
		"merchant": "商户", "Merchants": "商户", "Deposit": "入账", "Deposits": "入账",
		"Top up": "充值", "Top-up": "充值", "Sweep": "归集", "Sweeps": "归集",
	}
	for key, want := range required {
		if got := catalogZH[key]; got != want {
			t.Errorf("catalog[%q] = %q, want the approved %q", key, got, want)
		}
	}
	for _, key := range []string{
		"Account was created but the reserve wallet could not be prepared. Please try again.",
		"Merchant reserve wallet could not be prepared: %s",
		"Reserve wallets could not be read: %s",
		"Sign-in completed but the reserve wallet could not be prepared. Please try again.",
		"Withdrawals can only be made from the merchant reserve wallet.",
	} {
		if !strings.Contains(catalogZH[key], "储备钱包") {
			t.Errorf("reserve-wallet key %q = %q, want 储备钱包", key, catalogZH[key])
		}
	}
	// the three credential terms must stay distinct
	apiKey, apiSecret, webhookSecret := catalogZH["API key"], catalogZH["API secret"], catalogZH["Webhook signing secret"]
	if apiKey == apiSecret || apiSecret == webhookSecret || apiKey == webhookSecret {
		t.Errorf("credential terms collapsed: key=%q secret=%q webhook=%q", apiKey, apiSecret, webhookSecret)
	}
	for _, value := range []string{apiKey, apiSecret, webhookSecret} {
		if value == "" || !strings.Contains(value, "密钥") {
			t.Errorf("credential term %q is missing 密钥", value)
		}
	}
}
