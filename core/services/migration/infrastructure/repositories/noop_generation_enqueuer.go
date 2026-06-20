package repositories

import (
	"context"

	"milton_prism/core/services/migration/ports"
)

var _ ports.GenerationJobEnqueuer = (*NoOpGenerationEnqueuer)(nil)

// NoOpGenerationEnqueuer is used when no Redis is configured.
type NoOpGenerationEnqueuer struct{}

// NewNoOpGenerationEnqueuer returns a no-op enqueuer.
func NewNoOpGenerationEnqueuer() *NoOpGenerationEnqueuer {
	return &NoOpGenerationEnqueuer{}
}

func (*NoOpGenerationEnqueuer) EnqueueGeneration(_ context.Context, _ uint64, _ []string) error {
	return nil
}
