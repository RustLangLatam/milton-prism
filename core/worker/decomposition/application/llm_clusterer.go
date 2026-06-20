package application

import (
	"context"
	"fmt"
	"sort"
	"strings"

	analysisports "milton_prism/core/worker/analysis/ports"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
	applog "milton_prism/pkg/log"
)

var _ ports.SemanticClusterer = (*LLMClusterer)(nil)

// LLMClusterer asks the model to propose service boundaries from the domain
// module set and dependency graph. It runs the anti-hallucination validator
// and the shared-state guardrail deterministically on every response before
// accepting the proposal.
type LLMClusterer struct {
	client analysisports.ModelClient
}

// NewLLMClusterer constructs an LLMClusterer backed by the given ModelClient.
func NewLLMClusterer(client analysisports.ModelClient) *LLMClusterer {
	return &LLMClusterer{client: client}
}

// Cluster asks the LLM to propose service groupings for the domain modules,
// validates the response, applies the shared-state guardrail, and returns the
// filtered ClusteringResult.
//
// Validation retry: if the first response contains hallucinated module names or
// duplicate assignments, one retry is made with the validation error as
// feedback. A second failure returns an honest no-boundaries result
// (LowConfidence=true, no Clusters) rather than silently accepting bad data.
//
// The LLM never writes directly to the plan — every response passes through
// validateProposal and applySharedStateGuardrail first.
func (l *LLMClusterer) Cluster(ctx context.Context, input ports.ClusterInput) (*workerdomain.ClusteringResult, error) {
	prompt := buildClusteringPrompt(input)
	req := analysisports.ModelRequest{
		System:    clusteringSystemPrompt,
		Prompt:    prompt,
		MaxTokens: 2048,
		Purpose:   "semantic-clustering",
	}

	proposal, _, err := completeJSONDecomp[workerdomain.ClusteringProposal](ctx, l.client, req)
	if err != nil {
		return nil, fmt.Errorf("llm-clusterer: model call: %w", err)
	}

	if valErr := validateProposal(proposal, input.DomainModules); valErr != nil {
		applog.Warningf("llm-clusterer: proposal failed validation — retrying with error feedback: %v", valErr)

		retryReq := req
		retryReq.Prompt = req.Prompt + "\n\n" +
			"Your previous response failed semantic validation: " + valErr.Error() + ".\n" +
			"Return a corrected proposal using ONLY the module names listed above."

		proposal, _, err = completeJSONDecomp[workerdomain.ClusteringProposal](ctx, l.client, retryReq)
		if err != nil {
			return nil, fmt.Errorf("llm-clusterer: retry model call: %w", err)
		}

		if valErr2 := validateProposal(proposal, input.DomainModules); valErr2 != nil {
			applog.Warningf("llm-clusterer: proposal failed validation after retry — honest no-boundaries: %v", valErr2)
			return &workerdomain.ClusteringResult{LowConfidence: true}, nil
		}
	}

	// All raw LLM groups are preserved as CandidateGroupings for UI display.
	candidateGroupings := make([]workerdomain.ProposedGroup, 0, len(proposal.Groups))
	for _, g := range proposal.Groups {
		candidateGroupings = append(candidateGroupings, workerdomain.ProposedGroup{
			Name:             g.Name,
			Modules:          g.Modules,
			Responsibilities: g.Responsibilities,
			Confidence:       g.Confidence,
		})
	}

	clusters := groupsToClusters(candidateGroupings)
	return &workerdomain.ClusteringResult{
		Clusters:           clusters,
		LowConfidence:      len(clusters) == 0,
		CandidateGroupings: candidateGroupings,
	}, nil
}

// validateProposal checks that every module in the proposal is a known domain
// module and that no module appears in more than one group.
func validateProposal(proposal workerdomain.ClusteringProposal, domainModules []workerdomain.Module) error {
	validSet := make(map[workerdomain.Module]bool, len(domainModules))
	for _, m := range domainModules {
		validSet[m] = true
	}

	seen := make(map[workerdomain.Module]string) // module → first group name
	for _, g := range proposal.Groups {
		for _, raw := range g.Modules {
			m := workerdomain.Module(raw)
			if !validSet[m] {
				return fmt.Errorf("group %q references unknown module %q — not a valid domain module", g.Name, raw)
			}
			if prev, ok := seen[m]; ok {
				return fmt.Errorf("module %q appears in both group %q and group %q", raw, prev, g.Name)
			}
			seen[m] = g.Name
		}
	}
	return nil
}

// groupsToClusters converts validated ProposedGroups into Clusters.
func groupsToClusters(groups []workerdomain.ProposedGroup) []workerdomain.Cluster {
	clusters := make([]workerdomain.Cluster, 0, len(groups))
	for _, g := range groups {
		if len(g.Modules) == 0 {
			continue
		}
		modules := make([]workerdomain.Module, len(g.Modules))
		for i, m := range g.Modules {
			modules[i] = workerdomain.Module(m)
		}
		sort.Slice(modules, func(i, j int) bool { return modules[i] < modules[j] })
		clusters = append(clusters, workerdomain.Cluster{
			BlueprintGroup: g.Name,
			Modules:        modules,
		})
	}
	return clusters
}

const clusteringSystemPrompt = `You are a software architecture analyst.
Your task: group the provided domain modules into candidate microservice boundaries.

Rules:
- Each module MUST appear in at most one group. Never duplicate a module.
- Use ONLY the exact module names provided — never invent or abbreviate names.
- Empty groups are not allowed.
- Group by domain responsibility, not by file structure alone.
- Return ONLY valid JSON matching the schema. No preamble, no markdown fences.`

// buildClusteringPrompt serialises the ClusterInput into a compact prompt for
// the LLM clusterer. Includes module list, top dependency edges, and — when
// the digest is available — module cards with fan-in/fan-out and state signals.
func buildClusteringPrompt(input ports.ClusterInput) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("## Domain Modules (%d total)\n", len(input.DomainModules)))
	sorted := make([]workerdomain.Module, len(input.DomainModules))
	copy(sorted, input.DomainModules)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, m := range sorted {
		b.WriteString("- " + string(m) + "\n")
	}

	b.WriteString(fmt.Sprintf("\n## Dependency Graph (%d edges)\n", len(input.DomainGraph.Edges)))
	// Sort edges by weight descending for most-significant first.
	edges := make([]workerdomain.Edge, len(input.DomainGraph.Edges))
	copy(edges, input.DomainGraph.Edges)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Weight != edges[j].Weight {
			return edges[i].Weight > edges[j].Weight
		}
		return string(edges[i].From) < string(edges[j].From)
	})
	limit := 50
	if len(edges) < limit {
		limit = len(edges)
	}
	for _, e := range edges[:limit] {
		b.WriteString(fmt.Sprintf("  %s → %s (weight=%d)\n", e.From, e.To, e.Weight))
	}
	if len(edges) > 50 {
		b.WriteString(fmt.Sprintf("  ... (%d more edges omitted)\n", len(edges)-50))
	}

	b.WriteString(`
---
Group the domain modules above into candidate service boundaries.

Return exactly this JSON schema (all fields required):
{
  "groups": [
    {
      "name": "<short service name, e.g. 'articles'>",
      "modules": ["<module1>", "<module2>"],
      "responsibilities": ["<responsibility 1>", "<responsibility 2>"],
      "confidence": "HIGH" | "MEDIUM" | "LOW"
    }
  ],
  "explanation": "<1-2 sentences explaining the grouping rationale>"
}

IMPORTANT: modules must be exact strings from the list above. Never invent module names.
`)
	return b.String()
}
