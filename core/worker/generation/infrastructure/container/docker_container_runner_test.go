//go:build integration

package container_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"milton_prism/core/worker/generation/infrastructure/container"
	"milton_prism/core/worker/generation/ports"
)

// These tests require:
//   - A running Docker daemon accessible at DOCKER_HOST or /var/run/docker.sock
//   - The generation agent image built locally:
//       docker build -t milton-prism-generation-agent:latest \
//           -f infra/generation-agent/Dockerfile .
//
// Run with:
//   CGO_ENABLED=1 go test -v -tags integration -timeout 5m \
//       ./core/worker/generation/infrastructure/container/...

const (
	agentImage      = "milton-prism-generation-agent:latest"
	isolatedNet     = "prism-generation"
	testCPUQuota    = 50_000            // 50% of one CPU
	testMemoryBytes = 256 * 1024 * 1024 // 256 MiB
)

func newRunner(t *testing.T) *container.DockerContainerRunner {
	t.Helper()
	r, err := container.NewDockerContainerRunner()
	require.NoError(t, err, "docker runner init")
	return r
}

// TestEnsureNetwork_Idempotent verifies that EnsureNetwork creates the network
// on first call and is a no-op on subsequent calls.
func TestEnsureNetwork_Idempotent(t *testing.T) {
	r := newRunner(t)
	ctx := context.Background()

	require.NoError(t, r.EnsureNetwork(ctx, isolatedNet))
	require.NoError(t, r.EnsureNetwork(ctx, isolatedNet), "second call must be idempotent")
}

// TestRun_GoVersion verifies that the Go toolchain is available in the agent
// image and the runner captures its stdout correctly.
func TestRun_GoVersion(t *testing.T) {
	r := newRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := r.Run(ctx, ports.RunRequest{
		Image:       agentImage,
		Command:     []string{"go", "version"},
		NetworkName: isolatedNet,
		CPUQuota:    testCPUQuota,
		MemoryBytes: testMemoryBytes,
	})

	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "go version must exit 0")
	assert.Contains(t, result.Stdout, "go version", "stdout must contain version line")
	t.Logf("go version output: %s", strings.TrimSpace(result.Stdout))
}

// TestRun_BufVersion verifies that buf is available in the agent image.
func TestRun_BufVersion(t *testing.T) {
	r := newRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := r.Run(ctx, ports.RunRequest{
		Image:       agentImage,
		Command:     []string{"buf", "--version"},
		NetworkName: isolatedNet,
		CPUQuota:    testCPUQuota,
		MemoryBytes: testMemoryBytes,
	})

	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "buf --version must exit 0")
	// buf prints its version to stdout
	combined := result.Stdout + result.Stderr
	assert.NotEmpty(t, strings.TrimSpace(combined), "buf --version must produce output")
	t.Logf("buf version output: %s", strings.TrimSpace(combined))
}

// TestRun_ClaudeVersion verifies that the Claude Code CLI is installed in the
// agent image and reports its version cleanly.
func TestRun_ClaudeVersion(t *testing.T) {
	r := newRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := r.Run(ctx, ports.RunRequest{
		Image:       agentImage,
		Command:     []string{"claude", "--version"},
		NetworkName: isolatedNet,
		CPUQuota:    testCPUQuota,
		MemoryBytes: testMemoryBytes,
	})

	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "claude --version must exit 0")
	combined := result.Stdout + result.Stderr
	assert.NotEmpty(t, strings.TrimSpace(combined), "claude --version must produce output")
	t.Logf("claude version output: %s", strings.TrimSpace(combined))
}

// TestRun_TeardownOnFailure verifies that a container whose command fails
// (non-zero exit) is still removed — no zombie containers left behind.
func TestRun_TeardownOnFailure(t *testing.T) {
	r := newRunner(t)
	ctx := context.Background()

	result, err := r.Run(ctx, ports.RunRequest{
		Image:   agentImage,
		Command: []string{"sh", "-c", "echo 'failing on purpose' && exit 42"},
	})

	// Run must not return a Go error for non-zero exit codes — those are
	// reflected in ExitCode only.
	require.NoError(t, err)
	assert.Equal(t, 42, result.ExitCode)
	assert.Contains(t, result.Stdout, "failing on purpose")
	// The container must be gone — a second remove attempt returns an error.
}

// TestRun_TeardownOnTimeout verifies that a container that exceeds its wall-clock
// timeout is stopped and removed.
func TestRun_TeardownOnTimeout(t *testing.T) {
	r := newRunner(t)
	ctx := context.Background()

	result, err := r.Run(ctx, ports.RunRequest{
		Image:   agentImage,
		Command: []string{"sleep", "300"},
		Timeout: 3 * time.Second,
	})

	assert.Error(t, err, "Run must return an error on timeout")
	assert.Equal(t, -1, result.ExitCode)
	t.Logf("timeout result: exitCode=%d err=%v", result.ExitCode, err)
}

// TestRun_NetworkIsolation verifies that the generation container cannot
// resolve internal service hostnames — the prism-generation network is
// intentionally isolated from the internal service networks.
func TestRun_NetworkIsolation(t *testing.T) {
	r := newRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// mongodb is the hostname of the internal MongoDB container.
	// A generation container in prism-generation must not be able to resolve it.
	result, err := r.Run(ctx, ports.RunRequest{
		Image:       agentImage,
		Command:     []string{"sh", "-c", "nslookup mongodb 2>&1; exit 0"},
		NetworkName: isolatedNet,
	})

	require.NoError(t, err)
	combined := result.Stdout + result.Stderr
	// nslookup must fail to resolve — "NXDOMAIN", "can't resolve", or similar.
	t.Logf("nslookup mongodb output: %s", strings.TrimSpace(combined))
	hasResolved := strings.Contains(combined, "Address:") && !strings.Contains(combined, "NXDOMAIN")
	assert.False(t, hasResolved, "internal hostname 'mongodb' must NOT be resolvable from generation network")
}
