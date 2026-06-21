package domain

import "fmt"

// Error is the typed domain error for the articles service.
// The Code matches the ART error-prefix registry entry in the platform decomposition doc.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func newError(code, message string) *Error { return &Error{Code: code, Message: message} }

// ── Validation errors (ART1xx) ───────────────────────────────────────────────

const (
	ErrCodeMissingIdentifier       = "ART101"
	ErrCodeMissingPayload          = "ART102"
	ErrCodeMissingAuthorIdentifier = "ART103"
)

var (
	ErrMissingIdentifier       = newError(ErrCodeMissingIdentifier, "Failure_Missing_Identifier")
	ErrMissingPayload          = newError(ErrCodeMissingPayload, "Failure_Missing_Payload")
	ErrMissingAuthorIdentifier = newError(ErrCodeMissingAuthorIdentifier, "Failure_Missing_Author_Identifier")
)

// ── Domain errors (ART2xx) ───────────────────────────────────────────────────

const (
	ErrCodeArticleNotFound      = "ART201"
	ErrCodeArticleAlreadyExists = "ART202"
	ErrCodeTagNotFound          = "ART203"
	ErrCodeAuthorNotFound       = "ART204"
	ErrCodeForbiddenAccess      = "ART205"
)

var (
	ErrArticleNotFound      = newError(ErrCodeArticleNotFound, "Failure_Article_Not_Found")
	ErrArticleAlreadyExists = newError(ErrCodeArticleAlreadyExists, "Failure_Article_Already_Exists")
	ErrTagNotFound          = newError(ErrCodeTagNotFound, "Failure_Tag_Not_Found")
	ErrAuthorNotFound       = newError(ErrCodeAuthorNotFound, "Failure_Author_Not_Found")
	ErrForbiddenAccess      = newError(ErrCodeForbiddenAccess, "Failure_Access_Forbidden")
)

// ── Internal errors (ART5xx) ─────────────────────────────────────────────────

const ErrCodeInternal = "ART500"

var ErrInternal = newError(ErrCodeInternal, "Failure_Internal")
