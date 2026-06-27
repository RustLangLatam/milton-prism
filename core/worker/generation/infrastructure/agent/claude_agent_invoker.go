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
	"strings"
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

	// Heavy-tier resource limits (GAP #6e). Profiles that compile native/JVM
	// artifacts inside the container (rust, java) are far heavier in RAM, CPU and
	// wall-clock than the interpreted/fast-linking defaults above: the default
	// 1 GiB / 50% CPU cannot finish a Tonic/Prost `cargo build` (rustc + LLVM
	// codegen) nor a JVM+Maven build with its large heap. The agent image
	// pre-warms each toolchain's dependency cache so most of the dependency
	// compile is already done. See heavyProfiles / resourceTierFor for the
	// profile→tier mapping; only the named heavy profiles get these limits.
	heavyAgentCPUQuota = 200_000        // up to 2 CPUs for parallel codegen
	heavyAgentMemory   = int64(4) << 30 // 4 GiB — headroom for rustc/LLVM / JVM peaks
	// Container lifetime covers the whole run (agent reasoning + every build/test
	// iteration). Heavy builds are slow even with a warm cache, so the heavy
	// container gets a longer budget than the 60-min default. Kept in lockstep
	// with perServiceBudget in the jobs package so the per-service task budget
	// never truncates a heavy container mid-build.
	heavyAgentTimeout = 90 * time.Minute

	// maxArtifactBytes is the upper size bound for a file to be captured as a
	// generation artifact. Generated Go source and proto files are always well
	// under this threshold. Any file that exceeds it (compiled binary, archive,
	// or any other non-source artefact that ends up in the diff by mistake) is
	// silently dropped with a warning so it never reaches the MongoDB 16 MB
	// per-document limit in UpsertArtifacts.
	maxArtifactBytes = 1 << 20 // 1 MiB
)

// resourceTier bundles the container resource limits applied to one class of
// output profiles. Named tiers keep the selector extensible: adding a profile
// to an existing tier is a one-line map entry (not a new if-branch), and a new
// tier (e.g. an "xheavy" for cpp LTO builds) is one more var + map.
type resourceTier struct {
	cpuQuota    int64
	memoryBytes int64
	timeout     time.Duration
}

var (
	// defaultResourceTier mirrors the invoker's configured defaults and is the
	// safe fallback for every unrecognised profile.
	defaultResourceTier = resourceTier{
		cpuQuota:    defaultAgentCPUQuota,
		memoryBytes: defaultAgentMemory,
		timeout:     defaultAgentTimeout,
	}
	// heavyResourceTier is for profiles that compile native/JVM artifacts.
	heavyResourceTier = resourceTier{
		cpuQuota:    heavyAgentCPUQuota,
		memoryBytes: heavyAgentMemory,
		timeout:     heavyAgentTimeout,
	}
)

// heavyProfiles are the output profiles that compile heavy native or JVM
// artifacts inside the container and therefore run on heavyResourceTier:
//   - rust: rustc + LLVM codegen of the Tonic/Prost crate graph.
//   - java: JVM + Maven build with a large heap and long wall-clock. The commit
//     that claimed 4 GiB for Java never wired it (audit #6e); this is that wiring,
//     bringing Java up to the same tier as rust.
//   - csharp: Roslyn + `dotnet build` (NuGet restore graph + analyzers + Grpc.Tools
//     protoc/grpc_csharp_plugin codegen) peaks well above 1 GiB.
//   - cpp: g++/CMake compile + link of the grpc++/mongocxx C++ object graph (and
//     protoc/grpc_cpp_plugin codegen) is memory- and CPU-heavy. The grpc++/protobuf/
//     mongocxx libraries are PREINSTALLED (find_package, not FetchContent), so only
//     the service's own code is compiled — but the C++ link step still peaks >1 GiB.
//
// Go/Python/Node/Ruby intentionally stay on the default tier: Go links fast,
// Python/Node do not compile native code, and Ruby's native-extension gems are
// pre-built into the warmed GEM_HOME — so the 1 GiB / 50% CPU / 60-min defaults
// are sufficient.
var heavyProfiles = map[string]bool{
	"rust":   true,
	"java":   true,
	"csharp": true,
	"cpp":    true,
}

// resourceTierFor selects the resource tier for an output profile and reports
// whether a non-default (heavy) tier applies. Unknown, empty, or odd-cased
// profiles fall back to the safe default tier.
func resourceTierFor(profile string) (resourceTier, bool) {
	if heavyProfiles[strings.ToLower(strings.TrimSpace(profile))] {
		return heavyResourceTier, true
	}
	return defaultResourceTier, false
}

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
		req.HTTPFramework,
		req.AuthScheme,
		req.AuthSignatureAlg,
		req.Store,
		req.BoundarySpec,
		req.ProtoContent,
		req.SourceToPort,
		req.PreviousVerifyStderr,
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

	// Capture the agent run's own failure signal up-front: it classifies transient
	// (rate-limit/infra) retries and is the fallback gate for profiles whose
	// deterministic verify is not wired yet.
	agentRaw := extractFailureReason(runResult.Stdout + runResult.Stderr)

	// DETERMINISTIC GATE (the spine): instead of trusting Claude's exit code, run a
	// SECOND pass — verifyCmd — in the SAME image/workspace and derive GatesPassed
	// from ITS exit. The generated service must actually COMPILE and its tests must
	// PASS. The verify stderr is captured so a retry can inject the exact failures.
	verifyCmd, verifyWired := verifyCommandFor(req.OutputProfile, req.Protocol, req.ServiceName)
	switch {
	case runErr != nil:
		// Container/infra failure for the agent run itself — verify is meaningless.
		// Transient by classification; let the pipeline retry.
		out.GatesPassed = false
		out.RawFailureReason = agentRaw
		out.FailureReason = SanitizeFailureReason(agentRaw)
		applog.Warningf("agent invoker: service=%s agent container error (raw, server-only): %s", req.ServiceName, agentRaw)
	case runResult.ExitCode != 0 && isRateLimited(agentRaw):
		// The agent itself was rate-limited/overloaded before producing a result;
		// running verify would just fail on missing files. Surface it as transient.
		out.GatesPassed = false
		out.RawFailureReason = agentRaw
		out.FailureReason = SanitizeFailureReason(agentRaw)
		applog.Warningf("agent invoker: service=%s agent rate-limited (raw, server-only): %s", req.ServiceName, agentRaw)
	case !verifyWired:
		// Deterministic gate not certified for this profile's layout yet: preserve
		// the prior behaviour (GatesPassed from the agent's own exit) so this change
		// never regresses an uncertified language.
		out.GatesPassed = runResult.ExitCode == 0
		if !out.Success {
			out.RawFailureReason = agentRaw
			out.FailureReason = SanitizeFailureReason(agentRaw)
		}
		applog.Infof("agent invoker: service=%s profile=%s deterministic verify NOT wired — gate from agent exit=%d",
			req.ServiceName, req.OutputProfile, runResult.ExitCode)
	default:
		// The real behavioural gate: compile + tests in the same container.
		verifyRes, verifyErr := a.runVerify(ctx, workspaceDir, req, verifyCmd)
		out.VerifyRan = true
		out.VerifyExitCode = verifyRes.ExitCode
		out.GatesPassed = verifyErr == nil && verifyRes.ExitCode == 0
		applog.Infof("agent invoker: service=%s deterministic verify ran cmd=%q exitCode=%d gatesPassed=%v err=%v",
			req.ServiceName, verifyCmd, verifyRes.ExitCode, out.GatesPassed, verifyErr)
		if !out.GatesPassed {
			tail := verifyFailureTail(verifyRes.Stdout, verifyRes.Stderr, verifyErr)
			out.VerifyStderr = tail
			out.RawFailureReason = tail
			out.FailureReason = SanitizeFailureReason("deterministic gate failed: " + tail)
			applog.Warningf("agent invoker: service=%s deterministic gate RED (server-only tail): %s", req.ServiceName, tail)
		}
	}

	applog.Infof("agent invoker: done service=%s agentExit=%d verifyRan=%v verifyExit=%d gatesPassed=%v cost=%.4f "+
		"tokens(in=%d cacheCreate=%d cacheRead=%d out=%d) generatedFiles=%d",
		req.ServiceName, out.ExitCode, out.VerifyRan, out.VerifyExitCode, out.GatesPassed, out.TotalCostUSD,
		out.InputTokens, out.CacheCreationInputTokens, out.CacheReadInputTokens,
		out.OutputTokens, len(generated))

	if runErr != nil {
		return out, fmt.Errorf("agent invoker: container: %w", runErr)
	}
	return out, nil
}

// rateLimitKeywords mirrors the pipeline's transient classification so the invoker
// can short-circuit the deterministic verify when the agent itself was throttled
// (running verify on a workspace with no generated files would only produce noise).
var rateLimitKeywords = []string{
	"rate limit", "rate_limit", "too many requests",
	"429", "overloaded", "server temporarily unavailable",
}

func isRateLimited(raw string) bool {
	lower := strings.ToLower(raw)
	for _, kw := range rateLimitKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// verifyFailureTail builds a bounded, log-only snippet from the verify command's
// output for retry feedback. go build/test write diagnostics to stderr (and tests
// to stdout); both are included, stderr last so the compiler/test errors are the
// freshest text the next prompt sees.
func verifyFailureTail(stdout, stderr string, verifyErr error) string {
	var b strings.Builder
	if s := strings.TrimSpace(stdout); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(stderr); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	}
	if verifyErr != nil {
		b.WriteString("verify runner error: ")
		b.WriteString(verifyErr.Error())
	}
	out := strings.TrimSpace(b.String())
	const max = 8000
	if len(out) > max {
		out = out[len(out)-max:]
	}
	if out == "" {
		out = "verify command exited non-zero with no captured output"
	}
	return out
}

// runVerify runs the deterministic gate command in a fresh container over the SAME
// workspace the agent just wrote (bind-mounted), with the same Go module cache and
// the heavy/default resource tier for the profile. No model credential and no
// ~/.claude mount: this pass only compiles and tests — it never calls the model.
func (a *ClaudeAgentInvoker) runVerify(ctx context.Context, workspaceDir string, req ports.InvokeRequest, verifyCmd string) (ports.RunResult, error) {
	mounts := []string{workspaceDir + ":/workspace:rw"}
	if a.goModCache != "" {
		mounts = append(mounts, a.goModCache+":/go/pkg/mod:ro")
	}
	cpuQuota, memoryBytes, timeout := a.cpuQuota, a.memoryBytes, a.timeout
	if tier, heavy := resourceTierFor(req.OutputProfile); heavy {
		cpuQuota, memoryBytes, timeout = tier.cpuQuota, tier.memoryBytes, tier.timeout
	}
	return a.runner.Run(ctx, ports.RunRequest{
		Image:       a.image,
		Command:     []string{"sh", "-c", verifyCmd},
		WorkDir:     "/workspace",
		BindMounts:  mounts,
		CPUQuota:    cpuQuota,
		MemoryBytes: memoryBytes,
		NetworkName: a.networkName,
		Timeout:     timeout,
	})
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

	// Profile-aware resource limits (#6e): heavy profiles (rust, java) need more
	// RAM/CPU/time for their native/JVM build inside the container; every other
	// profile keeps the invoker's configured defaults. See resourceTierFor.
	cpuQuota, memoryBytes, timeout := a.cpuQuota, a.memoryBytes, a.timeout
	if tier, heavy := resourceTierFor(req.OutputProfile); heavy {
		cpuQuota, memoryBytes, timeout = tier.cpuQuota, tier.memoryBytes, tier.timeout
		applog.Infof("agent invoker: service=%s profile=%s heavy resource tier (cpuQuota=%d memoryBytes=%d timeout=%s)",
			req.ServiceName, req.OutputProfile, cpuQuota, memoryBytes, timeout)
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
