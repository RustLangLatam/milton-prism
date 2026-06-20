// Package migrability provides the analysis worker's MigrabilityScorer adapter.
// It lives in its own package to avoid an import cycle: the decomposition
// application package imports core/worker/analysis/infrastructure/adapters
// (via integration tests), and placing the scorer there would create a loop.
package migrability

import (
	"context"
	"fmt"

	analysisdomain "milton_prism/core/services/analysis/domain"
	workerapp "milton_prism/core/worker/analysis/application"
	workerports "milton_prism/core/worker/analysis/ports"
	decompapp "milton_prism/core/worker/decomposition/application"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	decompadapters "milton_prism/core/worker/decomposition/infrastructure/adapters"
	decompports "milton_prism/core/worker/decomposition/ports"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
)

var _ workerports.MigrabilityScorer = (*LouvainMigrabilityScorer)(nil)

// LouvainMigrabilityScorer implements ports.MigrabilityScorer using the same
// Louvain community detection used by the decomposition pipeline. It consumes
// the classification already produced by stage 6c — no InfraDetector re-run is
// needed; the PHPAwareInfraDetector's result is encoded in ModuleClassification.
type LouvainMigrabilityScorer struct {
	clusterer *decompadapters.LouvainClusterer
}

// NewLouvainMigrabilityScorer returns a LouvainMigrabilityScorer ready for wiring.
func NewLouvainMigrabilityScorer() *LouvainMigrabilityScorer {
	return &LouvainMigrabilityScorer{clusterer: decompadapters.NewLouvainClusterer()}
}

// Score computes the deterministic structural migrability score.
func (s *LouvainMigrabilityScorer) Score(
	ctx context.Context,
	edges []*analysisdomain.DependencyEdge,
	cls *analysisdomain.ModuleClassification,
	cards []*analysisdomain.ModuleCard,
	blueprints []*analysisdomain.BlueprintInfo,
) (*commonv1.MigrabilityScore, error) {
	workerCls := workerapp.ToWorkerClassification(cls)
	graph := workerapp.ToWorkerGraph(edges)
	domainGraph := workerdomain.DomainSubgraph(graph, workerCls.Domain)

	clusterResult, err := s.clusterer.Cluster(ctx, decompports.ClusterInput{
		DomainGraph:        domainGraph,
		DomainModules:      workerCls.Domain,
		StructuralFallback: workerCls.StructuralFallback,
	})
	if err != nil {
		return nil, fmt.Errorf("louvain migrability scorer: cluster: %w", err)
	}

	if decompapp.ApplyCoherenceGuardrail(workerCls, clusterResult, domainGraph) {
		// The guardrail discarded the domain partition as incoherent fallback
		// residue. Reflect the correction in the caller's classification so that
		// module_classification_bytes is consistent with the score (DomainEmpty=true).
		// Modules previously labelled domain move to infra — they are shared-state
		// spoke nodes, not real domain objects.
		cls.InfraModules = append(cls.InfraModules, cls.DomainModules...)
		cls.DomainModules = nil
	}

	// Technologies are intentionally nil here: the scorer port receives only cards
	// and blueprints (no tech slice) and the scoring signals do not use Framework.
	// If future signals need Framework, extend the Score() signature to include techs.
	workerCards := workerapp.ToWorkerSummaryCards(cards, blueprints, nil)
	digest := decompapp.Distill(graph, workerCls, clusterResult, workerCards, 0)
	score := decompapp.Score(digest)
	return workerapp.ToProtoMigrabilityScore(score), nil
}
