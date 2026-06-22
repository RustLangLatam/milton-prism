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
	ErrCodeMissingIdentifier       = "ANL101"
	ErrCodeMissingRepositoryID     = "ANL102"
	ErrCodeInvalidRootSubdirectory = "ANL103"
	// ErrCodeInvalidRootSelection: SelectRoot was called with a root_directory
	// that is empty or not among the analysis's detected root_candidates, or the
	// analysis is not in AWAITING_ROOT_SELECTION state. Fails closed.
	ErrCodeInvalidRootSelection = "ANL104"
)

var (
	ErrMissingIdentifier   = newError(ErrCodeMissingIdentifier, "Failure_Missing_Identifier")
	ErrMissingRepositoryID = newError(ErrCodeMissingRepositoryID, "Failure_Missing_Repository_ID")
	// ErrInvalidRootSubdirectory: the requested monorepo root subdirectory is not
	// a safe repository-relative path (absolute, traversal, or empty component).
	ErrInvalidRootSubdirectory = newError(ErrCodeInvalidRootSubdirectory, "Failure_Invalid_Root_Subdirectory")
	// ErrInvalidRootSelection: SelectRoot choice rejected (not a candidate, empty,
	// or the analysis is not awaiting a selection).
	ErrInvalidRootSelection = newError(ErrCodeInvalidRootSelection, "Failure_Invalid_Root_Selection")
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

// ── Plan / quota errors (ANL3xx) ──────────────────────────────────────────────

const (
	// ErrCodePlanLimitExceeded: RunAnalysis rejected because the owner's billing
	// plan count limit (analyses-per-month) has been reached. Hard block; the user
	// must upgrade their plan or wait for the next billing month. Maps to
	// FailedPrecondition (the gateway surfaces a 4xx).
	ErrCodePlanLimitExceeded = "ANL301"
)

// ErrPlanLimitExceeded carries an actionable message; NewErrPlanLimitExceeded
// builds a per-plan message with the concrete monthly cap.
var ErrPlanLimitExceeded = newError(ErrCodePlanLimitExceeded, "Failure_Plan_Limit_Exceeded")

// NewErrPlanLimitExceeded builds a plan-limit error whose message names the
// concrete monthly analysis cap so the panel can show an actionable notice.
func NewErrPlanLimitExceeded(limit int64) *Error {
	return &Error{
		Code:    ErrCodePlanLimitExceeded,
		Message: fmt.Sprintf("Plan limit reached: %d analyses per month. Upgrade your plan.", limit),
	}
}

// ── Internal errors (ANL5xx) ──────────────────────────────────────────────────

const ErrCodeInternal = "ANL500"

var ErrInternal = newError(ErrCodeInternal, "Failure_Internal")
