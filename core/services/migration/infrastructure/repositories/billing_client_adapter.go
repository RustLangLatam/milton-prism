package repositories

import (
	"context"
	"fmt"

	"milton_prism/core/services/migration/ports"
	"milton_prism/core/shared/auth_token"
	"milton_prism/core/shared/grpc_client_sdk"
	billingsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/billing/v1"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"google.golang.org/grpc/metadata"
)

var _ ports.BillingClient = (*BillingClientAdapter)(nil)

// SystemTokenProvider returns a short-lived system access token (system_user)
// used to authorize internal RecordUsage calls. It is derived from the binary's
// own token signing key — only migration-services mints it. The returned string
// is a secret and is never logged.
type SystemTokenProvider func(ctx context.Context) (string, error)

// BillingClientAdapter wraps the billing gRPC client and exposes the driven-port
// operations needed by the migration service. The underlying client reuses the
// analysis-services gRPC connection (BillingService is co-served there).
//
// Authorization model:
//   - GetUserPlan / CountUsageRecords forward the CALLER's auth token (a user may
//     only see their own data; system callers may see any).
//   - RecordUsage requires a SYSTEM caller (billing rejects non-system writers
//     with BIL101). It does NOT forward the caller's token; it builds a fresh
//     outgoing context carrying the system token from tokenProvider. When no
//     tokenProvider is wired the adapter falls back to forwarding the caller's
//     token (legacy behaviour) so non-system flows degrade visibly rather than
//     silently swapping identities.
type BillingClientAdapter struct {
	client        *grpc_client_sdk.BillingGrpcClient
	tokenProvider SystemTokenProvider
}

// NewBillingClientAdapter wraps a BillingGrpcClient behind the driven port.
// tokenProvider supplies the system token used only by RecordUsage; pass nil to
// keep the legacy token-forwarding behaviour (RecordUsage will then fail BIL101
// for non-system callers).
func NewBillingClientAdapter(c *grpc_client_sdk.BillingGrpcClient, tokenProvider SystemTokenProvider) *BillingClientAdapter {
	return &BillingClientAdapter{client: c, tokenProvider: tokenProvider}
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

// ListBilledServiceNames forwards the caller's token and returns the set of
// service names that already have a usage record for (migrationID, op). Used by
// the generation-spend finalize to stay idempotent per (migration, service):
// a service already present is never billed again. Records with an empty
// service_name are skipped (they are not service-scoped).
func (a *BillingClientAdapter) ListBilledServiceNames(ctx context.Context, migrationID uint64, op billingv1.UsageOperation) (map[string]bool, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	resp, err := a.client.ListUsageRecords(ctx, &billingsvcv1.ListUsageRecordsRequest{
		MigrationId: migrationID,
		PageParams:  &queryparamsv1.PageQueryParams{PageSize: 100},
	})
	if err != nil {
		return nil, err
	}
	billed := make(map[string]bool)
	for _, r := range resp.GetUsageRecords() {
		if r.GetOperation() == op && r.GetServiceName() != "" {
			billed[r.GetServiceName()] = true
		}
	}
	return billed, nil
}

// RecordUsage forwards an LLM spend event to the billing service over the
// RecordUsage gRPC RPC. Billing requires a SYSTEM caller for this write, so the
// adapter does NOT forward the caller's token: it derives a fresh outgoing
// context from ctx (preserving the deadline) and attaches the system token from
// tokenProvider, replacing any inbound auth metadata. Best-effort: the returned
// error is logged and swallowed by the caller — it must never break the LLM flow.
func (a *BillingClientAdapter) RecordUsage(ctx context.Context, spend ports.UsageSpend) error {
	out, err := a.systemOutgoingContext(ctx)
	if err != nil {
		return err
	}
	_, rpcErr := a.client.RecordUsage(out, &billingsvcv1.RecordUsageRequest{
		UsageRecord: &billingv1.UsageRecord{
			UserId:        spend.UserID,
			MigrationId:   spend.MigrationID,
			ServiceName:   spend.ServiceName,
			Operation:     spend.Operation,
			TokensIn:      spend.TokensIn,
			TokensOut:     spend.TokensOut,
			CostUsd:       spend.CostUSD,
			Model:         spend.Model,
			CostEstimated: spend.CostEstimated,
		},
	})
	return rpcErr
}

// systemOutgoingContext returns an outgoing context carrying ONLY the system
// authorization token (no inbound user metadata is forwarded). When no
// tokenProvider is configured it falls back to forwarding the inbound metadata
// so the legacy path still works (and visibly fails BIL101 for non-system).
func (a *BillingClientAdapter) systemOutgoingContext(ctx context.Context) (context.Context, error) {
	if a.tokenProvider == nil {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			return metadata.NewOutgoingContext(ctx, md), nil
		}
		return ctx, nil
	}
	tok, err := a.tokenProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("billing: system token: %w", err)
	}
	// Fresh metadata: deliberately drop the inbound user token so the billing
	// service sees ONLY the system identity for this write.
	md := metadata.New(map[string]string{auth_token.TokenAccessName: tok})
	return metadata.NewOutgoingContext(ctx, md), nil
}
