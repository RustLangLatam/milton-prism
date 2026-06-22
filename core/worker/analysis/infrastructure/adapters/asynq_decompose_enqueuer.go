package adapters

import (
	"context"
	"encoding/json"
	"fmt"

	"milton_prism/core/worker/analysis/ports"
	applog "milton_prism/pkg/log"

	"github.com/hibiken/asynq"
)

// taskTypeDecomposeRun must match jobs.TaskTypeDecomposeRun in the decomposition worker.
const taskTypeDecomposeRun = "decompose:run"

var _ ports.DecomposeJobEnqueuer = (*AsynqDecomposeEnqueuer)(nil)

// AsynqDecomposeEnqueuer dispatches decompose:run jobs to the Asynq queue.
type AsynqDecomposeEnqueuer struct {
	client *asynq.Client
}

// decomposePayload is the JSON shape of the decompose:run task payload.
// It must match workerdomain.JobPayload in the decomposition worker.
type decomposePayload struct {
	MigrationID      uint64 `json:"migration_id"`
	SummaryID        uint64 `json:"summary_id"`
	RemoteURL        string `json:"remote_url"`
	DefaultBranch    string `json:"default_branch"`
	RootSubdirectory string `json:"root_subdirectory,omitempty"`
}

// NewAsynqDecomposeEnqueuer returns an enqueuer using the given Asynq client.
func NewAsynqDecomposeEnqueuer(client *asynq.Client) *AsynqDecomposeEnqueuer {
	return &AsynqDecomposeEnqueuer{client: client}
}

// EnqueueDecompose enqueues a decompose:run task for the given migration and summary.
// remoteURL and defaultBranch are carried in the payload so the decomposition worker
// can re-acquire the source workspace for stage 5 (contract derivation).
func (e *AsynqDecomposeEnqueuer) EnqueueDecompose(ctx context.Context, migrationID, summaryID uint64, remoteURL, defaultBranch, rootSubdirectory string) error {
	payload, err := json.Marshal(decomposePayload{
		MigrationID:      migrationID,
		SummaryID:        summaryID,
		RemoteURL:        remoteURL,
		DefaultBranch:    defaultBranch,
		RootSubdirectory: rootSubdirectory,
	})
	if err != nil {
		return err
	}
	task := asynq.NewTask(taskTypeDecomposeRun, payload)
	info, err := e.client.EnqueueContext(ctx, task, asynq.Queue("analysis"))
	if err != nil {
		return fmt.Errorf("enqueue decompose: %w", err)
	}
	applog.Infof("analysis-worker: decompose job enqueued queue=%s task_id=%s migration_id=%d summary_id=%d",
		info.Queue, info.ID, migrationID, summaryID)
	return nil
}
