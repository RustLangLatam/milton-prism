package ports

import (
	"context"

	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
)

// PlanProvider is the driven port the analysis service uses to resolve a user's
// billing plan for quota enforcement. The billing capability is co-located in
// the analysis-services binary, so this is satisfied in-process by an adapter
// over the billing application service (no network hop on the hot path).
type PlanProvider interface {
	// GetUserPlan returns the billing plan associated with userID, defaulting to
	// the free plan when the user has no explicit association.
	GetUserPlan(ctx context.Context, userID uint64) (*billingv1.Plan, error)
}
