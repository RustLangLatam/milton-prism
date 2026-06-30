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

// FailureClass classifies WHY a service's generation failed so the panel can
// decide whether to offer (TRANSIENT) or de-emphasise (DESIGN) a retry. It is
// persisted as a short token on the generation_results record and mapped to the
// migration proto FailureClass enum in the migration read-path.
type FailureClass string

const (
	// FailureClassUnspecified — no classification (the service did not fail).
	FailureClassUnspecified FailureClass = ""
	// FailureClassTransient — rate-limit / overload / infrastructure / deadline.
	// A retry is likely to succeed once the throttle or outage clears.
	FailureClassTransient FailureClass = "transient"
	// FailureClassDesign — deterministic-gate red (did not compile / tests failed)
	// or a not-migrable / incomplete-contract service. A retry is de-emphasised.
	FailureClassDesign FailureClass = "design"
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
	// FailureClass classifies a failed service (transient vs design). Empty for
	// non-failed records. Computed via ClassifyFailure at persist time.
	FailureClass FailureClass
	TotalCostUSD             float64
	GeneratedFileCount       int
	InputTokens              int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	OutputTokens             int64
	// Model is the dominant model id reported by the agent for this run (e.g.
	// "claude-opus-4-8[1m]"). Empty when none was reported. Used downstream to
	// estimate cost by token when no real API cost is available.
	Model string
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
