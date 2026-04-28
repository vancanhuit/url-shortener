// Package buildinfo exposes build-time metadata that is injected via
// -ldflags at link time. The defaults are placeholder values used when the
// binary is built without those flags (e.g. `go run`, `go test`).
package buildinfo

// These variables are set via -ldflags -X at build time.
//
//nolint:gochecknoglobals // intentional: linker-injected metadata.
var (
	version = "0.0.0-dev"
	commit  = "unknown"
	date    = "1970-01-01T00:00:00Z"
)

// Info is a snapshot of the build metadata.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Get returns the build metadata baked into this binary.
func Get() Info {
	return Info{
		Version: version,
		Commit:  commit,
		Date:    date,
	}
}
