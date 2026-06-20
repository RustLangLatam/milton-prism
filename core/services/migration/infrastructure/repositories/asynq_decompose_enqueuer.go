package repositories

import (
	"context"
	"encoding/json"
	"fmt"

	"milton_prism/core/services/migration/ports"
	"milton_prism/pkg/config"
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

type decomposeAsynqPayload struct {
	MigrationID   uint64 `json:"migration_id"`
	SummaryID     uint64 `json:"summary_id"`
	RemoteURL     string `json:"remote_url"`
	DefaultBranch string `json:"default_branch"`
}

// NewAsynqDecomposeEnqueuer constructs an enqueuer connected to cfg.
func NewAsynqDecomposeEnqueuer(cfg *config.CacheCfg) *AsynqDecomposeEnqueuer {
	client := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Password: cfg.RequirePass,
	})
	return &AsynqDecomposeEnqueuer{client: client}
}

func (e *AsynqDecomposeEnqueuer) EnqueueDecompose(ctx context.Context, migrationID, summaryID uint64, remoteURL, defaultBranch string) error {
	payload, err := json.Marshal(decomposeAsynqPayload{
		MigrationID:   migrationID,
		SummaryID:     summaryID,
		RemoteURL:     remoteURL,
		DefaultBranch: defaultBranch,
	})
	if err != nil {
		return err
	}
	task := asynq.NewTask(taskTypeDecomposeRun, payload)
	info, err := e.client.EnqueueContext(ctx, task, asynq.Queue("analysis"))
	if err != nil {
		return fmt.Errorf("enqueue decompose: %w", err)
	}
	applog.Infof("migration: decompose job enqueued queue=%s task_id=%s migration_id=%d summary_id=%d",
		info.Queue, info.ID, migrationID, summaryID)
	return nil
}
