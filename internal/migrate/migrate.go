// Package migrate wraps goose to apply the SQL files embedded in the
// migrations package against a Postgres database.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/vancanhuit/url-shortener/migrations"
)

// Up applies all pending migrations. A Postgres session-level advisory lock
// is acquired before any migration runs, so concurrent AutoMigrate calls from
// multiple replicas starting simultaneously are serialized at the DB level.
func Up(ctx context.Context, databaseURL string) error {
	return withProvider(ctx, databaseURL, func(p *goose.Provider) error {
		results, err := p.Up(ctx)
		for _, r := range results {
			fmt.Println(r)
		}
		return err
	})
}

// Down rolls back the most recent migration.
func Down(ctx context.Context, databaseURL string) error {
	return withProvider(ctx, databaseURL, func(p *goose.Provider) error {
		result, err := p.Down(ctx)
		if result != nil {
			fmt.Println(result)
		}
		return err
	})
}

// Status prints the migration status (one line per migration) to stdout.
func Status(ctx context.Context, databaseURL string) error {
	return withProvider(ctx, databaseURL, func(p *goose.Provider) error {
		statuses, err := p.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			state := "pending"
			if s.State == goose.StateApplied {
				state = "applied"
			}
			if s.AppliedAt.IsZero() {
				fmt.Printf("%-7s -- v%05d %s\n", state, s.Source.Version, s.Source.Path)
			} else {
				fmt.Printf("%-7s %s v%05d %s\n", state, s.AppliedAt.UTC().Format("2006-01-02 15:04:05"), s.Source.Version, s.Source.Path)
			}
		}
		return nil
	})
}

// Versions returns the current applied version and the latest embedded
// migration version.
func Versions(ctx context.Context, databaseURL string) (current int64, latest int64, err error) {
	err = withProvider(ctx, databaseURL, func(p *goose.Provider) error {
		var e error
		current, latest, e = p.GetVersions(ctx)
		return e
	})
	return current, latest, err
}

// Redo rolls back the most recently applied migration and immediately
// re-applies it. Useful during development to iterate on the current
// migration without manually running down then up.
func Redo(ctx context.Context, databaseURL string) error {
	return withProvider(ctx, databaseURL, func(p *goose.Provider) error {
		result, err := p.Down(ctx)
		if err != nil {
			return err
		}
		if result != nil {
			fmt.Println(result)
		}
		results, err := p.Up(ctx)
		for _, r := range results {
			fmt.Println(r)
		}
		return err
	})
}

// Create scaffolds a new migration file on disk.
//
// This is intended for local development workflows; unlike Up/Down/Status,
// it does not touch the database.
func Create(dir, name, migrationType string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("migrate: migration name is empty")
	}
	if strings.TrimSpace(dir) == "" {
		return errors.New("migrate: migration dir is empty")
	}
	if migrationType == "" {
		migrationType = string(goose.TypeSQL)
	}
	return goose.Create(nil, dir, name, migrationType)
}

// withProvider opens a pgx pool, adapts it to *sql.DB for goose, builds a
// Provider with the embedded migrations FS and a Postgres advisory session
// locker, calls op, then closes everything.
func withProvider(ctx context.Context, databaseURL string, op func(*goose.Provider) error) error {
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

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migrate: create session locker: %w", err)
	}

	p, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrations.FS,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("migrate: create provider: %w", err)
	}
	defer func() { _ = p.Close() }()

	return op(p)
}
