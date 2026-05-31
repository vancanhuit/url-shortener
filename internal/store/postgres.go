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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vancanhuit/url-shortener/internal/store/db"
)

// ErrNotFound is returned when a requested row does not exist or has already
// been soft-deleted and the query filters on deleted_at IS NULL.
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

// DBTX allows store methods to participate in caller-managed transactions.
// It is an alias for db.DBTX, which is satisfied by pgx.Tx.
//
// Convention: pass nil to use the store's own connection pool (the common
// case -- every current call site does this). Pass a pgx.Tx obtained from
// s.Pool().Begin() only when multiple store operations must be atomic; each
// method checks for nil and falls back to s.pool automatically.
type DBTX = db.DBTX

// Link is a row from the `links` table.
//
// ExpiresAt and DeletedAt are nil for the common case (never expires,
// not deleted). Callers must use pointers rather than the zero time
// so "never set" stays distinguishable from "set to epoch" through
// encoding/json and through Postgres NULL handling.
type Link struct {
	ID         int64
	Code       string
	TargetURL  string
	CreatedAt  time.Time
	ClickCount int64
	ExpiresAt  *time.Time
	DeletedAt  *time.Time
}

// IsExpired reports whether the link has an expiry set and that expiry
// is in the past. A nil ExpiresAt always returns false ("never expires").
func (l Link) IsExpired() bool {
	return l.ExpiresAt != nil && time.Now().After(*l.ExpiresAt)
}

// IsDeleted reports whether the link has been soft-deleted. A nil
// DeletedAt always returns false ("not deleted"). The handler layer
// uses this to surface 410 Gone for retired links the same way it
// does for expired ones, while the dedup / list paths filter deleted
// rows out at the SQL level.
func (l Link) IsDeleted() bool {
	return l.DeletedAt != nil
}

// Store is the entry point for all DB access. It owns a pgx pool which it
// also exposes via Pool() so higher layers can begin transactions.
type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
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
	return &Store{pool: pool, queries: db.New(pool)}, nil
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

// Pool returns the underlying connection pool. Production callers should
// prefer Ping for liveness checks and WithTx for atomic multi-statement
// work; Pool is exposed so integration tests and ad-hoc admin scripts can
// run raw SQL (e.g. DELETE for fixture cleanup) without growing the
// store API for one-off operations.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ping checks the pool's liveness. It is the production-friendly
// alternative to Pool().Ping when callers only need a readiness probe.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// WithTx runs fn inside a database transaction. The transaction is
// committed when fn returns nil and rolled back otherwise (including
// when fn panics, in which case the panic is re-raised after rollback).
// Pass the supplied DBTX to store methods so they share the transaction.
//
// WithTx is the preferred way for callers to compose multiple store
// operations atomically without reaching into Pool() to manage the
// transaction lifecycle themselves.
func (s *Store) WithTx(ctx context.Context, fn func(DBTX) error) (err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			// Best-effort rollback; ignore its error so the
			// original panic is not masked.
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("store: rollback: %w", rbErr))
			}
			return
		}
		if cmErr := tx.Commit(ctx); cmErr != nil {
			err = fmt.Errorf("store: commit tx: %w", cmErr)
		}
	}()
	return fn(tx)
}

// queriesFor returns a *db.Queries backed by dbtx when non-nil, otherwise
// returns the store's own pool-backed Queries. This avoids allocating a
// new Queries on every call when using the pool (the common case).
func (s *Store) queriesFor(dbtx DBTX) *db.Queries {
	if dbtx != nil {
		return db.New(dbtx)
	}
	return s.queries
}

// toLink converts a generated db.Link (which uses pgtype.Timestamptz) to
// the public store.Link type (which uses time.Time / *time.Time).
func toLink(l db.Link) Link {
	return Link{
		ID:         l.ID,
		Code:       l.Code,
		TargetURL:  l.TargetUrl,
		CreatedAt:  l.CreatedAt.Time,
		ClickCount: l.ClickCount,
		ExpiresAt:  pgTimestamptzToPtr(l.ExpiresAt),
		DeletedAt:  pgTimestamptzToPtr(l.DeletedAt),
	}
}

// pgTimestamptzToPtr converts a pgtype.Timestamptz to *time.Time.
// Returns nil when the column was NULL (Valid == false).
func pgTimestamptzToPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

// timePtrToTimestamptz converts a *time.Time to pgtype.Timestamptz.
// A nil pointer maps to the zero Timestamptz (Valid == false, i.e. NULL).
func timePtrToTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// CreateLink inserts a new link row. expiresAt may be nil for a link
// that never expires. It returns ErrCodeTaken when the code collides
// with an existing row.
func (s *Store) CreateLink(ctx context.Context, dbtx DBTX, code, targetURL string, expiresAt *time.Time) (Link, error) {
	row, err := s.queriesFor(dbtx).CreateLink(ctx, db.CreateLinkParams{
		Code:      code,
		TargetUrl: targetURL,
		ExpiresAt: timePtrToTimestamptz(expiresAt),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Link{}, ErrCodeTaken
		}
		return Link{}, fmt.Errorf("store: create link: %w", err)
	}
	return toLink(row), nil
}

// CreateAutoLink atomically inserts a permanent auto-generated link or
// returns the existing one when a live, permanent auto-dedup row already
// covers targetURL. The operation is a single
// INSERT ... ON CONFLICT DO UPDATE RETURNING so it is safe across
// multiple concurrent replicas with no external locking.
//
// created is true when a fresh row was inserted and false when an
// existing row was returned (dedup hit). ErrCodeTaken is returned when
// the proposed code collides with a different existing row on the unique
// code constraint; callers should generate a fresh code and retry.
func (s *Store) CreateAutoLink(ctx context.Context, dbtx DBTX, code, targetURL string) (Link, bool, error) {
	row, err := s.queriesFor(dbtx).CreateAutoLink(ctx, db.CreateAutoLinkParams{
		Code:      code,
		TargetUrl: targetURL,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Link{}, false, ErrCodeTaken
		}
		return Link{}, false, fmt.Errorf("store: create auto link: %w", err)
	}
	// A freshly inserted row comes back with the code we proposed.
	// An ON CONFLICT hit returns the existing row's code instead.
	created := row.Code == code
	return toLink(row), created, nil
}

// IncrementClicks bumps the click counter on the link with the given
// code by one. Best-effort: a missing or soft-deleted row returns nil
// (the caller is the redirect handler, which has already verified the
// row exists, so a concurrent soft-delete shouldn't surface as an error
// to the user). Real DB errors are returned so the caller can log them.
func (s *Store) IncrementClicks(ctx context.Context, dbtx DBTX, code string) error {
	if err := s.queriesFor(dbtx).IncrementClicks(ctx, code); err != nil {
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
func (s *Store) ListLinks(ctx context.Context, dbtx DBTX, limit int, beforeID int64) ([]Link, error) {
	const maxLimit = 200
	switch {
	case limit <= 0:
		return nil, nil
	case limit > maxLimit:
		limit = maxLimit
	}

	q := s.queriesFor(dbtx)
	var (
		rows []db.Link
		err  error
	)
	// Soft-deleted and expired rows are filtered out of the recent list.
	if beforeID > 0 {
		rows, err = q.ListLinksBeforeID(ctx, db.ListLinksBeforeIDParams{
			ID:    beforeID,
			Limit: int32(limit),
		})
	} else {
		rows, err = q.ListLinks(ctx, int32(limit))
	}
	if err != nil {
		return nil, fmt.Errorf("store: list links: %w", err)
	}

	out := make([]Link, len(rows))
	for i, row := range rows {
		out[i] = toLink(row)
	}
	return out, nil
}

// GetLinkByCode looks up a link by its short code. Returns ErrNotFound when
// the row is missing. Expired and soft-deleted rows are still returned
// so callers can distinguish 410 from 404 themselves; check
// Link.IsExpired and Link.IsDeleted.
func (s *Store) GetLinkByCode(ctx context.Context, dbtx DBTX, code string) (Link, error) {
	row, err := s.queriesFor(dbtx).GetLinkByCode(ctx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Link{}, ErrNotFound
		}
		return Link{}, fmt.Errorf("store: get link by code: %w", err)
	}
	return toLink(row), nil
}

// GetLinkByTargetURL returns the oldest non-expired, non-deleted
// permanent link with the exact target_url. Used by the API to dedupe
// auto-generated codes -- if the caller is shortening a URL that's
// already in the table, return its existing code instead of minting a
// new one.
//
// Rows that have an expiry set (whether or not yet past) are excluded
// so dedup never reuses an ephemeral link as if it were permanent;
// callers asking for an expiring code already opt out of dedup at the
// handler layer. Soft-deleted rows are likewise excluded -- a retired
// link must never be silently re-served as a dedup hit. Multiple
// permanent rows can legally share a target (a user-supplied code is
// allowed even when an auto-generated row already covers the same
// target), so we deliberately pick the oldest by id ASC for stable
// behavior.
//
// Returns ErrNotFound when no row matches.
func (s *Store) GetLinkByTargetURL(ctx context.Context, dbtx DBTX, targetURL string) (Link, error) {
	row, err := s.queriesFor(dbtx).GetLinkByTargetURL(ctx, targetURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Link{}, ErrNotFound
		}
		return Link{}, fmt.Errorf("store: get link by target url: %w", err)
	}
	return toLink(row), nil
}

// SoftDeleteLink marks the link with the given code as deleted by
// stamping deleted_at = now(). It is not idempotent: the WHERE clause
// filters on deleted_at IS NULL, so a second call (or any call against
// a non-existent or already-deleted code) returns ErrNotFound via the
// zero RowsAffected count. Higher layers translate ErrNotFound to 404
// the same way they do for GetLinkByCode.
//
// The row is left in the table -- expires_at, click_count, and
// every audit column survive the delete -- so a future undelete
// recipe could clear deleted_at and bring the link back if the
// product ever needs it.
func (s *Store) SoftDeleteLink(ctx context.Context, dbtx DBTX, code string) error {
	tag, err := s.queriesFor(dbtx).SoftDeleteLink(ctx, code)
	if err != nil {
		return fmt.Errorf("store: soft delete link: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgeExpiredAndDeleted hard-deletes links that have been retired --
// soft-deleted (deleted_at IS NOT NULL) or past their expires_at -- for
// longer than the grace window. Negative or zero grace is rejected;
// callers that want "purge everything retired right now" should pass
// a small positive value (e.g. 1 second) explicitly so the operational
// intent is in the audit log, not implicit.
//
// Returns the number of rows physically removed.
func (s *Store) PurgeExpiredAndDeleted(ctx context.Context, dbtx DBTX, grace time.Duration) (int64, error) {
	if grace <= 0 {
		return 0, errors.New("store: purge grace must be > 0")
	}
	tag, err := s.queriesFor(dbtx).PurgeExpiredAndDeleted(ctx, grace.Seconds())
	if err != nil {
		return 0, fmt.Errorf("store: purge expired/deleted: %w", err)
	}
	return tag.RowsAffected(), nil
}
