// In-package test for unexported helpers. Kept separate from the
// external `handlers_test` package so the rest of the suite continues
// to exercise the public API surface.
package handlers

import "testing"

func TestNormalizeURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Identity cases -- these already match the canonical form.
		{"already canonical", "https://example.com/foo", "https://example.com/foo"},
		{
			"path with query and fragment preserved",
			"https://example.com/foo?a=1#frag",
			"https://example.com/foo?a=1#frag",
		},

		// Scheme + host lowercasing.
		{"uppercase scheme", "HTTP://example.com/foo", "http://example.com/foo"},
		{"mixed-case host", "https://Example.COM/foo", "https://example.com/foo"},
		{
			"both",
			"HTTPS://EXAMPLE.com/Path",
			"https://example.com/Path",
		},

		// Default-port stripping. The path case in the URL is
		// preserved (host is case-insensitive, path is not).
		{"http :80 stripped", "http://example.com:80/foo", "http://example.com/foo"},
		{"https :443 stripped", "https://example.com:443/foo", "https://example.com/foo"},

		// Non-default ports must be left intact -- this is the
		// regression guard for the suffix-match comment.
		{"http :8080 preserved", "http://example.com:8080/foo", "http://example.com:8080/foo"},
		{"https :443-lookalike port preserved", "https://example.com:8443/foo", "https://example.com:8443/foo"},
		{"http :443 not stripped (wrong scheme)", "http://example.com:443/foo", "http://example.com:443/foo"},
		{"https :80 not stripped (wrong scheme)", "https://example.com:80/foo", "https://example.com:80/foo"},

		// Path normalisation.
		{"bare slash dropped", "https://example.com/", "https://example.com"},
		{"non-bare slash preserved", "https://example.com/foo/", "https://example.com/foo/"},

		// Things we deliberately do NOT normalise.
		{"path case preserved", "https://example.com/Foo", "https://example.com/Foo"},
		{"percent-encoding case preserved", "https://example.com/%2A", "https://example.com/%2A"},
		{"trailing dot preserved", "https://example.com./foo", "https://example.com./foo"},
		{
			"query string preserved verbatim",
			"https://example.com/foo?b=2&a=1",
			"https://example.com/foo?b=2&a=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeURL(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("normalizeURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
