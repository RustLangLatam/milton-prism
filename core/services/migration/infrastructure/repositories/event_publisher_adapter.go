package repositories

import (
	"context"

	"milton_prism/core/services/migration/ports"
	"milton_prism/core/shared/event_bus"
)

// migrationEventPublisherAdapter adapts the shared event_bus.Publisher to the
// migration ports.MigrationEventPublisher driven port, keeping the application
// layer free of any pub-sub/redigo dependency.
type migrationEventPublisherAdapter struct {
	publisher *event_bus.Publisher
}

var _ ports.MigrationEventPublisher = (*migrationEventPublisherAdapter)(nil)

// NewMigrationEventPublisherAdapter wraps an event_bus.Publisher.
func NewMigrationEventPublisherAdapter(publisher *event_bus.Publisher) ports.MigrationEventPublisher {
	return &migrationEventPublisherAdapter{publisher: publisher}
}

func (a *migrationEventPublisherAdapter) PublishMigrationStateChanged(ctx context.Context, migrationID, ownerUserID uint64, state, previousState string) error {
	evt := event_bus.NewMigrationStateChangedEvent(migrationID, ownerUserID, state, previousState)
	return a.publisher.PublishMigrationStateChanged(ctx, ownerUserID, evt)
}
