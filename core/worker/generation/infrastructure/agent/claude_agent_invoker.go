// Package agent provides the Claude Code headless adapter for autonomous
// service generation (Camino B). It implements the AgentInvoker port by
// preparing an isolated workspace from the monorepo, injecting model
// credentials at runtime, and running Claude Code inside an ephemeral
// container via ContainerRunner.
package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"milton_prism/core/worker/generation/ports"
	applog "milton_prism/pkg/log"
)

const (
	generationAgentImage  = "milton-prism-generation-agent:latest"
	generationNetworkName = "prism-generation"
	defaultAgentCPUQuota  = 50_000         // 50% of one CPU
	defaultAgentMemory    = int64(1) << 30 // 1 GiB
	// A.4: extended to 60 min — subscription mode is slower than API-key mode
	// (no fast-mode, different rate limits). 30 min was insufficient for a
	// single service in subscription runs.
	defaultAgentTimeout = 60 * time.Minute

	// Rust (Tonic) profile resource limits. Compiling a Tonic/Prost service plus
	// its crate graph is far heavier in RAM and wall-clock than Go/Node/Python:
	// the default 1 GiB / 50% CPU cannot finish a `cargo build` (rustc + LLVM
	// codegen of tonic/prost/tokio/mongodb regularly peaks well above 1 GiB).
	// These limits apply only to the rust profile; every other profile keeps the
	// defaults above. The agent image pre-warms the Cargo registry + a compiled
	// dependency cache so the bulk of the crate compile is already done.
	rustAgentCPUQuota = 200_000        // up to 2 CPUs for parallel codegen
	rustAgentMemory   = int64(4) << 30 // 4 GiB — headroom for rustc/LLVM peaks
	// Container lifetime covers the whole run (agent reasoning + every cargo
	// build/test iteration). Rust builds are slow even with a warm cache, so the
	// rust container gets a longer budget than the 60-min default.
	rustAgentTimeout = 90 * time.Minute

	// maxArtifactBytes is the upper size bound for a file to be captured as a
	// generation artifact. Generated Go source and proto files are always well
	// under this threshold. Any file that exceeds it (compiled binary, archive,
	// or any other non-source artefact that ends up in the diff by mistake) is
	// silently dropped with a warning so it never reaches the MongoDB 16 MB
	// per-document limit in UpsertArtifacts.
	maxArtifactBytes = 1 << 20 // 1 MiB
)

var _ ports.AgentInvoker = (*ClaudeAgentInvoker)(nil)

// ClaudeAgentInvoker implements AgentInvoker by running Claude Code headless
// inside a container provisioned by the given ContainerRunner.
type ClaudeAgentInvoker struct {
	runner      ports.ContainerRunner
	image       string
	networkName string
	cpuQuota    int64
	memoryBytes int64
	timeout     time.Duration
	// goModCache is the host path to the Go module cache.
	// When set, it is mounted read-only at /go/pkg/mod inside the container
	// so the agent can compile without re-downloading all dependencies.
	// Defaults to empty (modules are downloaded fresh — slower but self-contained).
	goModCache string
	// workspaceTempDir is the base directory for ephemeral workspaces and
	// credential copies. Must be a host path visible to both this process and
	// the Docker daemon (critical when running inside a container via DooD).
	// Defaults to the OS temp dir when empty.
	workspaceTempDir string
}

// NewClaudeAgentInvoker creates an invoker backed by runner with the default
// resource limits from A.4.
func NewClaudeAgentInvoker(runner ports.ContainerRunner) *ClaudeAgentInvoker {
	return &ClaudeAgentInvoker{
		runner:      runner,
		image:       generationAgentImage,
		networkName: generationNetworkName,
		cpuQuota:    defaultAgentCPUQuota,
		memoryBytes: defaultAgentMemory,
		timeout:     defaultAgentTimeout,
	}
}

// WithGoModCache configures the invoker to mount the given host directory as
// the Go module cache inside the container (/go/pkg/mod), skipping downloads.
func (a *ClaudeAgentInvoker) WithGoModCache(hostPath string) *ClaudeAgentInvoker {
	a.goModCache = hostPath
	return a
}

// WithWorkspaceTempDir sets the base directory for ephemeral workspaces and
// credential temp dirs. Required when the worker itself runs inside Docker
// (Docker-out-of-Docker): the path must be on the host filesystem so the
// Docker daemon can bind-mount it into ephemeral containers.
func (a *ClaudeAgentInvoker) WithWorkspaceTempDir(dir string) *ClaudeAgentInvoker {
	a.workspaceTempDir = dir
	return a
}

// Invoke copies the monorepo at workspaceBase to a temp directory, removes
// pre-existing artifacts for req.ServiceName so the agent has a clean slate,
// writes the generation inputs, then runs Claude Code headless inside a
// container and returns the structured result.
//
// The API key is passed via container environment — never logged anywhere.
func (a *ClaudeAgentInvoker) Invoke(ctx context.Context, workspaceBase string, req ports.InvokeRequest) (ports.InvokeResult, error) {
	if req.APIKey == "" && req.SessionCredentialsDir == "" {
		return ports.InvokeResult{}, fmt.Errorf("agent invoker: set APIKey or SessionCredentialsDir")
	}
	if req.ServiceName == "" {
		return ports.InvokeResult{}, fmt.Errorf("agent invoker: ServiceName is required")
	}

	if err := a.runner.EnsureNetwork(ctx, a.networkName); err != nil {
		return ports.InvokeResult{}, fmt.Errorf("agent invoker: network: %w", err)
	}

	workspaceDir, cleanWorkspace, err := PrepareWorkspace(workspaceBase, req.ServiceName, a.workspaceTempDir)
	if err != nil {
		return ports.InvokeResult{}, fmt.Errorf("agent invoker: workspace: %w", err)
	}
	defer cleanWorkspace()

	promptRef := req.GeneratorPromptRef
	if promptRef == "" {
		promptRef = "docs/prism/milton-prism-service-generator-prompt.md"
	}
	if _, err := writeCombinedPrompt(
		workspaceDir,
		promptRef,
		req.ServiceName,
		req.ErrorPrefix,
		req.OutputProfile,
		req.Protocol,
		req.AuthScheme,
		req.AuthSignatureAlg,
		req.BoundarySpec,
		req.ProtoContent,
	); err != nil {
		return ports.InvokeResult{}, fmt.Errorf("agent invoker: write prompt: %w", err)
	}

	before, err := snapshotFiles(workspaceDir)
	if err != nil {
		return ports.InvokeResult{}, fmt.Errorf("agent invoker: snapshot: %w", err)
	}

	applog.Infof("agent invoker: starting service=%s workspace=%s", req.ServiceName, workspaceDir)

	runReq, credCleanup, err := a.buildRunRequest(workspaceDir, req)
	if err != nil {
		return ports.InvokeResult{}, fmt.Errorf("agent invoker: build run request: %w", err)
	}
	defer credCleanup()

	runResult, runErr := a.runner.Run(ctx, runReq)

	after, _ := snapshotFiles(workspaceDir)
	generated := diffFiles(before, after)

	// Capture file contents while the workspace still exists.
	// defer cleanWorkspace() fires after Invoke returns, so the workspace
	// is available here for reading. This is the only window to persist
	// generated file content before the ephemeral directory is removed.
	artifacts := captureArtifacts(workspaceDir, generated)

	out := ports.InvokeResult{
		ExitCode:       runResult.ExitCode,
		Success:        runResult.ExitCode == 0,
		GatesPassed:    runResult.ExitCode == 0,
		GeneratedFiles: generated,
		FileArtifacts:  artifacts,
	}

	parsed, _ := parseClaudeOutput(runResult.Stdout)
	if parsed != nil {
		out.RawResult = parsed.Result
		out.TotalCostUSD = parsed.TotalCostUSD
		out.InputTokens = parsed.Usage.InputTokens
		out.CacheCreationInputTokens = parsed.Usage.CacheCreationInputTokens
		out.CacheReadInputTokens = parsed.Usage.CacheReadInputTokens
		out.OutputTokens = parsed.Usage.OutputTokens
		out.Model = parsed.DominantModel()
	}

	if !out.Success {
		raw := extractFailureReason(runResult.Stdout + runResult.Stderr)
		out.RawFailureReason = raw
		out.FailureReason = SanitizeFailureReason(raw)
		// Log the full raw blob server-side for diagnosis; never expose it to the
		// user-visible FailureReason field.
		applog.Warningf("agent invoker: service=%s gate failure (raw, server-only): %s",
			req.ServiceName, raw)
	}

	applog.Infof("agent invoker: done service=%s exitCode=%d gatesPassed=%v cost=%.4f "+
		"tokens(in=%d cacheCreate=%d cacheRead=%d out=%d) generatedFiles=%d",
		req.ServiceName, out.ExitCode, out.GatesPassed, out.TotalCostUSD,
		out.InputTokens, out.CacheCreationInputTokens, out.CacheReadInputTokens,
		out.OutputTokens, len(generated))

	if runErr != nil {
		return out, fmt.Errorf("agent invoker: container: %w", runErr)
	}
	return out, nil
}

// buildRunRequest assembles the RunRequest for the given auth strategy.
//
// API key path (production): --bare mode, ANTHROPIC_API_KEY env var, no creds mount.
// Session credentials path (subscription): no --bare, the HOST ~/.claude directory
//
//	is bind-mounted read-write at /home/prism/.claude so Claude Code always sees the
//	live token state maintained by the host. Refreshed tokens written by the agent
//	are visible to subsequent containers through the shared directory.
//
// CONCURRENCY NOTE: the directory bind-mount is safe with cap=1. With cap>1
//
//	concurrent agents share the same ~/.claude directory; simultaneous token
//	refreshes can corrupt each other's in-flight credentials. Raise the cap only
//	after implementing an OAuth pre-refresh step in the worker.
//
// Returns the RunRequest, a noop cleanup func, and an error.
func (a *ClaudeAgentInvoker) buildRunRequest(workspaceDir string, req ports.InvokeRequest) (ports.RunRequest, func(), error) {
	noop := func() {}
	mounts := []string{workspaceDir + ":/workspace:rw"}
	var env []string
	var claudeCmd string

	// Profile-aware resource limits: the rust profile needs more RAM/CPU/time to
	// run `cargo build` inside the container (Tonic/Prost compile is heavy). All
	// other profiles keep the invoker's configured defaults.
	cpuQuota, memoryBytes, timeout := a.cpuQuota, a.memoryBytes, a.timeout
	if req.OutputProfile == "rust" {
		cpuQuota, memoryBytes, timeout = rustAgentCPUQuota, rustAgentMemory, rustAgentTimeout
	}

	if a.goModCache != "" {
		mounts = append(mounts, a.goModCache+":/go/pkg/mod:ro")
	}

	if req.APIKey != "" {
		// Production path: direct API key injected via env; --bare keeps the
		// container fully stateless (no ~/.claude/ reads).
		env = []string{credentialEnv(req.APIKey)} // never logged per A.7
		claudeCmd = "claude --bare --dangerously-skip-permissions --output-format json < /workspace/_prompt.md"
		return ports.RunRequest{
			Image:       a.image,
			Command:     []string{"sh", "-c", claudeCmd},
			WorkDir:     "/workspace",
			BindMounts:  mounts,
			Env:         env,
			CPUQuota:    cpuQuota,
			MemoryBytes: memoryBytes,
			NetworkName: a.networkName,
			Timeout:     timeout,
		}, noop, nil
	}

	// Subscription path: bind-mount the HOST ~/.claude directory read-write so
	// Claude Code inside the container authenticates with the live token state.
	// The host uid (slackve=1000) matches the container prism uid (1000), so no
	// chmod is needed — the prism user can read and write the directory as-is.
	// req.SessionCredentialsDir is the HOST path (e.g. /home/user/.claude) passed
	// directly to the Docker daemon for the bind-mount source.
	//
	// ~/.claude.json (sibling of the directory at the home level) is the primary
	// credentials file for recent Claude Code versions. It is mounted read-only:
	// the agent only needs to read the OAuth token, never to rotate it.
	//
	// We do NOT validate the source path with os.Stat here: this code runs inside
	// the generation-worker container, where the host home directory is not mounted.
	// The Docker daemon resolves the source path from the host filesystem at
	// container-create time and will return a clear error if the file is absent
	// ("invalid mount config for type 'bind': stat /path: no such file or directory").
	claudeJSON := filepath.Join(filepath.Dir(req.SessionCredentialsDir), ".claude.json")
	mounts = append(mounts, req.SessionCredentialsDir+":/home/prism/.claude:rw")
	mounts = append(mounts, claudeJSON+":/home/prism/.claude.json:ro")
	claudeCmd = "claude --dangerously-skip-permissions --output-format json < /workspace/_prompt.md"

	return ports.RunRequest{
		Image:       a.image,
		Command:     []string{"sh", "-c", claudeCmd},
		WorkDir:     "/workspace",
		BindMounts:  mounts,
		Env:         env,
		CPUQuota:    cpuQuota,
		MemoryBytes: memoryBytes,
		NetworkName: a.networkName,
		Timeout:     timeout,
	}, noop, nil
}
