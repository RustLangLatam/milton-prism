package ports

import (
	"context"

	"milton_prism/core/services/migration/domain"
)

// ArtifactReader reads the design artifacts persisted by the decomposition engine
// for a given migration. It is the driven port for assembling the GenerationPackage.
type ArtifactReader interface {
	ReadArtifacts(ctx context.Context, migrationID uint64) ([]domain.ServiceArtifact, error)
}
