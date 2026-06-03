package handlers_test

import (
	"net/http"
	"testing"

	openapi "github.com/vancanhuit/url-shortener/api"
)

// TestCreate_RejectsSSRFTargetsViaAPI exercises the full JSON create path
// (POST /api/v1/links) to prove that server-side request forgery targets are
// rejected with 422 validation_failed before any row is written. Unit tests in
// normalize_internal_test.go already cover validateTargetURL / isPrivateHost in
// isolation; this test pins the behavior at the API boundary so a future
// refactor of the handler wiring can't silently let an SSRF target through.
func TestCreate_RejectsSSRFTargetsViaAPI(t *testing.T) {
	t.Parallel()
	targets := []struct {
		name, url string
	}{
		{"ipv4_loopback", "http://127.0.0.1/admin"},
		{"ipv4_loopback_alt", "http://127.0.0.2/"},
		{"ipv6_loopback", "http://[::1]/"},
		{"aws_imds_link_local", "http://169.254.169.254/latest/meta-data/"},
		{"link_local", "http://169.254.0.1/"},
		{"rfc1918_10", "http://10.0.0.1/"},
		{"rfc1918_172", "http://172.16.0.1/"},
		{"rfc1918_192", "http://192.168.1.1/"},
		{"carrier_grade_nat", "http://100.64.0.1/"},
		{"localhost_hostname", "http://localhost/"},
	}
	for _, tt := range targets {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st, cc := newFakeStore(), newFakeCache()
			e, _ := newHandlerWithCache(t, st, cc, &scriptedGen{codes: []string{"unused0"}})

			body := `{"target_url":"` + tt.url + `"}`
			rec, out := doJSON(t, e, http.MethodPost, "/api/v1/links", body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusUnprocessableEntity, out)
			}
			if got := decodeError(t, out).Code; got != openapi.ErrorResponseCodeValidationFailed {
				t.Errorf("error code = %q, want %q (body=%s)", got, openapi.ErrorResponseCodeValidationFailed, out)
			}

			st.mu.Lock()
			n := len(st.links)
			st.mu.Unlock()
			if n != 0 {
				t.Errorf("store has %d link(s) after rejected SSRF target; want 0", n)
			}
		})
	}
}
