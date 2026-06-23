package repositories

import (
	"context"
	"os"
	"testing"

	"milton_prism/core/services/analysis/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	mongoopts "go.mongodb.org/mongo-driver/mongo/options"
)

// connectAnalysisIntegMongo skips if MONGO_URI is unset and returns an open DB
// scoped to a throwaway collection-friendly database. Mirrors the integration
// test pattern used elsewhere in the repository.
func connectAnalysisIntegMongo(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		t.Skip("MONGO_URI not set — skipping integration test")
	}
	dbName := os.Getenv("MONGO_DB_NAME")
	if dbName == "" {
		dbName = "milton_prism_analysis_test"
	}
	client, err := mongo.Connect(context.Background(), mongoopts.Client().ApplyURI(uri))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client.Database(dbName)
}

// TestCreate_ReAnalysis_UpdatesInPlace verifies the approved re-analysis rule:
// a second Create for the same (repository_id, source_branch) updates the
// existing summary in place — same identifier, no second row — resetting it to
// RUNNING and clearing the prior run's result fields.
func TestCreate_ReAnalysis_UpdatesInPlace(t *testing.T) {
	db := connectAnalysisIntegMongo(t)
	repo := NewMongoAnalysisSummaryRepository(db)
	ctx := context.Background()

	const repoID, branch = uint64(770001), "main"
	t.Cleanup(func() {
		_, _ = db.Collection(analysisSummariesCollName).DeleteMany(ctx, bson.M{"repository_id": repoID})
	})
	// Clean any leftovers from a prior failed run.
	_, _ = db.Collection(analysisSummariesCollName).DeleteMany(ctx, bson.M{"repository_id": repoID})

	first, err := repo.Create(ctx, &domain.AnalysisSummary{
		RepositoryId: repoID,
		SourceBranch: branch,
		CommitSha:    "oldsha",
		State:        domain.AnalysisStateCompleted,
		TotalFiles:   123,
	})
	require.NoError(t, err)
	require.NotZero(t, first.GetIdentifier())

	// Re-analysis: same repo+branch. Must reuse the identifier and reset state.
	second, err := repo.Create(ctx, &domain.AnalysisSummary{
		RepositoryId: repoID,
		SourceBranch: branch,
		State:        domain.AnalysisStateRunning,
	})
	require.NoError(t, err)
	assert.Equal(t, first.GetIdentifier(), second.GetIdentifier(), "re-analysis must reuse the existing identifier")
	assert.Equal(t, domain.AnalysisStateRunning, second.GetState())
	assert.Empty(t, second.GetCommitSha(), "prior commit_sha must be cleared on re-analysis")
	assert.Zero(t, second.GetTotalFiles(), "prior totals must be cleared on re-analysis")

	// Exactly one row exists for this repo+branch.
	n, err := db.Collection(analysisSummariesCollName).CountDocuments(ctx, bson.M{
		"repository_id": repoID,
		"source_branch": branch,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "re-analysis must never create a second row")
}
