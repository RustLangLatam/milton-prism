package ports

import (
	"context"

	"milton_prism/core/services/analysis/domain"
)

// AnalysisMigrabilityAssessor runs the opt-in LLM migrability assessment for
// a completed analysis summary. It loads the stored graph and cards, runs the
// Louvain clustering pipeline, calls the LLM assessor, persists the result,
// and returns the assessment.
type AnalysisMigrabilityAssessor interface {
	// language is the BCP-47 tag for the LLM prose (summary/reasons/blockers).
	// Empty → "en". Module names and identifiers are never translated.
	Assess(ctx context.Context, analysisSummaryID uint64, language string) (*domain.MigrabilityAssessment, error)
}
