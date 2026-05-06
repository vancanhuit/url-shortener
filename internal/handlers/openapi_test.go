package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/vancanhuit/url-shortener/internal/handlers"
)

// TestMountOpenAPI_ServesJSONAndYAML covers both meta endpoints in
// one test: each MIME type goes through the same Echo wiring, and
// running them together is faster than spinning up two servers.
//
// The assertions are deliberately loose on body contents -- the
// embed package's tests cover structural invariants -- and tight on
// MIME / status / non-emptiness, since those are what the HTTP
// surface actually promises clients.
func TestMountOpenAPI_ServesJSONAndYAML(t *testing.T) {
	t.Parallel()
	e := echo.New()
	handlers.MountOpenAPI(e)

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		ct := rec.Header().Get(echo.HeaderContentType)
		if !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json...", ct)
		}
		body, err := io.ReadAll(rec.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		// Round-tripping through json.Unmarshal is the cheapest
		// JSON validity check we have; a 4xx would have already
		// fired above if the embed produced garbage at init.
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		if doc["openapi"] != "3.1.0" {
			t.Errorf("openapi = %v, want 3.1.0", doc["openapi"])
		}
	})

	t.Run("yaml", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		ct := rec.Header().Get(echo.HeaderContentType)
		if !strings.Contains(ct, "application/yaml") {
			t.Errorf("Content-Type = %q, want application/yaml...", ct)
		}
		body, err := io.ReadAll(rec.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		// Sanity check: the YAML source has to declare
		// `openapi: 3.1.x` somewhere. The check skips a head
		// window because the spec leads with a substantial
		// comment block; matching anywhere in the document is
		// enough to prove we served the right file.
		if !strings.Contains(string(body), "\nopenapi: 3.1") {
			t.Errorf("body does not contain an `openapi: 3.1` pragma at column 0")
		}
	})
}
