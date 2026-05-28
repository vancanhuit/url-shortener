package server

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/cors"

	"github.com/vancanhuit/url-shortener/internal/config"
)

// corsMaxAgeSeconds is the cache lifetime advertised to browsers for
// preflight (OPTIONS) responses.
const corsMaxAgeSeconds = 600

// buildCORS returns the CORS middleware to mount on the Chi router,
// or nil when cfg.CORSAllowedOrigins is empty (the default).
func buildCORS(cfg config.Config, logger *slog.Logger) func(http.Handler) http.Handler {
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
		return nil
	}

	logger.Info("cors enabled",
		"origins", origins,
		"max_age_seconds", corsMaxAgeSeconds,
	)

	return cors.Handler(cors.Options{
		AllowedOrigins: origins,
		AllowedMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodDelete,
			http.MethodOptions,
		},
		AllowedHeaders: []string{
			"Content-Type",
			"Authorization",
			"X-Requested-With",
		},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: false,
		MaxAge:           corsMaxAgeSeconds,
	})
}
