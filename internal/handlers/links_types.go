package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/vancanhuit/url-shortener/api"
)

// errResp builds an ErrorResponse with both fields set. Centralized so
// every call site is forced to provide a code, preventing the public
// shape from drifting back into "human message only".
func errResp(code api.ErrorResponseCode, msg string) api.ErrorResponse {
	return api.ErrorResponse{Error: msg, Code: code}
}

// writeJSON writes a JSON-encoded value with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ValidationError signals a user-input failure that should map to HTTP 422
// (or an inline error in the HTML UI). The Msg is safe to display verbatim.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// PersistErrorKind classifies the failure modes of Links.Persist so
// HTTP layers can drive their response selection from a single switch
// instead of re-implementing the errors.As/errors.Is fork in every
// caller.
type PersistErrorKind int

// PersistError* are the kinds returned by ClassifyPersistError. None
// is the zero value, returned only when the underlying error is nil.
const (
	PersistErrNone PersistErrorKind = iota
	PersistErrValidation
	PersistErrCodeTaken
	PersistErrInternal
)
