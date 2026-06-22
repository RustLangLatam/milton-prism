package repositories

import (
	"context"

	"milton_prism/core/services/analysis/ports"
	applog "milton_prism/pkg/log"
)

var _ ports.JobEnqueuer = (*NoOpJobEnqueuer)(nil)

// NoOpJobEnqueuer is a stub used when no cache is configured.
type NoOpJobEnqueuer struct{}

// NewNoOpJobEnqueuer returns the no-op job enqueuer stub.
func NewNoOpJobEnqueuer() *NoOpJobEnqueuer {
	return &NoOpJobEnqueuer{}
}

// EnqueueAnalysis logs the intent and returns nil without dispatching a job.
func (e *NoOpJobEnqueuer) EnqueueAnalysis(_ context.Context, summaryID, repositoryID, migrationID uint64, _, _, _ string) error {
	applog.Infof("analysis: job enqueued (stub): summary_id=%d repository_id=%d migration_id=%d", summaryID, repositoryID, migrationID)
	return nil
}
