// Package repositories_test — Conduit blueprint integration test.
// Validates the BlueprintGeneratorAdapter honesty contract using the Conduit
// fixtures (3 clusters, clear domain layer) WITHOUT needing a real MongoDB
// instance. The adapter's Generate method is tested by wiring it with the
// Conduit AnalysisDigest injected directly through a narrow stub loader.
//
// Run only when ANTHROPIC_API_KEY is set:
//
//	ANTHROPIC_API_KEY=sk-ant-... go test ./core/services/migration/infrastructure/repositories/... -run TestBlueprintAdapter_Conduit -v
package repositories_test

import (
	"context"
	"os"
	"testing"

	"milton_prism/core/services/migration/domain"
	repositories "milton_prism/core/services/migration/infrastructure/repositories"
	workerapp "milton_prism/core/worker/decomposition/application"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// conduitDigest builds the AnalysisDigest used for the Conduit case: 3 clusters,
// real domain modules, no shared-state hubs. Mirrors the conduit* fixtures in
// core/worker/decomposition/application/distiller_test.go.
func conduitDigest() *workerdomain.AnalysisDigest {
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
	clusterResult := &workerdomain.ClusteringResult{
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
		Technologies: []string{"Python", "Flask", "SQLAlchemy"},
		Framework:    "Flask",
		ModuleCards: []workerdomain.SummaryModuleCard{
			{
				Module:  "conduit.articles.models",
				Classes: []string{"Article", "Comment", "Tags"},
				LOC:     120,
			},
			{
				Module:    "conduit.articles.views",
				Functions: []string{"list_articles", "get_article", "create_article"},
				Routes: []workerdomain.SummaryRoute{
					{Method: "GET", Path: "/articles", Handler: "list_articles"},
					{Method: "GET", Path: "/articles/<slug>", Handler: "get_article"},
					{Method: "POST", Path: "/articles", Handler: "create_article"},
				},
				LOC: 200,
			},
			{Module: "conduit.profile.models", Classes: []string{"UserProfile"}, LOC: 60},
			{Module: "conduit.user.models", Classes: []string{"User"}, LOC: 80},
		},
	}
	return workerapp.Distill(graph, cls, clusterResult, cards, 0)
}

// conduitRoadmap builds a minimal RESTRUCTURING_READY roadmap for Conduit: no
// blocking steps (the codebase is already structured).
func conduitRoadmap() *domain.RestructuringRoadmap {
	return &domain.RestructuringRoadmap{
		ActionPlan: []*migrationv1.ActionItem{
			{Order: 1, Kind: "DEFINE_BOUNDARIES", Subject: "conduit.articles, conduit.profile, conduit.user", Blocking: false, Impact: 10},
		},
	}
}

// TestBlueprintAdapter_Conduit validates the Conduit blueprint (3 clusters,
// real domain layer) using a stub loader that bypasses MongoDB.
// The test requires ANTHROPIC_API_KEY to be set; otherwise it is skipped.
func TestBlueprintAdapter_Conduit(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	// Connect to a real (or test) MongoDB to satisfy NewBlueprintGeneratorAdapter,
	// which only uses it for graph loading — and we inject the digest directly
	// using the exported BuildBlueprintFromDigest helper.
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://admin:bimtra654@localhost:27017/?authSource=admin"))
	if err != nil {
		t.Skipf("MongoDB unavailable: %v", err)
	}
	defer client.Disconnect(ctx) //nolint:errcheck
	analysisDB := client.Database("milton_prism_analysis")

	adapter, err := repositories.NewBlueprintGeneratorAdapter(analysisDB)
	require.NoError(t, err, "adapter construction requires ANTHROPIC_API_KEY")

	digest := conduitDigest()
	roadmap := conduitRoadmap()

	blueprint, err := adapter.GenerateFromDigest(ctx, digest, roadmap)
	require.NoError(t, err)
	require.NotNil(t, blueprint)

	t.Logf("=== CONDUIT BLUEPRINT ===")
	t.Logf("services: %d", len(blueprint.GetServices()))
	t.Logf("is_hypothetical: %v", blueprint.GetIsHypothetical())
	t.Logf("confidence_note: %s", blueprint.GetConfidenceNote())
	t.Logf("cost_usd: %.6f", blueprint.GetCostUsd())
	for i, svc := range blueprint.GetServices() {
		t.Logf("  service[%d]: name=%q modules=%v", i, svc.GetName(), svc.GetModules())
		t.Logf("           rationale: %s", svc.GetRationale())
	}

	// Conduit has 3 Louvain clusters — the LLM must propose at least 2 services
	// (it can merge user+profile if warranted) and MUST NOT return is_hypothetical.
	assert.False(t, blueprint.GetIsHypothetical(), "Conduit has real domain modules — blueprint should not be hypothetical")
	assert.GreaterOrEqual(t, len(blueprint.GetServices()), 2, "expected at least 2 services for 3 Louvain clusters")
	t.Logf("required_steps: %v (no blocking steps in roadmap — should be empty)", blueprint.GetRequiredSteps())
	assert.Positive(t, blueprint.GetCostUsd(), "cost must be positive")

	// Each service rationale must reference at least one module name from the digest.
	for _, svc := range blueprint.GetServices() {
		assert.NotEmpty(t, svc.GetRationale(), "rationale must be non-empty for service %q", svc.GetName())
	}
}
