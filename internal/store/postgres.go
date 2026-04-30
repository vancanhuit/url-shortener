// Package store provides a Postgres-backed repository for the URL shortener's
// domain entities (currently just `links`).
//
// All methods accept a context.Context for cancellation and a DBTX so callers
// can run them either against a connection pool (the common case) or inside
// a transaction (for multi-statement work in higher layers).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned by Get when the requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrCodeTaken is returned by Create when the unique constraint on
// `links.code` is violated. Callers can treat this as a benign retry signal.
var ErrCodeTaken = errors.New("store: code already taken")

// uniqueViolation is the Postgres SQLSTATE for a unique-constraint violation.
const uniqueViolation = "23505"

// Transient SQLSTATE codes recognized by IsTransient. The list is
// intentionally narrow: every entry must describe a failure that is
// safe to retry against an idempotent statement without changing the
// observed semantics. We deliberately exclude
//
//   - 40002 (transaction_integrity_constraint_violation): a constraint
//     check failed; retrying produces the same result, so this is a
//     real input-shape error, not a transient one.
//   - the 08* connection-exception class: pgxpool already reconnects
//     transparently when checking out a fresh connection; an 08* that
//     reaches the caller means the in-flight statement was lost
//     mid-flight and we don't know whether it committed.
const (
	sqlstateSerializationFailure       = "40001"
	sqlstateStatementCompletionUnknown = "40003"
	sqlstateDeadlockDetected           = "40P01"
)

// IsTransient reports whether err is a transient database failure that
// is safe to retry against an idempotent operation. Currently this
// covers serialization failures, deadlock detections, and statement-
// completion-unknown -- the SQLSTATE codes Postgres uses to signal
// "give it another go and it might just work" against READ COMMITTED
// or higher isolation. Any other error -- including a nil error -- is
// classified as non-transient.
//
// Callers should keep retries bounded (count + total time budget) and
// only apply this on operations that are safe to repeat: the increment
// counter is the canonical example. Do not retry write paths whose
// idempotency depends on application-level invariants without thinking
// through the failure modes first.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case sqlstateSerializationFailure,
		sqlstateStatementCompletionUnknown,
		sqlstateDeadlockDetected:
		return true
	}
	return false
}

// DBTX is satisfied by *pgxpool.Pool (and pgxpool.Conn) as well as pgx.Tx,
// allowing store methods to participate in caller-managed transactions.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Link is a row from the `links` table.
//
// ExpiresAt is nil when the link never expires; callers must use a
// pointer rather than the zero time so "never" and "epoch" stay
// distinguishable through encoding/json.
type Link struct {
	ID         int64
	Code       string
	TargetURL  string
	CreatedAt  time.Time
	ClickCount int64
	ExpiresAt  *time.Time
}

// IsExpired reports whether the link has an expiry set and that expiry
// is in the past. A nil ExpiresAt always returns false ("never expires").
func (l Link) IsExpired() bool {
	return l.ExpiresAt != nil && time.Now().After(*l.ExpiresAt)
}

// Store is the entry point for all DB access. It owns a pgx pool which it
// also exposes via Pool() so higher layers can begin transactions.
type Store struct {
	pool *pgxpool.Pool
}

// PoolConfig collects the pgxpool tunables the service exposes. A zero
// field means "leave the pgxpool default in place" so callers (and
// tests) can opt in to the knobs they care about without restating the
// rest. The defaults pgx ships with are tuned for short-lived clients;
// long-running services typically want a larger MaxConns and a shorter
// MaxConnLifetime to absorb DB-side connection churn (failover, PgBouncer
// rotations, periodic restarts).
type PoolConfig struct {
	// MaxConns is the upper bound on simultaneous connections.
	// pgx default: max(4, runtime.NumCPU()).
	MaxConns int32
	// MinConns is how many idle connections the pool keeps warm.
	// pgx default: 0.
	MinConns int32
	// MaxConnLifetime is the hard cap on a single connection's age,
	// after which it is retired even if otherwise healthy. Useful to
	// rotate through DB-side connection-state drift (prepared
	// statements, search_path) and to ride out floating-IP failovers.
	// pgx default: 1h.
	MaxConnLifetime time.Duration
	// MaxConnIdleTime is how long a connection may sit unused before
	// being closed. pgx default: 30m.
	MaxConnIdleTime time.Duration
	// HealthCheckPeriod is how often pgx scans the pool for stale
	// connections. pgx default: 1m.
	HealthCheckPeriod time.Duration
}

// New opens a pgx pool against databaseURL and returns a Store that owns it.
// Callers must call Close when done.
//
// New leaves the pool tunables at pgx's defaults; callers that need to
// override them (typically the production CLI) should use NewWithPool.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	return NewWithPool(ctx, databaseURL, PoolConfig{})
}

// NewWithPool is New plus the pool-tunable overrides described on
// PoolConfig. Zero fields are left untouched so the per-knob defaults
// remain whatever pgx considers sensible at the version we link.
func NewWithPool(ctx context.Context, databaseURL string, pc PoolConfig) (*Store, error) {
	if databaseURL == "" {
		return nil, errors.New("store: database url is empty")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse url: %w", err)
	}
	applyPoolConfig(cfg, pc)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// applyPoolConfig mutates cfg in place with any non-zero fields from pc.
// Split out so tests can exercise the merge logic without standing up a
// real Postgres.
func applyPoolConfig(cfg *pgxpool.Config, pc PoolConfig) {
	if pc.MaxConns > 0 {
		cfg.MaxConns = pc.MaxConns
	}
	if pc.MinConns > 0 {
		cfg.MinConns = pc.MinConns
	}
	if pc.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = pc.MaxConnLifetime
	}
	if pc.MaxConnIdleTime > 0 {
		cfg.MaxConnIdleTime = pc.MaxConnIdleTime
	}
	if pc.HealthCheckPeriod > 0 {
		cfg.HealthCheckPeriod = pc.HealthCheckPeriod
	}
}

// Close releases the underlying pgx pool.
func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

// Pool returns the underlying connection pool, primarily so callers can begin
// a transaction with pool.BeginTx and then pass the resulting pgx.Tx as the
// DBTX argument to store methods.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// CreateLink inserts a new link row. expiresAt may be nil for a link
// that never expires. It returns ErrCodeTaken when the code collides
// with an existing row.
func (s *Store) CreateLink(ctx context.Context, db DBTX, code, targetURL string, expiresAt *time.Time) (Link, error) {
	if db == nil {
		db = s.pool
	}
	const q = `
		INSERT INTO links (code, target_url, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id, code, target_url, created_at, click_count, expires_at
	`
	var l Link
	err := db.QueryRow(ctx, q, code, targetURL, expiresAt).
		Scan(&l.ID, &l.Code, &l.TargetURL, &l.CreatedAt, &l.ClickCount, &l.ExpiresAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Link{}, ErrCodeTaken
		}
		return Link{}, fmt.Errorf("store: create link: %w", err)
	}
	return l, nil
}

// IncrementClicks bumps the click counter on the link with the given
// code by one. Best-effort: a missing row returns nil (the caller is
// the redirect handler, which has already verified the row exists, so
// a concurrent delete -- if one is ever added -- shouldn't surface as
// an error to the user). Real DB errors are returned so the caller can
// log them.
func (s *Store) IncrementClicks(ctx context.Context, db DBTX, code string) error {
	if db == nil {
		db = s.pool
	}
	const q = `UPDATE links SET click_count = click_count + 1 WHERE code = $1`
	if _, err := db.Exec(ctx, q, code); err != nil {
		return fmt.Errorf("store: increment clicks: %w", err)
	}
	return nil
}

// ListLinks returns up to limit links ordered by id DESC (newest first).
// When beforeID > 0, only links with id < beforeID are returned, enabling
// stable cursor-based pagination over an append-mostly table. id is the
// PRIMARY KEY so the ordering uses the existing btree index for free.
//
// limit is clamped to a sane maximum so callers can pass user-supplied
// page sizes without worrying about runaway queries.
func (s *Store) ListLinks(ctx context.Context, db DBTX, limit int, beforeID int64) ([]Link, error) {
	if db == nil {
		db = s.pool
	}
	const maxLimit = 200
	switch {
	case limit <= 0:
		return nil, nil
	case limit > maxLimit:
		limit = maxLimit
	}

	var (
		rows pgx.Rows
		err  error
	)
	if beforeID > 0 {
		const q = `
			SELECT id, code, target_url, created_at, click_count, expires_at
			FROM links
			WHERE id < $1
			ORDER BY id DESC
			LIMIT $2
		`
		rows, err = db.Query(ctx, q, beforeID, limit)
	} else {
		const q = `
			SELECT id, code, target_url, created_at, click_count, expires_at
			FROM links
			ORDER BY id DESC
			LIMIT $1
		`
		rows, err = db.Query(ctx, q, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("store: list links: %w", err)
	}
	defer rows.Close()

	out := make([]Link, 0, limit)
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.Code, &l.TargetURL, &l.CreatedAt, &l.ClickCount, &l.ExpiresAt); err != nil {
			return nil, fmt.Errorf("store: list links: scan: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list links: rows: %w", err)
	}
	return out, nil
}

// GetLinkByCode looks up a link by its short code. Returns ErrNotFound when
// the row is missing. Expired rows are still returned so callers can
// distinguish 410 from 404 themselves; check Link.IsExpired.
func (s *Store) GetLinkByCode(ctx context.Context, db DBTX, code string) (Link, error) {
	if db == nil {
		db = s.pool
	}
	const q = `
		SELECT id, code, target_url, created_at, click_count, expires_at
		FROM links
		WHERE code = $1
	`
	var l Link
	err := db.QueryRow(ctx, q, code).
		Scan(&l.ID, &l.Code, &l.TargetURL, &l.CreatedAt, &l.ClickCount, &l.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Link{}, ErrNotFound
		}
		return Link{}, fmt.Errorf("store: get link by code: %w", err)
	}
	return l, nil
}

// GetLinkByTargetURL returns the oldest non-expired permanent link with
// the exact target_url. Used by the API to dedupe auto-generated codes
// -- if the caller is shortening a URL that's already in the table,
// return its existing code instead of minting a new one.
//
// Rows that have an expiry set (whether or not yet past) are excluded
// so dedup never reuses an ephemeral link as if it were permanent;
// callers asking for an expiring code already opt out of dedup at the
// handler layer. Multiple permanent rows can legally share a target
// (a user-supplied code is allowed even when an auto-generated row
// already covers the same target), so we deliberately pick the oldest
// by id ASC for stable behavior.
//
// Returns ErrNotFound when no row matches.
func (s *Store) GetLinkByTargetURL(ctx context.Context, db DBTX, targetURL string) (Link, error) {
	if db == nil {
		db = s.pool
	}
	const q = `
		SELECT id, code, target_url, created_at, click_count, expires_at
		FROM links
		WHERE target_url = $1
		  AND expires_at IS NULL
		ORDER BY id ASC
		LIMIT 1
	`
	var l Link
	err := db.QueryRow(ctx, q, targetURL).
		Scan(&l.ID, &l.Code, &l.TargetURL, &l.CreatedAt, &l.ClickCount, &l.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Link{}, ErrNotFound
		}
		return Link{}, fmt.Errorf("store: get link by target url: %w", err)
	}
	return l, nil
}
