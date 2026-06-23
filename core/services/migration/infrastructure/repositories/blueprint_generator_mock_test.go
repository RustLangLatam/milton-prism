package repositories

import (
	"context"
	"regexp"
	"sort"
	"testing"

	"milton_prism/core/services/migration/domain"
	analysisports "milton_prism/core/worker/analysis/ports"
	workerapp "milton_prism/core/worker/decomposition/application"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubBlueprintClient implements analysisports.ModelClient with a fixed JSON payload.
// Used to exercise GenerateFromDigest without an API key or network call.
type stubBlueprintClient struct {
	content string
}

func (s *stubBlueprintClient) Complete(_ context.Context, _ analysisports.ModelRequest) (analysisports.ModelResponse, error) {
	return analysisports.ModelResponse{
		Content:      s.content,
		InputTokens:  10,
		OutputTokens: 30,
		CostUSD:      0.0002,
	}, nil
}

// conduitMockDigest builds the 3-cluster Conduit-like AnalysisDigest used by
// TestBlueprintGeneratorAdapter_RealClusters. It mirrors the conduitDigest fixture
// in the external test but lives here so the stub adapter can be wired without MongoDB.
func conduitMockDigest() *workerdomain.AnalysisDigest {
	graph := &workerdomain.Graph{
		Edges: []workerdomain.Edge{
			{From: "conduit.articles.views", To: "conduit.articles.models", Weight: 2},
			{From: "conduit.articles.views", To: "conduit.user.models", Weight: 1},
			{From: "conduit.articles.models", To: "conduit.profile.models", Weight: 1},
			{From: "conduit.profile.views", To: "conduit.profile.models", Weight: 2},
			{From: "conduit.profile.views", To: "conduit.user.models", Weight: 1},
			{From: "conduit.user.views", To: "conduit.user.models", Weight: 2},
			{From: "conduit.user.views", To: "conduit.profile.models", Weight: 1},
		},
	}
	cls := &workerdomain.Classification{
		Domain: []workerdomain.Module{
			"conduit.articles.models",
			"conduit.articles.views",
			"conduit.profile.models",
			"conduit.profile.views",
			"conduit.user.models",
			"conduit.user.views",
		},
		Infra: []workerdomain.Module{"conduit.app", "conduit.config"},
	}
	clusters := &workerdomain.ClusteringResult{
		Clusters: []workerdomain.Cluster{
			{
				BlueprintGroup: "conduit.articles",
				Modules:        []workerdomain.Module{"conduit.articles.views", "conduit.articles.models"},
			},
			{
				BlueprintGroup: "conduit.profile",
				Modules:        []workerdomain.Module{"conduit.profile.views", "conduit.profile.models"},
			},
			{
				BlueprintGroup: "conduit.user",
				Modules:        []workerdomain.Module{"conduit.user.views", "conduit.user.models"},
			},
		},
		Modularity:    0.42,
		LowConfidence: false,
	}
	cards := &workerdomain.SummaryCards{
		Technologies: []string{"Python", "Flask"},
		Framework:    "Flask",
		ModuleCards: []workerdomain.SummaryModuleCard{
			{Module: "conduit.articles.models", Classes: []string{"Article", "Comment"}, LOC: 120},
			{
				Module:    "conduit.articles.views",
				Functions: []string{"list_articles", "create_article"},
				Routes: []workerdomain.SummaryRoute{
					{Method: "GET", Path: "/articles", Handler: "list_articles"},
					{Method: "POST", Path: "/articles", Handler: "create_article"},
				},
				LOC: 200,
			},
			{Module: "conduit.profile.models", Classes: []string{"UserProfile"}, LOC: 60},
			{Module: "conduit.user.models", Classes: []string{"User"}, LOC: 80},
		},
	}
	return workerapp.Distill(graph, cls, clusters, cards, 0)
}

// blueprintRealServicesResponse is the canned LLM reply for the 3-cluster Conduit fixture.
// CRITICAL: all prose (rationale, confidence_note, precondition_note) must be digit-free —
// categorical labels only; raw numbers violate the honesty contract verified in assertion (c).
const blueprintRealServicesResponse = `{
  "services": [
    {
      "name": "articles-service",
      "modules": ["conduit.articles.views", "conduit.articles.models"],
      "rationale": "The articles cluster forms a cohesive boundary: view handlers and model definitions share very-high intra-cluster coupling and represent the content lifecycle domain independently from user identity management."
    },
    {
      "name": "profile-service",
      "modules": ["conduit.profile.views", "conduit.profile.models"],
      "rationale": "Profile management clusters naturally around social graph data with low external coupling relative to the articles and user clusters, making it a stable extraction candidate."
    },
    {
      "name": "user-service",
      "modules": ["conduit.user.views", "conduit.user.models"],
      "rationale": "Authentication and account management form a foundational boundary consumed by both articles and profiles; this shared dependency makes it the highest-priority extraction target."
    }
  ],
  "is_hypothetical": false,
  "precondition_note": "",
  "required_steps": [],
  "confidence_note": "High structural confidence: domain-to-infra ratio is healthy, cross-cluster coupling is minimal, and Louvain partitioning yielded well-separated boundaries."
}`

var reAnyDigit = regexp.MustCompile(`\d`)

// TestBlueprintGeneratorAdapter_RealClusters exercises GenerateFromDigest with a
// synthetic 3-cluster digest and a stub ModelClient — no API key or MongoDB needed.
//
// This test covers the real-services branch (is_hypothetical=false) that is never
// reachable in the live flow: migrable repos never reach RESTRUCTURING_READY because
// GenerateRestructuringRoadmap requires a NOT_MIGRABLE verdict or no_service_boundaries.
// GenerateFromDigest is the only seam to exercise it deterministically.
//
// Assertions:
//
//	(a) is_hypothetical=false  — digest has real Louvain clusters, not a synthetic fallback.
//	(b) Services cover exactly the digest cluster modules — adapter maps LLM output faithfully.
//	(c) LLM prose is digit-free — categorical labels only, no raw numbers.
func TestBlueprintGeneratorAdapter_RealClusters(t *testing.T) {
	t.Parallel()

	digest := conduitMockDigest()
	adapter := &BlueprintGeneratorAdapter{client: &stubBlueprintClient{content: blueprintRealServicesResponse}}

	roadmap := &domain.RestructuringRoadmap{
		ActionPlan: []*migrationv1.ActionItem{
			{Order: 1, Kind: "DEFINE_BOUNDARIES", Subject: "conduit.articles, conduit.profile, conduit.user", Blocking: false, Impact: 10},
		},
	}

	blueprint, err := adapter.GenerateFromDigest(context.Background(), 0, 0, digest, roadmap)
	require.NoError(t, err)
	require.NotNil(t, blueprint)

	services := blueprint.GetServices()

	// (a) Digest has three real Louvain clusters — the real-services branch must not
	// flag is_hypothetical. (The hypothetical branch fires only when clusters is empty.)
	assert.False(t, blueprint.GetIsHypothetical(),
		"digest has real Louvain clusters — blueprint must not be flagged as hypothetical")

	// (b) Service count matches cluster count; every declared module is in the digest
	// graph, and together the services cover exactly the cluster module set.
	require.Equal(t, len(digest.Clusters), len(services),
		"service count must equal cluster count")

	nodeSet := make(map[string]bool, len(digest.Graph.Nodes))
	for _, n := range digest.Graph.Nodes {
		nodeSet[n] = true
	}

	var allServiceModules []string
	for _, svc := range services {
		for _, m := range svc.GetModules() {
			assert.Truef(t, nodeSet[m],
				"service %q declares module %q absent from digest graph", svc.GetName(), m)
			allServiceModules = append(allServiceModules, m)
		}
	}

	var clusterModules []string
	for _, c := range digest.Clusters {
		clusterModules = append(clusterModules, c.Modules...)
	}
	sort.Strings(clusterModules)
	sort.Strings(allServiceModules)
	assert.Equal(t, clusterModules, allServiceModules,
		"service modules must cover exactly the digest cluster modules — no additions, no omissions")

	// (c) LLM prose must not contain numeric digits — the honesty contract requires
	// categorical coupling labels (very-high, codebase-wide, …) not raw numbers.
	proseFields := map[string]string{
		"confidence_note":   blueprint.GetConfidenceNote(),
		"precondition_note": blueprint.GetPreconditionNote(),
	}
	for field, text := range proseFields {
		assert.Empty(t, reAnyDigit.FindString(text),
			"%s must not contain numeric digits", field)
	}
	for _, svc := range services {
		assert.Empty(t, reAnyDigit.FindString(svc.GetRationale()),
			"service %q rationale must not contain numeric digits", svc.GetName())
	}

	assert.Positive(t, blueprint.GetCostUsd(), "cost must be positive")
}
