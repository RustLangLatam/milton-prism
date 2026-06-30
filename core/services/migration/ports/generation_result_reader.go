package ports

import (
	"context"

	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

// ServiceGenerationUsage is the token/cost footprint of ONE service's generation
// record, read from the generation_results collection. It is the unit of
// per-service GENERATION billing: each done service is recorded in billing
// exactly once at its final cost, keyed by (migration_id, service_name).
//
// TokensIn is the sum of every input tier (fresh + cache-creation + cache-read);
// TokensOut is the output tokens; RealCostUSD is the agent-reported
// total_cost_usd (>0 only in apikey mode); Model is the model id reported for the
// run, used to estimate cost by token when RealCostUSD is 0 (subscription mode).
type ServiceGenerationUsage struct {
	ServiceName string
	// Status is the per-service lifecycle status (generating / done / failed).
	// Only done services are billed.
	Status      string
	TokensIn    int64
	TokensOut   int64
	RealCostUSD float64
	Model       string
}

// GenerationResultReader reads per-service generation records stored by the
// autonomous generation worker in the generation_results collection.
type GenerationResultReader interface {
	ReadResults(ctx context.Context, migrationID uint64) ([]*migrationv1.ServiceGenerationRecord, error)
	// ReadServiceUsages returns the per-service token/cost footprint of every
	// generation record for a migration. Used to record GENERATION spend
	// per-service (one billing record per done service, idempotent across retries).
	ReadServiceUsages(ctx context.Context, migrationID uint64) ([]ServiceGenerationUsage, error)
}
