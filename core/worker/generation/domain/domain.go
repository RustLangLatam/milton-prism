// Package domain defines the generation worker's internal types.
package domain

// ServiceStatus tracks the lifecycle of one service through autonomous generation.
type ServiceStatus string

const (
	ServiceStatusPending    ServiceStatus = "pending"
	ServiceStatusGenerating ServiceStatus = "generating"
	ServiceStatusDone       ServiceStatus = "done"
	ServiceStatusFailed     ServiceStatus = "failed"
)

// FileArtifact is a generated file captured from the agent workspace before cleanup.
type FileArtifact struct {
	// Path is the workspace-relative path (e.g. "core/services/user/wire.go").
	Path string
	// Content is the raw file content at the time of capture.
	Content []byte
}

// ServiceGenerationRecord persists the outcome of one service's generation attempt.
type ServiceGenerationRecord struct {
	MigrationID              uint64
	ServiceName              string
	Status                   ServiceStatus
	GatesPassed              bool
	FailureReason            string
	TotalCostUSD             float64
	GeneratedFileCount       int
	InputTokens              int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	OutputTokens             int64
	// AgentRawResult is the final "result" text from Claude Code's JSON output.
	// Preserved for debugging and post-hoc inspection.
	AgentRawResult string
}

// JobPayload is the Asynq task payload for generation:run jobs.
type JobPayload struct {
	MigrationID uint64 `json:"migration_id"`
	// ServiceFilter is the optional allowlist of service names to generate.
	// Empty (default) generates all services in the package.
	ServiceFilter []string `json:"service_filter,omitempty"`
}
