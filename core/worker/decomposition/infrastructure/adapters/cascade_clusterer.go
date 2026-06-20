package adapters

import (
	"context"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
	applog "milton_prism/pkg/log"
)

var _ ports.SemanticClusterer = (*CascadeClusterer)(nil)

// CascadeClusterer implements a two-stage clustering strategy: deterministic
// Louvain first, LLM fallback only when Louvain is insufficient and the
// coherence guardrail would not fire.
//
// Decision tree:
//  1. Run primary (Louvain).
//  2. If clusters > 0 and not low-confidence → done (deterministic path).
//  3. If the coherence guardrail would fire for this result (star-topology) →
//     return the Louvain result as-is; pipeline.ApplyCoherenceGuardrail will
//     reset it to no-boundaries upstream. The LLM is never called for a graph
//     that the guardrail would discard anyway.
//  4. Otherwise (real structure, insufficient confidence) → call LLM fallback.
//     On LLM error, fall back to the Louvain result with LowConfidence=true.
type CascadeClusterer struct {
	primary  ports.SemanticClusterer
	fallback ports.SemanticClusterer // nil when LLM is not wired
}

// NewCascadeClusterer constructs a cascade with a mandatory primary (Louvain)
// and an optional LLM fallback. When fallback is nil, the cascade behaves like
// the primary alone.
func NewCascadeClusterer(primary, fallback ports.SemanticClusterer) *CascadeClusterer {
	return &CascadeClusterer{primary: primary, fallback: fallback}
}

// Cluster runs the cascade. See type-level doc for the decision tree.
func (c *CascadeClusterer) Cluster(ctx context.Context, input ports.ClusterInput) (*workerdomain.ClusteringResult, error) {
	cr, err := c.primary.Cluster(ctx, input)
	if err != nil {
		return nil, err
	}

	// Predict low-confidence accounting for the structural-fallback flag that
	// ApplyCoherenceGuardrail would set. We need this value to decide whether
	// to call the LLM, before the pipeline has applied the guardrail.
	wouldBeLowConf := cr.LowConfidence || input.StructuralFallback

	// Deterministic path: Louvain produced high-confidence clusters.
	if len(cr.Clusters) > 0 && !wouldBeLowConf {
		return cr, nil
	}

	// Guardrail short-circuit: star-topology or empty-domain graph.
	// ApplyCoherenceGuardrail will handle this upstream; calling the LLM for a
	// hub-and-spoke graph would waste tokens and produce misleading candidates.
	guardrailWouldFire := input.StructuralFallback && wouldBeLowConf &&
		(len(cr.Clusters) == 0 || workerdomain.IsIncoherentFallback(input.DomainGraph, cr.Clusters))
	if guardrailWouldFire {
		applog.Infof("cascade-clusterer: guardrail would fire — skipping LLM for star-topology graph")
		return cr, nil
	}

	if c.fallback == nil {
		return cr, nil
	}

	applog.Infof("cascade-clusterer: Louvain insufficient (clusters=%d low_conf=%v) — invoking LLM fallback",
		len(cr.Clusters), wouldBeLowConf)

	llmResult, llmErr := c.fallback.Cluster(ctx, input)
	if llmErr != nil {
		// LLM errors are non-fatal: preserve Louvain result with LowConfidence.
		applog.Warningf("cascade-clusterer: LLM fallback error — using Louvain result: %v", llmErr)
		cr.LowConfidence = true
		return cr, nil
	}

	return llmResult, nil
}
