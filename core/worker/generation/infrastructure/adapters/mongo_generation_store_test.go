//go:build integration

// Integration tests for MongoGenerationStore.
//
// Requirements:
//   - MongoDB running at localhost:27017 with admin:bimtra654
//
// Run:
//
//	go test -v -tags integration -timeout 60s \
//	  ./core/worker/generation/infrastructure/adapters/...

package adapters_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	workerdomain "milton_prism/core/worker/generation/domain"
	"milton_prism/core/worker/generation/infrastructure/adapters"
)

// directConnection=true skips replica-set discovery so the driver connects
// immediately to the single node exposed by docker compose.
const testMongoURI = "mongodb://admin:bimtra654@localhost:27017/" +
	"?authSource=admin&directConnection=true"

func newTestDB(t *testing.T) *mongo.Database {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clientOpts := options.Client().
		ApplyURI(testMongoURI).
		SetServerSelectionTimeout(8 * time.Second).
		SetConnectTimeout(8 * time.Second)
	client, err := mongo.Connect(ctx, clientOpts)
	require.NoError(t, err, "connect to test MongoDB")
	require.NoError(t, client.Ping(ctx, nil), "ping test MongoDB — is it running?")
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	// Use a dedicated test database that is dropped on cleanup.
	db := client.Database("milton_prism_test_generation")
	t.Cleanup(func() { _ = db.Drop(context.Background()) })
	return db
}

// TestUpsertArtifacts_BulkWriteResilience is the primary gate-block test for
// the three-part persistence fix:
//
//  1. captureArtifacts (size cap) prevents oversized files from ever reaching
//     UpsertArtifacts — but we test UpsertArtifacts directly here to verify
//     that the store layer itself also survives a document-too-large rejection.
//
//  2. With SetOrdered(false), MongoDB writes every document it can and returns
//     per-document errors only for the rejected one.
//
//  3. UpsertArtifacts must return nil (not propagate the BulkWriteException)
//     and the good artifacts must be readable from the collection.
func TestUpsertArtifacts_BulkWriteResilience(t *testing.T) {
	db := newTestDB(t)
	store := adapters.NewMongoGenerationStore(db)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const migrationID uint64 = 999_001 // dedicated test ID, cleaned up by db.Drop
	const serviceName = "test-resilience"

	// Normal artifacts — small Go source files.
	normal := []workerdomain.FileArtifact{
		{Path: "core/services/test/domain/domain.go", Content: []byte("package domain\n")},
		{Path: "core/services/test/ports/repo.go", Content: []byte("package ports\n")},
		{Path: "core/services/test/wire.go", Content: []byte("package test\n")},
	}

	// One oversized artifact (17 MiB > MongoDB's 16 MiB per-document limit).
	// In the live pipeline this would be filtered by captureArtifacts before
	// reaching UpsertArtifacts, but we inject it directly to validate the
	// store-level defence.
	oversized := workerdomain.FileArtifact{
		Path:    "bin/test-binary",
		Content: make([]byte, 17<<20), // 17 MiB of zeros
	}

	artifacts := append(normal, oversized)

	err := store.UpsertArtifacts(ctx, migrationID, serviceName, artifacts)
	assert.NoError(t, err, "UpsertArtifacts must not return an error even when one document is rejected")

	// Verify the good artifacts ARE in the collection.
	coll := db.Collection("generation_file_artifacts")
	for _, a := range normal {
		var doc bson.M
		filter := bson.M{"migration_id": migrationID, "service_name": serviceName, "path": a.Path}
		findErr := coll.FindOne(ctx, filter).Decode(&doc)
		assert.NoError(t, findErr, "artifact %s must be persisted", a.Path)
	}

	// The oversized artifact must NOT be in the collection (rejected by MongoDB).
	var doc bson.M
	oversizedFilter := bson.M{"migration_id": migrationID, "service_name": serviceName, "path": oversized.Path}
	findErr := coll.FindOne(ctx, oversizedFilter).Decode(&doc)
	assert.ErrorIs(t, findErr, mongo.ErrNoDocuments, "oversized artifact must not be persisted")
}
