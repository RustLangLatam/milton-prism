// Package jobs contains the Asynq task handler for the generation worker.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"milton_prism/core/worker/generation/application"
	workerdomain "milton_prism/core/worker/generation/domain"
	applog "milton_prism/pkg/log"

	"github.com/hibiken/asynq"
)

// taskContextTimeout is the total wall-clock budget for one generation task.
// asynq's default is 30 min, which conflicts with the 60-min container timeout.
// Each service may take up to 60 min; allow 90 min total so post-run persist
// operations still have a valid context after the container exits.
const taskContextTimeout = 90 * time.Minute

// TaskTypeGenerationRun is the Asynq task type for generation:run jobs.
const TaskTypeGenerationRun = "generation:run"

// GenerationJobHandler is the Asynq handler for generation:run tasks.
type GenerationJobHandler struct {
	pipeline *application.Pipeline
}

// NewGenerationJobHandler constructs a handler backed by pipeline.
func NewGenerationJobHandler(pipeline *application.Pipeline) *GenerationJobHandler {
	return &GenerationJobHandler{pipeline: pipeline}
}

// ProcessTask decodes the job payload and delegates to the generation pipeline.
func (h *GenerationJobHandler) ProcessTask(asynqCtx context.Context, t *asynq.Task) error {
	var payload workerdomain.JobPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: %w", asynq.SkipRetry, err)
	}

	// asynq encodes timeout=1800s (30 min) into every task message by default,
	// which fires before the 60-min container timeout. Bypass it by deriving
	// from context.Background with our own budget. Graceful shutdown is handled
	// by the asynq server's ShutdownTimeout; not forwarding asynqCtx here is
	// intentional — the task will be re-queued on forceful shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), taskContextTimeout)
	defer cancel()
	_ = asynqCtx // retained for signature compliance

	applog.Infof("generation-worker: processing task type=%s migration_id=%d", t.Type(), payload.MigrationID)
	return h.pipeline.Run(ctx, payload)
}
