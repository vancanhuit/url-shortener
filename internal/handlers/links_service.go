package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vancanhuit/url-shortener/internal/shortener"
	"github.com/vancanhuit/url-shortener/internal/store"
)

// ClassifyPersistError maps an error returned by Persist to a kind,
// the HTTP status code matching that kind, and -- for validation
// failures -- the user-facing message extracted from the error. The
// JSON API and the HTML form share this classification but render the
// non-validation copy ("code already in use" vs "That code is already
// in use.") differently, which is why this returns the parts to plug
// into a response rather than a fully-formed reply.
//
// Internal errors are logged here as a side effect (with op as the
// caller-supplied scope label, e.g. "links: create" or "web: create")
// so callers don't have to repeat the slog call site.
func (h *Links) ClassifyPersistError(op string, err error) (kind PersistErrorKind, status int, msg string) {
	if err == nil {
		return PersistErrNone, 0, ""
	}
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		return PersistErrValidation, http.StatusUnprocessableEntity, verr.Msg
	case errors.Is(err, store.ErrCodeTaken):
		return PersistErrCodeTaken, http.StatusConflict, ""
	default:
		h.logger.Error(op+": persist failed", "error", err)
		return PersistErrInternal, http.StatusInternalServerError, ""
	}
}

// Persist validates the inputs, normalizes the target URL, and either
// creates a new link or returns an existing one (auto-generated codes
// with no expiry only). The returned errors are typed so callers can
// map them to content-appropriate responses:
//
//   - *ValidationError: bad target_url, code, or expiry -> 422
//   - store.ErrCodeTaken: duplicate user-supplied code  -> 409
//   - any other non-nil error: internal failure         -> 500
//
// expiresAt may be nil for a link that never expires. When non-nil it
// must be in the future. Dedup is suppressed whenever expiresAt is
// non-nil: an ephemeral request should never silently extend the
// lifetime of an existing permanent row, and the store layer already
// excludes expiring rows from the dedup lookup so the converse holds
// too.
//
// The boolean `created` is true when a new row was inserted and false
// when an existing row was reused (which only happens when userCode is
// empty AND expiresAt is nil AND a permanent row already covers the
// normalized target); JSON callers translate this into 201 vs 200.
//
// Note: dedup is best-effort. Two simultaneous requests for the same new
// target may both miss the lookup and both insert; this is no worse than
// today's behavior and avoids needing a unique constraint that would
// conflict with user-supplied-code semantics.
func (h *Links) Persist(ctx context.Context, target, userCode string, expiresAt *time.Time) (link store.Link, created bool, err error) {
	if err := validateTargetURL(target); err != nil {
		return store.Link{}, false, &ValidationError{Msg: err.Error()}
	}
	norm, err := normalizeURL(target)
	if err != nil {
		return store.Link{}, false, &ValidationError{Msg: err.Error()}
	}
	if err := validateExpiresAt(expiresAt); err != nil {
		return store.Link{}, false, &ValidationError{Msg: err.Error()}
	}

	if userCode != "" {
		if !shortener.ValidCode(userCode) {
			return store.Link{}, false, &ValidationError{
				Msg: fmt.Sprintf("code must be %d-%d base62 characters",
					shortener.MinLength, shortener.MaxLength),
			}
		}
		link, err = h.store.CreateLink(ctx, nil, userCode, norm, expiresAt)
		if err != nil {
			return store.Link{}, false, err
		}
		h.cachePut(ctx, link)
		return link, true, nil
	}

	// Auto-generated, permanent path: use an atomic get-or-insert so
	// concurrent requests for the same target URL across multiple replicas
	// converge on one row instead of racing to insert duplicates.
	if expiresAt == nil {
		link, created, err = h.createOrReuseAutoLink(ctx, norm)
		if err != nil {
			return store.Link{}, false, err
		}
		if created {
			h.cachePut(ctx, link)
		}
		return link, created, nil
	}

	link, err = h.createWithRandomCode(ctx, norm, expiresAt)
	if err != nil {
		return store.Link{}, false, err
	}
	h.cachePut(ctx, link)
	return link, true, nil
}

// listPage returns up to pageSize links ordered newest-first, plus a
// cursor for the next page (0 when there are no more rows). beforeID,
// when non-zero, advances past a previous page; pass 0 for the first
// page.
//
// Internally it requests pageSize+1 rows so the caller can detect
// "more available" without a separate COUNT query.
func (h *Links) listPage(ctx context.Context, pageSize int, beforeID int64) ([]store.Link, int64, error) {
	if pageSize <= 0 {
		return nil, 0, nil
	}
	rows, err := h.store.ListLinks(ctx, nil, pageSize+1, beforeID)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) <= pageSize {
		return rows, 0, nil
	}
	// Trim the probe row and use the last *kept* row's id as the cursor.
	rows = rows[:pageSize]
	return rows, rows[len(rows)-1].ID, nil
}

// createOrReuseAutoLink generates a random code and calls CreateAutoLink
// in a retry loop. CreateAutoLink is a single atomic INSERT ... ON CONFLICT
// DO UPDATE, so concurrent calls from multiple replicas for the same target
// URL converge on a single row. ErrCodeTaken (code collision on a different
// row) triggers a retry with a fresh code; all other errors are fatal.
//
// Returns (link, true, nil) when a fresh row is inserted and
// (link, false, nil) when an existing permanent row is reused.
func (h *Links) createOrReuseAutoLink(ctx context.Context, target string) (store.Link, bool, error) {
	for i := range CreateMaxRetries {
		code, err := h.gen.Generate()
		if err != nil {
			return store.Link{}, false, fmt.Errorf("generate code: %w", err)
		}
		link, created, err := h.store.CreateAutoLink(ctx, nil, code, target)
		if errors.Is(err, store.ErrCodeTaken) {
			h.logger.Warn("links: code collision; retrying", "attempt", i+1, "code", code)
			continue
		}
		if err != nil {
			return store.Link{}, false, err
		}
		return link, created, nil
	}
	return store.Link{}, false, fmt.Errorf("failed to generate unique code after %d attempts", CreateMaxRetries)
}

// createWithRandomCode generates a fresh code and retries on the rare
// unique-collision. After h.retries failed attempts it gives up: that
// implies either an exhausted keyspace or a degenerate generator and
// should surface as a 500.
func (h *Links) createWithRandomCode(ctx context.Context, target string, expiresAt *time.Time) (store.Link, error) {
	for i := range CreateMaxRetries {
		code, err := h.gen.Generate()
		if err != nil {
			return store.Link{}, fmt.Errorf("generate code: %w", err)
		}
		l, err := h.store.CreateLink(ctx, nil, code, target, expiresAt)
		if errors.Is(err, store.ErrCodeTaken) {
			h.logger.Warn("links: code collision; retrying", "attempt", i+1, "code", code)
			continue
		}
		return l, err
	}
	return store.Link{}, fmt.Errorf("failed to generate unique code after %d attempts", CreateMaxRetries)
}

// validateExpiresAt enforces that an explicit expiry is in the future
// (with a small grace window for honest clock skew between the client
// and server). A nil pointer means "never expires" and is always OK.
func validateExpiresAt(t *time.Time) error {
	if t == nil {
		return nil
	}
	if !t.After(time.Now().Add(-clockSkewGrace)) {
		return errors.New("expires_at must be in the future")
	}
	return nil
}

// normalizeURL returns a canonical form of target suitable for dedup
// lookups: lowercase scheme + host, default port (:80/:443) stripped,
// and a lone "/" path removed. Conservative on purpose -- query and
// fragment are left intact since they're user-meaningful, and
// percent-encoding case (%2A vs %2a, RFC-equivalent) is not touched
// because changing it could change semantics for servers that mis-decode.
// Trailing-dot hostnames (`example.com.`) are also left alone for the
// same reason; in practice no real client emits them.
//
// Returns an error for inputs that would not pass validateTargetURL; in
// practice callers should validate first, but this function is defensive.
func normalizeURL(target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	// Strip the default port. The leading ":" anchors the suffix match,
	// so something like ":8080" cannot accidentally be stripped: the
	// last 3 bytes of "...:8080" are "080", not ":80".
	switch {
	case u.Scheme == "http" && strings.HasSuffix(host, ":80"):
		host = strings.TrimSuffix(host, ":80")
	case u.Scheme == "https" && strings.HasSuffix(host, ":443"):
		host = strings.TrimSuffix(host, ":443")
	}
	u.Host = host
	// A bare "/" path is equivalent to no path; drop it for stable dedup.
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), nil
}

// privateRanges holds the CIDR blocks that must never appear as redirect
// targets: loopback, RFC-1918 private, link-local, carrier-grade NAT, and
// IPv6 unique-local. Initialized once at package load via an init-style
// variable so the parse cost is paid only once.
var privateRanges = func() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",    // IPv4 loopback
		"::1/128",        // IPv6 loopback
		"10.0.0.0/8",     // RFC 1918
		"172.16.0.0/12",  // RFC 1918
		"192.168.0.0/16", // RFC 1918
		"169.254.0.0/16", // IPv4 link-local (APIPA / AWS IMDS)
		"fe80::/10",      // IPv6 link-local
		"fc00::/7",       // IPv6 unique-local (fd00::/8 is a subset)
		"100.64.0.0/10",  // Carrier-grade NAT (RFC 6598)
		"0.0.0.0/8",      // "This" network
		"240.0.0.0/4",    // Reserved
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipnet, _ := net.ParseCIDR(cidr)
		out = append(out, ipnet)
	}
	return out
}()

// isPrivateHost reports whether the host component of a URL (including any
// optional port) resolves to an address that must not be used as a redirect
// target. It blocks IP literals in private/loopback/link-local ranges and
// the bare hostname "localhost". DNS resolution is intentionally avoided:
// it would add per-request latency and is vulnerable to DNS rebinding; any
// attack that requires a custom hostname is out of scope for this check.
func isPrivateHost(host string) bool {
	var hostname string
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	} else {
		// SplitHostPort fails for bare IPv6 like "[::1]" (no port).
		// Strip the brackets so net.ParseIP can handle it.
		hostname = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}
	// Drop any IPv6 zone identifier (e.g. "fe80::1%eth0"); ParseIP
	// rejects scoped addresses, but the scope is meaningless in a URL
	// and stripping it lets the link-local check below catch it.
	if i := strings.Index(hostname, "%"); i >= 0 {
		hostname = hostname[:i]
	}
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	if ip == nil {
		return false
	}
	for _, block := range privateRanges {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// validateTargetURL enforces the rules the API contract advertises:
// non-empty, length-capped, parseable, http(s) scheme, non-empty host,
// and a host that is not a private/loopback/link-local address.
func validateTargetURL(s string) error {
	if s == "" {
		return errors.New("target_url is required")
	}
	if len(s) > TargetURLMaxLen {
		return fmt.Errorf("target_url exceeds %d characters", TargetURLMaxLen)
	}
	u, err := url.Parse(s)
	if err != nil {
		return errors.New("target_url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("target_url must use http or https")
	}
	if u.Host == "" {
		return errors.New("target_url must have a host")
	}
	if isPrivateHost(u.Host) {
		return errors.New("target_url must not point to a private or internal address")
	}
	return nil
}
