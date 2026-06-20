package adapters

import (
	"context"
	"strings"

	"milton_prism/core/shared/phpclassify"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
)

var _ ports.InfraDetector = (*PHPAwareInfraDetector)(nil)

// PHPAwareInfraDetector extends DeterministicInfraDetector with PHP support.
//
// Detection strategy:
//   - PHP codebases (any module name contains `\`): classify via
//     phpclassify.LayerOf(). APPLICATION and INFRA layers (including exception
//     classes — see phpclassify.segmentLayer) are merged into cls.Infra.
//     DOMAIN → cls.Domain. TEST → cls.Tests.
//     Unmatched modules ("") use a fan-in heuristic without setting
//     StructuralFallback, so the Louvain confidence comes from modularity alone.
//   - All other codebases: delegated to DeterministicInfraDetector unchanged.
//
// This is the decomposition counterpart of the analysis worker's
// LanguageAwareClassifier + PHPModuleClassifier pair. Both share the segment
// rules from core/shared/phpclassify — the single source of truth.
type PHPAwareInfraDetector struct {
	inner *DeterministicInfraDetector
}

// NewPHPAwareInfraDetector returns a PHPAwareInfraDetector.
func NewPHPAwareInfraDetector() *PHPAwareInfraDetector {
	return &PHPAwareInfraDetector{inner: NewDeterministicInfraDetector()}
}

// Detect classifies all modules in graph into INFRA, DOMAIN, and TEST.
// PHP codebases are handled by namespace-segment rules; all others are
// delegated to DeterministicInfraDetector.
func (d *PHPAwareInfraDetector) Detect(ctx context.Context, graph *workerdomain.Graph) (*workerdomain.Classification, error) {
	all := graph.AllModules()
	if !isPHPGraph(all) {
		return d.inner.Detect(ctx, graph)
	}
	return classifyPHP(graph, all), nil
}

// isPHPGraph returns true when any module in the graph uses PHP's backslash
// namespace separator.
func isPHPGraph(modules []workerdomain.Module) bool {
	for _, m := range modules {
		if strings.Contains(string(m), `\`) {
			return true
		}
	}
	return false
}

// classifyPHP applies PHP namespace-segment rules to the module set.
//
// Layer mapping for the decomposition domain:
//   - "application" (controllers, console)           → cls.Infra  — excluded from clustering
//   - "infra" (repos, providers, exceptions, …)      → cls.Infra  — excluded from clustering
//   - "domain" (models, services, policies, …)       → cls.Domain — clustered by Louvain
//   - "test" (Tests\*, *Test class names)            → cls.Tests  — always excluded
//   - "" (unmatched, e.g. utility classes)           → fan-in fallback → Domain or Infra
//
// Exception classes fall into "infra" via phpclassify.segmentLayer — no override
// is needed here. StructuralFallback is intentionally NOT set: the PHP rules are
// authoritative. Louvain confidence comes from modularity on the clean domain
// subgraph, not from the presence of a few unmatched utility modules.
func classifyPHP(graph *workerdomain.Graph, all []workerdomain.Module) *workerdomain.Classification {
	cls := &workerdomain.Classification{}
	var unmatched []workerdomain.Module

	for _, m := range all {
		switch phpclassify.LayerOf(string(m)) {
		case "test":
			cls.Tests = append(cls.Tests, m)
		case "application", "infra":
			cls.Infra = append(cls.Infra, m)
		case "domain":
			cls.Domain = append(cls.Domain, m)
		default:
			unmatched = append(unmatched, m)
		}
	}

	if len(unmatched) == 0 {
		return cls
	}

	// Fan-in fallback for utility/unrecognised modules.
	// Threshold: max(2, len(unmatched)/4) — modules imported by > 25% of the
	// unmatched set are shared-state hubs → Infra; the rest → Domain.
	threshold := len(unmatched) / 4
	if threshold < 2 {
		threshold = 2
	}

	// Count unique importers per unmatched module, not raw edge count. A single
	// importer that references multiple symbols generates multiple edges but
	// represents one caller — counting edges would inflate fan-in artificially.
	fanIn := make(map[workerdomain.Module]map[workerdomain.Module]struct{}, len(unmatched))
	for _, m := range unmatched {
		fanIn[m] = make(map[workerdomain.Module]struct{})
	}
	for _, e := range graph.Edges {
		if importers, ok := fanIn[e.To]; ok {
			importers[e.From] = struct{}{}
		}
	}

	for _, m := range unmatched {
		if len(fanIn[m]) > threshold {
			cls.Infra = append(cls.Infra, m)
		} else {
			cls.Domain = append(cls.Domain, m)
		}
	}

	return cls
}
