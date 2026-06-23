package ports

import (
	"context"

	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

// GenerationUsageTotals is the aggregate token/cost footprint of all per-service
// generation records of one migration. It is derived from the generation_results
// collection and used to record the migration's GENERATION spend in billing.
//
// TokensIn is the sum of every input tier (fresh + cache-creation + cache-read)
// across all services; TokensOut is the sum of output tokens. RealCostUSD is the
// sum of the agent-reported total_cost_usd (>0 only in apikey mode). Model is the
// dominant model id across the records (the one with the largest token footprint),
// used to estimate cost by token when RealCostUSD is 0 (subscription mode).
type GenerationUsageTotals struct {
	TokensIn    int64
	TokensOut   int64
	RealCostUSD float64
	Model       string
	// Records is the number of per-service records that contributed to the
	// totals. Zero means there is nothing to bill.
	Records int
}

// GenerationResultReader reads per-service generation records stored by the
// autonomous generation worker in the generation_results collection.
type GenerationResultReader interface {
	ReadResults(ctx context.Context, migrationID uint64) ([]*migrationv1.ServiceGenerationRecord, error)
	// ReadUsageTotals aggregates the token/cost footprint of all per-service
	// records for a migration. Used to record the GENERATION spend at close.
	ReadUsageTotals(ctx context.Context, migrationID uint64) (GenerationUsageTotals, error)
}
