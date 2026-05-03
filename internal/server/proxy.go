package server

import (
	"net"

	"github.com/labstack/echo/v5"
)

// buildIPExtractor returns an IPExtractor configured to walk
// X-Forwarded-For only when the request's immediate peer falls inside
// one of the supplied CIDR ranges. It returns nil when the slice is
// empty (or contains only empty strings) so the caller can fall back
// to Echo's default RemoteAddr-based behavior -- callers should not
// install a nil extractor.
//
// CIDR strings are expected to have already been validated by
// config.Validate; entries that fail to parse here are silently
// skipped to keep the function total. (Validate runs at startup, so
// reaching here with an unparseable entry would mean the validator
// has a bug -- worth the small belt-and-suspenders.)
func buildIPExtractor(trustedProxies []string) echo.IPExtractor {
	opts := make([]echo.TrustOption, 0, len(trustedProxies))
	for _, cidr := range trustedProxies {
		if cidr == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		opts = append(opts, echo.TrustIPRange(ipnet))
	}
	if len(opts) == 0 {
		return nil
	}
	return echo.ExtractIPFromXFFHeader(opts...)
}
