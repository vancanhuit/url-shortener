package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBodyLimitMiddleware exercises bodyLimitMiddleware directly so the
// 413 path is covered without standing up the full integration stack.
// Also pins the contract that the cap is configurable (the production
// wiring now reads it from config.MaxRequestBodyBytes), so a regression
// that hard-codes the cap again would break this test.
func TestBodyLimitMiddleware(t *testing.T) {
	t.Parallel()

	const limit = 32
	// Inner handler that fully drains the body so MaxBytesReader gets a
	// chance to trip when the body exceeds the cap.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := bodyLimitMiddleware(limit)(inner)

	cases := []struct {
		name     string
		bodySize int
		want     int
	}{
		{"under cap", limit - 1, http.StatusNoContent},
		{"at cap", limit, http.StatusNoContent},
		{"over cap", limit + 1, http.StatusRequestEntityTooLarge},
		{"far over cap", limit * 4, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(strings.Repeat("x", tc.bodySize))))
			req.Header.Set("Content-Type", "application/octet-stream")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
