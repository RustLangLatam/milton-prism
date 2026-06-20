package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"milton_prism/core/worker/analysis/application"
	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/mocks"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// taskPayload encodes a JobPayload as a JSON asynq.Task.
func taskPayload(t *testing.T, payload workerdomain.JobPayload) *asynq.Task {
	t.Helper()
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return asynq.NewTask(TaskTypeAnalysisRun, data)
}

// withRetry overrides isLastAttempt for the duration of a test.
func withRetry(last bool) func() {
	old := isLastAttempt
	isLastAttempt = func(_ context.Context) bool { return last }
	return func() { isLastAttempt = old }
}

// ── handler tests ─────────────────────────────────────────────────────────────

func TestAnalysisHandler_Success_NoMarkFailed(t *testing.T) {
	writer := &mocks.MockSummaryWriter{}
	writer.On("Write", mock.Anything, mock.Anything).Return(nil)
	// Neither MarkFailed nor MarkAnalysisFailed must be called on success.

	p := application.NewPipeline(writer)
	h := NewAnalysisJobHandler(p)

	task := taskPayload(t, workerdomain.JobPayload{SummaryID: 1, MigrationID: 99})
	err := h.ProcessTask(context.Background(), task)
	require.NoError(t, err)
	writer.AssertNotCalled(t, "MarkFailed", mock.Anything, mock.Anything, mock.Anything)
	writer.AssertNotCalled(t, "MarkAnalysisFailed", mock.Anything, mock.Anything, mock.Anything)
}

func TestAnalysisHandler_TransientFailure_NoMarkFailed(t *testing.T) {
	defer withRetry(false)() // NOT last attempt — Asynq will retry
	writer := &mocks.MockSummaryWriter{}
	writer.On("Write", mock.Anything, mock.Anything).Return(errors.New("network timeout"))

	p := application.NewPipeline(writer)
	h := NewAnalysisJobHandler(p)

	task := taskPayload(t, workerdomain.JobPayload{SummaryID: 1, MigrationID: 99})
	err := h.ProcessTask(context.Background(), task)
	require.Error(t, err)
	writer.AssertNotCalled(t, "MarkFailed", mock.Anything, mock.Anything, mock.Anything)
	writer.AssertNotCalled(t, "MarkAnalysisFailed", mock.Anything, mock.Anything, mock.Anything)
}

func TestAnalysisHandler_PermanentFailure_MarksBoth(t *testing.T) {
	defer withRetry(true)() // last attempt: retries exhausted
	pipelineErr := errors.New("stage 1 (acquire): repository not found")
	writer := &mocks.MockSummaryWriter{}
	writer.On("Write", mock.Anything, mock.Anything).Return(pipelineErr)
	writer.On("MarkAnalysisFailed", mock.Anything, uint64(1), mock.MatchedBy(func(r string) bool {
		return assert.Contains(t, r, "stage 1 (acquire)")
	})).Return(nil)
	writer.On("MarkFailed", mock.Anything, uint64(99), mock.MatchedBy(func(r string) bool {
		return assert.Contains(t, r, "stage 1 (acquire)")
	})).Return(nil)

	p := application.NewPipeline(writer)
	h := NewAnalysisJobHandler(p)

	task := taskPayload(t, workerdomain.JobPayload{SummaryID: 1, MigrationID: 99})
	err := h.ProcessTask(context.Background(), task)
	// Handler still returns the pipeline error so Asynq archives correctly.
	require.Error(t, err)
	assert.ErrorIs(t, err, pipelineErr)
	writer.AssertExpectations(t)
}

func TestAnalysisHandler_PermanentFailure_Standalone_MarksAnalysisFailed(t *testing.T) {
	defer withRetry(true)()
	writer := &mocks.MockSummaryWriter{}
	writer.On("Write", mock.Anything, mock.Anything).Return(errors.New("stage 1 (acquire): boom"))
	// Standalone (MigrationID=0): MarkAnalysisFailed must be called; MarkFailed must NOT.
	writer.On("MarkAnalysisFailed", mock.Anything, uint64(1), mock.AnythingOfType("string")).Return(nil)

	p := application.NewPipeline(writer)
	h := NewAnalysisJobHandler(p)

	task := taskPayload(t, workerdomain.JobPayload{SummaryID: 1, MigrationID: 0})
	err := h.ProcessTask(context.Background(), task)
	require.Error(t, err)
	writer.AssertExpectations(t)
	writer.AssertNotCalled(t, "MarkFailed", mock.Anything, mock.Anything, mock.Anything)
}

func TestAnalysisFailureReason_TruncatesLongError(t *testing.T) {
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	reason := analysisFailureReason(errors.New(string(long)))
	// 300-char error is capped to 200 + "..." — full reason fits within ~270 chars.
	assert.Less(t, len(reason), 280)
	assert.Contains(t, reason, "...")
	assert.Contains(t, reason, "You can retry")
}

// ── analysisFailureReason with CloneError ─────────────────────────────────────

func TestAnalysisFailureReason_CloneError_AuthFailure(t *testing.T) {
	ce := &workerdomain.CloneError{
		Message: "Authentication failed for https://github.com/org/repo: the access token is invalid or does not have read permission on this repository.",
		Detail:  "fatal: could not read Password for 'https://ghp_xxx@github.com': No such device or address",
	}
	// Wrap as pipeline would: "stage 1 (acquire): <CloneError>"
	wrapped := fmt.Errorf("stage 1 (acquire): %w", ce)

	reason := analysisFailureReason(wrapped)

	// Must contain the user-friendly message, NOT the raw detail.
	assert.Contains(t, reason, "Authentication failed for https://github.com/org/repo")
	assert.Contains(t, reason, "You can update the token or URL")
	assert.NotContains(t, reason, "could not read Password")
	assert.NotContains(t, reason, "ghp_xxx")
	assert.NotContains(t, reason, "stage 1 (acquire)")
}

func TestAnalysisFailureReason_CloneError_NotFound(t *testing.T) {
	ce := &workerdomain.CloneError{
		Message: "Repository not found at https://github.com/org/repo: verify the URL is correct and the token has access.",
		Detail:  "ERROR: Repository not found.\nfatal: Could not read from remote repository.",
	}
	wrapped := fmt.Errorf("stage 1 (acquire): %w", ce)

	reason := analysisFailureReason(wrapped)

	assert.Contains(t, reason, "Repository not found")
	assert.NotContains(t, reason, "stage 1 (acquire)")
	assert.NotContains(t, reason, "Could not read from remote")
}

func TestAnalysisFailureReason_CloneError_NetworkFailure(t *testing.T) {
	ce := &workerdomain.CloneError{
		Message: "Could not connect to https://github.com/org/repo: check the URL or network connectivity.",
		Detail:  "fatal: unable to connect to github.com",
	}
	wrapped := fmt.Errorf("stage 1 (acquire): %w", ce)

	reason := analysisFailureReason(wrapped)

	assert.Contains(t, reason, "Could not connect")
	assert.NotContains(t, reason, "stage 1 (acquire)")
}

func TestAnalysisFailureReason_CloneError_Detail_NeverLeaks(t *testing.T) {
	// The Detail field may contain a raw token — must never appear in the reason.
	ce := &workerdomain.CloneError{
		Message: "Authentication failed for https://github.com/org/repo: the access token is invalid.",
		Detail:  "fatal: could not read Password for 'https://ghp_SECRET_TOKEN@github.com': No such device or address",
	}
	reason := analysisFailureReason(fmt.Errorf("stage 1 (acquire): %w", ce))
	assert.NotContains(t, reason, "ghp_SECRET_TOKEN", "raw token in Detail must not appear in failure_reason")
}
