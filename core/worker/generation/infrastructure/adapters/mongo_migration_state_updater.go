package adapters

import (
	"context"
	"fmt"
	"time"

	"milton_prism/core/worker/generation/ports"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

var _ ports.MigrationStateUpdater = (*MongoMigrationStateUpdater)(nil)

// MongoMigrationStateUpdater advances a migration to READY by writing
// MIGRATION_STATE_READY directly to the migrations collection.
type MongoMigrationStateUpdater struct {
	coll *mongo.Collection
}

// NewMongoMigrationStateUpdater returns a state updater backed by db.
func NewMongoMigrationStateUpdater(db *mongo.Database) *MongoMigrationStateUpdater {
	return &MongoMigrationStateUpdater{coll: db.Collection("migrations")}
}

func (u *MongoMigrationStateUpdater) MarkReady(ctx context.Context, migrationID uint64) error {
	return u.setFinalState(ctx, migrationID, migrationv1.MigrationState_MIGRATION_STATE_READY)
}

func (u *MongoMigrationStateUpdater) MarkFailed(ctx context.Context, migrationID uint64) error {
	return u.setFinalState(ctx, migrationID, migrationv1.MigrationState_MIGRATION_STATE_FAILED)
}

func (u *MongoMigrationStateUpdater) setFinalState(ctx context.Context, migrationID uint64, state migrationv1.MigrationState) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := u.coll.UpdateOne(
		ctx,
		bson.M{"identifier": migrationID, "delete_time": nil},
		bson.M{"$set": bson.M{
			"state":       int32(state),
			"update_time": now,
		}},
	)
	if err != nil {
		return fmt.Errorf("state-updater: migration_id=%d state=%v: %w", migrationID, state, err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("state-updater: migration_id=%d not found", migrationID)
	}
	return nil
}
