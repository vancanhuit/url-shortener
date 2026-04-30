// Package web exposes the HTML templates and static assets that make up
// the url-shortener web UI, embedded into the binary at compile time.
//
// The Tailwind v4 + HTMX 2 toolchain in `web/tailwind/` produces
// `web/static/styles.css` and `web/static/htmx.min.js`; rebuild via
// `just web-build` whenever templates or CSS classes change.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
)

//go:embed all:templates
var templatesFS embed.FS

// `static` (without the `all:` prefix) intentionally excludes dotfiles so
// the `.gitkeep` placeholder isn't served. The compile will fail with
// "no matching files" if styles.css and htmx.min.js are missing -- run
// `just web-build` to populate them.
//
//go:embed static
var staticFS embed.FS

// Static returns the static-asset filesystem rooted at `static/` (so it can
// be served directly under `/static/...`).
func Static() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Unreachable: the embed directive guarantees the directory exists.
		panic(fmt.Errorf("web: locate static dir: %w", err))
	}
	return sub
}

// ParseTemplates parses every file under `templates/` into a single
// associated *template.Template. Pages reference each other via {{template
// "name"}} blocks, with `layout` as the entry point.
//
// Returned template is safe for concurrent use (per html/template's
// documentation); callers should parse once at startup and reuse.
func ParseTemplates() (*template.Template, error) {
	t, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse templates: %w", err)
	}
	return t, nil
}
