package server

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/vancanhuit/url-shortener/internal/config"
)

// corsMaxAgeSeconds is the cache lifetime advertised to browsers for
// preflight (OPTIONS) responses. 10 minutes balances "rare config
// changes propagate within a deploy cycle" against "don't make every
// API call do a preflight roundtrip first".
const corsMaxAgeSeconds = 600

// buildCORS returns the CORS middleware to mount on the Echo server,
// or nil when cfg.CORSAllowedOrigins is empty (the default).
//
// The same-origin SPA + API setup this project ships does not need
// CORS at all -- the SPA and the JSON API are served from the same
// host, so the browser never tags requests with an Origin header
// that mismatches. CORS only matters when:
//
//   - the SPA is hosted separately (e.g. Cloudflare Pages or a CDN)
//     and talks to this API across origins, or
//   - third-party clients integrate with the JSON API from a browser
//     (a one-page integration page, a status dashboard, ...).
//
// Operators opt in by setting URL_SHORTENER_CORS_ALLOWED_ORIGINS to a
// comma-separated allow-list. Wildcard "*" is supported but disables
// AllowCredentials (per CORS spec); the typed config knob refuses
// half-baked values like "example.com" (no scheme) at startup so a
// silent never-match in production isn't possible.
func buildCORS(cfg config.Config, logger *slog.Logger) echo.MiddlewareFunc {
	if len(cfg.CORSAllowedOrigins) == 0 {
		return nil
	}

	origins := make([]string, 0, len(cfg.CORSAllowedOrigins))
	for _, o := range cfg.CORSAllowedOrigins {
		if o == "" {
			continue
		}
		origins = append(origins, o)
	}
	if len(origins) == 0 {
		// The slice was non-nil but contained only empty entries
		// (e.g. "URL_SHORTENER_CORS_ALLOWED_ORIGINS=,," would shake
		// out this way). Treat as off, same as the empty case.
		return nil
	}

	mw, err := middleware.CORSConfig{
		AllowOrigins: origins,
		AllowMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodDelete,
			http.MethodOptions,
		},
		// Allow the headers a JSON browser client realistically sends.
		// Authorization is included so a future API-token integration
		// doesn't need a follow-up CORS deploy. X-Requested-With keeps
		// classic XHR clients (jQuery, axios with that header) from
		// failing the preflight.
		AllowHeaders: []string{
			echo.HeaderContentType,
			echo.HeaderAuthorization,
			echo.HeaderXRequestedWith,
		},
		// Expose the request id so browser tools and clients can
		// correlate a failure with a server log line.
		ExposeHeaders: []string{echo.HeaderXRequestID},
		// Credentials stay off: this API is currently unauthenticated,
		// and AllowCredentials=true with AllowOrigins=["*"] is an
		// unsafe combination per the CORS spec. Flip on alongside a
		// real auth scheme when one lands.
		AllowCredentials: false,
		MaxAge:           corsMaxAgeSeconds,
	}.ToMiddleware()
	if err != nil {
		// CORSConfig.ToMiddleware errors only on incoherent configs
		// (e.g. empty AllowOrigins, which we already filtered out).
		// Fail fast at startup rather than silently dropping CORS.
		panic("server: build cors: " + err.Error())
	}

	logger.Info("cors enabled",
		"origins", origins,
		"max_age_seconds", corsMaxAgeSeconds,
	)
	return mw
}
