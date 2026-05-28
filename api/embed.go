// Package api embeds the OpenAPI specification (api/openapi.yaml) into
// the binary and exposes it both as the original YAML bytes and as
// canonical JSON. The handler layer uses SpecJSON to serve
// GET /api/v1/openapi.json without re-marshaling on every request.
//
// The directory is named `api/` rather than `internal/openapi/` so the
// spec lives at the path most OpenAPI tooling auto-discovers
// (`api/openapi.yaml`, the de-facto convention published in the
// golang-standards/project-layout repo).
package api

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Spec is the verbatim YAML source of the OpenAPI document.
//
//go:embed openapi.yaml
var Spec []byte

// SpecJSON is Spec converted to canonical JSON, computed once at
// package init so request handlers can serve it without per-call
// marshaling. A panic at init is appropriate here: a malformed spec
// is a build-time programming error, not a runtime condition the
// service can recover from.
//
//nolint:gochecknoglobals // intentional: precomputed at init.
var SpecJSON []byte

func init() {
	out, err := yamlToJSON(Spec)
	if err != nil {
		panic(fmt.Sprintf("openapi: convert embedded spec to JSON: %v", err))
	}
	SpecJSON = out
}

// yamlToJSON converts a YAML document to its JSON equivalent. The
// conversion goes YAML -> Go any -> JSON; yaml.v3 unmarshals mappings
// into map[string]any (not map[any]any) so encoding/json can emit
// them directly without a recursive key-coercion pass.
//
// Split out (rather than inlined into init) so a future test can
// exercise it against fixtures without re-embedding.
func yamlToJSON(in []byte) ([]byte, error) {
	var v any
	if err := yaml.Unmarshal(in, &v); err != nil {
		return nil, fmt.Errorf("yaml: unmarshal: %w", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("json: marshal: %w", err)
	}
	return out, nil
}
