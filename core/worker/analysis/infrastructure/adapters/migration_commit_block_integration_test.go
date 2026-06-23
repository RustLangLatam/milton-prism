package adapters

import (
	"context"
	"os"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	migrationdomain "milton_prism/core/services/migration/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	mongoopts "go.mongodb.org/mongo-driver/mongo/options"
)

// connectCommitBlockMongo skips if MONGO_URI is unset and returns analysis and
// migration databases. The migration repository is built so the
// uniq_repo_branch_commit_topology_language_protocol partial unique index exists.
func connectCommitBlockMongo(t *testing.T) (analysisDB, migrationDB *mongo.Database) {
	t.Helper()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		t.Skip("MONGO_URI not set — skipping integration test")
	}
	client, err := mongo.Connect(context.Background(), mongoopts.Client().ApplyURI(uri))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	analysisDB = client.Database("milton_prism_analysis_test")
	migrationDB = client.Database("milton_prism_migration_test")
	// Build the partial unique commit index — identical to the one the migration
	// repository creates (uniq_repo_branch_commit_topology_language_protocol).
	// Created here directly to avoid an import cycle (the migration repository
	// imports this adapters package). Drop any legacy index first so re-runs are
	// idempotent across the schema change.
	migColl := migrationDB.Collection("migrations")
	_, _ = migColl.Indexes().DropOne(context.Background(), "uniq_repo_branch_commit")
	_, _ = migColl.Indexes().DropOne(context.Background(), "uniq_repo_branch_commit_topology")
	_, err = migColl.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys: bson.D{
			{Key: "repository_id", Value: 1},
			{Key: "source_branch", Value: 1},
			{Key: "commit_sha", Value: 1},
			{Key: "topology", Value: 1},
			{Key: "language", Value: 1},
			{Key: "protocol", Value: 1},
		},
		Options: mongoopts.Index().
			SetUnique(true).
			SetName("uniq_repo_branch_commit_topology_language_protocol").
			SetPartialFilterExpression(bson.M{"commit_sha": bson.M{"$exists": true, "$gt": ""}}),
	})
	require.NoError(t, err)
	return analysisDB, migrationDB
}

// seedMigration inserts an ANALYZING migration directly with default
// topology/language/protocol (the zero-value cell). Use seedMigrationCell to
// pin a specific {topology, language, protocol} cell of the matrix.
func seedMigration(t *testing.T, db *mongo.Database, id, repoID uint64, branch, commit string, state migrationdomain.MigrationState) {
	t.Helper()
	seedMigrationCell(t, db, id, repoID, branch, commit, state, 0, 0, 0)
}

// seedMigrationCell inserts an ANALYZING migration pinned to a specific
// {topology, language, protocol} cell, so the 6-dimension uniqueness index can
// be exercised: same (repo, branch, commit) with a different cell must NOT collide.
func seedMigrationCell(t *testing.T, db *mongo.Database, id, repoID uint64, branch, commit string, state migrationdomain.MigrationState, topology, language, protocol int32) {
	t.Helper()
	doc := bson.M{
		"identifier":    id,
		"repository_id": repoID,
		"source_branch": branch,
		"state":         int32(state),
		"topology":      topology,
		"language":      language,
		"protocol":      protocol,
	}
	if commit != "" {
		doc["commit_sha"] = commit
	}
	_, err := db.Collection("migrations").InsertOne(context.Background(), doc)
	require.NoError(t, err)
}

// TestSummaryWriter_CommitBlock verifies decision D2: when the analysis resolves
// a commit, the migration's commit_sha is stamped and it advances to DESIGNING;
// but a SECOND migration on the same (repo, branch, commit) is blocked by the
// partial unique index and moved to FAILED (MIG223). A DIFFERENT commit advances
// normally.
func TestSummaryWriter_CommitBlock(t *testing.T) {
	analysisDB, migrationDB := connectCommitBlockMongo(t)
	w := NewMongoSummaryWriter(analysisDB, migrationDB)
	ctx := context.Background()

	const repoID, branch = uint64(880001), "main"
	const commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const commitB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	migColl := migrationDB.Collection("migrations")
	anColl := analysisDB.Collection("analysis_summaries")
	cleanup := func() {
		_, _ = migColl.DeleteMany(ctx, bson.M{"repository_id": repoID})
		_, _ = anColl.DeleteMany(ctx, bson.M{"repository_id": repoID})
	}
	cleanup()
	t.Cleanup(cleanup)

	// Migration #1 (id 8001), analysis #1 (id 9001) resolves commitA.
	seedMigration(t, migrationDB, 8001, repoID, branch, "", migrationdomain.MigrationStateAnalyzing)
	_, err := anColl.InsertOne(ctx, bson.M{
		"identifier": uint64(9001), "repository_id": repoID, "source_branch": "anbr1",
		"state": int32(analysisdomain.AnalysisStateRunning),
	})
	require.NoError(t, err)

	err = w.Write(ctx, &analysisdomain.AnalysisSummary{
		Identifier: 9001, RepositoryId: repoID, MigrationId: 8001,
		State: analysisdomain.AnalysisStateCompleted, CommitSha: commitA,
	})
	require.NoError(t, err)

	var mig1 struct {
		State     int32  `bson:"state"`
		CommitSHA string `bson:"commit_sha"`
	}
	require.NoError(t, migColl.FindOne(ctx, bson.M{"identifier": uint64(8001)}).Decode(&mig1))
	assert.Equal(t, int32(migrationdomain.MigrationStateDesigning), mig1.State, "first migration advances to DESIGNING")
	assert.Equal(t, commitA, mig1.CommitSHA, "commit_sha must be stamped on the migration")

	// Migration #2 (id 8002) on SAME repo+branch, analysis #2 resolves the SAME commitA.
	seedMigration(t, migrationDB, 8002, repoID, branch, "", migrationdomain.MigrationStateAnalyzing)
	_, err = anColl.InsertOne(ctx, bson.M{
		"identifier": uint64(9002), "repository_id": repoID, "source_branch": "anbr2",
		"state": int32(analysisdomain.AnalysisStateRunning),
	})
	require.NoError(t, err)

	err = w.Write(ctx, &analysisdomain.AnalysisSummary{
		Identifier: 9002, RepositoryId: repoID, MigrationId: 8002,
		State: analysisdomain.AnalysisStateCompleted, CommitSha: commitA,
	})
	require.NoError(t, err, "the writer handles the block internally, not via a returned error")

	var mig2 struct {
		State         int32  `bson:"state"`
		FailureReason string `bson:"failure_reason"`
	}
	require.NoError(t, migColl.FindOne(ctx, bson.M{"identifier": uint64(8002)}).Decode(&mig2))
	assert.Equal(t, int32(migrationdomain.MigrationStateFailed), mig2.State, "second migration at the same commit must FAIL (MIG223)")
	assert.Contains(t, mig2.FailureReason, migrationdomain.ErrBranchUnchanged.Message)

	// Migration #3 (id 8003) on SAME repo+branch but a DIFFERENT commitB advances fine.
	seedMigration(t, migrationDB, 8003, repoID, branch, "", migrationdomain.MigrationStateAnalyzing)
	_, err = anColl.InsertOne(ctx, bson.M{
		"identifier": uint64(9003), "repository_id": repoID, "source_branch": "anbr3",
		"state": int32(analysisdomain.AnalysisStateRunning),
	})
	require.NoError(t, err)

	err = w.Write(ctx, &analysisdomain.AnalysisSummary{
		Identifier: 9003, RepositoryId: repoID, MigrationId: 8003,
		State: analysisdomain.AnalysisStateCompleted, CommitSha: commitB,
	})
	require.NoError(t, err)

	var mig3 struct {
		State     int32  `bson:"state"`
		CommitSHA string `bson:"commit_sha"`
	}
	require.NoError(t, migColl.FindOne(ctx, bson.M{"identifier": uint64(8003)}).Decode(&mig3))
	assert.Equal(t, int32(migrationdomain.MigrationStateDesigning), mig3.State, "a different commit must advance normally")
	assert.Equal(t, commitB, mig3.CommitSHA)
}

// TestSummaryWriter_MatrixCellsDistinct proves the extended uniqueness key
// {repo, branch, commit, topology, language, protocol}: two migrations on the
// SAME (repo, branch, commit) that differ ONLY in language, and two that differ
// ONLY in protocol, must BOTH advance to DESIGNING (distinct matrix cells), while
// a third migration identical in all six dimensions is still blocked (MIG223).
func TestSummaryWriter_MatrixCellsDistinct(t *testing.T) {
	analysisDB, migrationDB := connectCommitBlockMongo(t)
	w := NewMongoSummaryWriter(analysisDB, migrationDB)
	ctx := context.Background()

	const repoID, branch = uint64(880002), "main"
	const commitA = "cccccccccccccccccccccccccccccccccccccccc"
	migColl := migrationDB.Collection("migrations")
	anColl := analysisDB.Collection("analysis_summaries")
	cleanup := func() {
		_, _ = migColl.DeleteMany(ctx, bson.M{"repository_id": repoID})
		_, _ = anColl.DeleteMany(ctx, bson.M{"repository_id": repoID})
	}
	cleanup()
	t.Cleanup(cleanup)

	// Matrix axes (mirror migrationv1 enum int values).
	const (
		topoMicro = int32(1)
		langGo    = int32(1)
		langPy    = int32(2)
		protoGRPC = int32(2)
		protoHTTP = int32(1)
	)

	// cell drives one migration through the writer at commitA and returns its
	// resulting state.
	cell := func(migID, anID uint64, topology, language, protocol int32) int32 {
		seedMigrationCell(t, migrationDB, migID, repoID, branch, "", migrationdomain.MigrationStateAnalyzing, topology, language, protocol)
		_, err := anColl.InsertOne(ctx, bson.M{
			"identifier": anID, "repository_id": repoID,
			"state": int32(analysisdomain.AnalysisStateRunning),
		})
		require.NoError(t, err)
		require.NoError(t, w.Write(ctx, &analysisdomain.AnalysisSummary{
			Identifier: anID, RepositoryId: repoID, MigrationId: migID,
			State: analysisdomain.AnalysisStateCompleted, CommitSha: commitA,
		}))
		var got struct {
			State int32 `bson:"state"`
		}
		require.NoError(t, migColl.FindOne(ctx, bson.M{"identifier": migID}).Decode(&got))
		return got.State
	}

	// Go + gRPC + micro: first cell, advances.
	assert.Equal(t, int32(migrationdomain.MigrationStateDesigning),
		cell(8101, 9101, topoMicro, langGo, protoGRPC), "first cell (Go+gRPC+micro) advances")
	// Python + gRPC + micro: differs ONLY in language — must also advance.
	assert.Equal(t, int32(migrationdomain.MigrationStateDesigning),
		cell(8102, 9102, topoMicro, langPy, protoGRPC), "differs only in language — distinct cell, advances")
	// Go + HTTP + micro: differs ONLY in protocol — must also advance.
	assert.Equal(t, int32(migrationdomain.MigrationStateDesigning),
		cell(8103, 9103, topoMicro, langGo, protoHTTP), "differs only in protocol — distinct cell, advances")
	// Go + gRPC + micro AGAIN: identical in all six dimensions — blocked (MIG223).
	assert.Equal(t, int32(migrationdomain.MigrationStateFailed),
		cell(8104, 9104, topoMicro, langGo, protoGRPC), "identical six-dimension cell — blocked (MIG223)")
}
