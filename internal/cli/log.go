package cli

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/vancanhuit/url-shortener/internal/config"
)

// newLogger builds the *slog.Logger described by cfg, writing to w.
func newLogger(cfg config.Config, w io.Writer) (*slog.Logger, error) {
	level, err := parseLevel(cfg.LogLevel)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch cfg.LogFormat {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	case "text":
		handler = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("cli: unknown log_format %q", cfg.LogFormat)
	}
	return slog.New(handler), nil
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("cli: unknown log_level %q", s)
	}
}
