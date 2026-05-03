package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vancanhuit/url-shortener/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	// Postgres + Redis are required fields; set the minimum needed for
	// Load() to succeed.
	t.Setenv("URL_SHORTENER_DATABASE_URL", "postgres://u:p@h:5432/db")
	t.Setenv("URL_SHORTENER_REDIS_URL", "redis://localhost:6379/0")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if cfg.Env != config.EnvProd {
		t.Errorf("Env = %q, want %q", cfg.Env, config.EnvProd)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json (prod default)", cfg.LogFormat)
	}
	if cfg.CodeLength != 7 {
		t.Errorf("CodeLength = %d, want 7", cfg.CodeLength)
	}
}

func TestLoad_DevDefaultsLogFormatToText(t *testing.T) {
	clearEnv(t)
	t.Setenv("URL_SHORTENER_ENV", "dev")
	t.Setenv("URL_SHORTENER_DATABASE_URL", "postgres://u:p@h:5432/db")
	t.Setenv("URL_SHORTENER_REDIS_URL", "redis://localhost:6379/0")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("LogFormat = %q, want text in dev", cfg.LogFormat)
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("URL_SHORTENER_ENV", "dev")
	t.Setenv("URL_SHORTENER_ADDR", ":9000")
	t.Setenv("URL_SHORTENER_BASE_URL", "http://example.test")
	t.Setenv("URL_SHORTENER_LOG_LEVEL", "debug")
	t.Setenv("URL_SHORTENER_LOG_FORMAT", "json")
	t.Setenv("URL_SHORTENER_DATABASE_URL", "postgres://u:p@h:5432/db")
	t.Setenv("URL_SHORTENER_REDIS_URL", "redis://h:6379/0")
	t.Setenv("URL_SHORTENER_AUTO_MIGRATE", "true")
	t.Setenv("URL_SHORTENER_CODE_LENGTH", "9")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	want := config.Config{
		Env:         "dev",
		Addr:        ":9000",
		BaseURL:     "http://example.test",
		LogLevel:    "debug",
		LogFormat:   "json",
		DatabaseURL: "postgres://u:p@h:5432/db",
		RedisURL:    "redis://h:6379/0",
		AutoMigrate: true,
		CodeLength:  9,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("cfg = %+v\nwant %+v", cfg, want)
	}
}

func TestLoad_DBPoolEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("URL_SHORTENER_DATABASE_URL", "postgres://u:p@h:5432/db")
	t.Setenv("URL_SHORTENER_REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("URL_SHORTENER_DB_MAX_CONNS", "32")
	t.Setenv("URL_SHORTENER_DB_MIN_CONNS", "4")
	t.Setenv("URL_SHORTENER_DB_MAX_CONN_LIFETIME", "30m")
	t.Setenv("URL_SHORTENER_DB_MAX_CONN_IDLE_TIME", "5m")
	t.Setenv("URL_SHORTENER_DB_HEALTH_CHECK_PERIOD", "15s")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if cfg.DBMaxConns != 32 {
		t.Errorf("DBMaxConns = %d, want 32", cfg.DBMaxConns)
	}
	if cfg.DBMinConns != 4 {
		t.Errorf("DBMinConns = %d, want 4", cfg.DBMinConns)
	}
	if cfg.DBMaxConnLifetime != 30*time.Minute {
		t.Errorf("DBMaxConnLifetime = %v, want 30m", cfg.DBMaxConnLifetime)
	}
	if cfg.DBMaxConnIdleTime != 5*time.Minute {
		t.Errorf("DBMaxConnIdleTime = %v, want 5m", cfg.DBMaxConnIdleTime)
	}
	if cfg.DBHealthCheckPeriod != 15*time.Second {
		t.Errorf("DBHealthCheckPeriod = %v, want 15s", cfg.DBHealthCheckPeriod)
	}
}

func TestValidate_RejectsBadDBPoolValues(t *testing.T) {
	t.Parallel()

	const (
		redisURL    = "redis://localhost:6379/0"
		databaseURL = "postgres://u:p@h:5432/db"
	)
	base := func() config.Config {
		return config.Config{
			Env: "prod", Addr: ":8080", BaseURL: "x",
			LogLevel: "info", LogFormat: "json",
			DatabaseURL: databaseURL, RedisURL: redisURL,
		}
	}

	cases := map[string]func(*config.Config){
		"negative max": func(c *config.Config) { c.DBMaxConns = -1 },
		"negative min": func(c *config.Config) { c.DBMinConns = -1 },
		"min greater": func(c *config.Config) {
			c.DBMaxConns = 5
			c.DBMinConns = 10
		},
		"negative lifetime":  func(c *config.Config) { c.DBMaxConnLifetime = -time.Second },
		"negative idle":      func(c *config.Config) { c.DBMaxConnIdleTime = -time.Second },
		"negative healthchk": func(c *config.Config) { c.DBHealthCheckPeriod = -time.Second },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := base()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Errorf("Validate() returned nil; want error")
			}
		})
	}
}

func TestValidate_RejectsBadValues(t *testing.T) {
	t.Parallel()

	// Every case sets non-empty DatabaseURL + RedisURL so the failure is
	// attributable to the field under test, except for the dedicated
	// empty-database-url and empty-redis-url cases.
	const (
		redisURL    = "redis://localhost:6379/0"
		databaseURL = "postgres://u:p@h:5432/db"
	)
	cases := map[string]config.Config{
		"bad env":            {Env: "staging", Addr: ":8080", BaseURL: "x", LogLevel: "info", LogFormat: "json", DatabaseURL: databaseURL, RedisURL: redisURL},
		"bad log_level":      {Env: "prod", Addr: ":8080", BaseURL: "x", LogLevel: "trace", LogFormat: "json", DatabaseURL: databaseURL, RedisURL: redisURL},
		"bad log_format":     {Env: "prod", Addr: ":8080", BaseURL: "x", LogLevel: "info", LogFormat: "yaml", DatabaseURL: databaseURL, RedisURL: redisURL},
		"empty addr":         {Env: "prod", Addr: "", BaseURL: "x", LogLevel: "info", LogFormat: "json", DatabaseURL: databaseURL, RedisURL: redisURL},
		"empty baseurl":      {Env: "prod", Addr: ":8080", BaseURL: "", LogLevel: "info", LogFormat: "json", DatabaseURL: databaseURL, RedisURL: redisURL},
		"empty database_url": {Env: "prod", Addr: ":8080", BaseURL: "x", LogLevel: "info", LogFormat: "json", DatabaseURL: "", RedisURL: redisURL},
		"empty redis_url":    {Env: "prod", Addr: ":8080", BaseURL: "x", LogLevel: "info", LogFormat: "json", DatabaseURL: databaseURL, RedisURL: ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := c.Validate(); err == nil {
				t.Errorf("Validate() returned nil; want error")
			}
		})
	}
}

func TestRedacted_StripsPasswords(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		DatabaseURL: "postgres://user:secret@db:5432/url_shortener",
		RedisURL:    "redis://:topsecret@cache:6379/0",
	}
	r := cfg.Redacted()
	if strings.Contains(r.DatabaseURL, "secret") {
		t.Errorf("DatabaseURL still contains password: %q", r.DatabaseURL)
	}
	if strings.Contains(r.RedisURL, "topsecret") {
		t.Errorf("RedisURL still contains password: %q", r.RedisURL)
	}
	if !strings.Contains(r.DatabaseURL, "REDACTED") {
		t.Errorf("DatabaseURL = %q, expected REDACTED marker", r.DatabaseURL)
	}
	if !strings.Contains(cfg.DatabaseURL, "secret") {
		t.Error("Redacted() must not mutate the receiver")
	}
}

func TestLoad_TLSAndTrustedProxiesEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("URL_SHORTENER_DATABASE_URL", "postgres://u:p@h:5432/db")
	t.Setenv("URL_SHORTENER_REDIS_URL", "redis://localhost:6379/0")
	// Files must exist for Validate's stat check to pass; create
	// scratch files with arbitrary content (Validate doesn't try to
	// parse the PEM, only Stat the path).
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, []byte("not a real cert"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("not a real key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	t.Setenv("URL_SHORTENER_TLS_CERT_FILE", certPath)
	t.Setenv("URL_SHORTENER_TLS_KEY_FILE", keyPath)
	// Comma-separated CIDR list -- viper's default decode hooks split
	// strings on commas when targeting a []string field.
	t.Setenv("URL_SHORTENER_TRUSTED_PROXIES", "127.0.0.1/32,10.0.0.0/8")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if cfg.TLSCertFile != certPath {
		t.Errorf("TLSCertFile = %q, want %q", cfg.TLSCertFile, certPath)
	}
	if cfg.TLSKeyFile != keyPath {
		t.Errorf("TLSKeyFile = %q, want %q", cfg.TLSKeyFile, keyPath)
	}
	wantProxies := []string{"127.0.0.1/32", "10.0.0.0/8"}
	if !reflect.DeepEqual(cfg.TrustedProxies, wantProxies) {
		t.Errorf("TrustedProxies = %v, want %v", cfg.TrustedProxies, wantProxies)
	}
}

func TestValidate_TLSAndTrustedProxies(t *testing.T) {
	t.Parallel()

	const (
		redisURL    = "redis://localhost:6379/0"
		databaseURL = "postgres://u:p@h:5432/db"
	)
	// realDir + realCert/realKey are paths that exist, used by the
	// happy-path subtests. Each subtest gets its own t.TempDir to
	// keep them parallel-safe.
	base := func(t *testing.T) (config.Config, string, string) {
		t.Helper()
		dir := t.TempDir()
		certPath := filepath.Join(dir, "cert.pem")
		keyPath := filepath.Join(dir, "key.pem")
		if err := os.WriteFile(certPath, []byte("x"), 0o600); err != nil {
			t.Fatalf("write cert: %v", err)
		}
		if err := os.WriteFile(keyPath, []byte("x"), 0o600); err != nil {
			t.Fatalf("write key: %v", err)
		}
		return config.Config{
			Env: "prod", Addr: ":8080", BaseURL: "http://x",
			LogLevel: "info", LogFormat: "json",
			DatabaseURL: databaseURL, RedisURL: redisURL,
		}, certPath, keyPath
	}

	t.Run("cert without key is rejected", func(t *testing.T) {
		t.Parallel()
		c, certPath, _ := base(t)
		c.TLSCertFile = certPath
		if err := c.Validate(); err == nil {
			t.Error("Validate() returned nil; want error for unpaired cert")
		}
	})

	t.Run("key without cert is rejected", func(t *testing.T) {
		t.Parallel()
		c, _, keyPath := base(t)
		c.TLSKeyFile = keyPath
		if err := c.Validate(); err == nil {
			t.Error("Validate() returned nil; want error for unpaired key")
		}
	})

	t.Run("missing cert file is rejected", func(t *testing.T) {
		t.Parallel()
		c, _, keyPath := base(t)
		c.TLSCertFile = filepath.Join(t.TempDir(), "nope.pem")
		c.TLSKeyFile = keyPath
		if err := c.Validate(); err == nil {
			t.Error("Validate() returned nil; want error for missing cert path")
		}
	})

	t.Run("both files set and present passes", func(t *testing.T) {
		t.Parallel()
		c, certPath, keyPath := base(t)
		c.TLSCertFile = certPath
		c.TLSKeyFile = keyPath
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() = %v; want nil", err)
		}
	})

	t.Run("invalid CIDR is rejected", func(t *testing.T) {
		t.Parallel()
		c, _, _ := base(t)
		c.TrustedProxies = []string{"not-a-cidr"}
		if err := c.Validate(); err == nil {
			t.Error("Validate() returned nil; want error for bad CIDR")
		}
	})

	t.Run("multiple valid CIDRs pass", func(t *testing.T) {
		t.Parallel()
		c, _, _ := base(t)
		c.TrustedProxies = []string{"127.0.0.1/32", "10.0.0.0/8", "::1/128"}
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() = %v; want nil", err)
		}
	})

	t.Run("empty entries are skipped", func(t *testing.T) {
		t.Parallel()
		c, _, _ := base(t)
		// Stray comma in env var produces an empty entry; should not
		// fail validation since the consumer ignores empties too.
		c.TrustedProxies = []string{"127.0.0.1/32", ""}
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() = %v; want nil", err)
		}
	})
}

// clearEnv unsets every URL_SHORTENER_* env var for the duration of the test
// and restores the original values on cleanup. Tests that call this cannot
// be run in parallel with each other (env is process-global).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		k := kv[:i]
		if !strings.HasPrefix(k, config.EnvPrefix+"_") {
			continue
		}
		orig := kv[i+1:]
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("Unsetenv(%q): %v", k, err)
		}
		t.Cleanup(func() {
			_ = os.Setenv(k, orig)
		})
	}
}
