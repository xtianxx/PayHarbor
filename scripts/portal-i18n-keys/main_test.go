package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TEST-02: the extractor's rules, with synthetic sources in a temporary tree.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ensureRoots creates the two standard scan roots so partial fixtures stay valid.
func ensureRoots(t *testing.T, dir string) string {
	t.Helper()
	for _, sub := range []string{"api", "views"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func keysOf(t *testing.T, root string) map[string]bool {
	t.Helper()
	found := map[string][]occurrence{}
	if errs := collect(root, found); len(errs) > 0 {
		t.Fatalf("collect reported errors: %v", errs)
	}
	out := map[string]bool{}
	for key := range found {
		out[key] = true
	}
	return out
}

func TestScanGoTakesOnlyTheSecondLiteralArgument(t *testing.T) {
	dir := ensureRoots(t, t.TempDir())
	writeFile(t, dir, "api/x.go", `package x

import loc "core/localization"
import "strconv"

func f(c any) {
	loc.T(loc.ZHCN, "KeyOne")
	loc.T("zh-CN", "KeyTwo")
	loc.T(loc.ZHCN, "KeyThree", "USER_ARG", 42)
	portalText(c, "KeyFour", "ANOTHER_USER_ARG")
	dynamicLabel := "NotAKey"
	loc.T(loc.ZHCN, dynamicLabel)
	strconv.Itoa(1)
}

func portalText(c any, key string, args ...any) string { return key }
`)
	writeFile(t, dir, "api/skip_test.go", `package x

import loc "core/localization"

func helper() { loc.T(loc.ZHCN, "KeyFromTestFile") }
`)
	keys := keysOf(t, dir)
	for _, want := range []string{"KeyOne", "KeyTwo", "KeyThree", "KeyFour"} {
		if !keys[want] {
			t.Errorf("missing key %q", want)
		}
	}
	for _, forbidden := range []string{"USER_ARG", "ANOTHER_USER_ARG", "NotAKey", "KeyFromTestFile"} {
		if keys[forbidden] {
			t.Errorf("key %q must never be extracted", forbidden)
		}
	}
	if len(keys) != 4 {
		t.Errorf("extracted %d keys, want 4: %v", len(keys), keys)
	}
}

func TestScanGoIgnoresForeignSelectors(t *testing.T) {
	dir := ensureRoots(t, t.TempDir())
	writeFile(t, dir, "api/y.go", `package x

import other "some/other/pkg"

func f() { other.T("ForeignKey") }
`)
	keys := keysOf(t, dir)
	if len(keys) != 0 {
		t.Errorf("foreign selector extracted keys: %v", keys)
	}
}

func TestScanTemplatesAndAssetsHandleEscapesAndMultilineKeys(t *testing.T) {
	dir := ensureRoots(t, t.TempDir())
	writeFile(t, dir, "views/t.html", `<h1>{{ t .Locale "Plain key" }}</h1>
<p>{{ t $.Locale "Quoted \"key\" with \\ backslash" }}</p>
<span>{{ t "zh-CN" "Literal locale key" }}</span>
<em>{{ t "en" "Another literal key" }}</em>
<b>{{ printf "%s" (t .Locale "Key with %s and %d verbs") }}</b>
`)
	writeFile(t, dir, "views/assets/a.js", `portalT('Single quoted key');
portalT("Double quoted key");
var notAKey = "plain string";
`)
	keys := keysOf(t, dir)
	for _, want := range []string{
		"Plain key", `Quoted "key" with \ backslash`, "Literal locale key", "Another literal key",
		"Key with %s and %d verbs", "Single quoted key", "Double quoted key",
	} {
		if !keys[want] {
			t.Errorf("missing key %q (have %v)", want, keys)
		}
	}
	if keys["plain string"] {
		t.Error("a plain JS string literal was treated as a key")
	}
}

func TestCollectReportsUnparsableSources(t *testing.T) {
	dir := ensureRoots(t, t.TempDir())
	writeFile(t, dir, "api/broken.go", "package x\n\nfunc broken( {\n")
	found := map[string][]occurrence{}
	errs := collect(dir, found)
	if len(errs) == 0 {
		t.Fatal("an unparsable Go file must be reported as an error")
	}
}

func TestCollectReportsMissingRoots(t *testing.T) {
	dir := t.TempDir()
	found := map[string][]occurrence{}
	errs := collect(dir, found)
	if len(errs) < 2 {
		t.Fatalf("missing scan roots must be reported, got %v", errs)
	}
}
