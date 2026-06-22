package repositories

import (
	"context"
	"errors"
	"testing"

	"milton_prism/core/services/analysis/ports"
	workerapp "milton_prism/core/worker/decomposition/application"

	"github.com/stretchr/testify/assert"
)

// fakeRecorder captures RecordSpend calls and optionally returns an error.
type fakeRecorder struct {
	calls  []ports.UsageSpend
	retErr error
}

func (f *fakeRecorder) RecordSpend(_ context.Context, s ports.UsageSpend) error {
	f.calls = append(f.calls, s)
	return f.retErr
}

func nonZeroResult() workerapp.AssessResult {
	return workerapp.AssessResult{InputTokens: 1200, OutputTokens: 300, CostUSD: 0.0024}
}

func TestRecordSpend_NilRecorder_NoPanic(t *testing.T) {
	t.Parallel()
	a := &AnalysisMigrabilityAssessorAdapter{} // usageRecorder nil
	a.recordSpend(context.Background(), 42, 10, 0, nonZeroResult())
	// no panic, nothing to assert beyond reaching here
}

func TestRecordSpend_ZeroOwner_Skips(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	a := (&AnalysisMigrabilityAssessorAdapter{}).WithUsageRecorder(rec)
	a.recordSpend(context.Background(), 0, 10, 0, nonZeroResult())
	assert.Empty(t, rec.calls, "must skip when owner is unknown")
}

func TestRecordSpend_ZeroSpend_Skips(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	a := (&AnalysisMigrabilityAssessorAdapter{}).WithUsageRecorder(rec)
	a.recordSpend(context.Background(), 42, 10, 0, workerapp.AssessResult{})
	assert.Empty(t, rec.calls, "must skip when no tokens / cost were spent")
}

func TestRecordSpend_Records(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	a := (&AnalysisMigrabilityAssessorAdapter{}).WithUsageRecorder(rec)
	a.recordSpend(context.Background(), 42, 10, 99, nonZeroResult())
	if assert.Len(t, rec.calls, 1) {
		got := rec.calls[0]
		assert.Equal(t, uint64(42), got.UserID)
		assert.Equal(t, uint64(10), got.AnalysisID)
		assert.Equal(t, uint64(99), got.MigrationID)
		assert.Equal(t, "assessment", got.Operation)
		assert.Equal(t, int64(1200), got.TokensIn)
		assert.Equal(t, int64(300), got.TokensOut)
		assert.InDelta(t, 0.0024, got.CostUSD, 1e-9)
	}
}

func TestRecordSpend_ErrorIsSwallowed(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{retErr: errors.New("mongo down")}
	a := (&AnalysisMigrabilityAssessorAdapter{}).WithUsageRecorder(rec)
	// Must not panic or propagate — best-effort.
	a.recordSpend(context.Background(), 42, 10, 0, nonZeroResult())
	assert.Len(t, rec.calls, 1)
}
