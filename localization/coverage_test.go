package localization

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This test is the ZH-03 coverage gate: every key that templates, Go handlers or the
// browser ask for must exist in the zh-CN catalog, otherwise the Chinese UI silently
// shows English. It also writes the exact gap list to the evidence directory so an
// incomplete translation pass is a recorded, reviewable list instead of a guess.

var (
	// {{ t .Locale "…" }} / {{ t $.Locale "…" }} inside templates.
	templateKeyPattern = regexp.MustCompile(`(?:^|[^\w.])t\s+(?:\$?\.Locale|"(?:zh-CN|en)")\s+"((?:[^"\\]|\\.)*)"`)
	// portalText(c, "…") and localization.T(locale, "…") inside Go handlers.
	goKeyPattern = regexp.MustCompile(`(?:portalText\(\s*[A-Za-z_][A-Za-z0-9_.]*\s*,\s*|localization\.T\(\s*[A-Za-z_][A-Za-z0-9_.]*\s*,\s*)"((?:[^"\\]|\\.)*)"`)
	// portalT('…') / portalT("…") inside browser assets.
	jsKeyPattern = regexp.MustCompile(`portalT\(\s*(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)")`)
)

func unescapeKey(raw string) string {
	return strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\\`, `\`, `\n`, "\n", `\t`, "\t").Replace(raw)
}

// collectKeys scans the given files with pattern and returns the sorted distinct keys.
func collectKeys(t *testing.T, pattern *regexp.Regexp, files []string, group int) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(content), -1) {
			key := unescapeKey(match[group])
			if key != "" {
				seen[key] = true
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestCatalogCoversEveryRequestedKey(t *testing.T) {
	views, err := filepath.Glob(filepath.Join("..", "views", "**", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	// filepath.Glob is not recursive: walk instead.
	views = nil
	_ = filepath.Walk(filepath.Join("..", "views"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr == nil && !info.IsDir() && strings.HasSuffix(path, ".html") {
			views = append(views, path)
		}
		return nil
	})
	handlers, _ := filepath.Glob(filepath.Join("..", "api", "handlers", "*.go"))
	assets, _ := filepath.Glob(filepath.Join("..", "views", "assets", "*.js"))

	templateKeys := collectKeys(t, templateKeyPattern, views, 1)
	goKeys := collectKeys(t, goKeyPattern, handlers, 1)
	jsKeys := append(collectKeys(t, jsKeyPattern, assets, 1), collectKeys(t, jsKeyPattern, assets, 2)...)

	catalog := make(map[string]bool, len(catalogZH))
	for key := range catalogZH {
		catalog[key] = true
	}

	var missing []string
	report := func(source string, keys []string) {
		for _, key := range keys {
			if key != "" && !catalog[key] {
				missing = append(missing, source+": "+key)
			}
		}
	}
	report("template", templateKeys)
	report("go", goKeys)
	report("js", jsKeys)

	summary := strings.Join([]string{
		"templates: " + itoa(len(templateKeys)) + " keys",
		"go: " + itoa(len(goKeys)) + " keys",
		"js: " + itoa(len(jsKeys)) + " keys",
		"catalog: " + itoa(len(catalogZH)) + " entries",
		"missing: " + itoa(len(missing)),
	}, ", ")

	evidence := filepath.Join("..", ".cache", "agent-evidence", "003-portal-zh-en", "evidence", "zh03-coverage-report.txt")
	body := "coverage summary: " + summary + "\n\nmissing keys:\n" + strings.Join(missing, "\n") + "\n"
	if err := os.WriteFile(evidence, []byte(body), 0o644); err != nil {
		t.Logf("could not write coverage evidence: %v", err)
	}
	t.Logf("coverage summary: %s", summary)

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("catalog is missing %d requested keys (see %s):\n%s", len(missing), evidence, strings.Join(missing, "\n"))
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}
