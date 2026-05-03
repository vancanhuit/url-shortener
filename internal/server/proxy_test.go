package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBuildIPExtractor exercises the pure CIDR-matching logic so the
// proxy-aware path is covered without needing a real Postgres + Redis
// (i.e. without the `integration` build tag).
//
// The IPExtractor returned by buildIPExtractor takes an *http.Request and
// walks X-Forwarded-For only if the immediate RemoteAddr peer falls
// inside one of the trusted CIDR ranges. The matrix below verifies the
// four interesting combinations: trusted+XFF, trusted+no-XFF,
// untrusted+XFF (must ignore XFF), and the empty-list base case (no
// extractor returned).
func TestBuildIPExtractor(t *testing.T) {
	t.Parallel()

	t.Run("empty list returns nil", func(t *testing.T) {
		t.Parallel()
		if got := buildIPExtractor(nil); got != nil {
			t.Errorf("buildIPExtractor(nil) = non-nil, want nil")
		}
		if got := buildIPExtractor([]string{}); got != nil {
			t.Errorf("buildIPExtractor([]string{}) = non-nil, want nil")
		}
		if got := buildIPExtractor([]string{""}); got != nil {
			t.Errorf("buildIPExtractor([]) of empty strings = non-nil, want nil")
		}
	})

	t.Run("invalid CIDR is silently dropped", func(t *testing.T) {
		t.Parallel()
		// Valid + invalid mix: extractor still installed, only valid
		// entry is honored. Defense in depth -- config.Validate already
		// guards against bad CIDRs reaching here.
		ext := buildIPExtractor([]string{"not-a-cidr", "127.0.0.1/32"})
		if ext == nil {
			t.Fatal("extractor = nil, want non-nil with one valid entry")
		}
	})

	t.Run("XFF honored iff RemoteAddr in trusted CIDR", func(t *testing.T) {
		t.Parallel()
		ext := buildIPExtractor([]string{"127.0.0.0/8", "10.0.0.0/8"})
		if ext == nil {
			t.Fatal("extractor = nil, want non-nil")
		}

		cases := []struct {
			name       string
			remoteAddr string
			xff        string
			want       string
		}{
			{
				name:       "trusted peer + XFF -> client from XFF",
				remoteAddr: "127.0.0.1:54321",
				xff:        "203.0.113.42",
				want:       "203.0.113.42",
			},
			{
				name:       "trusted peer + multi-hop XFF -> first untrusted from left",
				remoteAddr: "10.0.0.5:54321",
				xff:        "203.0.113.42, 10.0.0.7",
				want:       "203.0.113.42",
			},
			{
				name:       "trusted peer + no XFF -> RemoteAddr",
				remoteAddr: "127.0.0.1:54321",
				xff:        "",
				want:       "127.0.0.1",
			},
			{
				name:       "untrusted peer + spoofed XFF -> RemoteAddr (XFF ignored)",
				remoteAddr: "198.51.100.7:54321",
				xff:        "203.0.113.42",
				want:       "198.51.100.7",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.RemoteAddr = tc.remoteAddr
				if tc.xff != "" {
					req.Header.Set("X-Forwarded-For", tc.xff)
				}
				got := ext(req)
				if got != tc.want {
					t.Errorf("extractor(remote=%q, xff=%q) = %q, want %q",
						tc.remoteAddr, tc.xff, got, tc.want)
				}
			})
		}
	})
}
