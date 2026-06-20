package repositories

import (
	"context"
	"fmt"

	"milton_prism/core/services/migration/ports"
	workeradapters "milton_prism/core/worker/decomposition/infrastructure/adapters"

	"go.mongodb.org/mongo-driver/mongo"
)

var _ ports.StackDetector = (*StackDetectorAdapter)(nil)

// StackDetectorAdapter reads the technologies stored in the analysis summary
// document (analysis_summaries.technologies_bytes) and returns the primary
// framework and full technology list. This is a single MongoDB read — no
// pipeline re-run.
type StackDetectorAdapter struct {
	loader *workeradapters.MongoGraphLoader
}

// NewStackDetectorAdapter constructs the adapter against the analysis database.
func NewStackDetectorAdapter(analysisDB *mongo.Database) *StackDetectorAdapter {
	return &StackDetectorAdapter{loader: workeradapters.NewMongoGraphLoader(analysisDB)}
}

// Detect loads the summary cards for the given analysisSummaryID and returns
// the primary framework (category=="framework") and all technology names.
// Returns empty strings/slice (no error) when the summary has no technology data.
func (a *StackDetectorAdapter) Detect(ctx context.Context, analysisSummaryID uint64) (string, []string, error) {
	cards, err := a.loader.LoadCards(ctx, analysisSummaryID)
	if err != nil {
		return "", nil, fmt.Errorf("stack detector: %w", err)
	}
	return cards.Framework, cards.Technologies, nil
}
