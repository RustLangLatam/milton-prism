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
	"os"
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

// DockerContainerRunner implements ContainerRunner against a Docker daemon.
// By default it targets the LOCAL daemon (DOCKER_HOST env or the
// /var/run/docker.sock Unix socket). When a RemoteConfig with a non-empty Host
// is supplied, it instead targets that REMOTE daemon over tcp:// (optionally
// with TLS), enabling ephemeral generation containers to be spawned on a remote
// Docker host (Camino B, pending B2).
type DockerContainerRunner struct {
	cli *client.Client
}

// RemoteConfig declares how to reach a remote Docker daemon. A zero value
// (empty Host) means "use the local daemon" — the historical default, kept for
// no-regression with the local generation pipeline.
//
// When Host is set it must be a daemon URL the Docker SDK understands natively:
//   - tcp://HOST:2375        (plaintext, no TLS)
//   - tcp://HOST:2376        (mutual TLS — requires TLSCA, TLSCert and TLSKey)
//
// ssh://USER@HOST is NOT yet supported here: it requires the
// github.com/docker/cli connection helper, which is a separate (heavy)
// dependency. See the package docs / last-verification for the sub-pendiente.
type RemoteConfig struct {
	// Host is the daemon URL, e.g. "tcp://10.0.0.5:2376". Empty → local daemon.
	Host string
	// TLSCA / TLSCert / TLSKey are filesystem paths to the CA cert, client cert
	// and client key. All three must be set together to enable mutual TLS. When
	// any is empty, the connection is made without TLS (plaintext tcp://).
	TLSCA   string
	TLSCert string
	TLSKey  string
}

// IsRemote reports whether this config points the runner at a remote daemon.
func (c RemoteConfig) IsRemote() bool { return c.Host != "" }

// useTLS reports whether a full TLS triple (CA + cert + key) was provided.
func (c RemoteConfig) useTLS() bool {
	return c.TLSCA != "" && c.TLSCert != "" && c.TLSKey != ""
}

// RemoteConfigFromEnv reads the remote-host configuration from the worker's
// environment. It returns a zero (local) RemoteConfig when PRISM_DOCKER_HOST is
// unset, so the default behaviour is unchanged.
//
// Environment variables:
//   - PRISM_DOCKER_HOST     daemon URL, e.g. tcp://10.0.0.5:2376 (empty → local)
//   - PRISM_DOCKER_TLS_CA   path to ca.pem
//   - PRISM_DOCKER_TLS_CERT path to cert.pem
//   - PRISM_DOCKER_TLS_KEY  path to key.pem
func RemoteConfigFromEnv() RemoteConfig {
	return RemoteConfig{
		Host:    os.Getenv("PRISM_DOCKER_HOST"),
		TLSCA:   os.Getenv("PRISM_DOCKER_TLS_CA"),
		TLSCert: os.Getenv("PRISM_DOCKER_TLS_CERT"),
		TLSKey:  os.Getenv("PRISM_DOCKER_TLS_KEY"),
	}
}

// clientOpts builds the ordered Docker SDK option list for a RemoteConfig.
// It is pure (no daemon connection) so the endpoint-selection logic is unit
// testable without a running daemon.
func (c RemoteConfig) clientOpts() ([]client.Opt, error) {
	// Always start from the environment so DOCKER_API_VERSION and any other
	// standard knobs keep working, then layer explicit overrides on top.
	opts := []client.Opt{
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	}

	if !c.IsRemote() {
		// Local daemon: env / default socket. No-regression path.
		return opts, nil
	}

	// Remote daemon: override the host explicitly so config beats DOCKER_HOST.
	opts = append(opts, client.WithHost(c.Host))

	// Partial TLS (some but not all of the triple) is a misconfiguration we
	// refuse loudly rather than silently downgrading to plaintext.
	someTLS := c.TLSCA != "" || c.TLSCert != "" || c.TLSKey != ""
	if someTLS && !c.useTLS() {
		return nil, fmt.Errorf(
			"container runner: remote TLS requires all of PRISM_DOCKER_TLS_CA/CERT/KEY (got ca=%t cert=%t key=%t)",
			c.TLSCA != "", c.TLSCert != "", c.TLSKey != "")
	}
	if c.useTLS() {
		opts = append(opts, client.WithTLSClientConfig(c.TLSCA, c.TLSCert, c.TLSKey))
	}
	return opts, nil
}

// NewDockerContainerRunner creates a runner connected to the local Docker daemon
// (DOCKER_HOST env or the default Unix socket). It is the no-regression default
// used by existing call sites.
func NewDockerContainerRunner() (*DockerContainerRunner, error) {
	return NewDockerContainerRunnerWithConfig(RemoteConfig{})
}

// NewDockerContainerRunnerWithConfig creates a runner targeting the daemon
// described by cfg: the local daemon when cfg is zero, or a remote daemon over
// tcp:// (optionally TLS) when cfg.Host is set.
func NewDockerContainerRunnerWithConfig(cfg RemoteConfig) (*DockerContainerRunner, error) {
	opts, err := cfg.clientOpts()
	if err != nil {
		return nil, err
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("container runner: docker client: %w", err)
	}
	if cfg.IsRemote() {
		applog.Infof("container runner: targeting REMOTE docker host=%s tls=%t", cfg.Host, cfg.useTLS())
	} else {
		applog.Infof("container runner: targeting LOCAL docker daemon host=%s", cli.DaemonHost())
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
