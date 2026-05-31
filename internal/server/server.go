// Package server wires the Chi HTTP server: routes, middleware, and the
// graceful-shutdown lifecycle.
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

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

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

	// defaultMaxRequestBodyBytes is the fallback cap on any single
	// request body when config.MaxRequestBodyBytes is 0. See the
	// MaxRequestBodyBytes doc comment in internal/config for the
	// 16 KiB sizing rationale (largest legitimate payload is the JSON
	// create-link request; 16 KiB leaves an order of magnitude of
	// headroom while still rejecting buggy or adversarial uploads
	// before the JSON decoder allocates against them).
	defaultMaxRequestBodyBytes = 16 * 1024
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

	// Build the IP extractor once; nil means "use RemoteAddr directly".
	ipExtractor := buildIPExtractor(cfg.TrustedProxies)

	r := chi.NewRouter()

	r.Use(chimw.Recoverer)
	r.Use(chimw.RequestID)
	r.Use(slogRequestLogger(logger, ipExtractor))
	// Prometheus RED metrics: record every request's rate, error
	// count, and duration by route template and status code.
	reg := newMetricsRegistry()
	reqTotal, reqDuration := newRequestMetrics(reg)
	r.Use(buildMetricsMiddleware(reqTotal, reqDuration))
	// Cap request bodies before any handler reads them.
	bodyLimit := cfg.MaxRequestBodyBytes
	if bodyLimit <= 0 {
		bodyLimit = defaultMaxRequestBodyBytes
	}
	r.Use(bodyLimitMiddleware(bodyLimit))
	// Security headers on every response.
	r.Use(buildSecureHeaders())
	// CORS is opt-in via config; no-op when CORSAllowedOrigins is empty.
	if cors := buildCORS(cfg, logger); cors != nil {
		r.Use(cors)
	}

	op := handlers.NewOperational()
	op.AddReadinessCheck("postgres", deps.Store.Ping)
	op.AddReadinessCheck("redis", deps.Cache.Ping)
	op.Mount(r)
	mountMetrics(r, reg)

	// API self-description: mount the embedded OpenAPI document.
	handlers.MountOpenAPI(r)

	// Links API + redirect. The optional rate limiter applies only to
	// the abuse-prone POST /api/v1/links endpoint and is keyed on the
	// real client IP.
	links := handlers.NewLinks(handlers.LinksConfig{
		Store:            deps.Store,
		Cache:            deps.Cache,
		Generator:        deps.Generator,
		BaseURL:          cfg.BaseURL,
		Logger:           logger,
		CacheTTL:         cfg.CacheTTL,
		NegativeCacheTTL: cfg.NegativeCacheTTL,
	})
	createMW := buildCreateRateLimiter(cfg, deps.Cache, ipExtractor, logger)
	links.Mount(r, createMW...)

	// SPA shell + static assets.
	indexHTML, err := web.IndexHTML()
	if err != nil {
		panic(fmt.Errorf("server: read web index: %w", err))
	}
	spa := handlers.NewSPA(handlers.SPAConfig{
		DistFS:    web.DistFS(),
		IndexHTML: indexHTML,
		Logger:    logger,
	})
	spa.Mount(r)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           r,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	return &Server{cfg: cfg, logger: logger, deps: deps, http: httpSrv, links: links}
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

// slogRequestLogger returns Chi middleware that logs each request via slog.
// ipExtractor is used to determine the remote IP; nil falls back to RemoteAddr.
func slogRequestLogger(logger *slog.Logger, ipExtractor func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			dur := time.Since(start)

			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case rec.status >= 400:
				level = slog.LevelWarn
			}

			remoteIP := r.RemoteAddr
			if ipExtractor != nil {
				remoteIP = ipExtractor(r)
			}

			requestID := chimw.GetReqID(r.Context())
			logger.Log(r.Context(), level, "http request",
				"method", r.Method,
				"uri", r.RequestURI,
				"status", rec.status,
				"latency_ms", dur.Milliseconds(),
				"request_id", requestID,
				"remote_ip", remoteIP,
				"user_agent", r.UserAgent(),
			)
		})
	}
}

// bodyLimitMiddleware caps request body size at maxBytes. Requests with
// bodies that exceed the limit will encounter a read error mid-stream
// (http.MaxBytesReader) causing the handler to return an error.
func bodyLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
