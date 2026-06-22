// Package mocks contains testify-based stand-ins for the billing service driven
// ports. They live next to the real implementations so they can be used from any
// test in this module without a circular import.
package mocks

import (
	"context"

	"milton_prism/core/services/billing/domain"
	"milton_prism/core/services/billing/ports"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"github.com/stretchr/testify/mock"
)

// Compile-time interface checks.
var (
	_ ports.UsageRepository = (*MockUsageRepository)(nil)
	_ ports.PlanRepository  = (*MockPlanRepository)(nil)
)

// MockUsageRepository is a testify mock for ports.UsageRepository.
type MockUsageRepository struct {
	mock.Mock
}

func (m *MockUsageRepository) Record(ctx context.Context, rec *domain.UsageRecord) (*domain.UsageRecord, error) {
	args := m.Called(ctx, rec)
	v, _ := args.Get(0).(*domain.UsageRecord)
	return v, args.Error(1)
}

func (m *MockUsageRepository) List(ctx context.Context, filter ports.UsageFilter, params *queryparamsv1.PageQueryParams) ([]*domain.UsageRecord, *paginationv1.Pagination, error) {
	args := m.Called(ctx, filter, params)
	items, _ := args.Get(0).([]*domain.UsageRecord)
	pag, _ := args.Get(1).(*paginationv1.Pagination)
	return items, pag, args.Error(2)
}

func (m *MockUsageRepository) Aggregate(ctx context.Context, filter ports.UsageFilter) (*domain.UsageTotals, []*domain.OperationUsage, error) {
	args := m.Called(ctx, filter)
	tot, _ := args.Get(0).(*domain.UsageTotals)
	byOp, _ := args.Get(1).([]*domain.OperationUsage)
	return tot, byOp, args.Error(2)
}

// MockPlanRepository is a testify mock for ports.PlanRepository.
type MockPlanRepository struct {
	mock.Mock
}

func (m *MockPlanRepository) GetUserPlanCode(ctx context.Context, userID uint64) (string, bool, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Bool(1), args.Error(2)
}

func (m *MockPlanRepository) SetUserPlanCode(ctx context.Context, userID uint64, code string) error {
	args := m.Called(ctx, userID, code)
	return args.Error(0)
}
