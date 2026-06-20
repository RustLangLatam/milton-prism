// Package jobs contains the Asynq task handler for the decomposition worker.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	decompapp "milton_prism/core/worker/decomposition/application"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	applog "milton_prism/pkg/log"

	"github.com/hibiken/asynq"
)

// TaskTypeDecomposeRun is the Asynq task type for decompose:run jobs.
// Must match the constant used by the enqueuer in the analysis worker.
const TaskTypeDecomposeRun = "decompose:run"

// isLastAttempt returns true when this invocation is the last Asynq retry.
// Overridable in tests via package-level assignment.
var isLastAttempt = func(ctx context.Context) bool {
	retried, retriedOK := asynq.GetRetryCount(ctx)
	maxRetry, maxRetryOK := asynq.GetMaxRetry(ctx)
	return retriedOK && maxRetryOK && retried >= maxRetry
}

// DecomposeJobHandler is the Asynq handler for decompose:run tasks.
type DecomposeJobHandler struct {
	pipeline *decompapp.Pipeline
}

// NewDecomposeJobHandler constructs a DecomposeJobHandler backed by pipeline.
func NewDecomposeJobHandler(pipeline *decompapp.Pipeline) *DecomposeJobHandler {
	return &DecomposeJobHandler{pipeline: pipeline}
}

// ProcessTask decodes the job payload and delegates to the decomposition pipeline.
// On the last Asynq retry, a pipeline failure transitions the migration to
// FAILED with a human-readable reason so the user can act.
func (h *DecomposeJobHandler) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var payload workerdomain.JobPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		// Unrecoverable: a malformed payload must not be retried.
		return fmt.Errorf("%w: %w", asynq.SkipRetry, err)
	}
	applog.Infof("decomposition-worker: processing task type=%s migration_id=%d summary_id=%d",
		t.Type(), payload.MigrationID, payload.SummaryID)
	if err := h.pipeline.Run(ctx, payload); err != nil {
		if payload.MigrationID != 0 && isLastAttempt(ctx) {
			reason := decomposeFailureReason(err)
			applog.Errorf("decomposition-worker: retries exhausted migration_id=%d — marking FAILED reason=%q",
				payload.MigrationID, reason)
			if markErr := h.pipeline.MarkFailed(ctx, payload.MigrationID, reason); markErr != nil {
				applog.Errorf("decomposition-worker: MarkFailed failed migration_id=%d: %v", payload.MigrationID, markErr)
			}
		}
		return err
	}
	return nil
}

// decomposeFailureReason builds a human-readable failure message from a
// pipeline error. Truncates the technical error to keep it readable.
func decomposeFailureReason(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return fmt.Sprintf("Decomposition failed: %s. You can retry this migration.", msg)
}
