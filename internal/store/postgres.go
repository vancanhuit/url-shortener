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

// DBTX is satisfied by *pgxpool.Pool (and pgxpool.Conn) as well as pgx.Tx,
// allowing store methods to participate in caller-managed transactions.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Link is a row from the `links` table.
type Link struct {
	ID        int64
	Code      string
	TargetURL string
	CreatedAt time.Time
}

// Store is the entry point for all DB access. It owns a pgx pool which it
// also exposes via Pool() so higher layers can begin transactions.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a pgx pool against databaseURL and returns a Store that owns it.
// Callers must call Close when done.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	if databaseURL == "" {
		return nil, errors.New("store: database url is empty")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool}, nil
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

// CreateLink inserts a new link row. It returns ErrCodeTaken when the code
// collides with an existing row.
func (s *Store) CreateLink(ctx context.Context, db DBTX, code, targetURL string) (Link, error) {
	if db == nil {
		db = s.pool
	}
	const q = `
		INSERT INTO links (code, target_url)
		VALUES ($1, $2)
		RETURNING id, code, target_url, created_at
	`
	var l Link
	err := db.QueryRow(ctx, q, code, targetURL).
		Scan(&l.ID, &l.Code, &l.TargetURL, &l.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Link{}, ErrCodeTaken
		}
		return Link{}, fmt.Errorf("store: create link: %w", err)
	}
	return l, nil
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
			SELECT id, code, target_url, created_at
			FROM links
			WHERE id < $1
			ORDER BY id DESC
			LIMIT $2
		`
		rows, err = db.Query(ctx, q, beforeID, limit)
	} else {
		const q = `
			SELECT id, code, target_url, created_at
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
		if err := rows.Scan(&l.ID, &l.Code, &l.TargetURL, &l.CreatedAt); err != nil {
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
// the row is missing.
func (s *Store) GetLinkByCode(ctx context.Context, db DBTX, code string) (Link, error) {
	if db == nil {
		db = s.pool
	}
	const q = `
		SELECT id, code, target_url, created_at
		FROM links
		WHERE code = $1
	`
	var l Link
	err := db.QueryRow(ctx, q, code).
		Scan(&l.ID, &l.Code, &l.TargetURL, &l.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Link{}, ErrNotFound
		}
		return Link{}, fmt.Errorf("store: get link by code: %w", err)
	}
	return l, nil
}

// GetLinkByTargetURL returns the oldest existing link with the exact
// target_url. Used by the API to dedupe auto-generated codes -- if the
// caller is shortening a URL that's already in the table, return its
// existing code instead of minting a new one. Multiple rows can legally
// share a target (a user-supplied code is allowed even when an
// auto-generated row already covers the same target), so we deliberately
// pick the oldest by id ASC for stable behaviour.
//
// Returns ErrNotFound when no row matches.
func (s *Store) GetLinkByTargetURL(ctx context.Context, db DBTX, targetURL string) (Link, error) {
	if db == nil {
		db = s.pool
	}
	const q = `
		SELECT id, code, target_url, created_at
		FROM links
		WHERE target_url = $1
		ORDER BY id ASC
		LIMIT 1
	`
	var l Link
	err := db.QueryRow(ctx, q, targetURL).
		Scan(&l.ID, &l.Code, &l.TargetURL, &l.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Link{}, ErrNotFound
		}
		return Link{}, fmt.Errorf("store: get link by target url: %w", err)
	}
	return l, nil
}
