package ports

import "context"

// GenerationJobEnqueuer dispatches a generation:run job when a migration
// enters the GENERATING state.
type GenerationJobEnqueuer interface {
	EnqueueGeneration(ctx context.Context, migrationID uint64, serviceFilter []string) error
}
