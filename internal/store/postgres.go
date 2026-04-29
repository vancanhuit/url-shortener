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
