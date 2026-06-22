package application_test

import (
	"context"
	"errors"
	"testing"

	"milton_prism/core/services/billing/application"
	"milton_prism/core/services/billing/domain"
	"milton_prism/core/services/billing/mocks"
	"milton_prism/core/services/billing/ports"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func validRecord(userID, analysisID uint64) *domain.UsageRecord {
	return &billingv1.UsageRecord{
		UserId:     userID,
		AnalysisId: analysisID,
		Operation:  billingv1.UsageOperation_USAGE_OPERATION_ASSESSMENT,
		TokensIn:   100,
		TokensOut:  50,
		CostUsd:    0.0123,
		Model:      "claude-haiku-4-5-20251001",
	}
}

// ─── RecordUsage ─────────────────────────────────────────────────────────────

func TestRecordUsage_MissingPayload(t *testing.T) {
	t.Parallel()
	svc := application.NewService(&mocks.MockUsageRepository{}, &mocks.MockPlanRepository{})
	_, err := svc.RecordUsage(context.Background(), nil)
	assert.ErrorIs(t, err, domain.ErrMissingPayload)
}

func TestRecordUsage_MissingUserID(t *testing.T) {
	t.Parallel()
	svc := application.NewService(&mocks.MockUsageRepository{}, &mocks.MockPlanRepository{})
	_, err := svc.RecordUsage(context.Background(), validRecord(0, 10))
	assert.ErrorIs(t, err, domain.ErrMissingUserID)
}

func TestRecordUsage_OK(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUsageRepository{}
	stored := validRecord(42, 10)
	stored.Identifier = 10001
	repo.On("Record", mock.Anything, mock.Anything).Return(stored, nil)
	svc := application.NewService(repo, &mocks.MockPlanRepository{})
	out, err := svc.RecordUsage(context.Background(), validRecord(42, 10))
	assert.NoError(t, err)
	assert.Equal(t, uint64(10001), out.GetIdentifier())
	repo.AssertExpectations(t)
}

func TestRecordUsage_RepoErrorWrapsInternal(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUsageRepository{}
	repo.On("Record", mock.Anything, mock.Anything).Return((*domain.UsageRecord)(nil), errors.New("mongo down"))
	svc := application.NewService(repo, &mocks.MockPlanRepository{})
	_, err := svc.RecordUsage(context.Background(), validRecord(42, 10))
	assert.ErrorIs(t, err, domain.ErrInternal)
}

// ─── AggregateUsage ──────────────────────────────────────────────────────────

func TestAggregateUsage_DelegatesFilter(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUsageRepository{}
	want := ports.UsageFilter{UserID: 42}
	total := &domain.UsageTotals{RecordCount: 2, TokensIn: 200, TokensOut: 100, CostUsd: 0.5}
	repo.On("Aggregate", mock.Anything, want).Return(total, []*domain.OperationUsage{}, nil)
	svc := application.NewService(repo, &mocks.MockPlanRepository{})
	got, _, err := svc.AggregateUsage(context.Background(), want)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), got.GetRecordCount())
	assert.InDelta(t, 0.5, got.GetCostUsd(), 1e-9)
	repo.AssertExpectations(t)
}

// ─── Plans ───────────────────────────────────────────────────────────────────

func TestListPlans_ReturnsAtLeastThree(t *testing.T) {
	t.Parallel()
	svc := application.NewService(&mocks.MockUsageRepository{}, &mocks.MockPlanRepository{})
	plans := svc.ListPlans(context.Background())
	assert.GreaterOrEqual(t, len(plans), 3)
	codes := map[string]bool{}
	for _, p := range plans {
		codes[p.GetCode()] = true
	}
	assert.True(t, codes[domain.PlanCodeFree])
	assert.True(t, codes[domain.PlanCodePro])
	assert.True(t, codes[domain.PlanCodeEnterprise])
}

func TestGetUserPlan_DefaultWhenNoAssociation(t *testing.T) {
	t.Parallel()
	planRepo := &mocks.MockPlanRepository{}
	planRepo.On("GetUserPlanCode", mock.Anything, uint64(42)).Return("", false, nil)
	svc := application.NewService(&mocks.MockUsageRepository{}, planRepo)
	plan, err := svc.GetUserPlan(context.Background(), 42)
	assert.NoError(t, err)
	assert.Equal(t, domain.PlanCodeFree, plan.GetCode())
}

func TestGetUserPlan_ExplicitAssociation(t *testing.T) {
	t.Parallel()
	planRepo := &mocks.MockPlanRepository{}
	planRepo.On("GetUserPlanCode", mock.Anything, uint64(42)).Return(domain.PlanCodePro, true, nil)
	svc := application.NewService(&mocks.MockUsageRepository{}, planRepo)
	plan, err := svc.GetUserPlan(context.Background(), 42)
	assert.NoError(t, err)
	assert.Equal(t, domain.PlanCodePro, plan.GetCode())
}

func TestGetUserPlan_StaleCodeFallsBackToDefault(t *testing.T) {
	t.Parallel()
	planRepo := &mocks.MockPlanRepository{}
	planRepo.On("GetUserPlanCode", mock.Anything, uint64(42)).Return("legacy_tier", true, nil)
	svc := application.NewService(&mocks.MockUsageRepository{}, planRepo)
	plan, err := svc.GetUserPlan(context.Background(), 42)
	assert.NoError(t, err)
	assert.Equal(t, domain.PlanCodeFree, plan.GetCode())
}

func TestGetUserPlan_MissingUserID(t *testing.T) {
	t.Parallel()
	svc := application.NewService(&mocks.MockUsageRepository{}, &mocks.MockPlanRepository{})
	_, err := svc.GetUserPlan(context.Background(), 0)
	assert.ErrorIs(t, err, domain.ErrMissingUserID)
}

func TestSetUserPlan_UnknownCode(t *testing.T) {
	t.Parallel()
	svc := application.NewService(&mocks.MockUsageRepository{}, &mocks.MockPlanRepository{})
	err := svc.SetUserPlan(context.Background(), 42, "platinum")
	assert.ErrorIs(t, err, domain.ErrPlanNotFound)
}

func TestSetUserPlan_OK(t *testing.T) {
	t.Parallel()
	planRepo := &mocks.MockPlanRepository{}
	planRepo.On("SetUserPlanCode", mock.Anything, uint64(42), domain.PlanCodeEnterprise).Return(nil)
	svc := application.NewService(&mocks.MockUsageRepository{}, planRepo)
	err := svc.SetUserPlan(context.Background(), 42, domain.PlanCodeEnterprise)
	assert.NoError(t, err)
	planRepo.AssertExpectations(t)
}
