package domain

import "fmt"

// Error is the typed domain error for the migration service.
// The Code field matches the MIG error registry in the platform decomposition doc.
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

// ── Validation errors (MIG1xx) ────────────────────────────────────────────────

const (
	ErrCodeMissingIdentifier   = "MIG101"
	ErrCodeMissingPayload      = "MIG102"
	ErrCodeMissingOwnerUserID  = "MIG103"
	ErrCodeMissingRepositoryID     = "MIG104"
	ErrCodeInvalidTargetConfig     = "MIG105"
	ErrCodeInvalidRootSubdirectory = "MIG106"
	ErrCodeUnsupportedTargetLanguage = "MIG107"
)

var (
	ErrMissingIdentifier   = newError(ErrCodeMissingIdentifier, "Failure_Missing_Identifier")
	ErrMissingPayload      = newError(ErrCodeMissingPayload, "Failure_Missing_Payload")
	ErrMissingOwnerUserID  = newError(ErrCodeMissingOwnerUserID, "Failure_Missing_Owner_User_ID")
	ErrMissingRepositoryID = newError(ErrCodeMissingRepositoryID, "Failure_Missing_Repository_ID")
	ErrInvalidTargetConfig = newError(ErrCodeInvalidTargetConfig, "Failure_Invalid_Target_Config")
	// ErrInvalidRootSubdirectory: the requested monorepo root subdirectory is not
	// a safe repository-relative path (absolute or contains a ".." traversal).
	ErrInvalidRootSubdirectory = newError(ErrCodeInvalidRootSubdirectory, "Failure_Invalid_Root_Subdirectory")
	// ErrUnsupportedTargetLanguage: the requested target language has no code
	// generator profile yet (only Go and Python are generable). Rejected at
	// creation so a migration never silently falls back to Go.
	ErrUnsupportedTargetLanguage = newError(ErrCodeUnsupportedTargetLanguage, "Failure_Unsupported_Target_Language")
)

// ── Domain errors (MIG2xx) ────────────────────────────────────────────────────

const (
	ErrCodeMigrationNotFound      = "MIG201"
	ErrCodeInvalidStateTransition = "MIG202"
	ErrCodeRepositoryNotFound     = "MIG203"
	ErrCodeOwnerNotFound          = "MIG204"
	ErrCodeForbiddenAccess        = "MIG205"
	// ErrCodeNoServiceBoundaries is returned when the caller attempts to approve
	// a migration whose plan has no service boundaries. Generation of zero services
	// is not meaningful; the user must cancel or restructure the source code.
	ErrCodeNoServiceBoundaries = "MIG206"
	// ErrCodePushAuthFailed: write token was rejected by the target remote.
	ErrCodePushAuthFailed = "MIG207"
	// ErrCodePushConflict: remote rejected the push (non-fast-forward or hook).
	ErrCodePushConflict = "MIG208"
	// ErrCodePushNetworkError: could not reach the target remote during push.
	ErrCodePushNetworkError = "MIG209"
	// ErrCodeNoArtifacts: migration has no generated file artifacts to publish.
	ErrCodeNoArtifacts = "MIG210"
	// ErrCodeArtifactConflict: two or more services produced different content for
	// the same monorepo path. The push is blocked until generation is re-run and the
	// conflict resolved; the error message names the conflicting paths and services.
	ErrCodeArtifactConflict = "MIG211"
	// ErrCodeNotMigrableBlocked: Approve/Generate was attempted on a migration whose
	// migrability verdict is NOT_MIGRABLE and migrability_override is false.
	// The user must either call SetMigrabilityOverride or cancel the migration.
	ErrCodeNotMigrableBlocked = "MIG212"
	// ErrCodeNoAnalysisSummary: AssessMigrability was called but the migration has
	// no analysis summary yet (analysis has not completed).
	ErrCodeNoAnalysisSummary = "MIG213"
	// ErrCodeRepoAuthFailed: StartMigration rejected because the repository credential
	// is invalid or lacks read permission. The user must update the repository token.
	ErrCodeRepoAuthFailed = "MIG214"
	// ErrCodeRepoUnreachable: StartMigration rejected because the repository remote
	// could not be reached. Verify the repository URL and network connectivity.
	ErrCodeRepoUnreachable = "MIG215"
	// ErrCodeRoadmapUnavailable: GenerateRestructuringRoadmap was called on a migration
	// that does not qualify — its verdict is not NOT_MIGRABLE and the plan has no
	// no_service_boundaries flag. Only genuinely blocked migrations get a roadmap.
	ErrCodeRoadmapUnavailable = "MIG216"
	// ErrCodeSourceAnalysisNotFound: source_analysis_summary_id references an
	// AnalysisSummary that does not exist or does not belong to the caller.
	ErrCodeSourceAnalysisNotFound = "MIG217"
	// ErrCodeSourceAnalysisInvalid: the referenced AnalysisSummary cannot be adopted
	// because it belongs to a different repository or is not yet COMPLETED.
	ErrCodeSourceAnalysisInvalid = "MIG218"
	// ErrCodeNoRoadmap: EnrichRoadmap was called but the migration has no roadmap or
	// its action_plan is empty. Call GenerateRestructuringRoadmap first.
	ErrCodeNoRoadmap = "MIG219"
	// ErrCodeNoBlueprintAnalysis: GenerateBlueprint was called but the migration
	// has no completed analysis summary. The analysis pipeline must finish first.
	ErrCodeNoBlueprintAnalysis = "MIG220"
	ErrCodeNoActionPlan        = "MIG221"
	// ErrCodePlanLimitExceeded: CreateMigration rejected because the owner's billing
	// plan count limit (migrations-per-month) has been reached. Hard block; the user
	// must upgrade their plan or wait for the next billing month.
	ErrCodePlanLimitExceeded = "MIG222"
)

var (
	ErrMigrationNotFound      = newError(ErrCodeMigrationNotFound, "Failure_Migration_Not_Found")
	ErrInvalidStateTransition = newError(ErrCodeInvalidStateTransition, "Failure_Invalid_State_Transition")
	ErrRepositoryNotFound     = newError(ErrCodeRepositoryNotFound, "Failure_Repository_Not_Found")
	ErrOwnerNotFound          = newError(ErrCodeOwnerNotFound, "Failure_Owner_Not_Found")
	ErrForbiddenAccess        = newError(ErrCodeForbiddenAccess, "Failure_Access_Forbidden")
	ErrNoServiceBoundaries    = newError(ErrCodeNoServiceBoundaries, "Failure_No_Service_Boundaries")
	ErrPushAuthFailed         = newError(ErrCodePushAuthFailed, "Failure_Push_Auth_Failed")
	ErrPushConflict           = newError(ErrCodePushConflict, "Failure_Push_Rejected")
	ErrPushNetworkError       = newError(ErrCodePushNetworkError, "Failure_Push_Network_Error")
	ErrNoArtifacts            = newError(ErrCodeNoArtifacts, "Failure_No_Artifacts")
	ErrNotMigrableBlocked     = newError(ErrCodeNotMigrableBlocked, "Failure_Not_Migrable_Override_Required")
	ErrNoAnalysisSummary      = newError(ErrCodeNoAnalysisSummary, "Failure_No_Analysis_Summary")
	ErrRepoAuthFailed         = newError(ErrCodeRepoAuthFailed, "Failure_Repository_Auth_Failed")
	ErrRepoUnreachable        = newError(ErrCodeRepoUnreachable, "Failure_Repository_Unreachable")
	ErrRoadmapUnavailable     = newError(ErrCodeRoadmapUnavailable, "Failure_Roadmap_Unavailable")
	ErrSourceAnalysisNotFound = newError(ErrCodeSourceAnalysisNotFound, "Failure_Source_Analysis_Not_Found")
	ErrSourceAnalysisInvalid  = newError(ErrCodeSourceAnalysisInvalid, "Failure_Source_Analysis_Invalid")
	ErrNoRoadmap              = newError(ErrCodeNoRoadmap, "Failure_No_Roadmap")
	ErrNoBlueprintAnalysis    = newError(ErrCodeNoBlueprintAnalysis, "Failure_No_Blueprint_Analysis")
	ErrNoActionPlan           = newError(ErrCodeNoActionPlan, "Failure_No_Action_Plan")
	// ErrPlanLimitExceeded carries an actionable default message; use
	// NewErrPlanLimitExceeded to embed the concrete monthly cap.
	ErrPlanLimitExceeded = newError(ErrCodePlanLimitExceeded, "Failure_Plan_Limit_Exceeded")
)

// NewErrPlanLimitExceeded builds a plan-limit error whose message names the
// concrete monthly migration cap so the panel can show an actionable notice.
func NewErrPlanLimitExceeded(limit int64) *Error {
	return &Error{
		Code:    ErrCodePlanLimitExceeded,
		Message: fmt.Sprintf("Plan limit reached: %d migrations per month. Upgrade your plan.", limit),
	}
}

// NewErrArtifactConflict builds a conflict error that names the paths whose
// content diverges across services. writeToken is never part of the message.
func NewErrArtifactConflict(details string) *Error {
	return &Error{Code: ErrCodeArtifactConflict, Message: "Failure_Artifact_Path_Conflict: " + details}
}

// ── Internal errors (MIG5xx) ──────────────────────────────────────────────────

const ErrCodeInternal = "MIG500"

var ErrInternal = newError(ErrCodeInternal, "Failure_Internal")
