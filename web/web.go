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
	"time"
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
//
// The template set is registered with a small set of helper funcs so the
// recent-list partial can render expiry / click metadata without leaking
// formatting concerns into the Go handler.
func ParseTemplates() (*template.Template, error) {
	t, err := template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parse templates: %w", err)
	}
	return t, nil
}

// templateFuncs is the FuncMap registered on the template set.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"humanExpiry": humanExpiry,
		"plural":      plural,
	}
}

// humanExpiry renders an *time.Time as a coarse-grained badge label:
//
//	nil           -> ""           (caller hides the badge)
//	in the past   -> "expired"
//	< 1 minute    -> "<1m left"
//	< 1 hour      -> "Nm left"
//	< 1 day       -> "Nh left"
//	otherwise     -> "Nd left"
//
// Coarseness is deliberate: the recent-list isn't reactive, so a
// per-second countdown would be wrong almost as soon as it rendered.
func humanExpiry(t *time.Time) string {
	if t == nil {
		return ""
	}
	d := time.Until(*t)
	if d <= 0 {
		return "expired"
	}
	switch {
	case d < time.Minute:
		return "<1m left"
	case d < time.Hour:
		return fmt.Sprintf("%dm left", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh left", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd left", int(d/(24*time.Hour)))
	}
}

// plural returns "" when n == 1 and "s" otherwise. Cheap helper that
// keeps the templates from open-coding the same conditional.
func plural(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}
