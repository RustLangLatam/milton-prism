package repositories

import (
	"context"
	"fmt"

	"milton_prism/core/services/migration/ports"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var _ ports.GenerationRecordResetter = (*MongoGenerationRecordResetter)(nil)

// MongoGenerationRecordResetter resets the per-service generation records of
// failed services on the generation_results collection so a RetryGeneration
// starts those services from a clean (pending) slate.
type MongoGenerationRecordResetter struct {
	coll *mongo.Collection
}

// NewMongoGenerationRecordResetter binds the resetter to the generation_results
// collection (the same collection the worker writes and the result reader reads).
func NewMongoGenerationRecordResetter(db *mongo.Database) *MongoGenerationRecordResetter {
	return &MongoGenerationRecordResetter{coll: db.Collection("generation_results")}
}

// ResetServiceRecords sets each named service's record back to "pending" and
// clears its failure fields (failure_reason, failure_class) for the migration.
// A no-op when serviceNames is empty. Records the worker has not written yet
// (no document) are simply not matched — the worker will create them on its run.
func (r *MongoGenerationRecordResetter) ResetServiceRecords(ctx context.Context, migrationID uint64, serviceNames []string) error {
	if len(serviceNames) == 0 {
		return nil
	}
	filter := bson.M{
		"migration_id": migrationID,
		"service_name": bson.M{"$in": serviceNames},
	}
	update := bson.M{
		"$set":   bson.M{"status": "pending", "gates_passed": false},
		"$unset": bson.M{"failure_reason": "", "failure_class": ""},
	}
	if _, err := r.coll.UpdateMany(ctx, filter, update); err != nil {
		return fmt.Errorf("generation-record-resetter: reset migration_id=%d services=%v: %w", migrationID, serviceNames, err)
	}
	return nil
}
