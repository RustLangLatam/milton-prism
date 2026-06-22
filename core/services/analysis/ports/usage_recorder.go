package ports

import "context"

// UsageSpend describes a single LLM spend event to be accounted. It is the
// analysis service's transport-agnostic view of a usage record; the billing
// layer maps it to a persisted UsageRecord.
type UsageSpend struct {
	UserID      uint64
	AnalysisID  uint64
	MigrationID uint64
	Operation   string // "assessment" | "analysis" | "migration" | "generation"
	TokensIn    int64
	TokensOut   int64
	CostUSD     float64
	Model       string
}

// UsageRecorder persists LLM spend events. Implementations must be best-effort:
// a recording failure must never break the originating flow — it is logged and
// swallowed by the caller.
type UsageRecorder interface {
	RecordSpend(ctx context.Context, spend UsageSpend) error
}
