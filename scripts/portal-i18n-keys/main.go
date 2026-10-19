// Command portal-i18n-keys is the T105 extractor: it harvests every translation key that
// production code can request — Go calls (localization.T / portalText), template calls
// ({{ t .Locale "…" }}, {{ t $.Locale "…" }}, {{ t "zh-CN" "…" }}, {{ t "en" "…" }}) and
// browser calls (portalT('…')) — prints them with source locations, and reports which of them
// the zh catalog is missing. It is a development tool: no runtime dependency, stdlib only.
//
// Scope note: this reports *requested* keys. Copy that was never instrumented is invisible
// here, so a zero missing-list is not proof that every visible string is translated.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"core/localization"
)

type occurrence struct {
	source        string
	line          int
	literalLocale bool
}

var (
	templateKeyPattern        = regexp.MustCompile(`(?:^|[^\w.])t\s+\$?\.Locale\s+"((?:[^"\\]|\\.)*)"`)
	templateLiteralKeyPattern = regexp.MustCompile(`(?:^|[^\w.])t\s+"(?:zh-CN|en)"\s+"((?:[^"\\]|\\.)*)"`)
	jsKeyPattern              = regexp.MustCompile(`portalT\(\s*(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)")`)
)

func unescape(raw string) string {
	return strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\\`, `\`, `\n`, "\n", `\t`, "\t").Replace(raw)
}

// scanGo extracts keys from Go source with precise rules:
//   - only <localization import alias>.T(loc, key, args...) and the package's own
//     portalText(c, key, args...)
//   - only the SECOND argument may be the key; arguments after it are runtime values
//     (never keys, even when they are string literals)
//   - a selector from any other package (foo.T) and dynamic keys are ignored
func scanGo(path string, found map[string][]occurrence) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	localizationAlias := ""
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if importPath == "core/localization" || strings.HasSuffix(importPath, "/localization") {
			if spec.Name != nil {
				localizationAlias = spec.Name.Name
			} else {
				parts := strings.Split(importPath, "/")
				localizationAlias = parts[len(parts)-1]
			}
		}
	}
	record := func(lit *ast.BasicLit) {
		if lit == nil || lit.Kind != token.STRING {
			return
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || value == "" {
			return
		}
		found[value] = append(found[value], occurrence{source: path, line: fset.Position(lit.Pos()).Line})
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			ident, ok := fun.X.(*ast.Ident)
			if !ok || localizationAlias == "" || ident.Name != localizationAlias || fun.Sel.Name != "T" {
				return true
			}
		case *ast.Ident:
			if fun.Name != "portalText" {
				return true
			}
		default:
			return true
		}
		if lit, ok := call.Args[1].(*ast.BasicLit); ok {
			record(lit)
		}
		return true
	})
	return nil
}

func scanRegex(path string, pattern *regexp.Regexp, group int, literal bool, found map[string][]occurrence) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for i, line := range strings.Split(string(data), "\n") {
		for _, match := range pattern.FindAllStringSubmatch(line, -1) {
			value := unescape(match[group])
			if value == "" {
				continue
			}
			found[value] = append(found[value], occurrence{source: path, line: i + 1, literalLocale: literal})
		}
	}
	return nil
}

// collect walks the standard source locations under root and records every requested key.
// Read/parse failures are returned instead of being silently skipped.
func collect(root string, found map[string][]occurrence) []error {
	scanErrors := []error{}
	walk := func(dir string, fn func(path string) error) {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			scanErrors = append(scanErrors, fmt.Errorf("scan root %s: %w", base, err))
			return
		}
		_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				scanErrors = append(scanErrors, fmt.Errorf("walk %s: %w", path, err))
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if err := fn(path); err != nil {
				scanErrors = append(scanErrors, err)
			}
			return nil
		})
	}
	goFiles, templates, assets := 0, 0, 0
	walk("api", func(path string) error {
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		goFiles++
		return scanGo(path, found)
	})
	walk("views", func(path string) error {
		if strings.HasSuffix(path, ".html") {
			templates++
			if err := scanRegex(path, templateKeyPattern, 1, false, found); err != nil {
				return err
			}
			return scanRegex(path, templateLiteralKeyPattern, 1, true, found)
		}
		if strings.HasSuffix(path, ".js") {
			assets++
			if err := scanRegex(path, jsKeyPattern, 1, false, found); err != nil {
				return err
			}
			return scanRegex(path, jsKeyPattern, 2, false, found)
		}
		return nil
	})
	fmt.Printf("portal-i18n-keys: scanned %d Go files, %d templates, %d assets\n", goFiles, templates, assets)
	return scanErrors
}

func main() {
	root := flag.String("root", ".", "repository root")
	showAll := flag.Bool("all", false, "print every occurrence, not just the summary")
	reportOnly := flag.Bool("report", false, "report missing keys without failing (gate disabled)")
	flag.Parse()
	exitCode := 0

	found := map[string][]occurrence{}
	scanErrors := collect(*root, found)
	keys := make([]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	if len(scanErrors) > 0 {
		fmt.Fprintf(os.Stderr, "portal-i18n-keys: %d file(s) could not be read/parsed:\n", len(scanErrors))
		for _, err := range scanErrors {
			fmt.Fprintf(os.Stderr, "  %v\n", err)
		}
		os.Exit(2)
	}

	catalog := map[string]bool{}
	for _, key := range localization.Keys() {
		catalog[key] = true
	}
	missing := []string{}
	literalLocaleKeys := 0
	for _, key := range keys {
		if !catalog[key] {
			missing = append(missing, key)
		}
		for _, site := range found[key] {
			if site.literalLocale {
				literalLocaleKeys++
			}
		}
	}

	fmt.Printf("requested keys: %d (literal-locale occurrences: %d)\n", len(keys), literalLocaleKeys)
	if *showAll {
		for _, key := range keys {
			for _, site := range found[key] {
				locale := ""
				if site.literalLocale {
					locale = " [literal-locale]"
				}
				fmt.Printf("  %s:%d\t%s%s\n", site.source, site.line, key, locale)
			}
		}
	}
	if len(missing) == 0 {
		fmt.Println("catalog missing: 0")
	} else {
		exitCode = 1
		fmt.Printf("catalog missing: %d\n", len(missing))
		for _, key := range missing {
			fmt.Printf("  MISSING %q (first seen %s:%d)\n", key, found[key][0].source, found[key][0].line)
		}
	}
	if *reportOnly {
		exitCode = 0
	}
	os.Exit(exitCode)
}
