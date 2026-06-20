package ports

import (
	"context"

	"milton_prism/core/services/migration/domain"
)

// RoadmapEnricher is the driven port for the opt-in LLM enrichment of roadmap steps.
// The infra adapter builds a structural prompt from the persisted roadmap and returns
// one narrative per action step. No raw source code is ever sent to the model.
type RoadmapEnricher interface {
	Enrich(ctx context.Context, roadmap *domain.RestructuringRoadmap) (*domain.RoadmapEnrichment, error)
}
