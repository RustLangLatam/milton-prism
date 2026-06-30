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

	// ListBilledServiceNames returns the set of service names that already have a
	// usage record for (migrationID, op). It makes per-service GENERATION billing
	// idempotent: the finalize routine skips a service that is already billed, so
	// a service is charged exactly once across generation retries. Records with an
	// empty service_name are normalized out. The caller's auth token authorizes
	// the lookup.
	ListBilledServiceNames(ctx context.Context, migrationID uint64, op billingv1.UsageOperation) (map[string]bool, error)

	UsageRecorder
}

// UsageSpend describes a single LLM spend event the migration service wants
// accounted in billing. migration-services is a separate binary with no
// co-located billing repository, so the spend is forwarded over the billing
// gRPC RecordUsage RPC. Operation is the billing UsageOperation enum value
// (USAGE_OPERATION_MIGRATION for roadmap/blueprint, USAGE_OPERATION_ASSESSMENT
// for the migrability assessor).
type UsageSpend struct {
	UserID      uint64
	MigrationID uint64
	// ServiceName attributes the spend to a single service within the migration
	// (empty = not service-scoped). GENERATION spend is recorded per-service so
	// billing stays idempotent per (migration_id, service_name) across retries.
	ServiceName string
	Operation   billingv1.UsageOperation
	TokensIn    int64
	TokensOut   int64
	CostUSD     float64
	Model       string
	// CostEstimated is true when CostUSD is an estimate from the billing price
	// table (a subscription / Claude-Code run reporting no per-call dollar cost)
	// rather than the provider's real cost. Persisted on the UsageRecord so the UI
	// can label estimated spend instead of presenting it as a billed amount.
	CostEstimated bool
}

// UsageRecorder persists an LLM spend event through billing. Implementations
// MUST be best-effort: a recording failure must never break the originating LLM
// flow — the caller logs a warning and swallows the error.
type UsageRecorder interface {
	RecordUsage(ctx context.Context, spend UsageSpend) error
}
