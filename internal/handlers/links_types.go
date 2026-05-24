package handlers

import "time"

// --- request / response shapes ----------------------------------------------

type createReq struct {
	TargetURL string     `json:"target_url"`
	Code      string     `json:"code,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// ListResponse is the JSON shape returned by GET /api/v1/links. Items
// are ordered newest-first and capped by the request's `limit` query
// parameter (with a server-side default + maximum). NextCursor is a
// link id suitable for the `before` query parameter on the next page;
// it is rendered as `null` when there are no more rows.
type ListResponse struct {
	Items      []LinkResponse `json:"items"`
	NextCursor *int64         `json:"next_cursor"`
}

// LinkResponse is the JSON shape returned by Create and Get.
//
// ExpiresAt is omitted entirely (rather than rendered as null) for the
// common "never expires" case so JSON consumers can distinguish a
// permanent link with a single key check.
type LinkResponse struct {
	Code       string     `json:"code"`
	ShortURL   string     `json:"short_url"`
	TargetURL  string     `json:"target_url"`
	CreatedAt  time.Time  `json:"created_at"`
	ClickCount int64      `json:"click_count"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// ErrorResponse is the JSON shape returned for any non-2xx response from
// the JSON API. The Error field is the human-readable description (safe
// to surface in a UI); Code is a stable machine-readable identifier
// suitable for client-side branching, metric labels, and i18n keys. The
// pair is set together via errResp; callers should never construct an
// ErrorResponse with one field and not the other.
type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// API error codes. These strings are part of the public API contract:
// once published, the values must not change (clients may switch on
// them). Adding new codes is fine; renaming or removing an existing one
// is a breaking change and warrants a major-version bump.
const (
	ErrCodeInvalidJSONBody = "invalid_json_body" // 400 on POST when the body is not parseable JSON.
	ErrCodeValidation      = "validation_failed" // 422 when input fails our rules (bad URL, bad code, bad expiry).
	ErrCodeCodeTaken       = "code_taken"        // 409 when a user-supplied short code is already in use.
	ErrCodeNotFound        = "not_found"         // 404 when the requested code does not exist.
	ErrCodeLinkExpired     = "link_expired"      // 410 when the link existed but has passed its expires_at.
	ErrCodeLinkDeleted     = "link_deleted"      // 410 when the link existed but was soft-deleted via DELETE /api/v1/links/:code.
	ErrCodeInternal        = "internal_error"    // 500 for any other failure.
	ErrCodeRateLimited     = "rate_limited"      // 429 when a client exceeds the per-IP create budget.
)

// errResp builds an ErrorResponse with both fields set. Centralized so
// every call site is forced to provide a code, preventing the public
// shape from drifting back into "human message only".
func errResp(code, msg string) ErrorResponse {
	return ErrorResponse{Error: msg, Code: code}
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
