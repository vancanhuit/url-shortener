package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	openapi "github.com/vancanhuit/url-shortener/api"
)

// TestSpec_NotEmpty is a smoke test: the //go:embed directive silently
// embeds an empty byte slice if the target path doesn't exist, so an
// empty Spec means the file moved without the embed tag being updated.
func TestSpec_NotEmpty(t *testing.T) {
	t.Parallel()
	if len(openapi.Spec) == 0 {
		t.Fatal("openapi.Spec is empty -- did api/openapi.yaml move?")
	}
	// Cheap structural check: the OpenAPI version pragma has to be
	// declared at column 0 somewhere in the document. Verifying it
	// here keeps us honest if someone embeds the wrong file by
	// accident. The check is anchored on a leading newline rather
	// than the first N bytes because the spec leads with a comment
	// block.
	if !strings.Contains(string(openapi.Spec), "\nopenapi: 3.0") {
		t.Errorf("Spec does not contain an `openapi: 3.0` pragma at column 0")
	}
}

// TestSpecJSON_IsValidJSONAndPreservesStructure proves that the
// init-time YAML->JSON conversion succeeded and that the result is
// usable from Go (i.e. the keys we expect to be there *are* there).
// The test goes via json.Unmarshal rather than string-matching so
// it doesn't break under harmless whitespace or key-ordering changes.
func TestSpecJSON_IsValidJSONAndPreservesStructure(t *testing.T) {
	t.Parallel()
	if len(openapi.SpecJSON) == 0 {
		t.Fatal("openapi.SpecJSON is empty")
	}
	var doc map[string]any
	if err := json.Unmarshal(openapi.SpecJSON, &doc); err != nil {
		t.Fatalf("SpecJSON is not valid JSON: %v", err)
	}
	if v, _ := doc["openapi"].(string); v != "3.0.3" {
		t.Errorf("openapi version = %q, want 3.0.3", v)
	}
	paths, _ := doc["paths"].(map[string]any)
	if paths == nil {
		t.Fatal("paths missing from spec")
	}
	// Every endpoint we care about for this PR has to be present;
	// missing one almost certainly means the spec drifted out of
	// sync with the code.
	for _, p := range []string{
		"/api/v1/links",
		"/api/v1/links/{code}",
		"/r/{code}",
		"/api/v1/openapi.json",
		"/healthz",
		"/readyz",
		"/version",
	} {
		if _, ok := paths[p]; !ok {
			t.Errorf("paths missing %q", p)
		}
	}
}
