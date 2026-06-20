package ports

import "context"

// StackDetector reads the technologies detected during analysis and returns
// the primary framework name and the full technology list. Used by
// ExportActionPlanPrompt to select the restructuring profile without
// re-running the analysis pipeline.
type StackDetector interface {
	Detect(ctx context.Context, analysisSummaryID uint64) (framework string, technologies []string, err error)
}
