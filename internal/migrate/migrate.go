// Package migrate wraps goose to apply the SQL files embedded in the
// migrations package against a Postgres database.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

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

// Versions returns the current goose_db_version in the target database and
// the latest embedded migration version.
func Versions(ctx context.Context, databaseURL string) (current int64, latest int64, err error) {
	latest, err = latestEmbeddedVersion()
	if err != nil {
		return 0, 0, err
	}

	err = run(ctx, databaseURL, func(db *sql.DB) error {
		v, e := goose.EnsureDBVersionContext(ctx, db)
		if e != nil {
			return e
		}
		current = v
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return current, latest, nil
}

// Redo rolls back the most recently applied migration and immediately
// re-applies it. This is useful during development to iterate on the
// current migration without manually running down then up.
func Redo(ctx context.Context, databaseURL string) error {
	return run(ctx, databaseURL, func(db *sql.DB) error {
		return goose.RedoContext(ctx, db, migrationsDir)
	})
}

// sqlTemplate is written into every new migration file created by Create.
var sqlTemplate = template.Must(template.New("goose").Parse(`-- +goose Up
-- +goose StatementBegin

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- +goose StatementEnd
`))

// Create writes a new sequential goose SQL migration file into dir.
// The file is named NNNNN_<name>.sql where NNNNN is one greater than the
// highest version already present in dir.
// It returns the path of the created file.
func Create(dir, name string) (string, error) {
	if name == "" {
		return "", errors.New("migrate: migration name must not be empty")
	}

	next, err := nextVersion(dir)
	if err != nil {
		return "", err
	}

	filename := fmt.Sprintf("%05d_%s.sql", next, name)
	path := filepath.Join(dir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path is constructed from a user-supplied migration name
	if err != nil {
		return "", fmt.Errorf("migrate: create file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := sqlTemplate.Execute(f, nil); err != nil {
		return "", fmt.Errorf("migrate: write template: %w", err)
	}
	return path, nil
}

// nextVersion scans dir for files matching the goose sequential naming
// convention (NNNNN_*.sql) and returns max+1, or 1 if dir is empty.
func nextVersion(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("migrate: read dir %q: %w", dir, err)
	}

	var maxVer int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, err := goose.NumericComponent(e.Name())
		if err != nil {
			continue // skip files that don't match the pattern
		}
		if v > maxVer {
			maxVer = v
		}
	}
	return maxVer + 1, nil
}

func latestEmbeddedVersion() (int64, error) {
	entries, err := fs.ReadDir(migrations.FS, migrationsDir)
	if err != nil {
		return 0, fmt.Errorf("migrate: read embedded migrations: %w", err)
	}

	var latest int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, err := goose.NumericComponent(e.Name())
		if err != nil {
			return 0, fmt.Errorf("migrate: parse migration version %q: %w", e.Name(), err)
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		return 0, goose.ErrNoMigrationFiles
	}
	return latest, nil
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
