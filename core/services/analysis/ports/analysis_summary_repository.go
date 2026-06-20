// Package ports defines the driven port interfaces for the analysis service.
package ports

import (
	"context"

	"milton_prism/core/services/analysis/domain"
	analysissvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// AnalysisSummaryRepository is the driven port for persisting AnalysisSummary records.
type AnalysisSummaryRepository interface {
	// Create persists a new analysis summary and returns the stored record.
	Create(ctx context.Context, s *domain.AnalysisSummary) (*domain.AnalysisSummary, error)
	// GetByID fetches an analysis summary by its numeric identifier.
	// If includeDeleted is false, soft-deleted records are excluded.
	GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.AnalysisSummary, error)
	// List returns a paginated, filtered set of analysis summaries.
	List(ctx context.Context, filter *analysissvcv1.AnalysisSummariesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.AnalysisSummary, *paginationv1.Pagination, error)
	// SoftDelete marks the analysis summary as deleted without removing it.
	SoftDelete(ctx context.Context, identifier uint64) error
	// UpdateMigrabilityAssessment persists the LLM migrability assessment on an
	// existing AnalysisSummary. Called after the opt-in AssessMigrability RPC.
	UpdateMigrabilityAssessment(ctx context.Context, identifier uint64, assessment *domain.MigrabilityAssessment) error
}
