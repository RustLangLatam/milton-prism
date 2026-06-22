package repositories

import (
	"context"

	billingapp "milton_prism/core/services/billing/application"
	"milton_prism/core/services/analysis/ports"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
)

var _ ports.PlanProvider = (*BillingPlanProviderAdapter)(nil)

// BillingPlanProviderAdapter adapts the co-located billing application Service to
// the analysis service's PlanProvider port. Because billing runs in-process in
// the analysis-services binary, plan resolution is a direct call with no network
// hop on the analysis hot path.
type BillingPlanProviderAdapter struct {
	svc *billingapp.Service
}

// NewBillingPlanProviderAdapter wraps the billing application service.
func NewBillingPlanProviderAdapter(svc *billingapp.Service) *BillingPlanProviderAdapter {
	return &BillingPlanProviderAdapter{svc: svc}
}

// GetUserPlan resolves the user's billing plan (defaulting to free).
func (a *BillingPlanProviderAdapter) GetUserPlan(ctx context.Context, userID uint64) (*billingv1.Plan, error) {
	return a.svc.GetUserPlan(ctx, userID)
}
