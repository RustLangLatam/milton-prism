package adapters

import (
	"context"
	"strings"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
)

var _ ports.InfraDetector = (*DeterministicInfraDetector)(nil)

// domainIndicatorSuffixes are the last path components that signal a module
// carries domain logic (models, views, serializers, etc.). A module group
// that contains at least one such sub-module is a "blueprint group" whose
// members are classified as DOMAIN.
var domainIndicatorSuffixes = map[string]bool{
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

// DeterministicInfraDetector implements ports.InfraDetector using a
// blueprint-group heuristic that requires no LLM or clustering.
//
// Algorithm:
//  1. Derive the "group" of every module (first two path components when depth
//     > 2, otherwise the module name itself).
//  2. A group is a "blueprint group" if any module in the graph belongs to it
//     AND has a domain-indicator suffix (models, views, serializers, …).
//  3. A module is DOMAIN if its group is a blueprint group.
//  4. A module is TEST if its first path component starts with "test" or equals "tests".
//  5. Everything else is INFRA.
//
// On Conduit this correctly classifies conduit.database/settings/utils/extensions/
// exceptions/commands/app and autoapp as INFRA, and conduit.user/profile/articles
// sub-trees as DOMAIN.
type DeterministicInfraDetector struct{}

// NewDeterministicInfraDetector returns a DeterministicInfraDetector.
func NewDeterministicInfraDetector() *DeterministicInfraDetector {
	return &DeterministicInfraDetector{}
}

// Detect classifies all modules in graph into INFRA, DOMAIN, and TEST.
func (d *DeterministicInfraDetector) Detect(_ context.Context, graph *workerdomain.Graph) (*workerdomain.Classification, error) {
	all := graph.AllModules()

	// Pass 1: find blueprint groups — groups that have at least one sub-module
	// with a domain-indicator suffix in the graph.
	blueprintGroups := make(map[string]bool)
	for _, m := range all {
		if domainIndicatorSuffixes[lastComponent(string(m))] {
			blueprintGroups[groupOf(string(m))] = true
		}
	}

	// Pass 2: classify each module.
	cls := &workerdomain.Classification{}

	if len(blueprintGroups) > 0 {
		// Normal path: blueprint-group heuristic (Conduit and similar projects).
		for _, m := range all {
			name := string(m)
			switch {
			case isTestModule(name):
				cls.Tests = append(cls.Tests, m)
			case blueprintGroups[groupOf(name)]:
				cls.Domain = append(cls.Domain, m)
			default:
				cls.Infra = append(cls.Infra, m)
			}
		}
		return cls, nil
	}

	// Structural fallback: zero blueprint groups found. Apply a fan-in heuristic
	// to separate shared-state hubs (→ INFRA) from candidate domain modules.
	// This path activates only for codebases like notiplan that lack Django/Flask
	// blueprint structure but still have separable concerns.
	cls.StructuralFallback = true
	return classifyByFanIn(graph, all), nil
}

// classifyByFanIn applies the structural fallback when no blueprint groups exist.
// It computes fan-in (number of unique importers) for each non-test module and
// classifies high-fan-in modules as INFRA (shared state hubs). The remaining
// non-test modules become DOMAIN candidates. Tests are always excluded.
//
// Hub threshold: fan-in > max(2, len(nonTestModules)/4). This identifies modules
// imported by more than 25% of the codebase — reliable shared-state indicators.
func classifyByFanIn(graph *workerdomain.Graph, all []workerdomain.Module) *workerdomain.Classification {
	cls := &workerdomain.Classification{StructuralFallback: true}

	// Separate test modules immediately.
	var nonTest []workerdomain.Module
	for _, m := range all {
		if isTestModule(string(m)) {
			cls.Tests = append(cls.Tests, m)
		} else {
			nonTest = append(nonTest, m)
		}
	}
	if len(nonTest) == 0 {
		return cls
	}

	// Compute fan-in: count unique importers per module across all edges.
	fanIn := make(map[workerdomain.Module]map[workerdomain.Module]struct{}, len(nonTest))
	for _, m := range nonTest {
		fanIn[m] = make(map[workerdomain.Module]struct{})
	}
	for _, e := range graph.Edges {
		if _, ok := fanIn[e.To]; ok {
			fanIn[e.To][e.From] = struct{}{}
		}
	}

	// Hub threshold: more than 25% of non-test modules import this module.
	threshold := len(nonTest) / 4
	if threshold < 2 {
		threshold = 2
	}

	for _, m := range nonTest {
		if len(fanIn[m]) > threshold {
			cls.Infra = append(cls.Infra, m)
		} else {
			cls.Domain = append(cls.Domain, m)
		}
	}
	return cls
}

// groupOf returns the "group prefix" for a module name.
//
// Python (dot-separated): depth > 2 → first two components ("conduit.articles"
// for "conduit.articles.views"). Depth ≤ 2 → module name itself.
//
// PHP (backslash-separated): depth > 2 → first two namespace segments
// ("BookStack\Entities" for "BookStack\Entities\Models\Page"). Depth ≤ 2 →
// module name itself. Closing the separator class: this is the single function
// that determines blueprint grouping for both languages.
func groupOf(module string) string {
	if strings.Contains(module, `\`) {
		parts := strings.SplitN(module, `\`, 3)
		if len(parts) <= 2 {
			return module
		}
		return parts[0] + `\` + parts[1]
	}
	parts := strings.SplitN(module, ".", 3)
	if len(parts) <= 2 {
		return module
	}
	return parts[0] + "." + parts[1]
}

// lastComponent returns the portion after the last "." separator, or the
// module name itself when there is no separator.
func lastComponent(module string) string {
	if i := strings.LastIndex(module, "."); i >= 0 {
		return module[i+1:]
	}
	return module
}

// isTestModule returns true when the module clearly belongs to the test suite
// and should be excluded from both infra and domain partitions.
//
// Checks every dot-separated (Python) or backslash-separated (PHP) path
// component so that "backend.tests.conftest" is detected even though its
// first component is "backend". Matches whole components only — never
// substrings — to avoid false positives like "backend.contestant".
func isTestModule(module string) bool {
	sep := "."
	if strings.Contains(module, `\`) {
		sep = `\`
	}
	for _, part := range strings.Split(module, sep) {
		if part == "tests" || part == "test" || part == "spec" {
			return true
		}
		if strings.HasPrefix(part, "test_") || strings.HasSuffix(part, "_test") {
			return true
		}
	}
	return false
}
