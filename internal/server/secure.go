package server

import (
	"net/http"
)

// hstsMaxAge is the Strict-Transport-Security max-age value in seconds (2 years).
const hstsMaxAge = "63072000"

// csp is the Content-Security-Policy directive set for all responses.
const csp = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"worker-src 'self' blob:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"object-src 'none'"

// buildSecureHeaders returns middleware that sets security-related response headers.
func buildSecureHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hdr := w.Header()
			hdr.Set("Content-Security-Policy", csp)
			hdr.Set("X-Content-Type-Options", "nosniff")
			hdr.Set("X-Frame-Options", "SAMEORIGIN")
			hdr.Set("X-XSS-Protection", "1; mode=block")
			hdr.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				hdr.Set("Strict-Transport-Security",
					"max-age="+hstsMaxAge+"; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
