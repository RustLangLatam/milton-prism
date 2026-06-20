package repositories

import (
	"context"
	"encoding/json"
	"fmt"

	"milton_prism/core/services/analysis/ports"
	"milton_prism/pkg/config"
	applog "milton_prism/pkg/log"

	"github.com/hibiken/asynq"
)

// taskTypeAnalysisRun must match jobs.TaskTypeAnalysisRun in the worker.
const taskTypeAnalysisRun = "analysis:run"

var _ ports.JobEnqueuer = (*AsynqJobEnqueuer)(nil)

// AsynqJobEnqueuer dispatches analysis jobs to the Asynq queue backed by Redis.
type AsynqJobEnqueuer struct {
	client *asynq.Client
}

// asynqPayload is the JSON shape of the analysis:run task payload.
// It must match workerdomain.JobPayload in the worker.
type asynqPayload struct {
	SummaryID     uint64 `json:"summary_id"`
	RepositoryID  uint64 `json:"repository_id"`
	MigrationID   uint64 `json:"migration_id"`
	RemoteURL     string `json:"remote_url"`
	DefaultBranch string `json:"default_branch"`
}

// NewAsynqJobEnqueuer constructs an AsynqJobEnqueuer connected to the Redis
// instance described by cfg.
func NewAsynqJobEnqueuer(cfg *config.CacheCfg) *AsynqJobEnqueuer {
	client := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Password: cfg.RequirePass,
	})
	return &AsynqJobEnqueuer{client: client}
}

// Close releases the underlying Redis connection.
func (e *AsynqJobEnqueuer) Close() error {
	return e.client.Close()
}

// EnqueueAnalysis encodes the job as JSON and enqueues an analysis:run task.
func (e *AsynqJobEnqueuer) EnqueueAnalysis(ctx context.Context, summaryID, repositoryID, migrationID uint64, remoteURL, defaultBranch string) error {
	payload, err := json.Marshal(asynqPayload{
		SummaryID:     summaryID,
		RepositoryID:  repositoryID,
		MigrationID:   migrationID,
		RemoteURL:     remoteURL,
		DefaultBranch: defaultBranch,
	})
	if err != nil {
		return err
	}
	task := asynq.NewTask(taskTypeAnalysisRun, payload)
	info, err := e.client.EnqueueContext(ctx, task, asynq.Queue("analysis"))
	if err != nil {
		return err
	}
	applog.Infof("analysis: job enqueued queue=%s task_id=%s summary_id=%d", info.Queue, info.ID, summaryID)
	return nil
}
