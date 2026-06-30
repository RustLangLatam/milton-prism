package ports

import "context"

// MigrationEventPublisher publishes migration lifecycle events to the real-time
// event bus. It is a driven port: the application calls it best-effort after a
// committed state transition and must never fail the use case if publishing
// fails. A nil publisher (feature flag off) is a no-op.
type MigrationEventPublisher interface {
	// PublishMigrationStateChanged emits a migration.state_changed event for the
	// owner. state and previousState are the canonical state names.
	PublishMigrationStateChanged(ctx context.Context, migrationID, ownerUserID uint64, state, previousState string) error
}
