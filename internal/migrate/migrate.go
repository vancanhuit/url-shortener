// Package migrate wraps goose to apply the SQL files embedded in the
// migrations package against a Postgres database.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/vancanhuit/url-shortener/migrations"
)

// dialect is the goose dialect name for Postgres.
const dialect = "postgres"

// migrationsDir is the virtual directory inside the embedded FS. Goose uses
// this to namespace its file lookups; "." means "the FS root".
const migrationsDir = "."

// Up applies all pending migrations.
func Up(ctx context.Context, databaseURL string) error {
	return run(ctx, databaseURL, func(db *sql.DB) error {
		return goose.UpContext(ctx, db, migrationsDir)
	})
}

// Down rolls back the most recent migration.
func Down(ctx context.Context, databaseURL string) error {
	return run(ctx, databaseURL, func(db *sql.DB) error {
		return goose.DownContext(ctx, db, migrationsDir)
	})
}

// Status prints the migration status (one line per migration) via goose's
// default logger, which writes to stderr.
func Status(ctx context.Context, databaseURL string) error {
	return run(ctx, databaseURL, func(db *sql.DB) error {
		return goose.StatusContext(ctx, db, migrationsDir)
	})
}

// run opens a pgx pool, adapts it to *sql.DB for goose, configures goose with
// the embedded FS + dialect, and runs op. It always closes the pool.
func run(ctx context.Context, databaseURL string, op func(*sql.DB) error) error {
	if databaseURL == "" {
		return errors.New("migrate: database url is empty")
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: open pool: %w", err)
	}
	defer pool.Close()

	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("migrate: set dialect: %w", err)
	}
	goose.SetBaseFS(migrations.FS)

	if err := op(db); err != nil {
		return err
	}
	return nil
}
