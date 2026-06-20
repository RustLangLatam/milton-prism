package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"milton_prism/core/services/migration/ports"
	"milton_prism/pkg/config"
	applog "milton_prism/pkg/log"

	"github.com/hibiken/asynq"
)

// generationTaskTimeout overrides asynq's default 30-min task timeout.
// Must be > the container timeout (60 min) to allow post-run artifact persist.
const generationTaskTimeout = 90 * time.Minute

// taskTypeGenerationRun must match jobs.TaskTypeGenerationRun in the worker.
const taskTypeGenerationRun = "generation:run"

var _ ports.GenerationJobEnqueuer = (*AsynqGenerationEnqueuer)(nil)

// AsynqGenerationEnqueuer dispatches generation jobs to the Asynq queue backed by Redis.
type AsynqGenerationEnqueuer struct {
	client *asynq.Client
}

type generationAsynqPayload struct {
	MigrationID   uint64   `json:"migration_id"`
	ServiceFilter []string `json:"service_filter,omitempty"`
}

// NewAsynqGenerationEnqueuer constructs an enqueuer connected to cfg.
func NewAsynqGenerationEnqueuer(cfg *config.CacheCfg) *AsynqGenerationEnqueuer {
	client := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Password: cfg.RequirePass,
	})
	return &AsynqGenerationEnqueuer{client: client}
}

// Close releases the underlying Redis connection.
func (e *AsynqGenerationEnqueuer) Close() error {
	return e.client.Close()
}

func (e *AsynqGenerationEnqueuer) EnqueueGeneration(ctx context.Context, migrationID uint64, serviceFilter []string) error {
	payload, err := json.Marshal(generationAsynqPayload{MigrationID: migrationID, ServiceFilter: serviceFilter})
	if err != nil {
		return err
	}
	task := asynq.NewTask(taskTypeGenerationRun, payload)
	info, err := e.client.EnqueueContext(ctx, task,
		asynq.Queue("generation"),
		asynq.Timeout(generationTaskTimeout),
	)
	if err != nil {
		return err
	}
	applog.Infof("migration: generation job enqueued queue=%s task_id=%s migration_id=%d", info.Queue, info.ID, migrationID)
	return nil
}
