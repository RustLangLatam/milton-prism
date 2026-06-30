package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"milton_prism/core/shared/event_bus"
	"milton_prism/core/worker/generation/ports"
	"milton_prism/pkg/log"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var _ ports.MigrationStateUpdater = (*MongoMigrationStateUpdater)(nil)

// MongoMigrationStateUpdater advances a migration to READY by writing
// MIGRATION_STATE_READY directly to the migrations collection.
//
// The generation-worker owns the terminal GENERATING→READY/FAILED transition
// and writes it straight to Mongo (it does NOT route through migration-svc's
// application service), so this is the only place that can emit the matching
// real-time event for generation completion. When a publisher is wired it
// emits a best-effort migration.state_changed event (same envelope/type as the
// migration-svc emitter — Phase 1) so the existing gateway SSE + frontend
// pipeline relays generation completion with zero gateway/frontend change.
type MongoMigrationStateUpdater struct {
	coll *mongo.Collection
	pub  *event_bus.Publisher
}

// NewMongoMigrationStateUpdater returns a state updater backed by db.
func NewMongoMigrationStateUpdater(db *mongo.Database) *MongoMigrationStateUpdater {
	return &MongoMigrationStateUpdater{coll: db.Collection("migrations")}
}

// WithEventPublisher wires a best-effort real-time event publisher. When nil
// (no [cache] / KeyDB reachable) emission silently degrades to a no-op and the
// state write is unaffected. Returns the receiver for chaining.
func (u *MongoMigrationStateUpdater) WithEventPublisher(p *event_bus.Publisher) *MongoMigrationStateUpdater {
	u.pub = p
	return u
}

func (u *MongoMigrationStateUpdater) MarkReady(ctx context.Context, migrationID uint64) error {
	return u.setFinalState(ctx, migrationID, migrationv1.MigrationState_MIGRATION_STATE_READY)
}

func (u *MongoMigrationStateUpdater) MarkFailed(ctx context.Context, migrationID uint64) error {
	return u.setFinalState(ctx, migrationID, migrationv1.MigrationState_MIGRATION_STATE_FAILED)
}

func (u *MongoMigrationStateUpdater) setFinalState(ctx context.Context, migrationID uint64, state migrationv1.MigrationState) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())

	// FindOneAndUpdate returning the BEFORE document atomically performs the
	// terminal state write AND yields owner_user_id + the previous state
	// (GENERATING here) in a single round-trip, so the real-time event carries
	// the looked-up owner without an asynq payload contract change.
	var before struct {
		OwnerUserID uint64 `bson:"owner_user_id"`
		State       int32  `bson:"state"`
	}
	err := u.coll.FindOneAndUpdate(
		ctx,
		bson.M{"identifier": migrationID, "delete_time": nil},
		bson.M{"$set": bson.M{
			"state":       int32(state),
			"update_time": now,
		}},
		options.FindOneAndUpdate().SetReturnDocument(options.Before),
	).Decode(&before)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return fmt.Errorf("state-updater: migration_id=%d not found", migrationID)
		}
		return fmt.Errorf("state-updater: migration_id=%d state=%v: %w", migrationID, state, err)
	}

	// Best-effort real-time notification. NEVER fails the state write or the
	// generation pipeline: log.Warning and continue on any error.
	u.publishStateChanged(ctx, migrationID, before.OwnerUserID, migrationv1.MigrationState(before.State), state)
	return nil
}

// publishStateChanged emits a migration.state_changed event for the terminal
// generation transition. It is strictly additive and best-effort: a nil
// publisher or any publish error is logged and swallowed.
func (u *MongoMigrationStateUpdater) publishStateChanged(ctx context.Context, migrationID, ownerUserID uint64, previous, next migrationv1.MigrationState) {
	if u.pub == nil {
		return
	}
	evt := event_bus.NewMigrationStateChangedEvent(migrationID, ownerUserID, next.String(), previous.String())
	if err := u.pub.PublishMigrationStateChanged(ctx, ownerUserID, evt); err != nil {
		log.Warningf("state-updater: emit state_changed failed migration_id=%d owner_user_id=%d %s->%s: %v",
			migrationID, ownerUserID, previous.String(), next.String(), err)
	}
}
