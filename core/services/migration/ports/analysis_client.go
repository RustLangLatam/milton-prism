package ports

import (
	"context"

	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
)

// AnalysisClient is the driven port for communicating with the analysis service.
type AnalysisClient interface {
	// RunAnalysis requests the analysis service to begin an asynchronous
	// analysis run for the given repository and migration. sourceBranch
	// overrides the repository's default_branch when non-empty. rootSubdirectory
	// optionally scopes the analysis to a repository-relative subdirectory
	// (monorepo support); empty means the whole repository root. The call is
	// best-effort: a transport error does not block the state transition.
	RunAnalysis(ctx context.Context, repositoryID, migrationID, ownerUserID uint64, sourceBranch, rootSubdirectory string) error

	// GetAnalysisSummary fetches the header fields of an AnalysisSummary by
	// identifier. The analysis service enforces ownership: the caller's token
	// must belong to the owning user. Used by StartMigration to validate a
	// source_analysis_summary_id before adopting it.
	GetAnalysisSummary(ctx context.Context, identifier uint64) (*analysisv1.AnalysisSummary, error)
}
