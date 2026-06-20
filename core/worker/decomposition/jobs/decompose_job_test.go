package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"milton_prism/core/worker/decomposition/application"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/mocks"

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
	return asynq.NewTask(TaskTypeDecomposeRun, data)
}

// withRetry overrides isLastAttempt for the duration of a test.
func withRetry(last bool) func() {
	old := isLastAttempt
	isLastAttempt = func(_ context.Context) bool { return last }
	return func() { isLastAttempt = old }
}

// newPipelineWithMocks returns a Pipeline wired with a mock loader and plan writer.
// The loader returns a fixed error so Run fails deterministically at stage 1
// (before the detector is ever called).
func newPipelineWithMocks(t *testing.T, loaderErr error) (*application.Pipeline, *mocks.MockPlanWriter) {
	t.Helper()
	loader := &mocks.MockGraphLoader{}
	loader.On("Load", mock.Anything, mock.Anything).Return(nil, loaderErr)
	detector := &mocks.MockInfraDetector{} // never called: loader fails first
	planWriter := &mocks.MockPlanWriter{}
	p := application.NewPipeline(loader, detector).
		WithPlanWriter(planWriter)
	return p, planWriter
}

// ── handler tests ─────────────────────────────────────────────────────────────

func TestDecomposeHandler_TransientFailure_NoMarkFailed(t *testing.T) {
	defer withRetry(false)() // NOT last attempt
	loaderErr := errors.New("connection timeout")
	p, planWriter := newPipelineWithMocks(t, loaderErr)
	h := NewDecomposeJobHandler(p)

	task := taskPayload(t, workerdomain.JobPayload{MigrationID: 42, SummaryID: 1})
	err := h.ProcessTask(context.Background(), task)
	require.Error(t, err)
	// MarkFailed must NOT be called — Asynq will retry.
	planWriter.AssertNotCalled(t, "MarkFailed", mock.Anything, mock.Anything, mock.Anything)
}

func TestDecomposeHandler_PermanentFailure_MarksFailed(t *testing.T) {
	defer withRetry(true)() // last attempt: retries exhausted
	loaderErr := errors.New("stage 1 (graph-load): summary not found")
	p, planWriter := newPipelineWithMocks(t, loaderErr)
	planWriter.On("MarkFailed", mock.Anything, uint64(42), mock.MatchedBy(func(r string) bool {
		return assert.Contains(t, r, "stage 1") && assert.Contains(t, r, "You can retry")
	})).Return(nil)
	h := NewDecomposeJobHandler(p)

	task := taskPayload(t, workerdomain.JobPayload{MigrationID: 42, SummaryID: 1})
	err := h.ProcessTask(context.Background(), task)
	require.Error(t, err)
	planWriter.AssertExpectations(t)
}

func TestDecomposeHandler_PermanentFailure_ZeroMigrationID_NoMarkFailed(t *testing.T) {
	defer withRetry(true)()
	loaderErr := errors.New("boom")
	p, planWriter := newPipelineWithMocks(t, loaderErr)
	h := NewDecomposeJobHandler(p)

	task := taskPayload(t, workerdomain.JobPayload{MigrationID: 0, SummaryID: 1})
	err := h.ProcessTask(context.Background(), task)
	require.Error(t, err)
	planWriter.AssertNotCalled(t, "MarkFailed", mock.Anything, mock.Anything, mock.Anything)
}

func TestDecomposeFailureReason_TruncatesLongError(t *testing.T) {
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	reason := decomposeFailureReason(errors.New(string(long)))
	assert.Less(t, len(reason), 260)
	assert.Contains(t, reason, "...")
	assert.Contains(t, reason, "You can retry")
}
