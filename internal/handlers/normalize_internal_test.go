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

func TestIsPrivateHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host    string
		private bool
	}{
		// Public IPs — must pass
		{"93.184.216.34", false},                          // example.com
		{"2606:2800:21f:cb07:6820:80da:af6b:8b2c", false}, // example.com IPv6
		{"93.184.216.34:80", false},                       // with port
		{"example.com", false},                            // plain hostname

		// localhost by name
		{"localhost", true},
		{"LOCALHOST", true},
		{"localhost:8080", true},

		// IPv4 loopback
		{"127.0.0.1", true},
		{"127.255.255.255", true},
		{"127.0.0.1:5432", true},

		// IPv6 loopback
		{"::1", true},
		{"[::1]", true},
		{"[::1]:443", true},

		// RFC 1918 private
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"192.168.255.255", true},

		// Link-local (AWS IMDS etc.)
		{"169.254.169.254", true},
		{"169.254.0.1", true},

		// IPv6 link-local
		{"fe80::1", true},
		{"fe80::1%eth0", true},     // link-local with zone identifier
		{"[fe80::1%25eth0]", true}, // bracketed + percent-encoded zone

		// IPv6 unique-local
		{"fd00::1", true},
		{"fc00::1", true},

		// Carrier-grade NAT
		{"100.64.0.1", true},
		{"100.127.255.255", true},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := isPrivateHost(tc.host); got != tc.private {
				t.Errorf("isPrivateHost(%q) = %v, want %v", tc.host, got, tc.private)
			}
		})
	}
}

func TestValidateTargetURL_RejectsPrivateHosts(t *testing.T) {
	t.Parallel()
	cases := []string{
		"http://localhost/",
		"http://127.0.0.1/",
		"http://127.0.0.1:8080/admin",
		"https://[::1]/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/",
		"http://100.64.0.1/",
		"http://fd00::1/",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			if err := validateTargetURL(target); err == nil {
				t.Errorf("validateTargetURL(%q) = nil, want error", target)
			}
		})
	}
}
