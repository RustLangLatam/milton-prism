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
	// ErrCodeMissingSourceBranch: RunAnalysis was called without a source_branch.
	// The branch is mandatory — analyses are unique per (repository_id, source_branch).
	ErrCodeMissingSourceBranch = "ANL105"
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
	// ErrMissingSourceBranch: RunAnalysis rejected because no source_branch was
	// supplied. The branch is mandatory (analyses are unique per repo+branch).
	ErrMissingSourceBranch = newError(ErrCodeMissingSourceBranch, "Failure_Missing_Source_Branch")
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
	// ErrCodeAnalysisAlreadyExists: a unique-index duplicate-key collision on
	// (repository_id, source_branch) — another analysis already covers this
	// repo+branch. Safety net behind the update-in-place re-analysis path.
	ErrCodeAnalysisAlreadyExists = "ANL206"
	// ErrCodeAnalysisHasLiveMigrations: DeleteAnalysisSummary rejected because at
	// least one active (non-terminal) migration still references this analysis.
	// Deleting it would orphan a running migration. Maps to FailedPrecondition.
	ErrCodeAnalysisHasLiveMigrations = "ANL207"
	// ErrCodeInvalidStateTransition: an analysis lifecycle action was requested
	// from an incompatible state — CancelAnalysis on a terminal analysis, or
	// DeleteAnalysisSummary on a non-terminal one. Maps to FailedPrecondition.
	ErrCodeInvalidStateTransition = "ANL208"
)

var (
	ErrAnalysisSummaryNotFound = newError(ErrCodeAnalysisSummaryNotFound, "Failure_Analysis_Summary_Not_Found")
	ErrRepositoryNotFound      = newError(ErrCodeRepositoryNotFound, "Failure_Repository_Not_Found")
	ErrRepoAuthFailed          = newError(ErrCodeRepoAuthFailed, "Failure_Repository_Auth_Failed")
	ErrRepoUnreachable         = newError(ErrCodeRepoUnreachable, "Failure_Repository_Unreachable")
	ErrNoDeepData              = newError(ErrCodeNoDeepData, "Failure_No_Deep_Data")
	// ErrAnalysisAlreadyExists: unique-index collision on (repository_id, source_branch).
	ErrAnalysisAlreadyExists = newError(ErrCodeAnalysisAlreadyExists, "Failure_Analysis_Already_Exists")
	// ErrAnalysisHasLiveMigrations: DeleteAnalysisSummary blocked — an active
	// migration still references this analysis.
	ErrAnalysisHasLiveMigrations = newError(ErrCodeAnalysisHasLiveMigrations, "Failure_Analysis_Has_Live_Migrations")
	// ErrInvalidStateTransition: cancel/delete requested from an incompatible
	// analysis lifecycle state.
	ErrInvalidStateTransition = newError(ErrCodeInvalidStateTransition, "Failure_Invalid_State_Transition")
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
