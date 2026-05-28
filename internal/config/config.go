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

	"github.com/caarlos0/env/v11"
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
	Env string `env:"URL_SHORTENER_ENV" envDefault:"prod" json:"env"`

	// Addr is the TCP address the HTTP server listens on, e.g. ":8080".
	Addr string `env:"URL_SHORTENER_ADDR" envDefault:":8080" json:"addr"`

	// BaseURL is the public origin used to build short-link URLs.
	BaseURL string `env:"URL_SHORTENER_BASE_URL" envDefault:"http://localhost:8080" json:"base_url"`

	// LogLevel is one of "debug", "info", "warn", "error".
	LogLevel string `env:"URL_SHORTENER_LOG_LEVEL" envDefault:"info" json:"log_level"`

	// LogFormat is "text" (human-readable) or "json" (production).
	LogFormat string `env:"URL_SHORTENER_LOG_FORMAT" json:"log_format"`

	// DatabaseURL is a Postgres connection string. Sensitive: redacted by
	// Redacted() and by the JSON marshaller.
	DatabaseURL string `env:"URL_SHORTENER_DATABASE_URL" json:"database_url"`

	// RedisURL is a Redis connection string. Sensitive: redacted by
	// Redacted() and by the JSON marshaller.
	RedisURL string `env:"URL_SHORTENER_REDIS_URL" json:"redis_url"`

	// AutoMigrate, when true (the default), makes `run` apply any pending
	// migrations before starting the HTTP server. The migration path uses a
	// Postgres session-level advisory lock, so multiple replicas starting
	// simultaneously are safe: the first to acquire the lock applies all
	// pending migrations; the others wait, then see no pending work and
	// continue. Set to false when you prefer to run `migrate up` as an
	// explicit, separately-audited step outside the application process.
	AutoMigrate bool `env:"URL_SHORTENER_AUTO_MIGRATE" envDefault:"true" json:"auto_migrate"`

	// CodeLength is the length of auto-generated short codes. Validated by
	// the shortener package: must be in [shortener.MinLength, MaxLength].
	CodeLength int `env:"URL_SHORTENER_CODE_LENGTH" envDefault:"7" json:"code_length"`

	// Postgres connection-pool tunables. All zero by default, in which
	// case pgx's own defaults apply. See store.PoolConfig for the
	// per-knob semantics. Production deployments typically want at
	// least DBMaxConns set above pgx's default of max(4, NumCPU) to
	// absorb burst load without queueing requests on the pool.
	DBMaxConns          int32         `env:"URL_SHORTENER_DB_MAX_CONNS"           json:"db_max_conns"`
	DBMinConns          int32         `env:"URL_SHORTENER_DB_MIN_CONNS"           json:"db_min_conns"`
	DBMaxConnLifetime   time.Duration `env:"URL_SHORTENER_DB_MAX_CONN_LIFETIME"   json:"db_max_conn_lifetime"`
	DBMaxConnIdleTime   time.Duration `env:"URL_SHORTENER_DB_MAX_CONN_IDLE_TIME"  json:"db_max_conn_idle_time"`
	DBHealthCheckPeriod time.Duration `env:"URL_SHORTENER_DB_HEALTH_CHECK_PERIOD" json:"db_health_check_period"`

	// TLSCertFile and TLSKeyFile, when both non-empty, switch the HTTP
	// server to HTTPS on Addr using the PEM-encoded certificate and
	// private key at the given paths. Both must be set together --
	// Validate rejects half-configured pairs. Empty (the default) keeps
	// the server on plain HTTP, which is the right choice when fronted
	// by a TLS-terminating reverse proxy (Caddy/nginx/Traefik).
	TLSCertFile string `env:"URL_SHORTENER_TLS_CERT_FILE" json:"tls_cert_file"`
	TLSKeyFile  string `env:"URL_SHORTENER_TLS_KEY_FILE"  json:"tls_key_file"`

	// RateLimitRPS, when > 0, enables the in-memory rate limiter on
	// `POST /api/v1/links`. The value is the steady-state requests-per-
	// second budget per real client IP (extracted via TrustedProxies
	// when set). 0 -- the default -- disables rate limiting entirely;
	// production deployments are expected to set this explicitly
	// (typical starting point: a handful of req/s for unauthenticated
	// link creation behind a reverse proxy with its own quotas).
	RateLimitRPS float64 `env:"URL_SHORTENER_RATE_LIMIT_RPS"   json:"rate_limit_rps"`

	// RateLimitBurst is the bucket capacity for the rate limiter --
	// the number of requests a single IP can issue at once before the
	// token bucket starts gating on RateLimitRPS. 0 means "derive from
	// RateLimitRPS" (currently 2x the steady-state rate, floored at 1)
	// so the simple "set just RPS" deployment still works. Ignored
	// when RateLimitRPS is 0.
	RateLimitBurst int `env:"URL_SHORTENER_RATE_LIMIT_BURST" json:"rate_limit_burst"`

	// CORSAllowedOrigins is a comma-separated list of allowed Origin
	// header values for cross-origin requests. Each entry is matched
	// exactly (scheme + host + optional port) -- no wildcards on
	// substrings. The literal "*" is accepted to allow any origin
	// (in which case browsers will not send credentials, matching
	// the spec); anything else must parse as an absolute URL.
	// Empty (the default) leaves CORS off entirely, which is correct
	// when the SPA and API share an origin.
	CORSAllowedOrigins []string `env:"URL_SHORTENER_CORS_ALLOWED_ORIGINS" envSeparator:"," json:"cors_allowed_origins"`

	// TrustedProxies is a comma-separated list of CIDR blocks (parsed by
	// net.ParseCIDR) whose request peers are trusted to forward client
	// IP addresses via X-Forwarded-For. When a request's RemoteAddr
	// falls inside one of these ranges, the server walks XFF to find
	// the original client IP; otherwise XFF is ignored and RemoteAddr
	// stands. Empty (the default) means no proxy in front. Set this to
	// e.g. `127.0.0.1/32,10.0.0.0/8` when running behind a reverse proxy.
	TrustedProxies []string `env:"URL_SHORTENER_TRUSTED_PROXIES" envSeparator:"," json:"trusted_proxies"`

	// CacheTTL is how long a positive redirect lookup is cached in
	// Redis. 0 uses the handler default (1 hour).
	CacheTTL time.Duration `env:"URL_SHORTENER_CACHE_TTL" json:"cache_ttl"`

	// NegativeCacheTTL is how long a "not found / gone" answer for
	// /r/:code is held in Redis. 0 uses the handler default (30s).
	NegativeCacheTTL time.Duration `env:"URL_SHORTENER_NEGATIVE_CACHE_TTL" json:"negative_cache_ttl"`
}

// Load reads the configuration from environment variables and applies the
// defaults. It returns an error if the resulting config fails validation.
func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("config: parse env: %w", err)
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

	// Rate-limit knobs: both must be non-negative. RateLimitBurst
	// without RateLimitRPS is silently ignored (the middleware is
	// only installed when RPS > 0), but a negative value is still a
	// clear misconfiguration -- refuse it.
	if c.RateLimitRPS < 0 {
		return fmt.Errorf("config: rate_limit_rps must be >= 0")
	}
	if c.RateLimitBurst < 0 {
		return fmt.Errorf("config: rate_limit_burst must be >= 0")
	}

	// CORS origins: each entry must be either the literal "*" or an
	// absolute URL with scheme + host (no path, no query). Reject
	// anything else early -- a typo like `example.com` (without
	// scheme) silently never matches a real Origin header at
	// runtime, which is a confusing failure mode in production.
	for _, origin := range c.CORSAllowedOrigins {
		if origin == "" {
			continue
		}
		if origin == "*" {
			continue
		}
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" {
			return fmt.Errorf("config: cors_allowed_origins entry %q must be \"*\" or an absolute scheme://host[:port] URL", origin)
		}
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
