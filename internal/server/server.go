// Package server wires the Echo HTTP server: routes, middleware, and the
// graceful-shutdown lifecycle.
//
// Echo v5 dropped its built-in Start/Shutdown helpers, so we drive the
// listener with a plain *http.Server using e.ServeHTTP as the handler. This
// is the idiomatic Go pattern and gives us full control over timeouts.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/vancanhuit/url-shortener/internal/cache"
	"github.com/vancanhuit/url-shortener/internal/config"
	"github.com/vancanhuit/url-shortener/internal/handlers"
	"github.com/vancanhuit/url-shortener/internal/store"
	"github.com/vancanhuit/url-shortener/web"
)

// Conservative HTTP server timeouts. They protect the process from slow or
// malicious clients while still being generous enough for normal traffic.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 15 * time.Second

	// maxRequestBodyBytes caps the size of any single request body
	// the server will accept. The largest legitimate payload is the
	// JSON create-link request, which carries at most a 2 KiB
	// target_url plus a small envelope; 16 KiB leaves an order of
	// magnitude of headroom while still rejecting buggy or
	// adversarial uploads early -- before the JSON decoder allocates
	// against them. The HTML form route is also covered because the
	// middleware runs before any handler-level decoding.
	maxRequestBodyBytes = 16 * 1024
)

// Deps groups the optional runtime dependencies the server needs. Each
// field may be nil; the server gracefully degrades (e.g. /readyz simply
// omits checks for missing deps and the links API is not mounted when
// Store is missing).
type Deps struct {
	Store     *store.Store
	Cache     *cache.Client
	Generator handlers.Generator
}

// Server is the HTTP server with its dependencies and lifecycle.
type Server struct {
	cfg    config.Config
	logger *slog.Logger
	deps   Deps
	echo   *echo.Echo
	http   *http.Server

	// links is the constructed handler bundle (or nil when the
	// caller didn't supply enough deps to mount it). Held on the
	// server so Serve can drain its background goroutines during
	// graceful shutdown.
	links *handlers.Links
}

// New builds a Server with all routes and middleware mounted. It does not
// start listening; call Run for that.
func New(cfg config.Config, logger *slog.Logger, deps Deps) *Server {
	e := echo.New()
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(slogRequestLogger(logger))
	// Cap request bodies before any handler reads them. Echo's
	// BodyLimit short-circuits with 413 Request Entity Too Large
	// when Content-Length exceeds the cap, and wraps the body
	// reader so chunked / unknown-length requests are caught mid-
	// read too. Applies to every route, including the static
	// asset handler (where bodies are always empty in practice).
	e.Use(middleware.BodyLimit(maxRequestBodyBytes))

	op := handlers.NewOperational()
	if deps.Store != nil {
		op.AddReadinessCheck("postgres", func(ctx context.Context) error {
			return deps.Store.Pool().Ping(ctx)
		})
	}
	if deps.Cache != nil {
		op.AddReadinessCheck("redis", deps.Cache.Ping)
	}
	op.Mount(e)

	// Links API + redirect: requires the full set of deps. Cache is
	// non-optional -- the handler treats it as always-present (config
	// validation guarantees URL_SHORTENER_REDIS_URL is set in production).
	// The HTML web UI (form + recent list) is mounted alongside whenever
	// the API is up; both reuse the same underlying *handlers.Links.
	var links *handlers.Links
	if deps.Store != nil && deps.Cache != nil && deps.Generator != nil {
		links = handlers.NewLinks(handlers.LinksConfig{
			Store:     deps.Store,
			Cache:     deps.Cache,
			Generator: deps.Generator,
			BaseURL:   cfg.BaseURL,
			Logger:    logger,
		})
		links.Mount(e)

		tmpl, err := web.ParseTemplates()
		if err != nil {
			// Templates ship inside the binary, so a parse failure means
			// a programming error: fail fast at startup.
			panic(fmt.Errorf("server: parse web templates: %w", err))
		}
		webH := handlers.NewWeb(handlers.WebConfig{
			Links:     links,
			Templates: tmpl,
			Logger:    logger,
		})
		webH.Mount(e, web.Static())
	}

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           e,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	return &Server{cfg: cfg, logger: logger, deps: deps, echo: e, http: httpSrv, links: links}
}

// Run starts the HTTP server and blocks until ctx is cancelled (typically by
// SIGINT/SIGTERM), at which point it performs a graceful shutdown.
//
// Returns nil on a clean shutdown, a non-nil error on a startup failure or a
// shutdown that timed out.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("server: listen: %w", err)
	}
	return s.Serve(ctx, ln)
}

// Serve runs the HTTP server using the provided already-bound listener. It
// blocks until ctx is cancelled and then shuts down gracefully. Tests use
// this with a port-0 listener so they can pick up the bound address.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("http server starting", "addr", ln.Addr().String())
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server: serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		s.logger.Info("http server shutting down", "reason", ctx.Err())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := s.http.Shutdown(shutdownCtx)

	// Drain background goroutines (currently just async click counter
	// increments) before reporting shutdown complete. Any work fired
	// from a request that landed before Shutdown() drew the curtain
	// gets a chance to commit; without this drain SIGTERM can drop
	// the last few clicks every deploy. We use whatever budget is
	// left in shutdownCtx -- typically most of the 15s, since
	// http.Shutdown returns as soon as the last in-flight request
	// finishes -- so the overall stop time is still bounded.
	if s.links != nil {
		remaining := timeUntil(shutdownCtx)
		if remaining > 0 {
			if !s.links.WaitForBackgroundTasks(remaining) {
				s.logger.Warn("background tasks did not drain before shutdown deadline",
					"budget", remaining)
			}
		}
	}

	if shutdownErr != nil {
		return fmt.Errorf("server: shutdown: %w", shutdownErr)
	}
	s.logger.Info("http server stopped")
	return nil
}

// timeUntil returns the duration left until ctx's deadline, or 0 when
// the deadline has already passed (or no deadline is set, in which
// case there's no budget to allocate to drain). Split out so the
// shutdown path stays readable.
func timeUntil(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// slogRequestLogger returns Echo middleware that logs each request via slog.
func slogRequestLogger(logger *slog.Logger) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:    true,
		LogURI:       true,
		LogMethod:    true,
		LogLatency:   true,
		LogRequestID: true,
		HandleError:  true,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			level := slog.LevelInfo
			switch {
			case v.Error != nil, v.Status >= 500:
				level = slog.LevelError
			case v.Status >= 400:
				level = slog.LevelWarn
			}

			attrs := []any{
				"method", v.Method,
				"uri", v.URI,
				"status", v.Status,
				"latency_ms", v.Latency.Milliseconds(),
				"request_id", v.RequestID,
			}
			if v.Error != nil {
				attrs = append(attrs, "error", v.Error.Error())
			}
			// Forward the request context so handlers like otelslog can
			// extract the trace/span IDs the http server installed.
			// Falls back to Background only if the request is somehow
			// detached (e.g. tests that synthesise a logger value
			// directly), which keeps the call total-safe.
			ctx := context.Background()
			if c != nil && c.Request() != nil {
				ctx = c.Request().Context()
			}
			logger.Log(ctx, level, "http request", attrs...)
			return nil
		},
	})
}
