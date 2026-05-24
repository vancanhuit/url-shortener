package server

import (
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// hstsMaxAge is the Strict-Transport-Security max-age value in seconds
// (2 years). The header is only emitted by Echo when the request
// arrives over TLS (or with X-Forwarded-Proto: https), so this
// constant is safe to include in the config regardless of whether the
// deployment uses TLS.
const hstsMaxAge = 63072000 // 2 years

// csp is the Content-Security-Policy directive set for all responses.
//
//   - default-src 'self'        — baseline; only same-origin resources.
//   - script-src 'self'         — all scripts must be served from this
//     origin; no inline scripts, so injected JS is blocked (XSS).
//   - style-src 'self' 'unsafe-inline' — same-origin stylesheets; inline
//     styles are permitted because Swagger UI and Redoc inject them via
//     React at runtime and those cannot be hashed ahead of time.
//   - img-src 'self' data:      — data URIs used for icons by Swagger UI.
//   - font-src 'self' data:     — data URIs used for embedded fonts in
//     vendored CSS bundles.
//   - connect-src 'self'        — fetch/XHR must target this origin.
//   - worker-src 'self' blob:   — Redoc spawns a blob: Web Worker for
//     YAML parsing.
//   - frame-ancestors 'none'    — prevents iframe embedding (clickjacking),
//     complementing X-Frame-Options: SAMEORIGIN.
//   - base-uri 'self'           — prevents <base> tag injection.
//   - form-action 'self'        — form submissions stay on-origin.
//   - object-src 'none'         — blocks Flash and other legacy plugins.
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

// buildSecureHeaders returns the Echo Secure middleware configured for
// this service. The returned middleware always sets:
//
//   - Content-Security-Policy: (see [csp] constant above)
//   - X-Content-Type-Options: nosniff
//   - X-Frame-Options: SAMEORIGIN
//   - X-XSS-Protection: 1; mode=block
//   - Referrer-Policy: strict-origin-when-cross-origin
//
// When the request arrives over HTTPS it additionally emits:
//
//   - Strict-Transport-Security: max-age=63072000; includeSubDomains
func buildSecureHeaders() echo.MiddlewareFunc {
	return middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:         "1; mode=block",
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "SAMEORIGIN",
		HSTSMaxAge:            hstsMaxAge,
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		ContentSecurityPolicy: csp,
	})
}
