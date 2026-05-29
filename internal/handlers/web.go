// This file serves the embedded Svelte SPA. The build artifacts live
// in `web/dist/` (produced by `npm --prefix web run build`) and are
// `//go:embed`ed into the binary by the `web` package.
package handlers

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// indexCacheControl is the Cache-Control header on the SPA shell.
const indexCacheControl = "no-cache"

// hashedAssetCacheControl is the Cache-Control header on the Vite-hashed bundles.
const hashedAssetCacheControl = "public, max-age=31536000, immutable"

// vendoredAssetCacheControl is the Cache-Control header on the vendored doc assets.
const vendoredAssetCacheControl = "public, max-age=3600, must-revalidate"

// SPAConfig groups the SPA handler constructor arguments.
type SPAConfig struct {
	// DistFS is the rooted fs.FS that contains `index.html`, `assets/`, and `static/`.
	DistFS fs.FS
	// IndexHTML is the bytes of `dist/index.html`.
	IndexHTML []byte
	Logger    *slog.Logger
}

// SPA is the static-asset + SPA-shell handler bundle.
type SPA struct {
	dist              fs.FS
	indexHTML         []byte
	logger            *slog.Logger
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

// rootJSFiles are the non-hashed JS files placed at the dist root by
// Vite (copied verbatim from web/public/). They are served with
// no-cache so a deploy is reflected on the next navigation, matching
// the policy on index.html.
var rootJSFiles = []string{"swagger-ui-init.js", "theme-init.js"}

// Mount registers the SPA + asset routes on r.
func (s *SPA) Mount(r chi.Router) {
	r.Get("/", s.Index)

	assetsFS := mustSub(s.dist, "assets")
	staticFS := mustSub(s.dist, "static")
	assetsHandler := http.StripPrefix("/assets/",
		cacheHeaders(http.FileServer(http.FS(assetsFS)), hashedAssetCacheControl))
	staticHandler := http.StripPrefix("/static/",
		cacheHeaders(http.FileServer(http.FS(staticFS)), vendoredAssetCacheControl))

	r.Get("/assets/*", assetsHandler.ServeHTTP)
	r.Get("/static/*", staticHandler.ServeHTTP)

	for _, name := range rootJSFiles {
		r.Get("/"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", indexCacheControl)
			http.ServeFileFS(w, r, s.dist, name)
		})
	}
}

// Index serves the SPA shell.
func (s *SPA) Index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", indexCacheControl)
	w.Header().Set("Last-Modified", s.indexLastModified.Format(http.TimeFormat))
	w.Header().Set("Content-Length", strconv.Itoa(len(s.indexHTML)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.indexHTML)
}

// cacheHeaders sets a Cache-Control header on every response from next.
func cacheHeaders(next http.Handler, value string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

// mustSub is a panicking fs.Sub.
func mustSub(root fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(root, dir)
	if err != nil {
		panic("handlers: web embed missing " + dir + ": " + err.Error())
	}
	return sub
}
