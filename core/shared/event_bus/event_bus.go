// Package event_bus provides a thin best-effort publisher for real-time
// platform events over a KeyDB/Redis pub-sub channel. It is the single source
// of truth for the event envelope and the per-user channel naming, shared by
// the emitters (migration / generation services) and the gateway SSE
// subscriber so both sides stay byte-compatible.
package event_bus

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/google/uuid"
)

const (
	// EventTypeMigrationStateChanged is emitted whenever a migration's
	// lifecycle state transitions.
	EventTypeMigrationStateChanged = "migration.state_changed"

	// channelPrefix namespaces per-user pub-sub channels. The owner id is
	// appended verbatim (e.g. "milton:events:user:10004").
	channelPrefix = "milton:events:user:"
)

// UserChannel returns the pub-sub channel a given user's events are published
// to. This is the single source of truth shared by the emitter and the gateway
// SSE subscriber — never derive the channel from a request parameter on the
// subscriber side (cross-user leak guard).
func UserChannel(ownerUserID uint64) string {
	return fmt.Sprintf("%s%d", channelPrefix, ownerUserID)
}

// Event is the JSON envelope published on a user's channel. The field set is
// intentionally flat and stable; subscribers (and the frontend) key off `type`.
type Event struct {
	EventID       string `json:"event_id"`
	Type          string `json:"type"`
	MigrationID   uint64 `json:"migration_id"`
	OwnerUserID   uint64 `json:"owner_user_id"`
	State         string `json:"state"`
	PreviousState string `json:"previous_state"`
	OccurredAt    string `json:"occurred_at"`
}

// NewMigrationStateChangedEvent builds a migration.state_changed envelope with
// a fresh unique id and an occurred_at timestamp.
func NewMigrationStateChangedEvent(migrationID, ownerUserID uint64, state, previousState string) Event {
	return Event{
		EventID:       uuid.NewString(),
		Type:          EventTypeMigrationStateChanged,
		MigrationID:   migrationID,
		OwnerUserID:   ownerUserID,
		State:         state,
		PreviousState: previousState,
		OccurredAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// Publisher publishes events to KeyDB/Redis pub-sub channels over a redigo
// connection pool. It reuses the shared cache_client pool semantics.
type Publisher struct {
	pool *redis.Pool
}

// NewPublisher wraps an existing redigo pool. The pool is owned by the caller.
func NewPublisher(pool *redis.Pool) *Publisher {
	return &Publisher{pool: pool}
}

// PublishMigrationStateChanged marshals evt and PUBLISHes it to the owner's
// channel. It is best-effort by contract: callers log-and-continue on error and
// must never fail an RPC/transition because publishing failed.
func (p *Publisher) PublishMigrationStateChanged(ctx context.Context, ownerUserID uint64, evt Event) error {
	if p == nil || p.pool == nil {
		return nil
	}

	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("event_bus: marshal event: %w", err)
	}

	conn := p.pool.Get()
	defer func() { _ = conn.Close() }()

	if _, err := redis.DoContext(conn, ctx, "PUBLISH", UserChannel(ownerUserID), payload); err != nil {
		return fmt.Errorf("event_bus: publish channel=%s: %w", UserChannel(ownerUserID), err)
	}
	return nil
}
