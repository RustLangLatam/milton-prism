package ports

import (
	"context"

	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

// GenerationResultReader reads per-service generation records stored by the
// autonomous generation worker in the generation_results collection.
type GenerationResultReader interface {
	ReadResults(ctx context.Context, migrationID uint64) ([]*migrationv1.ServiceGenerationRecord, error)
}
