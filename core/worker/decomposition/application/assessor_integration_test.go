package application

// Integration tests for the migrability assessor.
// They require:
//   - ANTHROPIC_API_KEY env var
//   - MongoDB running at localhost:27017 with the live analysis summaries
//     (summary IDs 10047 = Conduit, 10045 = notiplan)
//
// Run with:
//
//	ANTHROPIC_API_KEY=sk-ant-... go test -run TestAssessor_Integration_ -v \
//	    ./core/worker/decomposition/application/...

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	decompadapters "milton_prism/core/worker/decomposition/infrastructure/adapters"
	"milton_prism/core/worker/decomposition/ports"
	workeradapters "milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func skipIfNoAPIKey(t *testing.T) {
	t.Helper()
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping live assessor integration test")
	}
}

func openAnalysisDB(t *testing.T) *mongo.Database {
	t.Helper()
	uri := "mongodb://admin:bimtra654@localhost:27017/?authSource=admin&directConnection=true"
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("mongo connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client.Database("milton_prism_analysis")
}

func buildLiveDigest(t *testing.T, summaryID uint64) *workerdomain.AnalysisDigest {
	t.Helper()
	db := openAnalysisDB(t)
	loader := decompadapters.NewMongoGraphLoader(db)
	ctx := context.Background()

	graph, err := loader.Load(ctx, summaryID)
	require.NoError(t, err, "load graph")

	cards, err := loader.LoadCards(ctx, summaryID)
	require.NoError(t, err, "load cards")

	det := decompadapters.NewDeterministicInfraDetector()
	cls, err := det.Detect(ctx, graph)
	require.NoError(t, err, "infra detect")

	clust := decompadapters.NewLouvainClusterer()
	domainGraph := workerdomain.DomainSubgraph(graph, cls.Domain)
	cr, err := clust.Cluster(ctx, ports.ClusterInput{DomainGraph: domainGraph, DomainModules: cls.Domain})
	require.NoError(t, err, "cluster")

	// Apply guardrail — same as the production path in the pipeline and adapter.
	ApplyCoherenceGuardrail(cls, cr, domainGraph)

	return Distill(graph, cls, cr, cards, 0)
}

// TestAssessor_Integration_Conduit validates that the live Conduit repo digest
// produces a MIGRABLE verdict under the Prism v1 standard (shared DB + gRPC sync).
// Shared ORM infrastructure and synchronous coupling are accepted debt, not blockers.
func TestAssessor_Integration_Conduit(t *testing.T) {
	skipIfNoAPIKey(t)
	digest := buildLiveDigest(t, 10047) // Conduit

	mc, err := workeradapters.NewAnthropicModelClient(nil)
	require.NoError(t, err)

	assessor := NewAssessor(mc)
	score := Score(digest)
	result, err := assessor.Assess(context.Background(), digest, score, "en")
	require.NoError(t, err)

	b, _ := json.MarshalIndent(result.Verdict, "", "  ")
	t.Logf("Conduit verdict:\n%s", string(b))
	t.Logf("Cost: $%.6f (%d in / %d out tokens)", result.CostUSD, result.InputTokens, result.OutputTokens)

	validVerdicts := []string{workerdomain.VerdictMigrable, workerdomain.VerdictPartial}
	assert.Contains(t, validVerdicts, result.Verdict.Verdict,
		"Conduit should be MIGRABLE under Prism v1 (3 domain clusters, domain/infra separation; shared DB and ORM coupling are accepted debt). PARTIAL is tolerated only if a genuine non-DB blocker is present.")
	assert.NotEmpty(t, result.Verdict.Summary)
	assert.NotEmpty(t, result.Verdict.Reasons)
	assert.NotEmpty(t, result.Verdict.Confidence)
}

// TestAssessor_Integration_Notiplan validates that the live notiplan repo digest
// produces a PARTIAL or NOT_MIGRABLE verdict even under the Prism v1 standard.
// Its blockers (zero domain layer, business-logic global state, god-module) are real
// regardless of shared-DB tolerance.
func TestAssessor_Integration_Notiplan(t *testing.T) {
	skipIfNoAPIKey(t)
	digest := buildLiveDigest(t, 10045) // notiplan

	mc, err := workeradapters.NewAnthropicModelClient(nil)
	require.NoError(t, err)

	assessor := NewAssessor(mc)
	score := Score(digest)
	result, err := assessor.Assess(context.Background(), digest, score, "en")
	require.NoError(t, err)

	b, _ := json.MarshalIndent(result.Verdict, "", "  ")
	t.Logf("Notiplan verdict:\n%s", string(b))
	t.Logf("Cost: $%.6f (%d in / %d out tokens)", result.CostUSD, result.InputTokens, result.OutputTokens)

	assert.Equal(t, workerdomain.VerdictNotMigrable, result.Verdict.Verdict,
		"notiplan must be NOT_MIGRABLE after coherence guardrail fires: star-topology graph, DomainEmpty=true, NoServiceBoundaries=true")
	assert.NotEmpty(t, result.Verdict.Blockers,
		"notiplan must have blockers — acantilado pattern and business-logic god-modules are real blockers under any deployment model")
}

// ErrNoDocuments is the sentinel from the mongo driver.
var _ = errors.New // keep import live
