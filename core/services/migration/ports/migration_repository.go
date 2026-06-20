// Package ports defines the driven port interfaces for the migration service.
package ports

import (
	"context"

	"milton_prism/core/services/migration/domain"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// MigrationRepository is the driven port for persisting migration records.
type MigrationRepository interface {
	// Create persists a new migration and returns the stored record.
	Create(ctx context.Context, m *domain.Migration) (*domain.Migration, error)
	// GetByID fetches a migration by its numeric identifier.
	// If includeDeleted is false, soft-deleted records are excluded.
	GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.Migration, error)
	// List returns a paginated, filtered set of migrations.
	List(ctx context.Context, filter *domain.MigrationsFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Migration, *paginationv1.Pagination, error)
	// UpdateState persists only the state field for the given migration.
	UpdateState(ctx context.Context, identifier uint64, state domain.MigrationState) error
	// SetRepositoryURL persists repository_url for an existing record.
	// Used to backfill migrations created before the snapshot feature existed.
	SetRepositoryURL(ctx context.Context, identifier uint64, url string) error
	// SetMigrabilityAssessment persists the LLM verdict sub-document on the migration.
	// Idempotent: re-running replaces the previous verdict.
	SetMigrabilityAssessment(ctx context.Context, identifier uint64, assessment *domain.MigrabilityAssessment) error
	// SetMigrabilityOverride sets or clears the migrability_override flag.
	// Idempotent: setting the same value twice is a no-op.
	SetMigrabilityOverride(ctx context.Context, identifier uint64, override bool) error
	// SetRestructuringRoadmap persists the roadmap and atomically transitions the
	// migration from AWAITING_APPROVAL to RESTRUCTURING_READY (terminal).
	SetRestructuringRoadmap(ctx context.Context, identifier uint64, roadmap *domain.RestructuringRoadmap) error
	// SetRoadmapEnrichment persists the LLM enrichment sub-document on the migration.
	// Idempotent: re-running replaces the previous enrichment.
	// Does not change the migration state (RESTRUCTURING_READY remains terminal).
	SetRoadmapEnrichment(ctx context.Context, identifier uint64, enrichment *domain.RoadmapEnrichment) error
	// SetServiceBlueprint persists the LLM blueprint sub-document on the migration.
	// Idempotent: re-running replaces the previous blueprint.
	// Does not change the migration state (RESTRUCTURING_READY remains terminal).
	SetServiceBlueprint(ctx context.Context, identifier uint64, blueprint *domain.ServiceBlueprint) error
	// AdoptAnalysis atomically transitions the migration from ANALYZING to
	// DESIGNING and records that an existing AnalysisSummary was adopted.
	// Sets analysis_summary_id, analysis_reused=true, and — when sourceBranch
	// is non-empty — updates source_branch on the migration record so that the
	// inherited branch is visible to callers. Guarded by ANALYZING state so
	// re-runs are safe.
	AdoptAnalysis(ctx context.Context, migrationID, analysisSummaryID uint64, sourceBranch string) error
	// SoftDelete marks the migration as deleted without removing it.
	SoftDelete(ctx context.Context, identifier uint64) error
}
