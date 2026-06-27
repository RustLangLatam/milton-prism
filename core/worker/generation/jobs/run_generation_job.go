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

// Per-service-scaled task budget (GAP #6d).
//
// The old design used a single flat 90-min budget for the WHOLE task, shared by
// every service in the migration. asynq's own 30-min default is bypassed (see
// ProcessTask) because it fires before even one container timeout. But a flat
// total is itself fragile: with the per-service container timeouts now reaching
// 90 min on the heavy tier (rust/java), the 2nd or 3rd service in a multi-service
// migration exhausts the shared budget and dies with "context deadline exceeded"
// (observed: mig36 service=profile). Asynq's retry masks it via idempotence, but
// slowly and unreliably.
//
// Fix: the TOTAL budget scales linearly with the number of services so a slow
// service never starves the services that follow it, and the final service's
// post-container persistence still runs inside a live context:
//
//	budget = baseTaskOverhead + numServices*perServiceBudget + persistOverhead
const (
	// perServiceBudget is the wall-clock budget for one service: its container
	// run plus the persistence of its artifacts. It is kept >= the heaviest
	// per-service container timeout (heavy tier = 90 min in claude_agent_invoker),
	// so a slow language (rust/java) can finish and still be persisted inside the
	// shared task context.
	perServiceBudget = 90 * time.Minute

	// baseTaskOverhead covers the package read, record bootstrapping, and the
	// final READY/FAILED state transition that bracket the per-service loop.
	baseTaskOverhead = 5 * time.Minute

	// persistOverhead is extra headroom so the context persisting the LAST
	// service's artifacts stays valid AFTER its container has already exited.
	persistOverhead = 15 * time.Minute

	// defaultServiceCountEstimate is the assumed service count for the rarer
	// generate-all path (no explicit ServiceFilter in the payload). Camino B
	// always drives with an explicit ServiceFilter (exact count), so this only
	// bounds the generate-all fallback; it must be generous enough to cover a
	// realistic multi-service migration without truncating the shared budget.
	defaultServiceCountEstimate = 6
)

// taskContextBudget derives the TOTAL wall-clock budget for one generation task
// from the number of services it will generate. numServices < 1 is clamped to 1
// (a task always generates at least one service).
func taskContextBudget(numServices int) time.Duration {
	if numServices < 1 {
		numServices = 1
	}
	return baseTaskOverhead + time.Duration(numServices)*perServiceBudget + persistOverhead
}

// payloadServiceCount returns the best available estimate of how many services a
// payload will generate: the explicit ServiceFilter length when present (the
// Camino B drive path always sets it, giving an exact count), else the
// conservative generate-all estimate.
func payloadServiceCount(payload workerdomain.JobPayload) int {
	if n := len(payload.ServiceFilter); n > 0 {
		return n
	}
	return defaultServiceCountEstimate
}

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
	// which fires before even a single container timeout. Bypass it by deriving
	// from context.Background with our own per-service-scaled budget (#6d).
	// Graceful shutdown is handled by the asynq server's ShutdownTimeout; not
	// forwarding asynqCtx here is intentional — the task will be re-queued on
	// forceful shutdown.
	numServices := payloadServiceCount(payload)
	budget := taskContextBudget(numServices)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	_ = asynqCtx // retained for signature compliance

	applog.Infof("generation-worker: processing task type=%s migration_id=%d services~=%d budget=%s",
		t.Type(), payload.MigrationID, numServices, budget)
	return h.pipeline.Run(ctx, payload)
}
