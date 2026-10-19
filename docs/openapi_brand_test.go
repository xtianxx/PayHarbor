package docs

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"core/constants"

	"go.yaml.in/yaml/v3"
)

// A8: the published OpenAPI title is product surface. All four maintained copies
// (main.go's swag annotation, docs/docs.go SwaggerInfo, docs/swagger.json and
// docs/swagger.yaml) must carry the same PayHarbor brand, and the runtime product
// name must compose it.
func TestOpenAPIBrandTitleIsPayHarborEverywhere(t *testing.T) {
	const wantTitle = "PayHarbor API"

	if got := constants.PRODUCT_NAME; got+" API" != wantTitle {
		t.Fatalf("runtime product name %q does not compose the OpenAPI title %q", got, wantTitle)
	}

	raw, err := os.ReadFile("swagger.json")
	if err != nil {
		t.Fatalf("read swagger.json: %v", err)
	}
	var jsonSpec struct {
		Swagger string `json:"swagger"`
		Info    struct {
			Title string `json:"title"`
		} `json:"info"`
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(raw, &jsonSpec); err != nil {
		t.Fatalf("parse swagger.json: %v", err)
	}
	if jsonSpec.Info.Title != wantTitle {
		t.Fatalf("swagger.json info.title = %q, want %q", jsonSpec.Info.Title, wantTitle)
	}
	if jsonSpec.Swagger == "" || len(jsonSpec.Paths) == 0 {
		t.Fatal("swagger.json must stay a real OpenAPI document (version and paths present)")
	}

	yamlRaw, err := os.ReadFile("swagger.yaml")
	if err != nil {
		t.Fatalf("read swagger.yaml: %v", err)
	}
	var yamlSpec struct {
		Swagger string `yaml:"swagger"`
		Info    struct {
			Title string `yaml:"title"`
		} `yaml:"info"`
		Paths map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(yamlRaw, &yamlSpec); err != nil {
		t.Fatalf("parse swagger.yaml: %v", err)
	}
	if yamlSpec.Info.Title != wantTitle {
		t.Fatalf("swagger.yaml info.title = %q, want %q", yamlSpec.Info.Title, wantTitle)
	}
	if yamlSpec.Swagger == "" || len(yamlSpec.Paths) == 0 {
		t.Fatal("swagger.yaml must stay a real OpenAPI document (version and paths present)")
	}

	if SwaggerInfo.Title != wantTitle {
		t.Fatalf("SwaggerInfo.Title = %q, want %q", SwaggerInfo.Title, wantTitle)
	}
	if expanded := SwaggerInfo.ReadDoc(); !strings.Contains(expanded, `"title": "`+wantTitle+`"`) {
		t.Fatal("SwaggerInfo.ReadDoc() does not expand the PayHarbor title")
	}

	source, err := os.ReadFile("../main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	annotation := ""
	for _, line := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "// @title ") {
			annotation = strings.TrimSpace(strings.TrimPrefix(trimmed, "// @title "))
			break
		}
	}
	if annotation != wantTitle {
		t.Fatalf("main.go @title annotation = %q, want %q", annotation, wantTitle)
	}
}
