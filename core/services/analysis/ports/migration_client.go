package ports

import "context"

// MigrationClient is the driven port the analysis service uses to query the
// migration service across the service boundary. It exists so DeleteAnalysisSummary
// can refuse to delete an analysis that is still referenced by an active
// (non-terminal) migration — deleting it would orphan a running migration.
type MigrationClient interface {
	// CountLiveMigrationsByAnalysis returns how many active (non-terminal)
	// migrations reference the given analysis summary. Active means any state
	// other than the terminal set (PUSHED, FAILED, CANCELLED, RESTRUCTURING_READY).
	// Implementations forward the caller's bearer token so the migration service
	// scopes the count to migrations the caller may see.
	CountLiveMigrationsByAnalysis(ctx context.Context, analysisSummaryID uint64) (int64, error)
}
