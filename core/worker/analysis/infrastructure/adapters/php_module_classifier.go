package adapters

import (
	"context"
	"sort"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/shared/phpclassify"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.ModuleClassifier = (*PHPModuleClassifier)(nil)

// PHPModuleClassifier implements ports.ModuleClassifier for dependency graphs
// whose node names use PHP's backslash namespace separator.
//
// Classification is driven by well-known namespace segment names from Laravel,
// Symfony, and CodeIgniter 4. Modules whose segments match no known pattern fall
// through to the same structural fan-in heuristic used by DeterministicModuleClassifier.
//
// Priority: test > application > infra > domain > structural fallback.
type PHPModuleClassifier struct{}

// NewPHPModuleClassifier returns a PHPModuleClassifier.
func NewPHPModuleClassifier() *PHPModuleClassifier { return &PHPModuleClassifier{} }

// Classify classifies all modules found in edges into domain, application,
// infra, and test buckets using PHP namespace segment patterns.
func (c *PHPModuleClassifier) Classify(_ context.Context, edges []*analysisdomain.DependencyEdge) (*analysisdomain.ModuleClassification, error) {
	// Collect unique PHP module names.
	moduleSet := make(map[string]struct{}, len(edges)*2)
	for _, e := range edges {
		if m := e.GetFromModule(); m != "" {
			moduleSet[m] = struct{}{}
		}
		if m := e.GetToModule(); m != "" {
			moduleSet[m] = struct{}{}
		}
	}

	result := &analysisdomain.ModuleClassification{}
	var unmatched []string

	for m := range moduleSet {
		switch phpclassify.LayerOf(m) {
		case "test":
			result.TestModules = append(result.TestModules, m)
		case "application":
			result.ApplicationModules = append(result.ApplicationModules, m)
		case "infra":
			result.InfraModules = append(result.InfraModules, m)
		case "domain":
			result.DomainModules = append(result.DomainModules, m)
		default:
			unmatched = append(unmatched, m)
		}
	}

	// Structural fan-in fallback for modules with no matching namespace pattern.
	if len(unmatched) > 0 {
		result.StructuralFallback = true
		fanIn := buildFanIn(unmatched, edges)
		threshold := len(unmatched) / 4
		if threshold < 2 {
			threshold = 2
		}
		for _, m := range unmatched {
			if len(fanIn[m]) > threshold {
				result.InfraModules = append(result.InfraModules, m)
			} else {
				result.DomainModules = append(result.DomainModules, m)
			}
		}
	}

	sort.Strings(result.DomainModules)
	sort.Strings(result.ApplicationModules)
	sort.Strings(result.InfraModules)
	sort.Strings(result.TestModules)
	return result, nil
}

// buildFanIn returns a map from each module in the candidate set to the set of
// unique modules that import it, derived from edges.
func buildFanIn(candidates []string, edges []*analysisdomain.DependencyEdge) map[string]map[string]struct{} {
	set := make(map[string]struct{}, len(candidates))
	for _, m := range candidates {
		set[m] = struct{}{}
	}
	fanIn := make(map[string]map[string]struct{}, len(candidates))
	for _, m := range candidates {
		fanIn[m] = make(map[string]struct{})
	}
	for _, e := range edges {
		to, from := e.GetToModule(), e.GetFromModule()
		if importers, ok := fanIn[to]; ok {
			importers[from] = struct{}{}
		}
	}
	return fanIn
}
