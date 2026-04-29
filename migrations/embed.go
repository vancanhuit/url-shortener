// Package migrations exposes the embedded goose SQL files as an fs.FS so the
// migration runner in internal/migrate can apply them without depending on a
// directory layout at runtime.
package migrations

import "embed"

// FS is the embedded set of goose migration files.
//
//go:embed *.sql
var FS embed.FS
