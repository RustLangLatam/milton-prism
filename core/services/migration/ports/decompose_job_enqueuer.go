package ports

import "context"

// DecomposeJobEnqueuer dispatches a decompose:run job when a migration
// enters the DESIGNING state (either via normal analysis completion or
// via the analysis-reuse path in StartMigration).
type DecomposeJobEnqueuer interface {
	EnqueueDecompose(ctx context.Context, migrationID, summaryID uint64, remoteURL, defaultBranch string) error
}
