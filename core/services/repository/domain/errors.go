package domain

import "fmt"

// Error is the typed domain error for the repository service.
// The Code field matches the REPO error registry in the platform decomposition doc.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func newError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// ── Validation errors (REPO1xx) ──────────────────────────────────────────────

const (
	ErrCodeMissingIdentifier  = "REPO101"
	ErrCodeMissingPayload     = "REPO102"
	ErrCodeMissingOwnerUserID = "REPO103"
	ErrCodeInvalidRemoteURL   = "REPO104"
)

var (
	ErrMissingIdentifier  = newError(ErrCodeMissingIdentifier, "Failure_Missing_Identifier")
	ErrMissingPayload     = newError(ErrCodeMissingPayload, "Failure_Missing_Payload")
	ErrMissingOwnerUserID = newError(ErrCodeMissingOwnerUserID, "Failure_Missing_Owner_User_ID")
	ErrInvalidRemoteURL   = newError(ErrCodeInvalidRemoteURL, "Failure_Invalid_Remote_URL")
)

// ── Domain errors (REPO2xx) ──────────────────────────────────────────────────

const (
	ErrCodeRepositoryNotFound      = "REPO201"
	ErrCodeRepositoryAlreadyExists = "REPO202"
	ErrCodeOwnerNotFound           = "REPO203"
	ErrCodeConnectionFailed        = "REPO204"
	ErrCodeForbiddenAccess         = "REPO205"
	ErrCodePushAuthFailed          = "REPO206"
	ErrCodePushConflict            = "REPO207"
	ErrCodePushNetworkError        = "REPO208"
	ErrCodeTargetNotEmpty          = "REPO209"
)

var (
	ErrRepositoryNotFound      = newError(ErrCodeRepositoryNotFound, "Failure_Repository_Not_Found")
	ErrRepositoryAlreadyExists = newError(ErrCodeRepositoryAlreadyExists, "Failure_Repository_Already_Exists")
	ErrOwnerNotFound           = newError(ErrCodeOwnerNotFound, "Failure_Owner_Not_Found")
	ErrConnectionFailed        = newError(ErrCodeConnectionFailed, "Failure_Connection_Failed")
	ErrForbiddenAccess         = newError(ErrCodeForbiddenAccess, "Failure_Access_Forbidden")
	// ErrPushAuthFailed: the write token was rejected by the remote.
	ErrPushAuthFailed = newError(ErrCodePushAuthFailed, "Failure_Push_Auth_Failed")
	// ErrPushConflict: the remote rejected the push (non-fast-forward or hook).
	ErrPushConflict = newError(ErrCodePushConflict, "Failure_Push_Rejected")
	// ErrPushNetworkError: could not reach the remote during push.
	ErrPushNetworkError = newError(ErrCodePushNetworkError, "Failure_Push_Network_Error")
	// ErrTargetNotEmpty: the push destination already contains refs/commits.
	// A.3 requires a freshly created, empty target repository for v1.
	ErrTargetNotEmpty = newError(ErrCodeTargetNotEmpty, "Failure_Target_Not_Empty")
)

// ── Internal errors (REPO5xx) ────────────────────────────────────────────────

const ErrCodeInternal = "REPO500"

var ErrInternal = newError(ErrCodeInternal, "Failure_Internal")
