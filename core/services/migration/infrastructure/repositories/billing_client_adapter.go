package repositories

import (
	"context"

	"milton_prism/core/services/migration/ports"
	"milton_prism/core/shared/grpc_client_sdk"
	billingsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/billing/v1"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"

	"google.golang.org/grpc/metadata"
)

var _ ports.BillingClient = (*BillingClientAdapter)(nil)

// BillingClientAdapter wraps the billing gRPC client and exposes the driven-port
// operations needed by the migration service. The underlying client reuses the
// analysis-services gRPC connection (BillingService is co-served there).
type BillingClientAdapter struct {
	client *grpc_client_sdk.BillingGrpcClient
}

// NewBillingClientAdapter wraps a BillingGrpcClient behind the driven port.
func NewBillingClientAdapter(c *grpc_client_sdk.BillingGrpcClient) *BillingClientAdapter {
	return &BillingClientAdapter{client: c}
}

// GetUserPlan fetches the user's billing plan, forwarding the caller's auth
// token so the billing service can authorize the lookup (a user may only query
// their own plan; system callers may query any user).
func (a *BillingClientAdapter) GetUserPlan(ctx context.Context, userID uint64) (*billingv1.Plan, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	return a.client.GetUserPlan(ctx, &billingsvcv1.GetUserPlanRequest{UserId: userID})
}
