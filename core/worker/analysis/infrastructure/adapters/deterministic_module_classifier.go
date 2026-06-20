package adapters

import (
	"context"
	"sort"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.ModuleClassifier = (*DeterministicModuleClassifier)(nil)

// classifierDomainSuffixes mirrors domainIndicatorSuffixes in the decomposition
// worker's DeterministicInfraDetector. Both sets must stay in sync so that
// analysis-standalone and decomposition classify identically today; they are
// intentionally separate implementations so each can evolve independently.
var classifierDomainSuffixes = map[string]bool{
	"models":       true,
	"views":        true,
	"serializers":  true,
	"resources":    true,
	"api":          true,
	"handlers":     true,
	"routes":       true,
	"schema":       true,
	"schemas":      true,
	"entities":     true,
	"repository":   true,
	"repositories": true,
}

// DeterministicModuleClassifier implements ports.ModuleClassifier using the
// blueprint-group heuristic (or fan-in structural fallback) on a flat list of
// DependencyEdge proto messages. The algorithm is identical to the decomposition
// worker's DeterministicInfraDetector so both classifiers produce the same
// output for the same dependency graph.
//
// Algorithm:
//  1. Collect all unique module names from edge endpoints.
//  2. A "group" is the first two dot-separated path components.
//  3. A group is a "blueprint group" if any module in it has a domain-indicator
//     suffix (models, views, serializers, …).
//  4. DOMAIN = modules in a blueprint group; INFRA = everything else.
//  5. If no blueprint groups: structural fallback — classify high-fan-in modules
//     (fan-in > max(2, len(nonTest)/4)) as INFRA, the rest as DOMAIN.
//  6. TEST = modules whose first path component is "test", "tests", "spec", or
//     starts with "test_". Always excluded from domain/infra partitions.
//
// Note: PHP modules (backslash-separated namespaces) never reach this classifier.
// LanguageAwareClassifier routes all modules whose names contain a backslash to
// PHPModuleClassifier before this classifier is invoked, so the dot-only path
// component logic above is sufficient for the languages this classifier handles.
type DeterministicModuleClassifier struct{}

// NewDeterministicModuleClassifier returns a DeterministicModuleClassifier.
func NewDeterministicModuleClassifier() *DeterministicModuleClassifier {
	return &DeterministicModuleClassifier{}
}

// Classify classifies all modules found in edges into domain, infra, and test.
func (c *DeterministicModuleClassifier) Classify(_ context.Context, edges []*analysisdomain.DependencyEdge) (*analysisdomain.ModuleClassification, error) {
	// Collect all unique module names from edge endpoints.
	moduleSet := make(map[string]struct{}, len(edges)*2)
	for _, e := range edges {
		if m := e.GetFromModule(); m != "" {
			moduleSet[m] = struct{}{}
		}
		if m := e.GetToModule(); m != "" {
			moduleSet[m] = struct{}{}
		}
	}

	// Pass 1: identify blueprint groups — groups with a domain-indicator suffix.
	blueprintGroups := make(map[string]bool)
	for m := range moduleSet {
		if classifierDomainSuffixes[classifierLastComponent(m)] {
			blueprintGroups[classifierGroupOf(m)] = true
		}
	}

	result := &analysisdomain.ModuleClassification{}

	if len(blueprintGroups) > 0 {
		for m := range moduleSet {
			switch {
			case classifierIsTest(m):
				result.TestModules = append(result.TestModules, m)
			case blueprintGroups[classifierGroupOf(m)]:
				result.DomainModules = append(result.DomainModules, m)
			default:
				result.InfraModules = append(result.InfraModules, m)
			}
		}
		sort.Strings(result.DomainModules)
		sort.Strings(result.InfraModules)
		sort.Strings(result.TestModules)
		return result, nil
	}

	// Structural fallback: no blueprint groups found. Classify by fan-in:
	// modules imported by more than 25% of non-test modules → INFRA.
	result.StructuralFallback = true

	var nonTest []string
	for m := range moduleSet {
		if classifierIsTest(m) {
			result.TestModules = append(result.TestModules, m)
		} else {
			nonTest = append(nonTest, m)
		}
	}

	if len(nonTest) == 0 {
		sort.Strings(result.TestModules)
		return result, nil
	}

	// Build fan-in map: count unique importers per non-test module.
	fanIn := make(map[string]map[string]struct{}, len(nonTest))
	for _, m := range nonTest {
		fanIn[m] = make(map[string]struct{})
	}
	for _, e := range edges {
		to := e.GetToModule()
		from := e.GetFromModule()
		if set, ok := fanIn[to]; ok {
			set[from] = struct{}{}
		}
	}

	threshold := len(nonTest) / 4
	if threshold < 2 {
		threshold = 2
	}

	for _, m := range nonTest {
		if len(fanIn[m]) > threshold {
			result.InfraModules = append(result.InfraModules, m)
		} else {
			result.DomainModules = append(result.DomainModules, m)
		}
	}
	sort.Strings(result.DomainModules)
	sort.Strings(result.InfraModules)
	sort.Strings(result.TestModules)
	return result, nil
}

// classifierGroupOf returns the first two dot-separated components of module,
// or the module name itself when depth ≤ 2.
func classifierGroupOf(module string) string {
	parts := strings.SplitN(module, ".", 3)
	if len(parts) <= 2 {
		return module
	}
	return parts[0] + "." + parts[1]
}

// classifierLastComponent returns the component after the last "." separator.
func classifierLastComponent(module string) string {
	if i := strings.LastIndex(module, "."); i >= 0 {
		return module[i+1:]
	}
	return module
}

// classifierIsTest returns true for modules that clearly belong to the test suite.
// Checks every dot-separated path component so that "backend.tests.conftest" is
// detected even when the first component is not a test indicator. Matches whole
// components only — never substrings — to avoid "backend.contestant" false positives.
func classifierIsTest(module string) bool {
	for _, part := range strings.Split(module, ".") {
		if part == "tests" || part == "test" || part == "spec" {
			return true
		}
		if strings.HasPrefix(part, "test_") || strings.HasSuffix(part, "_test") {
			return true
		}
	}
	return false
}
