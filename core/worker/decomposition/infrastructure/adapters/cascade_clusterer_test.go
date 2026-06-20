package adapters

import (
	"context"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/mocks"
	"milton_prism/core/worker/decomposition/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// cascadeConduitEdges is a Conduit-like fixture with 3 blueprint groups
// (articles, profile, user), each with .models and .views sub-modules, and a
// shared infrastructure module (database). Blueprint-biased Louvain produces
// 3 high-confidence clusters → cascade stays on deterministic path.
var cascadeConduitEdges = []workerdomain.Edge{
	{From: "conduit.articles.views", To: "conduit.articles.models", Weight: 3},
	{From: "conduit.articles.models", To: "conduit.database", Weight: 6},
	{From: "conduit.articles.models", To: "conduit.profile.models", Weight: 1},
	{From: "conduit.profile.views", To: "conduit.profile.models", Weight: 2},
	{From: "conduit.profile.models", To: "conduit.database", Weight: 5},
	{From: "conduit.profile.models", To: "conduit.user.models", Weight: 1},
	{From: "conduit.user.views", To: "conduit.user.models", Weight: 2},
	{From: "conduit.user.models", To: "conduit.database", Weight: 4},
}

// cascadeNotiplanEdges is the exact live notiplan graph (star topology, 26
// edges, backend.var and backend.funcs as shared-state hubs). The structural
// fallback classifies all non-test modules as domain. Louvain produces 8
// clusters with modularity=0.14 (LowConfidence). The coherence guardrail fires
// because 7/8 clusters are singletons with no internal edges.
var cascadeNotiplanEdges = []workerdomain.Edge{
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

// TestCascade_Louvain_Sufficient_Conduit verifies the deterministic path:
// Conduit has 3 blueprint groups, so Louvain produces 3 high-confidence
// clusters. The cascade returns them directly and never calls the LLM.
func TestCascade_Louvain_Sufficient_Conduit(t *testing.T) {
	t.Parallel()

	graph := &workerdomain.Graph{Edges: cascadeConduitEdges}
	det := NewDeterministicInfraDetector()
	cls, err := det.Detect(context.Background(), graph)
	require.NoError(t, err)
	require.False(t, cls.StructuralFallback,
		"Conduit must NOT trigger structural fallback — it has 3 blueprint groups")

	domainGraph := workerdomain.DomainSubgraph(graph, cls.Domain)
	louvain := NewLouvainClusterer()

	// No expectations set on the LLM mock → any call panics the test.
	llmMock := &mocks.MockSemanticClusterer{}

	cascade := NewCascadeClusterer(louvain, llmMock)
	result, err := cascade.Cluster(context.Background(), ports.ClusterInput{
		DomainGraph:        domainGraph,
		DomainModules:      cls.Domain,
		StructuralFallback: cls.StructuralFallback,
	})

	require.NoError(t, err)
	assert.Greater(t, len(result.Clusters), 0, "Louvain should find clusters in Conduit")
	assert.False(t, result.LowConfidence, "Conduit clusters must be high-confidence")
	// Conduit fixture produces Q ≈ 0.44; must exceed the low-confidence threshold.
	assert.Greater(t, result.Modularity, 0.25,
		"Conduit partition_modularity must exceed 0.25 (got %.4f)", result.Modularity)

	// The LLM was never called.
	llmMock.AssertNotCalled(t, "Cluster")
}

// TestCascade_Notiplan_GuardrailShortCircuit verifies that the cascade does NOT
// invoke the LLM for notiplan: the coherence guardrail predicts that the
// star-topology result (StructuralFallback=true, LowConfidence=true, 7/8
// incoherent clusters) would be discarded upstream. Calling the LLM would
// waste tokens and produce misleading candidate groupings for a graph with no
// real domain seams.
func TestCascade_Notiplan_GuardrailShortCircuit(t *testing.T) {
	t.Parallel()

	graph := &workerdomain.Graph{Edges: cascadeNotiplanEdges}
	det := NewDeterministicInfraDetector()
	cls, err := det.Detect(context.Background(), graph)
	require.NoError(t, err)
	require.True(t, cls.StructuralFallback,
		"notiplan must trigger structural fallback — no blueprint groups in graph")

	domainGraph := workerdomain.DomainSubgraph(graph, cls.Domain)
	louvain := NewLouvainClusterer()

	// No expectations set on the LLM mock → any call panics the test.
	llmMock := &mocks.MockSemanticClusterer{}

	cascade := NewCascadeClusterer(louvain, llmMock)
	result, err := cascade.Cluster(context.Background(), ports.ClusterInput{
		DomainGraph:        domainGraph,
		DomainModules:      cls.Domain,
		StructuralFallback: cls.StructuralFallback,
	})

	require.NoError(t, err)
	// Louvain result is returned as-is (guardrail fires upstream, not here).
	// The cascade's job is only to short-circuit the LLM call.
	assert.NotNil(t, result)
	// Notiplan star-topology produces Q ≈ 0.14 — below the low-confidence threshold.
	assert.Less(t, result.Modularity, 0.25,
		"Notiplan partition_modularity must be below 0.25 (got %.4f)", result.Modularity)

	// The LLM was never called — star-topology graph, guardrail would fire.
	llmMock.AssertNotCalled(t, "Cluster")
}

// TestCascade_LowConfidence_LLM_Called verifies that the LLM fallback IS
// invoked when Louvain returns LowConfidence but the coherence guardrail would
// NOT fire (there is real domain structure, just insufficient modularity).
//
// Fixture: 2 clusters, each with one internal edge → IsIncoherentFallback=false.
// StructuralFallback=false (blueprint groups exist). LowConfidence forced by
// a mock primary that returns LowConfidence=true.
func TestCascade_LowConfidence_LLM_Called(t *testing.T) {
	t.Parallel()

	domainGraph := &workerdomain.Graph{
		Edges: []workerdomain.Edge{
			{From: "app.a1", To: "app.a2", Weight: 1}, // cluster A internal
			{From: "app.b1", To: "app.b2", Weight: 1}, // cluster B internal
		},
	}
	domainModules := []workerdomain.Module{"app.a1", "app.a2", "app.b1", "app.b2"}

	// Primary returns LowConfidence=true with 2 coherent clusters.
	louvainMock := &mocks.MockSemanticClusterer{}
	louvainMock.On("Cluster", mock.Anything, mock.Anything).
		Return(&workerdomain.ClusteringResult{
			Clusters: []workerdomain.Cluster{
				{BlueprintGroup: "a", Modules: []workerdomain.Module{"app.a1", "app.a2"}},
				{BlueprintGroup: "b", Modules: []workerdomain.Module{"app.b1", "app.b2"}},
			},
			LowConfidence: true,
		}, nil).
		Once()

	// LLM returns a better result.
	llmMock := &mocks.MockSemanticClusterer{}
	llmMock.On("Cluster", mock.Anything, mock.Anything).
		Return(&workerdomain.ClusteringResult{
			Clusters: []workerdomain.Cluster{
				{BlueprintGroup: "a", Modules: []workerdomain.Module{"app.a1", "app.a2"}},
				{BlueprintGroup: "b", Modules: []workerdomain.Module{"app.b1", "app.b2"}},
			},
			LowConfidence: false,
		}, nil).
		Once()

	cascade := NewCascadeClusterer(louvainMock, llmMock)
	result, err := cascade.Cluster(context.Background(), ports.ClusterInput{
		DomainGraph:        domainGraph,
		DomainModules:      domainModules,
		StructuralFallback: false, // blueprint groups exist — guardrail won't fire
	})

	require.NoError(t, err)
	assert.Len(t, result.Clusters, 2)
	assert.False(t, result.LowConfidence, "LLM result replaces Louvain result")

	louvainMock.AssertExpectations(t)
	llmMock.AssertExpectations(t) // confirms LLM was called exactly once
}
