// Package jobs contains Asynq task handlers for the analysis worker.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"milton_prism/core/worker/analysis/application"
	workerdomain "milton_prism/core/worker/analysis/domain"
	applog "milton_prism/pkg/log"

	"github.com/hibiken/asynq"
)

// TaskTypeAnalysisRun is the Asynq task type for analysis pipeline jobs.
// Must match the constant used by the enqueuer in the analysis service.
const TaskTypeAnalysisRun = "analysis:run"

// isLastAttempt returns true when this invocation is the last Asynq retry.
// Overridable in tests via package-level assignment.
var isLastAttempt = func(ctx context.Context) bool {
	retried, retriedOK := asynq.GetRetryCount(ctx)
	maxRetry, maxRetryOK := asynq.GetMaxRetry(ctx)
	return retriedOK && maxRetryOK && retried >= maxRetry
}

// AnalysisJobHandler is the Asynq handler for analysis:run tasks.
type AnalysisJobHandler struct {
	pipeline *application.Pipeline
}

// NewAnalysisJobHandler constructs an AnalysisJobHandler backed by pipeline.
func NewAnalysisJobHandler(pipeline *application.Pipeline) *AnalysisJobHandler {
	return &AnalysisJobHandler{pipeline: pipeline}
}

// ProcessTask decodes the job payload and delegates to the analysis pipeline.
// On the last Asynq retry, a pipeline failure transitions the analysis summary
// (and the associated migration, if any) to FAILED with a human-readable reason.
func (h *AnalysisJobHandler) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var payload workerdomain.JobPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		// Unrecoverable: bad payload should not be retried.
		return fmt.Errorf("%w: %w", asynq.SkipRetry, err)
	}
	applog.Infof("analysis-worker: processing task type=%s summary_id=%d", t.Type(), payload.SummaryID)
	if err := h.pipeline.Run(ctx, payload); err != nil {
		if isLastAttempt(ctx) {
			reason := analysisFailureReason(err)
			// Always mark the analysis summary as FAILED on the final attempt.
			applog.Errorf("analysis-worker: retries exhausted summary_id=%d — marking FAILED reason=%q",
				payload.SummaryID, reason)
			if markErr := h.pipeline.MarkAnalysisFailed(ctx, payload.SummaryID, reason); markErr != nil {
				applog.Errorf("analysis-worker: MarkAnalysisFailed failed summary_id=%d: %v", payload.SummaryID, markErr)
			}
			// Also mark the associated migration as FAILED for migration-linked analyses.
			if payload.MigrationID != 0 {
				applog.Errorf("analysis-worker: retries exhausted migration_id=%d — marking FAILED",
					payload.MigrationID)
				if markErr := h.pipeline.MarkFailed(ctx, payload.MigrationID, reason); markErr != nil {
					applog.Errorf("analysis-worker: MarkFailed failed migration_id=%d: %v", payload.MigrationID, markErr)
				}
			}
		}
		return err
	}
	return nil
}

// analysisFailureReason builds a human-readable failure message from a
// pipeline error. CloneErrors carry a pre-classified user message (no
// credentials, actionable) and are forwarded directly. Other errors are
// truncated and wrapped in a generic prefix.
func analysisFailureReason(err error) string {
	var ce *workerdomain.CloneError
	if errors.As(err, &ce) {
		return ce.Message + " You can update the token or URL and retry this analysis."
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return fmt.Sprintf("Repository analysis failed: %s. You can retry this analysis.", msg)
}
