// Package ports defines the driven port interfaces for the analysis service.
package ports

import (
	"context"
	"time"

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
	// MarkRootSelected transitions an analysis from AWAITING_ROOT_SELECTION back
	// to RUNNING, persisting the chosen root_subdirectory and clearing the
	// candidate list. Guarded on the AWAITING_ROOT_SELECTION state so a double
	// selection (or a selection on a non-awaiting analysis) matches nothing and
	// returns ErrInvalidRootSelection. Returns the updated summary on success.
	MarkRootSelected(ctx context.Context, identifier uint64, rootSubdirectory string) (*domain.AnalysisSummary, error)
	// CountByOwnerSince returns the number of (non-deleted) analysis summaries
	// owned by ownerID whose create_time is at or after since. Used for billing
	// plan quota enforcement (analyses-per-month). since must be UTC.
	CountByOwnerSince(ctx context.Context, ownerID uint64, since time.Time) (int64, error)
}
