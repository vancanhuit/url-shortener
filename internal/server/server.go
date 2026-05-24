// Package server wires the Echo HTTP server: routes, middleware, and the
// graceful-shutdown lifecycle.
//
// Echo v5 dropped its built-in Start/Shutdown helpers, so we drive the
// listener with a plain *http.Server using e.ServeHTTP as the handler. This
// is the idiomatic Go pattern and gives us full control over timeouts.
package server

import (
	"context"
	"crypto/tls"
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

// Deps groups the runtime dependencies the server needs. Every field is
// required: Postgres is the system of record, Redis is on the redirect
// hot path, and Generator mints short codes for new links. New panics
// if any field is nil -- there is no meaningful "degraded" mode for a
// link shortener that is missing one of them, and silently mounting a
// half-wired server would only manifest as 500s in production.
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

	// links is the constructed handler bundle. Held on the server
	// so Serve can drain its background goroutines (async click
	// counter increments) during graceful shutdown.
	links *handlers.Links
}

// New builds a Server with all routes and middleware mounted. It does not
// start listening; call Run for that. Panics if any field of deps is nil
// (see Deps for rationale).
func New(cfg config.Config, logger *slog.Logger, deps Deps) *Server {
	switch {
	case deps.Store == nil:
		panic("server: Deps.Store must not be nil")
	case deps.Cache == nil:
		panic("server: Deps.Cache must not be nil")
	case deps.Generator == nil:
		panic("server: Deps.Generator must not be nil")
	}

	e := echo.New()

	// IPExtractor is consulted by Context.RealIP() and by the request
	// logger's LogRemoteIP path. With cfg.TrustedProxies empty (the
	// default), we leave IPExtractor unset so Echo falls back to its
	// legacy RemoteAddr-based behavior -- equivalent to "no proxy in
	// front", which is correct for direct deployments and for the
	// docker compose stack today. When CIDRs are configured, install
	// an XFF-aware extractor that only honors the header when the
	// immediate peer falls inside one of those ranges; spoofed XFF
	// from untrusted clients is ignored.
	if extractor := buildIPExtractor(cfg.TrustedProxies); extractor != nil {
		e.IPExtractor = extractor
	}

	reg := newMetricsRegistry()
	reqTotal, reqDuration := newRequestMetrics(reg)

	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(slogRequestLogger(logger))
	// Prometheus RED metrics: record every request's rate, error
	// count, and duration by route template and status code.
	e.Use(buildMetricsMiddleware(reqTotal, reqDuration))
	// Cap request bodies before any handler reads them. Echo's
	// BodyLimit short-circuits with 413 Request Entity Too Large
	// when Content-Length exceeds the cap, and wraps the body
	// reader so chunked / unknown-length requests are caught mid-
	// read too. Applies to every route, including the static
	// asset handler (where bodies are always empty in practice).
	e.Use(middleware.BodyLimit(maxRequestBodyBytes))
	// Security headers: X-Content-Type-Options, X-Frame-Options,
	// X-XSS-Protection, Referrer-Policy, and (for HTTPS requests)
	// Strict-Transport-Security. Applied to every response.
	e.Use(buildSecureHeaders())
	// CORS is opt-in via config; no-op when CORSAllowedOrigins is
	// empty (the default for same-origin SPA + API deployments).
	if cors := buildCORS(cfg, logger); cors != nil {
		e.Use(cors)
	}

	op := handlers.NewOperational()
	op.AddReadinessCheck("postgres", func(ctx context.Context) error {
		return deps.Store.Pool().Ping(ctx)
	})
	op.AddReadinessCheck("redis", deps.Cache.Ping)
	op.Mount(e)
	mountMetrics(e, reg)

	// API self-description: mount the embedded OpenAPI 3.1 document
	// at /api/v1/openapi.{json,yaml} ahead of the JSON API itself so
	// the routes are co-located with the rest of /api/v1/* in the
	// router's mount log.
	handlers.MountOpenAPI(e)

	// Links API + redirect. The optional rate limiter applies only to
	// the abuse-prone POST /api/v1/links endpoint and is keyed on the
	// real client IP (already correct via cfg.TrustedProxies above).
	links := handlers.NewLinks(handlers.LinksConfig{
		Store:            deps.Store,
		Cache:            deps.Cache,
		Generator:        deps.Generator,
		BaseURL:          cfg.BaseURL,
		Logger:           logger,
		CacheTTL:         cfg.CacheTTL,
		NegativeCacheTTL: cfg.NegativeCacheTTL,
	})
	createMW := buildCreateRateLimiter(cfg, logger)
	links.Mount(e, createMW...)

	// SPA shell + static assets. The Vite + Svelte build emits
	// `web/dist/` which is //go:embed'd into the binary; the SPA
	// then drives the JSON API directly -- no server-side templating
	// or htmx-style partials remain.
	indexHTML, err := web.IndexHTML()
	if err != nil {
		// dist/index.html is part of the //go:embed set, so a read
		// failure means a programming error: fail fast at startup.
		panic(fmt.Errorf("server: read web index: %w", err))
	}
	spa := handlers.NewSPA(handlers.SPAConfig{
		DistFS:    web.DistFS(),
		IndexHTML: indexHTML,
		Logger:    logger,
	})
	spa.Mount(e)

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

// Run starts the HTTP server and blocks until ctx is canceled (typically by
// SIGINT/SIGTERM), at which point it performs a graceful shutdown.
//
// Returns nil on a clean shutdown, a non-nil error on a startup failure or a
// shutdown that timed out.
func (s *Server) Run(ctx context.Context) error {
	// ListenConfig{}.Listen takes a context so net's listener-side
	// callbacks (currently a no-op KeepAlive) can be canceled if
	// startup gets interrupted before Serve takes over.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("server: listen: %w", err)
	}
	return s.Serve(ctx, ln)
}

// Serve runs the HTTP server using the provided already-bound listener. It
// blocks until ctx is canceled and then shuts down gracefully. Tests use
// this with a port-0 listener so they can pick up the bound address.
//
// When cfg.TLSCertFile and cfg.TLSKeyFile are both set, the server speaks
// HTTPS on the listener; otherwise it speaks plain HTTP. The TLS path
// uses http.Server.ServeTLS, which loads the cert+key on each call;
// config.Validate already stat'd both paths at startup so a bad path
// would have failed earlier with a clear error.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error, 1)
	tlsEnabled := s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != ""
	go func() {
		s.logger.Info("http server starting",
			"addr", ln.Addr().String(),
			"tls", tlsEnabled,
		)
		var err error
		if tlsEnabled {
			reloader, reloadErr := newCertReloader(s.cfg.TLSCertFile, s.cfg.TLSKeyFile, s.logger)
			if reloadErr != nil {
				errCh <- reloadErr
				close(errCh)
				return
			}
			stopReload, reloadErr := reloader.start(ctx)
			if reloadErr != nil {
				errCh <- reloadErr
				close(errCh)
				return
			}
			defer stopReload()

			// Serve TLS with a dynamic certificate callback so certificate
			// replacement on disk takes effect without process restart.
			tlsCfg := &tls.Config{
				GetCertificate: reloader.getCertificate,
			}
			if s.http.TLSConfig != nil {
				tlsCfg = s.http.TLSConfig.Clone()
				tlsCfg.GetCertificate = reloader.getCertificate
			}
			s.http.TLSConfig = tlsCfg
			err = s.http.ServeTLS(ln, "", "")
		} else {
			err = s.http.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
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

	// Deliberately root the shutdown context at Background, not the
	// already-canceled `ctx`: http.Server.Shutdown needs a live
	// context to drain, and the 15s timeout is the bound we want on
	// the stop sequence regardless of how Serve was woken.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := s.http.Shutdown(shutdownCtx) //nolint:contextcheck // see comment above; fresh ctx is intentional.

	// Drain background goroutines (currently just async click counter
	// increments) before reporting shutdown complete. Any work fired
	// from a request that landed before Shutdown() drew the curtain
	// gets a chance to commit; without this drain SIGTERM can drop
	// the last few clicks every deploy. We use whatever budget is
	// left in shutdownCtx -- typically most of the 15s, since
	// http.Shutdown returns as soon as the last in-flight request
	// finishes -- so the overall stop time is still bounded.
	if remaining := timeUntil(shutdownCtx); remaining > 0 { //nolint:contextcheck // shutdownCtx is intentionally fresh; see Shutdown call above.
		if !s.links.WaitForBackgroundTasks(remaining) {
			s.logger.Warn("background tasks did not drain before shutdown deadline",
				"budget", remaining)
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
		LogRemoteIP:  true,
		LogUserAgent: true,
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
				"remote_ip", v.RemoteIP,
				"user_agent", v.UserAgent,
			}
			if v.Error != nil {
				attrs = append(attrs, "error", v.Error.Error())
			}
			// Forward the request context so handlers like otelslog can
			// extract the trace/span IDs the http server installed.
			// Falls back to Background only if the request is somehow
			// detached (e.g. tests that synthesize a logger value
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
