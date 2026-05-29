// Package web exposes the static assets that make up the url-shortener
// SPA, embedded into the binary at compile time.
//
// The Vite + Svelte + Tailwind v4 toolchain in `web/` produces a
// `web/dist/` directory containing:
//
//   - index.html              the SPA shell, references hashed assets
//   - assets/index-<hash>.js  Vite-bundled application code
//   - assets/index-<hash>.css Tailwind-processed styles
//   - static/swagger-ui.*     vendored Swagger UI bundle
//   - static/redoc.*          vendored Redoc bundle
//   - swagger-ui-init.js      Swagger UI initialisation (copied from web/public/)
//   - theme-init.js           dark/light theme initialisation (copied from web/public/)
//
// Re-run `just web-build` (or `npm --prefix web run build`) after
// touching anything under `web/src/`, `web/index.html`, or
// `web/public/`. The compile will fail with "no matching files" if
// `web/dist/` is missing -- it's a //go:embed target with `all:`.
package web

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS returns the SPA build output rooted at `dist/`. Mount it
// with `http.FileServer(http.FS(DistFS()))` to serve the SPA shell at
// `/` plus its hashed asset bundles under `/assets/`. Vendored docs
// assets land under `/static/`.
func DistFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Unreachable: the embed directive guarantees the directory
		// exists at compile time.
		panic(fmt.Errorf("web: locate dist dir: %w", err))
	}
	return sub
}

// IndexHTML returns the bytes of `dist/index.html`. The Go server
// uses it as the SPA shell at `/` (and as the fallback for non-API
// routes once a client-side router is added).
func IndexHTML() ([]byte, error) {
	b, err := fs.ReadFile(distFS, "dist/index.html")
	if err != nil {
		return nil, fmt.Errorf("web: read dist/index.html: %w", err)
	}
	return b, nil
}
