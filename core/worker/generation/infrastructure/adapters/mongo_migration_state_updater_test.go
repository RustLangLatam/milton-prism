//go:build integration

// Integration test for MongoMigrationStateUpdater's real-time event emission.
//
// Proves — token-free, WITHOUT any (paid) generation run — that the terminal
// GENERATING→READY write performed by the generation-worker:
//   - looks up owner_user_id + previous state from the migrations doc, and
//   - publishes a migration.state_changed event (SAME type the Phase 3 frontend
//     filters on) to the per-user KeyDB channel.
//
// Requirements (already up in the local compose env):
//   - MongoDB at localhost:27017 (admin:bimtra654)
//   - KeyDB/Redis at localhost:6379 (requirePass 1qaz2WSX)
//
// Run:
//
//	go test -v -tags integration -timeout 60s -run StateUpdater_Publishes \
//	  ./core/worker/generation/infrastructure/adapters/...
package adapters_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"milton_prism/core/shared/cache_client"
	"milton_prism/core/shared/event_bus"
	"milton_prism/core/worker/generation/infrastructure/adapters"
	"milton_prism/pkg/config"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

func newTestCachePool(t *testing.T) *redis.Pool {
	t.Helper()
	pool, err := cache_client.NewPool(&config.CacheCfg{
		Host:                "127.0.0.1",
		Port:                "6379",
		ProtectedMode:       true,
		RequirePass:         "1qaz2WSX",
		ConnectionPoolCount: 5,
		MaxIdle:             3,
	})
	require.NoError(t, err, "connect to test KeyDB — is it running?")
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// TestStateUpdater_Publishes_MigrationStateChanged is the Phase 2 transport gate:
// MarkReady on a GENERATING doc must emit migration.state_changed{READY} with the
// looked-up owner and previous_state=GENERATING on milton:events:user:<owner>.
func TestStateUpdater_Publishes_MigrationStateChanged(t *testing.T) {
	const (
		migrationID uint64 = 999000111
		ownerUserID uint64 = 10004
	)

	db := newTestDB(t)
	coll := db.Collection("migrations")

	// Seed a migration currently in GENERATING (the state the worker transitions
	// FROM). Mongo ids are BSON Long → identifier is an int64.
	_, err := coll.InsertOne(context.Background(), bson.M{
		"identifier":    int64(migrationID),
		"owner_user_id": int64(ownerUserID),
		"state":         int32(migrationv1.MigrationState_MIGRATION_STATE_GENERATING),
		"delete_time":   nil,
	})
	require.NoError(t, err, "seed GENERATING migration doc")

	pool := newTestCachePool(t)

	// Subscribe to the owner's channel BEFORE triggering the write.
	subConn := pool.Get()
	defer func() { _ = subConn.Close() }()
	psc := redis.PubSubConn{Conn: subConn}
	require.NoError(t, psc.Subscribe(event_bus.UserChannel(ownerUserID)))
	// Drain the subscribe confirmation.
	for {
		switch v := psc.Receive().(type) {
		case redis.Subscription:
			if v.Kind == "subscribe" {
				goto subscribed
			}
		case error:
			t.Fatalf("subscribe failed: %v", v)
		}
	}
subscribed:

	updater := adapters.NewMongoMigrationStateUpdater(db).
		WithEventPublisher(event_bus.NewPublisher(pool))

	// The terminal write the generation-worker performs.
	require.NoError(t, updater.MarkReady(context.Background(), migrationID))

	// The state write itself must have committed READY.
	var after struct {
		State int32 `bson:"state"`
	}
	require.NoError(t, coll.FindOne(context.Background(), bson.M{"identifier": int64(migrationID)}).Decode(&after))
	assert.Equal(t, int32(migrationv1.MigrationState_MIGRATION_STATE_READY), after.State)

	// And the real-time event must have been relayed on the owner channel.
	msgCh := make(chan []byte, 1)
	go func() {
		if m, ok := psc.Receive().(redis.Message); ok {
			msgCh <- m.Data
		}
	}()

	select {
	case payload := <-msgCh:
		var evt event_bus.Event
		require.NoError(t, json.Unmarshal(payload, &evt))
		assert.Equal(t, event_bus.EventTypeMigrationStateChanged, evt.Type, "frontend filters on this exact type")
		assert.Equal(t, "migration.state_changed", evt.Type)
		assert.Equal(t, migrationID, evt.MigrationID)
		assert.Equal(t, ownerUserID, evt.OwnerUserID)
		assert.Equal(t, "MIGRATION_STATE_READY", evt.State)
		assert.Equal(t, "MIGRATION_STATE_GENERATING", evt.PreviousState)
		assert.NotEmpty(t, evt.EventID)
		assert.NotEmpty(t, evt.OccurredAt)
	case <-time.After(5 * time.Second):
		t.Fatal("no migration.state_changed event received on owner channel within 5s")
	}
}

// TestStateUpdater_NoPublisher_NoCrash proves the best-effort/feature-flag
// behavior: with no publisher wired (nil [cache]) the state write still
// succeeds and nothing panics.
func TestStateUpdater_NoPublisher_NoCrash(t *testing.T) {
	const migrationID uint64 = 999000222

	db := newTestDB(t)
	coll := db.Collection("migrations")
	_, err := coll.InsertOne(context.Background(), bson.M{
		"identifier":    int64(migrationID),
		"owner_user_id": int64(10004),
		"state":         int32(migrationv1.MigrationState_MIGRATION_STATE_GENERATING),
		"delete_time":   nil,
	})
	require.NoError(t, err)

	updater := adapters.NewMongoMigrationStateUpdater(db) // no publisher
	require.NoError(t, updater.MarkFailed(context.Background(), migrationID))

	var after struct {
		State int32 `bson:"state"`
	}
	require.NoError(t, coll.FindOne(context.Background(), bson.M{"identifier": int64(migrationID)}).Decode(&after))
	assert.Equal(t, int32(migrationv1.MigrationState_MIGRATION_STATE_FAILED), after.State)
}
