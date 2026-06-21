// Package container provides the Docker-backed ContainerRunner adapter for the
// generation worker. Each Run call creates one ephemeral container, executes
// the requested command, captures its output, and force-removes the container
// before returning — even on failure or context cancellation.
package container

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"milton_prism/core/worker/generation/ports"
	applog "milton_prism/pkg/log"
)

const (
	// generationLabel marks every container created by this runner so a startup
	// cleanup scan in B3 can identify and remove zombie containers from
	// prior worker crashes.
	generationLabel = "com.milton-prism.runner=generation"

	// removeGrace is the maximum time allowed for a force-remove on teardown.
	removeGrace = 30 * time.Second

	// stopGrace is the maximum time given to the container to stop cleanly
	// before the context cancellation path force-kills it.
	stopGrace = 10 * time.Second

	// logCollectTimeout is the timeout for post-exit log collection.
	// Uses a fresh context so a cancelled runCtx does not skip log capture.
	logCollectTimeout = 30 * time.Second
)

var _ ports.ContainerRunner = (*DockerContainerRunner)(nil)

// DockerContainerRunner implements ContainerRunner against the local Docker
// daemon. It connects via the DOCKER_HOST environment variable or the default
// Unix socket (/var/run/docker.sock).
type DockerContainerRunner struct {
	cli *client.Client
}

// NewDockerContainerRunner creates a runner connected to the local Docker daemon.
func NewDockerContainerRunner() (*DockerContainerRunner, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("container runner: docker client: %w", err)
	}
	return &DockerContainerRunner{cli: cli}, nil
}

// EnsureNetwork creates networkName as a bridge network if it does not exist.
//
// Network policy: the network is intentionally NOT connected to
// milton-prism-network or cache-network, so generation containers cannot
// resolve internal service hostnames (MongoDB, KeyDB, gRPC services). Egress
// to the internet is available via the host's NAT for the three endpoints
// the toolchain requires: api.anthropic.com (Claude Code), proxy.golang.org
// (Go modules), and buf.build (buf schema registry).
//
// Future hardening: add iptables-level egress rules to restrict outbound
// traffic to those three CIDRs only.
func (r *DockerContainerRunner) EnsureNetwork(ctx context.Context, networkName string) error {
	f := filters.NewArgs(filters.Arg("name", networkName))
	existing, err := r.cli.NetworkList(ctx, dockernetwork.ListOptions{Filters: f})
	if err != nil {
		return fmt.Errorf("container runner: network list: %w", err)
	}
	for _, n := range existing {
		if n.Name == networkName {
			return nil
		}
	}
	_, err = r.cli.NetworkCreate(ctx, networkName, dockernetwork.CreateOptions{
		Driver:   "bridge",
		Internal: false, // internet egress via host NAT
	})
	if err != nil {
		return fmt.Errorf("container runner: network create %q: %w", networkName, err)
	}
	applog.Infof("container runner: network %q created", networkName)
	return nil
}

// Run starts req.Image, waits for exit, captures stdout/stderr, removes the
// container, and returns. Teardown is unconditional — a deferred force-remove
// runs even if start fails, the context is cancelled, or the worker panics.
func (r *DockerContainerRunner) Run(ctx context.Context, req ports.RunRequest) (ports.RunResult, error) {
	runCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	id, err := r.create(runCtx, req)
	if err != nil {
		return ports.RunResult{}, err
	}

	// Unconditional teardown: runs even on panic, early return, or cancelled ctx.
	// Uses context.Background() so the remove is not blocked by a cancelled runCtx.
	defer func() {
		rmCtx, cancel := context.WithTimeout(context.Background(), removeGrace)
		defer cancel()
		if rmErr := r.cli.ContainerRemove(rmCtx, id, container.RemoveOptions{Force: true}); rmErr != nil {
			applog.Warningf("container runner: remove %s: %v", shortID(id), rmErr)
		} else {
			applog.Infof("container runner: removed %s", shortID(id))
		}
	}()

	if err := r.cli.ContainerStart(runCtx, id, container.StartOptions{}); err != nil {
		return ports.RunResult{}, fmt.Errorf("container runner: start %s: %w", shortID(id), err)
	}
	applog.Infof("container runner: started %s image=%s", shortID(id), req.Image)

	exitCode, waitErr := r.waitForExit(runCtx, id)

	// Collect logs with a fresh context — runCtx may already be cancelled on
	// timeout, but we still want to capture whatever the container produced.
	logCtx, logCancel := context.WithTimeout(context.Background(), logCollectTimeout)
	defer logCancel()
	stdout, stderr, logErr := r.collectLogs(logCtx, id)
	if logErr != nil {
		applog.Warningf("container runner: log collection %s: %v", shortID(id), logErr)
	}

	if waitErr != nil {
		return ports.RunResult{ExitCode: -1, Stdout: stdout, Stderr: stderr}, waitErr
	}
	return ports.RunResult{ExitCode: exitCode, Stdout: stdout, Stderr: stderr}, nil
}

func (r *DockerContainerRunner) create(ctx context.Context, req ports.RunRequest) (string, error) {
	mounts, err := parseMounts(req.BindMounts)
	if err != nil {
		return "", fmt.Errorf("container runner: mounts: %w", err)
	}

	cfg := &container.Config{
		Image:      req.Image,
		Cmd:        req.Command,
		WorkingDir: req.WorkDir,
		Env:        req.Env, // never logged — carries runtime secrets per A.7
		Labels:     map[string]string{generationLabel: "true"},
	}

	hostCfg := &container.HostConfig{
		Mounts:     mounts,
		AutoRemove: false, // explicit defer-remove so we can collect logs after exit
		Resources: container.Resources{
			CPUQuota:  req.CPUQuota,
			CPUPeriod: 100_000, // 100 ms scheduling period
			Memory:    req.MemoryBytes,
		},
	}

	var netCfg *dockernetwork.NetworkingConfig
	if req.NetworkName != "" {
		netCfg = &dockernetwork.NetworkingConfig{
			EndpointsConfig: map[string]*dockernetwork.EndpointSettings{
				req.NetworkName: {},
			},
		}
	}

	resp, err := r.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, "")
	if err != nil {
		return "", fmt.Errorf("container runner: create: %w", err)
	}
	return resp.ID, nil
}

func (r *DockerContainerRunner) waitForExit(ctx context.Context, id string) (int, error) {
	statusCh, errCh := r.cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)
	select {
	case status := <-statusCh:
		if status.Error != nil {
			return -1, fmt.Errorf("container runner: wait error in %s: %s", shortID(id), status.Error.Message)
		}
		applog.Infof("container runner: %s exited code=%d", shortID(id), status.StatusCode)
		return int(status.StatusCode), nil
	case err := <-errCh:
		return -1, fmt.Errorf("container runner: wait %s: %w", shortID(id), err)
	case <-ctx.Done():
		// Stop the container to unblock the wait channel, then return the ctx error.
		stopCtx, cancel := context.WithTimeout(context.Background(), stopGrace)
		defer cancel()
		_ = r.cli.ContainerStop(stopCtx, id, container.StopOptions{})
		return -1, fmt.Errorf("container runner: %s: %w", shortID(id), ctx.Err())
	}
}

func (r *DockerContainerRunner) collectLogs(ctx context.Context, id string) (stdout, stderr string, err error) {
	rc, err := r.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return "", "", err
	}
	defer rc.Close()

	var outBuf, errBuf bytes.Buffer
	if _, copyErr := stdcopy.StdCopy(&outBuf, &errBuf, rc); copyErr != nil && copyErr != io.EOF {
		return "", "", copyErr
	}
	return outBuf.String(), errBuf.String(), nil
}

// parseMounts converts "host:container[:ro]" strings to Docker mount specs.
func parseMounts(specs []string) ([]mount.Mount, error) {
	out := make([]mount.Mount, 0, len(specs))
	for _, spec := range specs {
		parts := strings.SplitN(spec, ":", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid bind mount %q: expected host:container[:ro]", spec)
		}
		m := mount.Mount{
			Type:   mount.TypeBind,
			Source: parts[0],
			Target: parts[1],
		}
		if len(parts) == 3 && parts[2] == "ro" {
			m.ReadOnly = true
		}
		out = append(out, m)
	}
	return out, nil
}

// shortID returns the first 12 characters of a container ID for log readability.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
