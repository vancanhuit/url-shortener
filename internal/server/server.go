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
)

// Conservative HTTP server timeouts. They protect the process from slow or
// malicious clients while still being generous enough for normal traffic.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 15 * time.Second
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
}

// New builds a Server with all routes and middleware mounted. It does not
// start listening; call Run for that.
func New(cfg config.Config, logger *slog.Logger, deps Deps) *Server {
	e := echo.New()
	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(slogRequestLogger(logger))

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
	if deps.Store != nil && deps.Cache != nil && deps.Generator != nil {
		links := handlers.NewLinks(handlers.LinksConfig{
			Store:     deps.Store,
			Cache:     deps.Cache,
			Generator: deps.Generator,
			BaseURL:   cfg.BaseURL,
			Logger:    logger,
		})
		links.Mount(e)
	}

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           e,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	return &Server{cfg: cfg, logger: logger, deps: deps, echo: e, http: httpSrv}
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
	if err := s.http.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server: shutdown: %w", err)
	}
	s.logger.Info("http server stopped")
	return nil
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
		LogValuesFunc: func(_ *echo.Context, v middleware.RequestLoggerValues) error {
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
			logger.Log(context.Background(), level, "http request", attrs...)
			return nil
		},
	})
}
