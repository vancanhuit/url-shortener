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

// buildSecureHeaders returns the Echo Secure middleware configured for
// this service. The returned middleware always sets:
//
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
		XSSProtection:      "1; mode=block",
		ContentTypeNosniff: "nosniff",
		XFrameOptions:      "SAMEORIGIN",
		HSTSMaxAge:         hstsMaxAge,
		ReferrerPolicy:     "strict-origin-when-cross-origin",
	})
}
