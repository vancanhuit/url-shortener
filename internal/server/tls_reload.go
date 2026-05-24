package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// certReloader serves TLS certificates via tls.Config.GetCertificate and
// hot-reloads keypair files when either path changes on disk.
type certReloader struct {
	certPath string
	keyPath  string
	logger   *slog.Logger

	mu   sync.RWMutex
	cert *tls.Certificate
}

func newCertReloader(certPath, keyPath string, logger *slog.Logger) (*certReloader, error) {
	r := &certReloader{
		certPath: filepath.Clean(certPath),
		keyPath:  filepath.Clean(keyPath),
		logger:   logger,
	}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *certReloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		return fmt.Errorf("tls reload: load key pair: %w", err)
	}
	r.mu.Lock()
	r.cert = &cert
	r.mu.Unlock()
	return nil
}

func (r *certReloader) getCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	cert := r.cert
	r.mu.RUnlock()
	if cert == nil {
		return nil, errors.New("tls reload: certificate not loaded")
	}
	return cert, nil
}

func (r *certReloader) start(ctx context.Context) (func(), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("tls reload: new watcher: %w", err)
	}

	watchDirs := map[string]struct{}{
		filepath.Dir(r.certPath): {},
		filepath.Dir(r.keyPath):  {},
	}
	for dir := range watchDirs {
		if err := w.Add(dir); err != nil {
			_ = w.Close()
			return nil, fmt.Errorf("tls reload: watch %s: %w", dir, err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				r.logger.Warn("tls reload watcher error", "error", err)
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				if !r.isWatchedPath(event.Name) {
					continue
				}
				if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Chmod) == 0 {
					continue
				}
				if err := r.reload(); err != nil {
					r.logger.Warn("tls reload failed", "error", err, "path", event.Name)
					continue
				}
				r.logger.Info("tls certificate reloaded", "path", event.Name)
			}
		}
	}()

	stop := func() {
		_ = w.Close()
		<-done
	}
	return stop, nil
}

func (r *certReloader) isWatchedPath(path string) bool {
	clean := filepath.Clean(path)
	return clean == r.certPath || clean == r.keyPath
}
