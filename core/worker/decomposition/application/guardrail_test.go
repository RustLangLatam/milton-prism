package application

import (
	"context"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	decompadapters "milton_prism/core/worker/decomposition/infrastructure/adapters"
	"milton_prism/core/worker/decomposition/ports"
)

// notiplanEdges is the exact dependency graph of the live notiplan repo (analysis
// summary 10048) as decoded from the stored dependency_graph_bytes in MongoDB.
// 15 nodes, 26 edges — star topology anchored on backend.var (fan-in=19) and
// backend.funcs (fan-in≈12).
var notiplanEdges = []workerdomain.Edge{
	{From: "backend.202222ingeteam_backend", To: "backend.funcs", Weight: 1},
	{From: "backend.202222ingeteam_backend", To: "backend.parametros_iniciales", Weight: 1},
	{From: "backend.202222ingeteam_backend", To: "backend.plant_table_report", Weight: 2},
	{From: "backend.202222ingeteam_backend", To: "backend.var", Weight: 1},
	{From: "backend.create_tables", To: "backend.var", Weight: 2},
	{From: "backend.funcs", To: "backend.parametros_iniciales", Weight: 2},
	{From: "backend.funcs", To: "backend.var", Weight: 3},
	{From: "backend.ingeteam_backend", To: "backend.funcs", Weight: 1},
	{From: "backend.ingeteam_backend", To: "backend.parametros_iniciales", Weight: 1},
	{From: "backend.ingeteam_backend", To: "backend.plant_table_report", Weight: 2},
	{From: "backend.ingeteam_backend", To: "backend.var", Weight: 1},
	{From: "backend.itxi", To: "backend.var", Weight: 1},
	{From: "backend.itxi-ikusi", To: "backend.var", Weight: 1},
	{From: "backend.op_disp", To: "backend.var", Weight: 2},
	{From: "backend.operario_disp", To: "backend.var", Weight: 2},
	{From: "backend.parametros_iniciales", To: "backend.op_disp", Weight: 1},
	{From: "backend.parametros_iniciales", To: "backend.operario_disp", Weight: 1},
	{From: "backend.parametros_iniciales", To: "backend.var", Weight: 2},
	{From: "backend.plant_table_report", To: "backend.funcs", Weight: 8},
	{From: "backend.plant_table_report", To: "backend.var", Weight: 2},
	{From: "backend.reset_tareas", To: "backend.var", Weight: 1},
	{From: "backend.session", To: "backend.var", Weight: 1},
	{From: "backend.tests.test_generate_card_json", To: "backend.funcs", Weight: 1},
	{From: "backend.tests.test_generate_card_json", To: "backend.ingeteam_backend", Weight: 1},
	{From: "backend.tests.test_sync_servidor", To: "backend.funcs", Weight: 1},
	{From: "backend.tests.test_sync_servidor", To: "backend.ingeteam_backend", Weight: 1},
}

// TestGuardrail_Notiplan_DigestIsEmpty verifies the full deterministic path for
// the live notiplan graph: detect → cluster → ApplyCoherenceGuardrail → Distill
// must produce DomainEmpty=true and NoServiceBoundaries=true.
// No LLM call, no MongoDB. Uses the exact graph decoded from analysis 10048.
func TestGuardrail_Notiplan_DigestIsEmpty(t *testing.T) {
	graph := &workerdomain.Graph{Edges: notiplanEdges}
	ctx := context.Background()

	det := decompadapters.NewDeterministicInfraDetector()
	cls, err := det.Detect(ctx, graph)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !cls.StructuralFallback {
		t.Fatal("notiplan must trigger structural fallback — no domain-indicator suffixes in graph")
	}
	t.Logf("structural fallback: domain=%d infra=%d", len(cls.Domain), len(cls.Infra))

	clust := decompadapters.NewLouvainClusterer()
	domainGraph := workerdomain.DomainSubgraph(graph, cls.Domain)
	cr, err := clust.Cluster(ctx, ports.ClusterInput{DomainGraph: domainGraph, DomainModules: cls.Domain})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	t.Logf("before guardrail: clusters=%d modularity=%.4f low_conf=%v",
		len(cr.Clusters), cr.Modularity, cr.LowConfidence)

	fired := ApplyCoherenceGuardrail(cls, cr, domainGraph)
	if !fired {
		t.Fatal("guardrail must fire for notiplan — star-topology graph with isolated spoke nodes")
	}
	t.Logf("guardrail fired: clusters=%d domain=%d", len(cr.Clusters), len(cls.Domain))

	if len(cr.Clusters) != 0 {
		t.Errorf("after guardrail: expected 0 clusters, got %d", len(cr.Clusters))
	}
	if len(cls.Domain) != 0 {
		t.Errorf("after guardrail: expected empty domain, got %v", cls.Domain)
	}

	digest := Distill(graph, cls, cr, nil, 0)
	if !digest.Classification.DomainEmpty {
		t.Error("digest must have DomainEmpty=true after guardrail")
	}
	if !digest.NoServiceBoundaries {
		t.Error("digest must have NoServiceBoundaries=true after guardrail")
	}
	if !digest.LowConfidence {
		t.Error("digest must have LowConfidence=true after guardrail")
	}
	t.Logf("digest: DomainEmpty=%v NoServiceBoundaries=%v LowConfidence=%v",
		digest.Classification.DomainEmpty, digest.NoServiceBoundaries, digest.LowConfidence)
}

// conduitLikeEdges is a minimal Conduit-like fixture with 3 blueprint groups
// (articles, profile, user), each with .models and .views sub-modules, and a
// shared infrastructure module (database). Used by guardrail regression tests.
var conduitLikeEdges = []workerdomain.Edge{
	// articles blueprint group
	{From: "conduit.articles.views", To: "conduit.articles.models", Weight: 3},
	{From: "conduit.articles.models", To: "conduit.database", Weight: 6},
	{From: "conduit.articles.models", To: "conduit.profile.models", Weight: 1},
	// profile blueprint group
	{From: "conduit.profile.views", To: "conduit.profile.models", Weight: 2},
	{From: "conduit.profile.models", To: "conduit.database", Weight: 5},
	{From: "conduit.profile.models", To: "conduit.user.models", Weight: 1},
	// user blueprint group
	{From: "conduit.user.views", To: "conduit.user.models", Weight: 2},
	{From: "conduit.user.models", To: "conduit.database", Weight: 4},
}

// TestGuardrail_Conduit_NoRegression verifies that the coherence guardrail does
// NOT fire for Conduit: blueprint groups are found, StructuralFallback=false, so
// the guardrail path is never entered and the clusters remain intact.
func TestGuardrail_Conduit_NoRegression(t *testing.T) {
	graph := &workerdomain.Graph{Edges: conduitLikeEdges}
	ctx := context.Background()

	det := decompadapters.NewDeterministicInfraDetector()
	cls, err := det.Detect(ctx, graph)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if cls.StructuralFallback {
		t.Fatal("Conduit must NOT trigger structural fallback — it has 3 blueprint groups")
	}

	clust := decompadapters.NewLouvainClusterer()
	domainGraph := workerdomain.DomainSubgraph(graph, cls.Domain)
	cr, err := clust.Cluster(ctx, ports.ClusterInput{DomainGraph: domainGraph, DomainModules: cls.Domain})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	clustersBeforeGuardrail := len(cr.Clusters)
	domainBeforeGuardrail := len(cls.Domain)

	fired := ApplyCoherenceGuardrail(cls, cr, domainGraph)
	if fired {
		t.Error("guardrail must NOT fire for Conduit — it has blueprint groups, not structural fallback")
	}
	if len(cr.Clusters) != clustersBeforeGuardrail {
		t.Errorf("Conduit clusters changed after guardrail: %d → %d", clustersBeforeGuardrail, len(cr.Clusters))
	}
	if len(cls.Domain) != domainBeforeGuardrail {
		t.Errorf("Conduit domain changed after guardrail: %d → %d", domainBeforeGuardrail, len(cls.Domain))
	}
	t.Logf("Conduit: clusters=%d domain=%d (unchanged — guardrail did not fire)", len(cr.Clusters), len(cls.Domain))
}
