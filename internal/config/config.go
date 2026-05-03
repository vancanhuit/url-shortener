// Package config loads and validates the service configuration.
//
// All settings come from environment variables prefixed with URL_SHORTENER_
// (12-factor style). Defaults are tuned for production; the local compose
// stack overrides them via service-level `environment:` blocks.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// EnvPrefix is the prefix applied to every environment variable.
const EnvPrefix = "URL_SHORTENER"

// Environment values.
const (
	EnvDev  = "dev"
	EnvProd = "prod"
)

// Config is the resolved service configuration.
type Config struct {
	// Env is the deployment environment ("dev" or "prod"). Influences sane
	// defaults like log format.
	Env string `mapstructure:"env"        json:"env"`

	// Addr is the TCP address the HTTP server listens on, e.g. ":8080".
	Addr string `mapstructure:"addr"       json:"addr"`

	// BaseURL is the public origin used to build short-link URLs.
	BaseURL string `mapstructure:"base_url"   json:"base_url"`

	// LogLevel is one of "debug", "info", "warn", "error".
	LogLevel string `mapstructure:"log_level"  json:"log_level"`

	// LogFormat is "text" (human-readable) or "json" (production).
	LogFormat string `mapstructure:"log_format" json:"log_format"`

	// DatabaseURL is a Postgres connection string. Sensitive: redacted by
	// Redacted() and by the JSON marshaller.
	DatabaseURL string `mapstructure:"database_url" json:"database_url"`

	// RedisURL is a Redis connection string. Sensitive: redacted by
	// Redacted() and by the JSON marshaller.
	RedisURL string `mapstructure:"redis_url"    json:"redis_url"`

	// AutoMigrate, when true, makes `run` apply pending migrations before
	// starting the HTTP server. Convenient for local dev and CI where there
	// is exactly one process; production deployments should keep this false
	// and run `migrate up` as a separate one-shot step (avoids races between
	// replicas).
	AutoMigrate bool `mapstructure:"auto_migrate" json:"auto_migrate"`

	// CodeLength is the length of auto-generated short codes. Validated by
	// the shortener package: must be in [shortener.MinLength, MaxLength].
	CodeLength int `mapstructure:"code_length" json:"code_length"`

	// Postgres connection-pool tunables. All zero by default, in which
	// case pgx's own defaults apply. See store.PoolConfig for the
	// per-knob semantics. Production deployments typically want at
	// least DBMaxConns set above pgx's default of max(4, NumCPU) to
	// absorb burst load without queueing requests on the pool.
	DBMaxConns          int32         `mapstructure:"db_max_conns"           json:"db_max_conns"`
	DBMinConns          int32         `mapstructure:"db_min_conns"           json:"db_min_conns"`
	DBMaxConnLifetime   time.Duration `mapstructure:"db_max_conn_lifetime"   json:"db_max_conn_lifetime"`
	DBMaxConnIdleTime   time.Duration `mapstructure:"db_max_conn_idle_time"  json:"db_max_conn_idle_time"`
	DBHealthCheckPeriod time.Duration `mapstructure:"db_health_check_period" json:"db_health_check_period"`

	// TLSCertFile and TLSKeyFile, when both non-empty, switch the HTTP
	// server to HTTPS on Addr using the PEM-encoded certificate and
	// private key at the given paths. Both must be set together --
	// Validate rejects half-configured pairs. Empty (the default) keeps
	// the server on plain HTTP, which is the right choice when fronted
	// by a TLS-terminating reverse proxy (Caddy/nginx/Traefik).
	TLSCertFile string `mapstructure:"tls_cert_file" json:"tls_cert_file"`
	TLSKeyFile  string `mapstructure:"tls_key_file"  json:"tls_key_file"`

	// TrustedProxies is a comma-separated list of CIDR blocks (parsed by
	// net.ParseCIDR) whose request peers are trusted to forward client
	// IP addresses via X-Forwarded-For. When a request's RemoteAddr
	// falls inside one of these ranges, the server walks XFF to find
	// the original client IP; otherwise XFF is ignored and RemoteAddr
	// stands. Empty (the default) leaves Echo's IPExtractor unset --
	// equivalent to "no proxy in front". Set this to e.g.
	// `127.0.0.1/32,10.0.0.0/8` when running behind a reverse proxy.
	TrustedProxies []string `mapstructure:"trusted_proxies" json:"trusted_proxies"`
}

// Load reads the configuration from environment variables and applies the
// defaults. It returns an error if the resulting config fails validation.
func Load() (Config, error) {
	v := viper.New()

	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Bind every key so AutomaticEnv resolves it on Unmarshal even when the
	// key has no default (notably log_format, whose default depends on env).
	for _, key := range []string{
		"env", "addr", "base_url", "log_level", "log_format",
		"database_url", "redis_url", "auto_migrate", "code_length",
		"db_max_conns", "db_min_conns",
		"db_max_conn_lifetime", "db_max_conn_idle_time",
		"db_health_check_period",
		"tls_cert_file", "tls_key_file", "trusted_proxies",
	} {
		_ = v.BindEnv(key)
	}

	v.SetDefault("env", EnvProd)
	v.SetDefault("addr", ":8080")
	v.SetDefault("base_url", "http://localhost:8080")
	v.SetDefault("log_level", "info")
	v.SetDefault("code_length", 7)
	// log_format default is decided after env is known (text in dev, json in prod).

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: unmarshal: %w", err)
	}

	if cfg.LogFormat == "" {
		if cfg.Env == EnvDev {
			cfg.LogFormat = "text"
		} else {
			cfg.LogFormat = "json"
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate returns a non-nil error when c contains an invalid setting.
func (c Config) Validate() error {
	switch c.Env {
	case EnvDev, EnvProd:
	default:
		return fmt.Errorf("config: invalid env %q (want %q or %q)", c.Env, EnvDev, EnvProd)
	}

	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: invalid log_level %q", c.LogLevel)
	}

	switch c.LogFormat {
	case "text", "json":
	default:
		return fmt.Errorf("config: invalid log_format %q (want text or json)", c.LogFormat)
	}

	if c.Addr == "" {
		return fmt.Errorf("config: addr must not be empty")
	}
	if c.BaseURL == "" {
		return fmt.Errorf("config: base_url must not be empty")
	}
	if _, err := url.Parse(c.BaseURL); err != nil {
		return fmt.Errorf("config: base_url is not a valid URL: %w", err)
	}
	// Postgres is a required runtime dependency: the link store is the
	// system of record. Refuse to start instead of booting a server that
	// would 500 on every request.
	if c.DatabaseURL == "" {
		return fmt.Errorf("config: database_url must not be empty")
	}
	// Redis is a required runtime dependency: the cache is on the hot path
	// for redirect lookups and the API treats it as always-present. Refuse
	// to start instead of silently degrading.
	if c.RedisURL == "" {
		return fmt.Errorf("config: redis_url must not be empty")
	}

	// Pool tunables: any explicit value must be non-negative; when both
	// MinConns and MaxConns are set, MinConns must not exceed MaxConns.
	// Zero is the "leave pgx default" sentinel and is always allowed.
	if c.DBMaxConns < 0 {
		return fmt.Errorf("config: db_max_conns must be >= 0")
	}
	if c.DBMinConns < 0 {
		return fmt.Errorf("config: db_min_conns must be >= 0")
	}
	if c.DBMaxConns > 0 && c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("config: db_min_conns (%d) must not exceed db_max_conns (%d)",
			c.DBMinConns, c.DBMaxConns)
	}
	if c.DBMaxConnLifetime < 0 {
		return fmt.Errorf("config: db_max_conn_lifetime must be >= 0")
	}
	if c.DBMaxConnIdleTime < 0 {
		return fmt.Errorf("config: db_max_conn_idle_time must be >= 0")
	}
	if c.DBHealthCheckPeriod < 0 {
		return fmt.Errorf("config: db_health_check_period must be >= 0")
	}

	// TLS cert + key are paired -- one without the other is almost
	// always a misconfiguration (e.g. a Helm values typo) and we'd
	// rather refuse to start than silently fall back to plain HTTP
	// when the operator thought TLS was on.
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return fmt.Errorf("config: tls_cert_file and tls_key_file must be set together")
	}
	if c.TLSCertFile != "" {
		// Stat both files at startup so missing-file errors surface as
		// a config validation failure (clear stderr line, exit 1)
		// rather than as the first request hitting a nil TLS config
		// many seconds into the run.
		if _, err := os.Stat(c.TLSCertFile); err != nil {
			return fmt.Errorf("config: tls_cert_file: %w", err)
		}
		if _, err := os.Stat(c.TLSKeyFile); err != nil {
			return fmt.Errorf("config: tls_key_file: %w", err)
		}
	}

	// Each TrustedProxies entry must be a valid CIDR. Empty strings
	// (which can sneak in via stray commas like "127.0.0.1/32,") are
	// silently skipped on the consuming side, so don't error here.
	for _, cidr := range c.TrustedProxies {
		if cidr == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("config: trusted_proxies entry %q: %w", cidr, err)
		}
	}

	return nil
}

// Redacted returns a copy of c with passwords stripped from URL-shaped
// fields, suitable for logging or printing.
func (c Config) Redacted() Config {
	out := c
	out.DatabaseURL = redactURL(c.DatabaseURL)
	out.RedisURL = redactURL(c.RedisURL)
	return out
}

func redactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		// If it's unparseable we'd rather not leak it: return a placeholder.
		return "<unparseable>"
	}
	if u.User != nil {
		if _, hasPwd := u.User.Password(); hasPwd {
			u.User = url.UserPassword(u.User.Username(), "REDACTED")
		}
	}
	return u.String()
}
