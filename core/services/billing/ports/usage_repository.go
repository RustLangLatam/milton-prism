// Package ports defines the driven ports for the billing service.
package ports

import (
	"context"

	"milton_prism/core/services/billing/domain"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// UsageFilter narrows usage queries. Zero-valued fields are not applied.
type UsageFilter struct {
	UserID      uint64
	AnalysisID  uint64
	MigrationID uint64
}

// UsageRepository persists and aggregates LLM usage records.
type UsageRepository interface {
	// Record persists a single usage record, assigning identifier and
	// create_time. It returns the stored record.
	Record(ctx context.Context, rec *domain.UsageRecord) (*domain.UsageRecord, error)
	// List returns a paginated, filtered page of raw usage records.
	List(ctx context.Context, filter UsageFilter, params *queryparamsv1.PageQueryParams) ([]*domain.UsageRecord, *paginationv1.Pagination, error)
	// Aggregate returns the grand total and per-operation breakdown for the
	// records matching filter.
	Aggregate(ctx context.Context, filter UsageFilter) (*domain.UsageTotals, []*domain.OperationUsage, error)
}

// PlanRepository persists the user→plan association. The plan catalog itself is
// code-defined (domain.PlanCatalog); this port only stores which catalog code a
// user is on.
type PlanRepository interface {
	// GetUserPlanCode returns the plan code a user is associated with. When no
	// association exists it returns domain.DefaultPlanCode and ok=false.
	GetUserPlanCode(ctx context.Context, userID uint64) (code string, ok bool, err error)
	// SetUserPlanCode upserts the user→plan association.
	SetUserPlanCode(ctx context.Context, userID uint64, code string) error
}
