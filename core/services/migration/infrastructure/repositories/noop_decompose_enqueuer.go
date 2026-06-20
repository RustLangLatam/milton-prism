package repositories

import (
	"context"

	"milton_prism/core/services/migration/ports"
)

var _ ports.DecomposeJobEnqueuer = (*NoOpDecomposeEnqueuer)(nil)

// NoOpDecomposeEnqueuer is used when no Redis/cache is configured.
type NoOpDecomposeEnqueuer struct{}

// NewNoOpDecomposeEnqueuer returns a no-op enqueuer.
func NewNoOpDecomposeEnqueuer() *NoOpDecomposeEnqueuer {
	return &NoOpDecomposeEnqueuer{}
}

func (*NoOpDecomposeEnqueuer) EnqueueDecompose(_ context.Context, _, _ uint64, _, _ string) error {
	return nil
}
