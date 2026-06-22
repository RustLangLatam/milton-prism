package ports

import (
	"context"

	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
)

// BillingClient is the driven port the migration service uses to resolve a
// user's billing plan for quota enforcement. migration-services is a separate
// binary; BillingService is served on the same analysis-services gRPC endpoint,
// so the adapter reuses the analysis client's gRPC connection.
type BillingClient interface {
	// GetUserPlan returns the billing plan associated with userID, defaulting to
	// the free plan when the user has no explicit association. The caller's auth
	// token must be forwarded so the billing service can authorize the lookup.
	GetUserPlan(ctx context.Context, userID uint64) (*billingv1.Plan, error)
}
