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
// OpenAPI document and the two interactive documentation viewers:
//
//   - GET /api/v1/openapi.json -- canonical machine-readable form.
//   - GET /api/v1/openapi.yaml -- the original source for humans and
//     for tools that prefer YAML.
//   - GET /api/v1/docs         -- Swagger UI (try-it-out interactive
//     console). Useful for poking at the API from a browser without
//     reaching for curl.
//   - GET /api/v1/redoc        -- Redoc (read-only reference doc, with
//     a nicer information density for browsing the schemas).
//
// The spec bytes are precomputed at package init in `api`; the
// handlers therefore do no work per request beyond writing the
// response. The HTML pages reference the assets vendored into
// `web/static/` (swagger-ui-bundle.js, redoc.standalone.js, etc.),
// embedded into the binary by the `web` package. All four responses
// are bytewise stable for a given build, which lets a CDN or HTTP
// cache front them trivially.
func MountOpenAPI(e *echo.Echo) {
	e.GET("/api/v1/openapi.json", func(c *echo.Context) error {
		return c.Blob(http.StatusOK, echo.MIMEApplicationJSON, openapi.SpecJSON)
	})
	e.GET("/api/v1/openapi.yaml", func(c *echo.Context) error {
		// Echo doesn't define a YAML MIME constant; the IANA
		// registration is `application/yaml` (RFC 9512).
		return c.Blob(http.StatusOK, "application/yaml; charset=utf-8", openapi.Spec)
	})
	e.GET("/api/v1/docs", func(c *echo.Context) error {
		return c.HTML(http.StatusOK, swaggerUIHTML)
	})
	e.GET("/api/v1/redoc", func(c *echo.Context) error {
		return c.HTML(http.StatusOK, redocHTML)
	})
}

// swaggerUIHTML is the bootstrap page for the Swagger UI bundle.
// The CSS and JS files are vendored into web/static/ from the
// swagger-ui-dist npm package by `just web-build`. The spec URL is
// relative (`./openapi.json`) so the page works behind any
// reverse-proxy path prefix without server-side URL rewriting.
//
// `deepLinking: true` makes the address bar reflect the currently
// expanded operation, which is what users expect when sharing
// links to specific endpoints. `tryItOutEnabled: true` opens the
// "try it out" panel by default since try-it-out is the whole
// point of choosing Swagger UI over Redoc.
//
//nolint:gochecknoglobals,lll // intentional: static asset.
var swaggerUIHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>url-shortener API -- Swagger UI</title>
  <link rel="stylesheet" href="/static/swagger-ui.css">
</head>
<body style="margin:0">
  <div id="swagger-ui"></div>
  <script src="/static/swagger-ui-bundle.js" crossorigin></script>
  <script src="/static/swagger-ui-standalone-preset.js" crossorigin></script>
  <script src="/swagger-ui-init.js" crossorigin></script>
</body>
</html>
`

// redocHTML is the bootstrap page for Redoc. Same vendoring story as
// swaggerUIHTML, just one JS file (redoc.standalone.js bundles its
// React + dependencies). `<redoc>` is a custom element registered by
// the bundle, which kicks off the render once it sees the
// `spec-url` attribute.
//
//nolint:gochecknoglobals,lll // intentional: static asset.
var redocHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>url-shortener API -- Redoc</title>
  <style>body { margin: 0; padding: 0; }</style>
</head>
<body>
  <redoc spec-url="./openapi.json"></redoc>
  <script src="/static/redoc.standalone.js" crossorigin></script>
</body>
</html>
`
