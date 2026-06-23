package repositories

import (
	"context"
	"errors"
	"testing"

	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/ports"
	analysisports "milton_prism/core/worker/analysis/ports"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingStub captures the spends passed to RecordUsage. When err is non-nil
// it returns it from every call so tests can assert best-effort swallowing.
type recordingStub struct {
	calls []ports.UsageSpend
	err   error
}

func (r *recordingStub) RecordUsage(_ context.Context, spend ports.UsageSpend) error {
	r.calls = append(r.calls, spend)
	return r.err
}

// fixedModelClient returns a fixed ModelResponse for every Complete call.
type fixedModelClient struct {
	resp analysisports.ModelResponse
}

func (c *fixedModelClient) Complete(_ context.Context, _ analysisports.ModelRequest) (analysisports.ModelResponse, error) {
	return c.resp, nil
}

const enrichResponseJSON = `{"steps":[{"step_order":1,"narrative":"Extract the domain entities into a dedicated package separating business rules from infrastructure wiring."}]}`

func enrichRoadmapFixture() *domain.RestructuringRoadmap {
	return &domain.RestructuringRoadmap{
		ActionPlan: []*migrationv1.ActionItem{
			{Order: 1, Kind: "EXTRACT_DOMAIN", Subject: "backend.funcs", Impact: 40, Blocking: true},
		},
	}
}

// TestRoadmapEnricher_RecordsSpend verifies the enricher records one MIGRATION
// spend with the model's token counts after a successful LLM call.
func TestRoadmapEnricher_RecordsSpend(t *testing.T) {
	t.Parallel()

	rec := &recordingStub{}
	adapter := &RoadmapEnricherAdapter{
		client:   &fixedModelClient{resp: analysisports.ModelResponse{Content: enrichResponseJSON, InputTokens: 111, OutputTokens: 222, CostUSD: 0.0009}},
		recorder: rec,
	}

	out, err := adapter.Enrich(context.Background(), 42, 7, enrichRoadmapFixture())
	require.NoError(t, err)
	require.NotNil(t, out)

	require.Len(t, rec.calls, 1, "exactly one spend must be recorded")
	got := rec.calls[0]
	assert.Equal(t, uint64(42), got.UserID)
	assert.Equal(t, uint64(7), got.MigrationID)
	assert.Equal(t, billingv1.UsageOperation_USAGE_OPERATION_MIGRATION, got.Operation)
	assert.Equal(t, int64(111), got.TokensIn)
	assert.Equal(t, int64(222), got.TokensOut)
}

// TestRoadmapEnricher_RecorderErrorSwallowed verifies a recorder failure does
// not break the enrichment (best-effort semantics).
func TestRoadmapEnricher_RecorderErrorSwallowed(t *testing.T) {
	t.Parallel()

	rec := &recordingStub{err: errors.New("billing unavailable")}
	adapter := &RoadmapEnricherAdapter{
		client:   &fixedModelClient{resp: analysisports.ModelResponse{Content: enrichResponseJSON, InputTokens: 5, OutputTokens: 6, CostUSD: 0.0001}},
		recorder: rec,
	}

	out, err := adapter.Enrich(context.Background(), 1, 2, enrichRoadmapFixture())
	require.NoError(t, err, "recorder error must be swallowed — enrichment must still succeed")
	require.NotNil(t, out)
	require.Len(t, rec.calls, 1, "RecordUsage was attempted exactly once")
}

// TestRoadmapEnricher_NilRecorder verifies a nil recorder is a safe no-op.
func TestRoadmapEnricher_NilRecorder(t *testing.T) {
	t.Parallel()

	adapter := &RoadmapEnricherAdapter{
		client:   &fixedModelClient{resp: analysisports.ModelResponse{Content: enrichResponseJSON, InputTokens: 5, OutputTokens: 6}},
		recorder: nil,
	}

	out, err := adapter.Enrich(context.Background(), 1, 2, enrichRoadmapFixture())
	require.NoError(t, err)
	require.NotNil(t, out)
}

// TestBlueprintGenerator_RecordsSpend verifies GenerateFromDigest records one
// MIGRATION spend with the model's token counts after a successful LLM call.
func TestBlueprintGenerator_RecordsSpend(t *testing.T) {
	t.Parallel()

	rec := &recordingStub{}
	adapter := &BlueprintGeneratorAdapter{
		client:   &stubBlueprintClient{content: blueprintRealServicesResponse},
		recorder: rec,
	}

	digest := conduitMockDigest()
	roadmap := &domain.RestructuringRoadmap{
		ActionPlan: []*migrationv1.ActionItem{
			{Order: 1, Kind: "DEFINE_BOUNDARIES", Subject: "conduit.articles", Blocking: false, Impact: 10},
		},
	}

	out, err := adapter.GenerateFromDigest(context.Background(), 99, 8, digest, roadmap)
	require.NoError(t, err)
	require.NotNil(t, out)

	require.Len(t, rec.calls, 1, "exactly one spend must be recorded")
	got := rec.calls[0]
	assert.Equal(t, uint64(99), got.UserID)
	assert.Equal(t, uint64(8), got.MigrationID)
	assert.Equal(t, billingv1.UsageOperation_USAGE_OPERATION_MIGRATION, got.Operation)
	// stubBlueprintClient returns InputTokens:10, OutputTokens:30.
	assert.Equal(t, int64(10), got.TokensIn)
	assert.Equal(t, int64(30), got.TokensOut)
}

// TestBlueprintGenerator_RecorderErrorSwallowed verifies a recorder failure does
// not break generation (best-effort semantics).
func TestBlueprintGenerator_RecorderErrorSwallowed(t *testing.T) {
	t.Parallel()

	rec := &recordingStub{err: errors.New("billing down")}
	adapter := &BlueprintGeneratorAdapter{
		client:   &stubBlueprintClient{content: blueprintRealServicesResponse},
		recorder: rec,
	}

	digest := conduitMockDigest()
	roadmap := &domain.RestructuringRoadmap{
		ActionPlan: []*migrationv1.ActionItem{
			{Order: 1, Kind: "DEFINE_BOUNDARIES", Subject: "conduit.articles", Blocking: false, Impact: 10},
		},
	}

	out, err := adapter.GenerateFromDigest(context.Background(), 1, 2, digest, roadmap)
	require.NoError(t, err, "recorder error must be swallowed — generation must still succeed")
	require.NotNil(t, out)
	require.Len(t, rec.calls, 1)
}
