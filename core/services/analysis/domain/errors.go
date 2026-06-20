package domain

import "fmt"

// Error is the typed domain error for the analysis service.
// The Code field matches the ANL error registry in the platform decomposition doc.
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

// ── Validation errors (ANL1xx) ────────────────────────────────────────────────

const (
	ErrCodeMissingIdentifier  = "ANL101"
	ErrCodeMissingRepositoryID = "ANL102"
)

var (
	ErrMissingIdentifier   = newError(ErrCodeMissingIdentifier, "Failure_Missing_Identifier")
	ErrMissingRepositoryID = newError(ErrCodeMissingRepositoryID, "Failure_Missing_Repository_ID")
)

// ── Domain errors (ANL2xx) ────────────────────────────────────────────────────

const (
	ErrCodeAnalysisSummaryNotFound = "ANL201"
	ErrCodeRepositoryNotFound      = "ANL202"
	// ErrCodeRepoAuthFailed: RunAnalysis rejected because the repository credential
	// is invalid or lacks read permission. The user must update the repository token.
	ErrCodeRepoAuthFailed = "ANL203"
	// ErrCodeRepoUnreachable: RunAnalysis rejected because the repository remote
	// could not be reached. Verify the repository URL and network connectivity.
	ErrCodeRepoUnreachable = "ANL204"
	// ErrCodeNoDeepData: AssessMigrability rejected because the analysis summary
	// has no dependency graph — deep analysis data is required for assessment.
	ErrCodeNoDeepData = "ANL205"
)

var (
	ErrAnalysisSummaryNotFound = newError(ErrCodeAnalysisSummaryNotFound, "Failure_Analysis_Summary_Not_Found")
	ErrRepositoryNotFound      = newError(ErrCodeRepositoryNotFound, "Failure_Repository_Not_Found")
	ErrRepoAuthFailed          = newError(ErrCodeRepoAuthFailed, "Failure_Repository_Auth_Failed")
	ErrRepoUnreachable         = newError(ErrCodeRepoUnreachable, "Failure_Repository_Unreachable")
	ErrNoDeepData              = newError(ErrCodeNoDeepData, "Failure_No_Deep_Data")
)

// ── Internal errors (ANL5xx) ──────────────────────────────────────────────────

const ErrCodeInternal = "ANL500"

var ErrInternal = newError(ErrCodeInternal, "Failure_Internal")
