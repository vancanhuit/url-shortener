//go:build integration

// End-to-end TLS smoke test: generates a self-signed cert at test time,
// brings up the full server with cfg.TLSCertFile + cfg.TLSKeyFile set,
// hits /healthz over HTTPS, and verifies the handshake + response work.
//
// This exercises the cfg.TLSCertFile branch of server.Serve without
// requiring any cert material to be checked into the repo. Requires
// real Postgres + Redis (URL_SHORTENER_TEST_*), same as the other
// integration tests.

package server_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vancanhuit/url-shortener/internal/cache"
	"github.com/vancanhuit/url-shortener/internal/config"
	"github.com/vancanhuit/url-shortener/internal/server"
	"github.com/vancanhuit/url-shortener/internal/shortener"
	"github.com/vancanhuit/url-shortener/internal/store"
)

// generateSelfSignedCert writes a self-signed P-256 ECDSA certificate
// for 127.0.0.1 + ::1 + localhost into dir as cert.pem / key.pem and
// returns those two paths. The cert is valid for one hour, which is
// effectively forever for a single test run.
func generateSelfSignedCert(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("rand serial: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "url-shortener-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("x509.CreateCertificate: %v", err)
	}

	certPath = filepath.Join(dir, "cert.pem")
	certFile, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("create cert.pem: %v", err)
	}
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("pem encode cert: %v", err)
	}
	if err := certFile.Close(); err != nil {
		t.Fatalf("close cert.pem: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey: %v", err)
	}
	keyPath = filepath.Join(dir, "key.pem")
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("create key.pem: %v", err)
	}
	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		t.Fatalf("pem encode key: %v", err)
	}
	if err := keyFile.Close(); err != nil {
		t.Fatalf("close key.pem: %v", err)
	}
	return certPath, keyPath
}

func TestServer_TLSListenerServesHTTPS(t *testing.T) {
	dbURL := os.Getenv("URL_SHORTENER_TEST_DATABASE_URL")
	redisURL := os.Getenv("URL_SHORTENER_TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("URL_SHORTENER_TEST_DATABASE_URL / URL_SHORTENER_TEST_REDIS_URL not set; skipping integration test")
	}

	certPath, keyPath := generateSelfSignedCert(t, t.TempDir())

	ctx := t.Context()
	st, err := store.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(st.Close)
	cc, err := cache.New(ctx, redisURL)
	if err != nil {
		t.Fatalf("cache.New: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	gen, err := shortener.NewGenerator(shortener.DefaultLength)
	if err != nil {
		t.Fatalf("shortener.NewGenerator: %v", err)
	}

	cfg := config.Config{
		Env:         config.EnvDev,
		Addr:        "127.0.0.1:0",
		BaseURL:     "https://short.test",
		LogLevel:    "info",
		LogFormat:   "text",
		CodeLength:  shortener.DefaultLength,
		TLSCertFile: certPath,
		TLSKeyFile:  keyPath,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := server.New(cfg, logger, server.Deps{Store: st, Cache: cc, Generator: gen})

	runCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(runCtx, ln) }()

	addr := "https://" + ln.Addr().String()

	// Build a client that trusts the test cert by reading it back from
	// disk and adding it to a fresh root pool. This is stricter than
	// InsecureSkipVerify and means the test would catch a regression
	// where ServeTLS served the wrong cert (e.g. a default fallback).
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert.pem: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("AppendCertsFromPEM returned false; cert.pem unusable")
	}
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		Timeout: 5 * time.Second,
	}

	// Poll /healthz until the server is up. The server starts the
	// listener synchronously inside Serve's goroutine but TLS adds a
	// handshake on top, so the first few attempts may race.
	deadline := time.Now().Add(5 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		resp, err = client.Get(addr + "/healthz")
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET %s/healthz over HTTPS: %v", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	// Negative check: a plain-HTTP request to a TLS listener triggers
	// Go's stdlib "client sent an HTTP request to an HTTPS server"
	// canned response, served as 400 Bad Request from inside the TLS
	// state machine. We can't assert err != nil (the response *is*
	// successful at the HTTP level), but the 400 status -- distinct
	// from the 200 we'd see if a plain-HTTP server were also on the
	// listener -- proves the TLS branch was the only one mounted.
	plainResp, plainErr := http.Get("http://" + ln.Addr().String() + "/healthz")
	if plainErr != nil {
		t.Logf("plain-HTTP-to-TLS request errored (also acceptable): %v", plainErr)
	} else {
		defer func() { _ = plainResp.Body.Close() }()
		if plainResp.StatusCode == http.StatusOK {
			t.Errorf("plain-HTTP request to TLS port returned 200; expected 400 or connection error")
		}
	}

	cancel()
	select {
	case serveErr := <-done:
		if serveErr != nil {
			t.Errorf("Serve returned error: %v", serveErr)
		}
	case <-time.After(20 * time.Second):
		t.Error("Serve did not return within 20s of cancellation")
	}
}
