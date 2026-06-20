package adapters

import (
	"context"
	"sort"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.ModuleClassifier = (*LanguageAwareClassifier)(nil)

// LanguageAwareClassifier wraps PHPModuleClassifier and DeterministicModuleClassifier,
// routing each edge to the correct classifier based on its module separator:
//   - Backslash → PHP (PHPModuleClassifier, framework-aware)
//   - Dot/other → generic (DeterministicModuleClassifier, Python-tuned)
//
// Mixed-language graphs are partitioned, classified independently, and merged.
// This keeps Python classification identical to the standalone deterministic
// classifier while adding framework-aware PHP layer detection.
type LanguageAwareClassifier struct {
	phpClassifier     *PHPModuleClassifier
	genericClassifier *DeterministicModuleClassifier
}

// NewLanguageAwareClassifier returns a LanguageAwareClassifier.
func NewLanguageAwareClassifier() *LanguageAwareClassifier {
	return &LanguageAwareClassifier{
		phpClassifier:     NewPHPModuleClassifier(),
		genericClassifier: NewDeterministicModuleClassifier(),
	}
}

// Classify partitions edges by language, classifies each partition, and merges.
func (c *LanguageAwareClassifier) Classify(ctx context.Context, edges []*analysisdomain.DependencyEdge) (*analysisdomain.ModuleClassification, error) {
	var phpEdges, genericEdges []*analysisdomain.DependencyEdge
	for _, e := range edges {
		if isPHPEdge(e) {
			phpEdges = append(phpEdges, e)
		} else {
			genericEdges = append(genericEdges, e)
		}
	}

	result := &analysisdomain.ModuleClassification{}

	if len(phpEdges) > 0 {
		mc, err := c.phpClassifier.Classify(ctx, phpEdges)
		if err != nil {
			return nil, err
		}
		result.DomainModules = append(result.DomainModules, mc.DomainModules...)
		result.ApplicationModules = append(result.ApplicationModules, mc.ApplicationModules...)
		result.InfraModules = append(result.InfraModules, mc.InfraModules...)
		result.TestModules = append(result.TestModules, mc.TestModules...)
		result.StructuralFallback = result.StructuralFallback || mc.StructuralFallback
	}

	if len(genericEdges) > 0 {
		mc, err := c.genericClassifier.Classify(ctx, genericEdges)
		if err != nil {
			return nil, err
		}
		result.DomainModules = append(result.DomainModules, mc.DomainModules...)
		result.InfraModules = append(result.InfraModules, mc.InfraModules...)
		result.TestModules = append(result.TestModules, mc.TestModules...)
		result.StructuralFallback = result.StructuralFallback || mc.StructuralFallback
	}

	sort.Strings(result.DomainModules)
	sort.Strings(result.ApplicationModules)
	sort.Strings(result.InfraModules)
	sort.Strings(result.TestModules)
	return result, nil
}

// isPHPEdge reports whether an edge belongs to a PHP dependency graph.
// PHP FQNs use backslash as the namespace separator (e.g. "App\Models\User").
func isPHPEdge(e *analysisdomain.DependencyEdge) bool {
	return strings.Contains(e.GetFromModule(), `\`) || strings.Contains(e.GetToModule(), `\`)
}
