package adapters

import (
	"context"
	"fmt"
	"time"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
	applog "milton_prism/pkg/log"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var _ ports.ArtifactStore = (*MongoArtifactStore)(nil)

// MongoArtifactStore persists design artifacts to the design_artifacts collection.
// Documents are upserted on (migration_id, service_name) — idempotent by design.
type MongoArtifactStore struct {
	coll *mongo.Collection
}

// NewMongoArtifactStore returns a MongoArtifactStore backed by the given database.
func NewMongoArtifactStore(db *mongo.Database) *MongoArtifactStore {
	return &MongoArtifactStore{coll: db.Collection("design_artifacts")}
}

// UpsertArtifacts writes one document per artifact into design_artifacts,
// updating existing records if the pipeline is re-run for the same migration.
func (s *MongoArtifactStore) UpsertArtifacts(
	ctx context.Context,
	migrationID uint64,
	artifacts []workerdomain.ServiceArtifact,
) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	upsert := true
	opts := options.UpdateOptions{Upsert: &upsert}

	for _, a := range artifacts {
		filter := bson.M{
			"migration_id": migrationID,
			"service_name": a.ServiceName,
		}
		update := bson.M{"$set": bson.M{
			"migration_id":      migrationID,
			"service_name":      a.ServiceName,
			"proto_content":     a.ProtoContent,
			"boundary_spec":     a.BoundarySpec,
			"incomplete":        a.Incomplete,
			"incomplete_reason": a.IncompleteReason,
			"update_time":       now,
		}}
		if _, err := s.coll.UpdateOne(ctx, filter, update, &opts); err != nil {
			return fmt.Errorf("artifact-store: upsert service=%s migration=%d: %w",
				a.ServiceName, migrationID, err)
		}
	}

	applog.Infof("artifact-store: upserted migration_id=%d services=%d", migrationID, len(artifacts))
	return nil
}
