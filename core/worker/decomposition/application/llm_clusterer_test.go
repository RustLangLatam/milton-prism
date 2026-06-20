package application

import (
	"context"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/mocks"
	"milton_prism/core/worker/decomposition/ports"
	analysisports "milton_prism/core/worker/analysis/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// domainModules is the shared fixture for all LLM clusterer tests.
var testDomainModules = []workerdomain.Module{
	"app.orders.models",
	"app.orders.views",
	"app.payments.models",
	"app.payments.views",
	"app.shared.hub",
}

var testDomainGraph = &workerdomain.Graph{
	Edges: []workerdomain.Edge{
		{From: "app.orders.views", To: "app.orders.models", Weight: 3},
		{From: "app.payments.views", To: "app.payments.models", Weight: 2},
		{From: "app.orders.models", To: "app.shared.hub", Weight: 4},
		{From: "app.payments.models", To: "app.shared.hub", Weight: 3},
	},
}

// validProposalJSON is returned by the mock for the happy-path test.
const validProposalJSON = `{
  "groups": [
    {"name":"orders","modules":["app.orders.models","app.orders.views"],"responsibilities":["Order management"],"confidence":"HIGH"},
    {"name":"payments","modules":["app.payments.models","app.payments.views"],"responsibilities":["Payment processing"],"confidence":"HIGH"}
  ],
  "explanation":"Two clear domain groups with no shared state."
}`

// TestLLMClusterer_ValidProposal_OK verifies that a syntactically and semantically
// correct LLM response (no hallucinated modules, no duplicates, no shared-state
// coupling) is accepted as clusters without any retry.
func TestLLMClusterer_ValidProposal_OK(t *testing.T) {
	t.Parallel()

	mc := &mocks.MockModelClient{}
	mc.On("Complete", mock.Anything, mock.Anything).
		Return(analysisports.ModelResponse{Content: validProposalJSON}, nil).
		Once()

	clusterer := NewLLMClusterer(mc)
	result, err := clusterer.Cluster(context.Background(), ports.ClusterInput{
		DomainGraph:   testDomainGraph,
		DomainModules: testDomainModules,
	})

	require.NoError(t, err)
	assert.Len(t, result.Clusters, 2, "valid proposal must produce 2 clusters")
	assert.False(t, result.LowConfidence)
	assert.Len(t, result.CandidateGroupings, 2, "raw LLM groups preserved as CandidateGroupings")
	assert.Empty(t, result.RestructureRecs)

	// Verify correct module assignment.
	byGroup := make(map[string][]workerdomain.Module)
	for _, c := range result.Clusters {
		byGroup[c.BlueprintGroup] = c.Modules
	}
	assert.ElementsMatch(t,
		[]workerdomain.Module{"app.orders.models", "app.orders.views"},
		byGroup["orders"])
	assert.ElementsMatch(t,
		[]workerdomain.Module{"app.payments.models", "app.payments.views"},
		byGroup["payments"])

	mc.AssertExpectations(t)
}

// TestLLMClusterer_Hallucination_RetryFallback verifies the anti-hallucination
// path: when the model returns a module name that is not in the domain module
// set, the clusterer retries once with error feedback. If the retry also
// hallucinates, the result is an honest no-boundaries (LowConfidence=true,
// no Clusters) — the LLM output is never silently accepted.
func TestLLMClusterer_Hallucination_RetryFallback(t *testing.T) {
	t.Parallel()

	// Both attempts return a proposal with a hallucinated module name.
	hallucinatedJSON := `{
  "groups": [
    {"name":"ghost","modules":["app.orders.models","nonexistent.phantom.module"],"responsibilities":[],"confidence":"LOW"}
  ],
  "explanation":"Made up a module."
}`

	mc := &mocks.MockModelClient{}
	// Two Complete calls expected: attempt 1 + retry attempt.
	mc.On("Complete", mock.Anything, mock.Anything).
		Return(analysisports.ModelResponse{Content: hallucinatedJSON}, nil).
		Times(2)

	clusterer := NewLLMClusterer(mc)
	result, err := clusterer.Cluster(context.Background(), ports.ClusterInput{
		DomainGraph:   testDomainGraph,
		DomainModules: testDomainModules,
	})

	require.NoError(t, err, "hallucination fallback must not return an error")
	assert.Empty(t, result.Clusters, "hallucination fallback must produce no clusters")
	assert.True(t, result.LowConfidence, "hallucination fallback must be LowConfidence")

	mc.AssertExpectations(t) // confirms exactly 2 calls were made
}

// TestLLMClusterer_SharedHubProposalPassesThrough verifies that when a valid
// proposal includes groups whose modules share a hub, the LLMClusterer accepts
// the partition and returns clusters. Shared-state rejection (ApplyCoherenceGuardrail)
// runs post-clustering in the pipeline, not inside the clusterer.
func TestLLMClusterer_SharedHubProposalPassesThrough(t *testing.T) {
	t.Parallel()

	// Proposal: two groups, hub is not in either (it's a cross-cluster shared dep).
	proposalWithSharedHub := `{
  "groups": [
    {"name":"orders","modules":["app.orders.models","app.orders.views"],"responsibilities":["Order management"],"confidence":"HIGH"},
    {"name":"payments","modules":["app.payments.models","app.payments.views"],"responsibilities":["Payment processing"],"confidence":"MEDIUM"}
  ],
  "explanation":"Two groups that both import the shared hub."
}`

	mc := &mocks.MockModelClient{}
	mc.On("Complete", mock.Anything, mock.Anything).
		Return(analysisports.ModelResponse{Content: proposalWithSharedHub}, nil).
		Once()

	clusterer := NewLLMClusterer(mc)
	result, err := clusterer.Cluster(context.Background(), ports.ClusterInput{
		DomainGraph:   testDomainGraph,
		DomainModules: testDomainModules,
	})

	require.NoError(t, err)
	assert.Len(t, result.Clusters, 2,
		"valid proposal must produce clusters — shared-state check is post-clustering")
	assert.False(t, result.LowConfidence)
	assert.Len(t, result.CandidateGroupings, 2,
		"raw LLM groups preserved in CandidateGroupings")
	assert.Empty(t, result.RestructureRecs,
		"LLMClusterer does not emit restructure recs — that is ApplyCoherenceGuardrail's job")

	mc.AssertExpectations(t)
}
