package ports

import (
	"context"

	"milton_prism/core/services/migration/domain"
)

// BlueprintGenerator proposes a target microservice grouping anchored to the
// measured coupling in the dependency graph. It runs the full Distill pipeline
// (graph load → infra detect → cluster → cards) and calls the LLM with the
// resulting AnalysisDigest — never with raw source code.
//
// The roadmap is passed so the LLM can reference blocking action_plan steps in
// the precondition_note and populate required_steps.
type BlueprintGenerator interface {
	Generate(ctx context.Context, analysisSummaryID uint64, roadmap *domain.RestructuringRoadmap) (*domain.ServiceBlueprint, error)
}
