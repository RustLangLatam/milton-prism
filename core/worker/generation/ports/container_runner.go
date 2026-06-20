// Package ports defines the driven ports of the generation pipeline worker.
// All ports follow the Canon's dependency rule: application orchestrates them;
// adapters in the infrastructure layer implement them.
package ports

import (
	"context"
	"time"
)

// RunRequest configures a single ephemeral container execution.
type RunRequest struct {
	// Image is the container image reference (must be pre-built or pullable).
	Image string
	// Command overrides the image's default entrypoint+cmd.
	Command []string
	// WorkDir is the working directory inside the container.
	WorkDir string
	// BindMounts maps host paths to container paths in "host:container" or
	// "host:container:ro" notation.
	BindMounts []string
	// Env holds KEY=VALUE environment variables injected at runtime.
	// Callers MUST NOT log this slice — it carries runtime secrets (A.7).
	Env []string
	// CPUQuota in microseconds per CPUPeriod (100 000 µs default period).
	// 50 000 = 50% of one CPU. 0 = unlimited.
	CPUQuota int64
	// MemoryBytes is the hard memory limit. 0 = unlimited.
	MemoryBytes int64
	// NetworkName attaches the container to this Docker network.
	// Empty string uses Docker's default bridge.
	NetworkName string
	// Timeout is the wall-clock limit for the entire run.
	// 0 = rely on ctx cancellation only.
	Timeout time.Duration
}

// RunResult captures the outcome of a single container execution.
type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// ContainerRunner runs a command in an ephemeral container and guarantees
// teardown on completion, regardless of success or failure.
type ContainerRunner interface {
	// Run starts the container described by req, waits for exit (honouring
	// req.Timeout and ctx cancellation), captures stdout/stderr, removes the
	// container, and returns the result. The container is always removed before
	// Run returns — callers must not attempt manual teardown.
	Run(ctx context.Context, req RunRequest) (RunResult, error)

	// EnsureNetwork creates the named Docker bridge network if it does not
	// already exist. Idempotent; safe to call at worker startup.
	EnsureNetwork(ctx context.Context, networkName string) error
}
