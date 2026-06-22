package ports

import "context"

// JobEnqueuer is the driven port for dispatching background analysis jobs to
// the analysis engine worker.
type JobEnqueuer interface {
	// EnqueueAnalysis dispatches an analysis job for the given summary.
	// remoteURL and defaultBranch are passed so the worker can clone the
	// source without an extra round-trip to the repository service.
	// rootSubdirectory optionally scopes the analysis to a repository-relative
	// subdirectory (monorepo support); empty means the whole repository root.
	EnqueueAnalysis(ctx context.Context, summaryID, repositoryID, migrationID uint64, remoteURL, defaultBranch, rootSubdirectory string) error
}
