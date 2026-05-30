package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vancanhuit/url-shortener/internal/handlers"
)

// newChiRouter returns a Chi router with the operational handlers mounted.
func newChiRouter(checks map[string]func(ctx context.Context) error) chi.Router {
	r := chi.NewRouter()
	op := handlers.NewOperational()
	for name, fn := range checks {
		op.AddReadinessCheck(name, fn)
	}
	op.Mount(r)
	return r
}

// do issues a GET to path and returns status + decoded JSON body.
func do(t *testing.T, h http.Handler, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
	}
	return rec.Code, body
}

func TestLivez_AlwaysOK(t *testing.T) {
	t.Parallel()

	e := newChiRouter(nil)
	code, body := do(t, e, "/livez")
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want \"ok\"", body["status"])
	}
}

func TestVersion_ReturnsBuildInfo(t *testing.T) {
	t.Parallel()

	e := newChiRouter(nil)
	code, body := do(t, e, "/version")
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
	for _, key := range []string{"version", "commit", "date"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing %q in response %v", key, body)
		}
	}
}

func TestReadyz_NoChecksIsOK(t *testing.T) {
	t.Parallel()

	e := newChiRouter(nil)
	code, body := do(t, e, "/readyz")
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want \"ok\"", body["status"])
	}
}

func TestReadyz_AllChecksPass(t *testing.T) {
	t.Parallel()

	e := newChiRouter(map[string]func(ctx context.Context) error{
		"db":    func(_ context.Context) error { return nil },
		"cache": func(_ context.Context) error { return nil },
	})
	code, body := do(t, e, "/readyz")
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
	if body["db"] != "ok" || body["cache"] != "ok" {
		t.Errorf("expected all checks ok, got %v", body)
	}
}

func TestReadyz_FailingCheckReturns503(t *testing.T) {
	t.Parallel()

	e := newChiRouter(map[string]func(ctx context.Context) error{
		"db":    func(_ context.Context) error { return nil },
		"cache": func(_ context.Context) error { return errors.New("connection refused") },
	})
	code, body := do(t, e, "/readyz")
	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", code)
	}
	if body["db"] != "ok" {
		t.Errorf("db = %v, want ok", body["db"])
	}
	cache, _ := body["cache"].(string)
	if !strings.HasPrefix(cache, "error:") {
		t.Errorf("cache = %q, want \"error: ...\" prefix", cache)
	}
	if body["status"] != "unready" {
		t.Errorf("status = %v, want \"unready\"", body["status"])
	}
}
