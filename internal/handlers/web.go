// This file serves the embedded Svelte SPA. The build artifacts live
// in `web/dist/` (produced by `npm --prefix web run build`) and are
// `//go:embed`ed into the binary by the `web` package. The HTML form
// + htmx pages that used to live here were retired with the SPA
// refactor; the JSON API at `/api/v1/*` is now the only contract the
// browser talks to.
package handlers

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
)

// indexCacheControl is the Cache-Control header on the SPA shell
// (`/`, plus the SPA fallback). The shell is tiny and references
// long-lived hashed bundles, so it must revalidate every request --
// otherwise a deploy that ships new bundles would leave clients
// stranded on a stale shell pointing at deleted asset names.
const indexCacheControl = "no-cache"

// hashedAssetCacheControl is the Cache-Control header on the Vite-
// hashed bundles under `/assets/`. The filename includes a content
// hash, so browsers + CDNs are safe to keep them indefinitely; flip
// to a fresh URL on every deploy. 1 year + immutable matches Vite's
// own recommendation.
const hashedAssetCacheControl = "public, max-age=31536000, immutable"

// vendoredAssetCacheControl is the Cache-Control header on the
// vendored Swagger UI / Redoc bundles under `/static/`. Their names
// are NOT content-hashed (we copy them straight out of npm), so a
// modest max-age plus must-revalidate keeps the round-trip cheap on
// repeat doc visits while letting a release upgrade propagate.
const vendoredAssetCacheControl = "public, max-age=3600, must-revalidate"

// SPAConfig groups the SPA handler's constructor arguments.
type SPAConfig struct {
	// DistFS is the rooted fs.FS that contains `index.html`,
	// `assets/`, and `static/`. Pass `web.DistFS()` in production.
	DistFS fs.FS
	// IndexHTML is the bytes of `dist/index.html`, served as the SPA
	// shell at `/` (and as the fallback for non-asset routes once a
	// client-side router is added).
	IndexHTML []byte
	Logger    *slog.Logger
}

// SPA is the static-asset + SPA-shell handler bundle. It owns three
// route trees:
//
//   - GET /             -- the SPA shell (index.html, no-cache)
//   - GET /assets/*     -- Vite-hashed bundles (immutable, 1y)
//   - GET /static/*     -- vendored docs assets (3600s, revalidate)
//
// The HTML form / htmx routes from the old templated UI have been
// retired; the SPA drives everything via the JSON API.
type SPA struct {
	dist      fs.FS
	indexHTML []byte
	logger    *slog.Logger

	// indexLastModified is the timestamp the SPA shell reports in the
	// Last-Modified header. embed.FS files report a zero modtime, so
	// we fall back to the binary's start time -- close enough for
	// browsers' weak validation, and still bumps every redeploy.
	indexLastModified time.Time
}

// NewSPA constructs an SPA handler.
func NewSPA(cfg SPAConfig) *SPA {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &SPA{
		dist:              cfg.DistFS,
		indexHTML:         cfg.IndexHTML,
		logger:            logger,
		indexLastModified: time.Now().UTC(),
	}
}

// Mount registers the SPA + asset routes on e.
//
// No CSRF protection is applied: the service has no auth concept, so
// there are no per-user mutations to forge -- the JSON API is
// effectively public. Revisit if accounts / quotas are added.
func (s *SPA) Mount(e *echo.Echo) {
	e.GET("/", s.Index)

	// `http.FileServer` matches against the request URL path, so we
	// need to feed it a request whose path is rooted at the same
	// place as the FS. http.StripPrefix takes care of that for the
	// `/assets/` and `/static/` mounts below.
	assetsFS := mustSub(s.dist, "assets")
	staticFS := mustSub(s.dist, "static")
	assetsHandler := http.StripPrefix("/assets/",
		cacheHeaders(http.FileServer(http.FS(assetsFS)), hashedAssetCacheControl))
	staticHandler := http.StripPrefix("/static/",
		cacheHeaders(http.FileServer(http.FS(staticFS)), vendoredAssetCacheControl))

	e.GET("/assets/*", echo.WrapHandler(assetsHandler))
	e.GET("/static/*", echo.WrapHandler(staticHandler))
}

// Index serves the SPA shell. Sets a Last-Modified for browsers that
// honor weak revalidation; the no-cache directive forces them to
// confirm with a HEAD/GET on each navigation, which is what we want
// while we're shipping new hashed bundle URLs in every redeploy.
func (s *SPA) Index(c *echo.Context) error {
	w := c.Response()
	w.Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	w.Header().Set("Cache-Control", indexCacheControl)
	w.Header().Set("Last-Modified", s.indexLastModified.Format(http.TimeFormat))
	w.Header().Set("Content-Length", strconv.Itoa(len(s.indexHTML)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(s.indexHTML)
	return err
}

// cacheHeaders sets a Cache-Control header on every response from
// next. Kept tiny + dependency-free so it composes cleanly with
// http.StripPrefix and http.FileServer.
func cacheHeaders(next http.Handler, value string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

// mustSub is a panicking fs.Sub that turns a missing subtree into a
// startup failure -- the SPA build is checked into the binary, so a
// missing subdir means the embed directive and the Vite output
// drifted apart. Failing fast is the right call.
func mustSub(root fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(root, dir)
	if err != nil {
		panic("handlers: web embed missing " + dir + ": " + err.Error())
	}
	return sub
}
