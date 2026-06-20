package ports

import (
	"context"

	workerdomain "milton_prism/core/worker/generation/domain"
)

// InvokeRequest mirrors ServiceGenerationSpec from the migration proto types,
// augmented with the model credential needed at runtime.
type InvokeRequest struct {
	// ServiceName is the snake_case service name (e.g. "articles").
	ServiceName string
	// ErrorPrefix is the registry-assigned error prefix (e.g. "ART").
	ErrorPrefix string
	// ProtoContent is the raw .proto file content (types + service combined).
	ProtoContent string
	// BoundarySpec is the YAML boundary specification for the generator prompt.
	BoundarySpec string
	// GeneratorPromptRef is the workspace-relative path to the generator prompt
	// (e.g. "docs/prism/milton-prism-service-generator-prompt.md").
	GeneratorPromptRef string
	// OutputProfile identifies the target stack (e.g. "go").
	OutputProfile string
	// APIKey is ANTHROPIC_API_KEY for production use (sk-ant-api03-…).
	// Passed to the container as an env var with --bare mode.
	// Callers MUST NOT log this field — it carries a runtime secret (A.7).
	// Set exactly one of APIKey or SessionCredentialsFile.
	APIKey string

	// SessionCredentialsDir is the HOST-side path to the ~/.claude directory
	// (e.g. /home/user/.claude). Used in development when no direct API key is
	// available — the directory is bind-mounted read-write into the agent
	// container at /home/prism/.claude so Claude Code can authenticate via the
	// live session and write back any refreshed OAuth tokens.
	//
	// CONCURRENCY NOTE: safe with cap=1. With cap>1 concurrent agents share the
	// same .claude/ directory; simultaneous token refreshes can corrupt each
	// other. The robust fix for cap>1 is an OAuth refresh step in the worker
	// before each invocation — deferred for now.
	//
	// Set exactly one of APIKey or SessionCredentialsDir.
	SessionCredentialsDir string
}

// InvokeResult captures the outcome of a single agent invocation.
type InvokeResult struct {
	// Success is true when the agent exited with code 0.
	Success bool
	// ExitCode from claude --bare.
	ExitCode int
	// RawResult is the "result" field from Claude Code's JSON output.
	RawResult string
	// GatesPassed is true when the self-verification loop inside the agent
	// confirmed all gates green (buf lint, go build, go vet, go test).
	// Derived from ExitCode == 0 per the generator prompt contract.
	GatesPassed bool
	// GeneratedFiles is the list of file paths created or modified in the
	// workspace during the agent run (relative to the workspace root).
	GeneratedFiles []string
	// FileArtifacts contains the content of all files in GeneratedFiles,
	// captured from the workspace before cleanup. Empty on failure.
	FileArtifacts []workerdomain.FileArtifact
	// TotalCostUSD is the server-computed cost from Claude Code's JSON output.
	// Includes all token tiers (fresh input, cache creation, cache read, output).
	// Reliable for pricing and monitoring.
	TotalCostUSD float64
	// InputTokens is the count of non-cached input tokens in this invocation.
	// This is typically small when prompt caching is active. For total token
	// volume, sum InputTokens + CacheCreationInputTokens + CacheReadInputTokens.
	InputTokens int64
	// CacheCreationInputTokens is the count of tokens written to a new cache
	// entry. Billed at a higher rate than cache reads.
	CacheCreationInputTokens int64
	// CacheReadInputTokens is the count of tokens served from an existing cache
	// entry. Billed at a significantly lower rate than fresh input.
	CacheReadInputTokens int64
	// OutputTokens produced in this invocation.
	OutputTokens int64
	// FailureReason is set when GatesPassed is false and contains a
	// best-effort summary of what went wrong, extracted from the agent output.
	FailureReason string
}

// AgentInvoker runs Claude Code headless inside an ephemeral container to
// generate one microservice from a ServiceGenerationSpec.
type AgentInvoker interface {
	// Invoke prepares an isolated workspace from workspaceBase (the monorepo
	// root on the host), runs Claude Code headless inside a container, and
	// returns the generation result. The workspace copy is always cleaned up
	// before Invoke returns.
	Invoke(ctx context.Context, workspaceBase string, req InvokeRequest) (InvokeResult, error)
}
