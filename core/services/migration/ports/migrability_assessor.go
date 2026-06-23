package ports

import (
	"context"

	"milton_prism/core/services/migration/domain"
)

// MigrabilityAssessor is the driven port for the opt-in LLM migrability assessment.
// The infra adapter loads the analysis digest from the database and calls the model.
// analysisSummaryID is the ID of the completed AnalysisSummary for the migration.
type MigrabilityAssessor interface {
	// language is the BCP-47 tag for LLM prose (summary/reasons/blockers).
	// Empty → "en". Module names and identifiers are never translated.
	// userID/migrationID identify the spend owner so the adapter can record LLM
	// token usage in billing (best-effort) after the model call.
	Assess(ctx context.Context, userID, migrationID, analysisSummaryID uint64, language string) (*domain.MigrabilityAssessment, error)
}
