package localization

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// TEST-02 — T502: hard-coded Han characters are allowed only inside the localization
// package. Go sources are scanned through go/parser (so comments are ignored by
// construction and real line numbers are reported); templates and browser assets use a
// minimal string-preserving scanner so a "//" inside a URL or a "/*" inside a quoted
// literal is never mistaken for a comment. The frozen checkout surface is out of scope.

func containsHan(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// scanGoForHan reports the first string literal containing Han in a Go file (comments are
// never inspected because only ast.BasicLit values are decoded).
func scanGoForHan(path string) (int, string, bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return 0, "", false, err
	}
	found, text, line := false, "", 0
	ast.Inspect(file, func(node ast.Node) bool {
		if found {
			return false
		}
		lit, ok := node.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || !containsHan(value) {
			return true
		}
		found, text, line = true, value, fset.Position(lit.Pos()).Line
		return false
	})
	if !found {
		return 0, "", false, nil
	}
	return line, text, true, nil
}

// scanTextForHan walks templates/JS while tracking string literals and comments, so Han is
// detected in quoted copy (including URLs) but never inside comments.
func scanTextForHan(text string, lineComment, blockOpen, blockClose string, htmlComments bool) string {
	runes := []rune(text)
	inString, inComment, inLineComment, inHTMLComment := false, false, false, false
	var quote rune
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inHTMLComment {
			if strings.HasPrefix(string(runes[i:]), "-->") {
				inHTMLComment, i = false, i+2
			}
			continue
		}
		if inLineComment {
			// a "//" comment ends at the newline; only block comments use the closing token
			if r == '\n' {
				inLineComment = false
			}
			continue
		}
		if inComment {
			if blockClose != "" && strings.HasPrefix(string(runes[i:]), blockClose) {
				inComment, i = false, i+len(blockClose)-1
			}
			continue
		}
		if inString {
			if containsHan(string(r)) {
				return string(r)
			}
			if r == '\\' { // skip escaped character
				i++
				continue
			}
			if r == quote {
				inString = false
			}
			continue
		}
		if htmlComments && strings.HasPrefix(string(runes[i:]), "<!--") {
			inHTMLComment, i = true, i+3
			continue
		}
		if lineComment != "" && strings.HasPrefix(string(runes[i:]), lineComment) {
			inLineComment, i = true, i+len(lineComment)-1
			continue
		}
		if blockOpen != "" && strings.HasPrefix(string(runes[i:]), blockOpen) {
			inComment, i = true, i+len(blockOpen)-1
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			inString, quote = true, r
			continue
		}
		if containsHan(string(r)) {
			return string(r)
		}
	}
	return ""
}

func scanFileForHan(t *testing.T, path string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("required source %s could not be read: %v", path, err)
		return "", false
	}
	switch {
	case strings.HasSuffix(path, ".go"):
		_, text, found, err := scanGoForHan(path)
		if err != nil {
			t.Errorf("required source %s could not be parsed: %v", path, err)
			return "", false
		}
		return text, found
	case strings.HasSuffix(path, ".html"):
		if value := scanTextForHan(string(raw), "", "{{/*", "*/}}", true); value != "" {
			return value, true
		}
		return "", false
	case strings.HasSuffix(path, ".js"):
		if value := scanTextForHan(string(raw), "//", "/*", "*/", false); value != "" {
			return value, true
		}
		return "", false
	}
	return "", false
}

// requiredHanScope lists the files that MUST exist; a missing file fails the test instead of
// silently shrinking the scan.
func requiredHanScope(t *testing.T) []string {
	t.Helper()
	required := []string{"../views/invoice.html", "../views/assets/dashboard.js", "../views/assets/portal-i18n.js", "../main.go"}
	var optional []string
	for _, root := range []string{"../api", "../helpers"} {
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("scan root %s is missing", root)
		}
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				t.Errorf("walk %s: %v", path, err)
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				optional = append(optional, path)
			}
			return nil
		})
	}
	if _, err := os.Stat("../views/dealer"); err != nil {
		t.Fatalf("dealer template directory is missing")
	}
	_ = filepath.Walk("../views/dealer", func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".html") {
			optional = append(optional, path)
		}
		return nil
	})
	return append(required, optional...)
}

func TestProductionSourcesKeepHanInTheCatalog(t *testing.T) {
	scope := requiredHanScope(t)
	if len(scope) < 20 {
		t.Fatalf("scan scope shrank to %d files", len(scope))
	}
	scopeCount := 0
	for _, file := range scope {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("required file %s is missing: %v", file, err)
		}
		scopeCount++
		if value, found := scanFileForHan(t, file); found {
			t.Errorf("%s contains hard-coded Han text (%q); route it through the catalog", file, value)
		}
	}
	if scopeCount != len(scope) {
		t.Fatalf("scanned %d of %d files", scopeCount, len(scope))
	}
	// the catalog is the only allowed home for Han — prove the scanner detects it there
	if value, found := scanFileForHan(t, "catalog_zh.go"); !found {
		t.Errorf("the catalog was not detected as containing Han (scanner broken), got %q", value)
	}
}

func TestURLWithHanIsDetected(t *testing.T) {
	dir := t.TempDir()
	js := filepath.Join(dir, "x.js")
	if err := os.WriteFile(js, []byte("var u = \"https://例子.com/path\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, found := scanFileForHan(t, js); !found {
		t.Error("a URL string containing Han must be detected (the scanner must not treat // as a comment)")
	}
	goFile := filepath.Join(dir, "x.go")
	if err := os.WriteFile(goFile, []byte("package x\n\nvar u = \"https://例子.com\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, text, found, err := scanGoForHan(goFile); err != nil || !found || !strings.Contains(text, "例子") {
		t.Errorf("Go URL literal with Han not detected: found=%v text=%q err=%v", found, text, err)
	}
}

func TestHanCommentNotCopy(t *testing.T) {
	dir := t.TempDir()
	js := filepath.Join(dir, "y.js")
	if err := os.WriteFile(js, []byte("var a = 1; // 中文注释\n/* 中文块注释 */\nvar b = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, found := scanFileForHan(t, js); found {
		t.Error("Han inside JS comments must not be reported as copy")
	}
	goFile := filepath.Join(dir, "y.go")
	if err := os.WriteFile(goFile, []byte("package x\n\n// 中文注释\n/* 中文块注释 */\nvar a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := scanGoForHan(goFile); err != nil || found {
		t.Errorf("Han inside Go comments must not be reported: found=%v err=%v", found, err)
	}
	htmlFile := filepath.Join(dir, "y.html")
	if err := os.WriteFile(htmlFile, []byte("<!-- 中文注释 -->\n{{/* 中文动作注释 */}}\n<p>plain</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, found := scanFileForHan(t, htmlFile); found {
		t.Error("Han inside template/HTML comments must not be reported as copy")
	}
}

func TestLineCommentDoesNotHideFollowingCopy(t *testing.T) {
	dir := t.TempDir()
	js := filepath.Join(dir, "line.js")
	if err := os.WriteFile(js, []byte("// comment\nvar s = \"汉字\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, found := scanFileForHan(t, js); !found {
		t.Error("a // comment must not hide the Han copy that follows it")
	}
	htmlFile := filepath.Join(dir, "url.html")
	if err := os.WriteFile(htmlFile, []byte("<p>see http://例子.example/path for details</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, found := scanFileForHan(t, htmlFile); !found {
		t.Error("an unquoted URL containing Han must be detected in HTML")
	}
	actionFile := filepath.Join(dir, "action.html")
	if err := os.WriteFile(actionFile, []byte("{{/* comment */}}\n<p>汉字 copy</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, found := scanFileForHan(t, actionFile); !found {
		t.Error("Han after a template action comment must be detected")
	}
}

func TestGoScannerReportsTheRealLine(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "line.go")
	if err := os.WriteFile(file, []byte("package x\n\n// comment\nvar a = \"ok\"\n\nvar b = \"汉字\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	line, text, found, err := scanGoForHan(file)
	if err != nil || !found {
		t.Fatalf("scanGoForHan = (%d, %q, %v, %v)", line, text, found, err)
	}
	if line != 6 {
		t.Errorf("reported line %d, want 6 (the real position of the literal)", line)
	}
}
