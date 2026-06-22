// Package application contains the billing service's use-case logic. It depends
// only on domain types and driven port interfaces.
package application

import (
	"context"
	"fmt"

	"milton_prism/core/services/billing/domain"
	"milton_prism/core/services/billing/ports"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// Service orchestrates billing use cases: usage recording, usage aggregation,
// and plan catalog / association.
type Service struct {
	usage ports.UsageRepository
	plans ports.PlanRepository
}

// NewService wires the port implementations into the application service.
func NewService(usage ports.UsageRepository, plans ports.PlanRepository) *Service {
	return &Service{usage: usage, plans: plans}
}

// RecordUsage validates and persists a usage record.
func (s *Service) RecordUsage(ctx context.Context, rec *domain.UsageRecord) (*domain.UsageRecord, error) {
	if rec == nil {
		return nil, domain.ErrMissingPayload
	}
	if rec.GetUserId() == 0 {
		return nil, domain.ErrMissingUserID
	}
	out, err := s.usage.Record(ctx, rec)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	return out, nil
}

// ListUsageRecords returns a paginated, filtered list of raw usage records.
func (s *Service) ListUsageRecords(ctx context.Context, filter ports.UsageFilter, params *queryparamsv1.PageQueryParams) ([]*domain.UsageRecord, *paginationv1.Pagination, error) {
	return s.usage.List(ctx, filter, params)
}

// AggregateUsage returns the grand total and per-operation breakdown for filter.
func (s *Service) AggregateUsage(ctx context.Context, filter ports.UsageFilter) (*domain.UsageTotals, []*domain.OperationUsage, error) {
	return s.usage.Aggregate(ctx, filter)
}

// ListPlans returns the code-defined plan catalog.
func (s *Service) ListPlans(_ context.Context) []*domain.Plan {
	return domain.PlanCatalog
}

// GetUserPlan returns the plan a user is associated with, defaulting to the free
// plan when no explicit association exists.
func (s *Service) GetUserPlan(ctx context.Context, userID uint64) (*billingv1.Plan, error) {
	if userID == 0 {
		return nil, domain.ErrMissingUserID
	}
	code, ok, err := s.plans.GetUserPlanCode(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	if !ok {
		code = domain.DefaultPlanCode
	}
	plan := domain.PlanByCode(code)
	if plan == nil {
		// Stored code no longer maps to a catalog tier: fall back to default so a
		// stale association never produces a 404 for the user's plan.
		plan = domain.DefaultPlan()
		if plan == nil {
			return nil, domain.ErrPlanNotFound
		}
	}
	return plan, nil
}

// SetUserPlan associates a user with a catalog plan code. The code must exist in
// the catalog.
func (s *Service) SetUserPlan(ctx context.Context, userID uint64, code string) error {
	if userID == 0 {
		return domain.ErrMissingUserID
	}
	if domain.PlanByCode(code) == nil {
		return domain.ErrPlanNotFound
	}
	if err := s.plans.SetUserPlanCode(ctx, userID, code); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInternal, err)
	}
	return nil
}
