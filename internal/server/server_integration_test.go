//go:build integration

// End-to-end tests for the HTTP server lifecycle. Run with:
//
//	just test-integration
//
// These tests bind a real TCP listener (on a random port) and hit the
// running server through the network, exercising the full middleware chain
// and the graceful-shutdown path.

package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// waitForReady polls /healthz until it returns 200 or the deadline expires.
func waitForReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready", url)
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	out := map[string]any{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode body %q: %v", body, err)
		}
	}
	return resp.StatusCode, out
}

func TestServer_OperationalEndpointsOverRealNetwork(t *testing.T) {
	base, stop := startFullServer(t)
	defer stop()

	t.Run("healthz", func(t *testing.T) {
		code, body := getJSON(t, base+"/healthz")
		if code != http.StatusOK {
			t.Errorf("status = %d, want 200", code)
		}
		if body["status"] != "ok" {
			t.Errorf("body = %v", body)
		}
	})

	t.Run("readyz", func(t *testing.T) {
		// Postgres + Redis checks are registered against the real
		// dependencies wired in startFullServer; both should be
		// reachable in the test environment, so /readyz returns 200.
		code, body := getJSON(t, base+"/readyz")
		if code != http.StatusOK {
			t.Errorf("status = %d, want 200", code)
		}
		if body["status"] != "ok" {
			t.Errorf("body = %v", body)
		}
	})

	t.Run("version", func(t *testing.T) {
		code, body := getJSON(t, base+"/version")
		if code != http.StatusOK {
			t.Errorf("status = %d, want 200", code)
		}
		for _, key := range []string{"version", "commit", "date"} {
			if _, ok := body[key]; !ok {
				t.Errorf("missing %q in %v", key, body)
			}
		}
	})

	t.Run("unknown route 404", func(t *testing.T) {
		code, _ := getJSON(t, base+"/no-such-thing")
		if code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", code)
		}
	})
}

func TestServer_RejectsOversizedRequestBody(t *testing.T) {
	base, stop := startFullServer(t)
	defer stop()

	// 1 MiB of payload is two orders of magnitude over the 16 KiB cap;
	// /healthz is a route that doesn't otherwise read the body, so a
	// successful 413 here proves the BodyLimit middleware fires before
	// the request reaches any handler. The path is irrelevant -- any
	// route under the global middleware chain will do.
	payload := bytes.Repeat([]byte("x"), 1<<20)
	req, err := http.NewRequestWithContext(t.Context(),
		http.MethodPost, base+"/healthz", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestServer_GracefulShutdownClosesListener(t *testing.T) {
	base, stop := startFullServer(t)

	// Verify the server is responsive.
	if code, _ := getJSON(t, base+"/healthz"); code != http.StatusOK {
		t.Fatalf("pre-shutdown healthz = %d", code)
	}

	stop() // cancels context, waits for Serve to return

	// After shutdown a fresh dial must fail (listener closed).
	host := base[len("http://"):]
	conn, err := net.DialTimeout("tcp", host, 500*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Errorf("expected dial to %s to fail after shutdown", host)
	}
}

// TestMain runs every test in this file with a small global guard so a hung
// listener can't keep CI tied up forever.
func TestMain(m *testing.M) {
	exit := make(chan int, 1)
	go func() { exit <- m.Run() }()
	select {
	case code := <-exit:
		os.Exit(code)
	case <-time.After(60 * time.Second):
		_, _ = os.Stderr.WriteString("server integration tests exceeded 60s\n")
		os.Exit(1)
	}
}
