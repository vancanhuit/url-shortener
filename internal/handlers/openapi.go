// This file implements the OpenAPI self-description endpoints. The spec
// lives at api/openapi.yaml in the repo and is embedded into the binary
// at build time; this handler just serves the precomputed bytes.
package handlers

import (
	"net/http"

	"github.com/labstack/echo/v5"

	openapi "github.com/vancanhuit/url-shortener/api"
)

// MountOpenAPI registers the meta endpoints that expose this binary's
// OpenAPI document:
//
//   - GET /api/v1/openapi.json -- canonical machine-readable form.
//   - GET /api/v1/openapi.yaml -- the original source for humans and
//     for tools that prefer YAML.
//
// Both bytes are precomputed at package init in `api`; the handlers
// therefore do no work per request beyond writing the response. The
// content is bytewise stable for a given build, which lets a CDN
// or HTTP cache front this endpoint trivially if anyone ever wants
// to.
func MountOpenAPI(e *echo.Echo) {
	e.GET("/api/v1/openapi.json", func(c *echo.Context) error {
		return c.Blob(http.StatusOK, echo.MIMEApplicationJSON, openapi.SpecJSON)
	})
	e.GET("/api/v1/openapi.yaml", func(c *echo.Context) error {
		// Echo doesn't define a YAML MIME constant; the IANA
		// registration is `application/yaml` (RFC 9512).
		return c.Blob(http.StatusOK, "application/yaml; charset=utf-8", openapi.Spec)
	})
}
