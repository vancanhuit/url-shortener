package server

import (
	"net"
	"net/http"
	"strings"
)

// buildIPExtractor returns a function that extracts the real client IP
// from a request. When trustedProxies is non-empty, X-Forwarded-For is
// honored only when the immediate peer (RemoteAddr) falls inside one
// of those CIDR ranges; otherwise RemoteAddr is returned directly.
//
// A nil return means no trusted proxies were configured; callers
// should fall back to using r.RemoteAddr (or net.SplitHostPort) directly.
//
// CIDR strings are expected to have already been validated by
// config.Validate; entries that fail to parse here are silently
// skipped to keep the function total.
func buildIPExtractor(trustedProxies []string) func(r *http.Request) string {
	nets := make([]*net.IPNet, 0, len(trustedProxies))
	for _, cidr := range trustedProxies {
		if cidr == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		nets = append(nets, ipnet)
	}
	if len(nets) == 0 {
		return nil
	}

	return func(r *http.Request) string {
		peerStr, _, _ := net.SplitHostPort(r.RemoteAddr)
		peer := net.ParseIP(peerStr)

		trusted := false
		if peer != nil {
			for _, ipnet := range nets {
				if ipnet.Contains(peer) {
					trusted = true
					break
				}
			}
		}

		if trusted {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				// Take the leftmost (client-provided) IP. A header value may
				// be space-padded or contain garbage if an upstream proxy
				// passed through a malformed value -- only return it if it
				// parses as a real IP. Otherwise fall through to RemoteAddr
				// so the rate-limit key is still well-formed.
				parts := strings.SplitN(xff, ",", 2)
				if ip := strings.TrimSpace(parts[0]); ip != "" {
					if net.ParseIP(ip) != nil {
						return ip
					}
				}
			}
		}
		if peerStr != "" {
			return peerStr
		}
		return r.RemoteAddr
	}
}
