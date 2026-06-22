// Unit tests for the local/remote Docker endpoint selector. These do NOT need a
// running Docker daemon: client.NewClientWithOpts only constructs the client and
// resolves the target host, it does not dial. We assert the resolved endpoint
// via the runner's DaemonHost() and the pure clientOpts logic.
package container

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DaemonHost exposes the resolved daemon endpoint of the underlying client for
// assertions. It is test-only (same package) and never used in production.
func (r *DockerContainerRunner) daemonHost() string { return r.cli.DaemonHost() }

func TestRemoteConfig_IsRemote(t *testing.T) {
	assert.False(t, RemoteConfig{}.IsRemote(), "zero config must be local")
	assert.True(t, RemoteConfig{Host: "tcp://10.0.0.5:2376"}.IsRemote())
}

// TestEndpointSelection_LocalDefault verifies the no-regression path: with no
// remote config, the runner targets the local daemon (unix socket or whatever
// DOCKER_HOST resolves to — never a tcp:// host we injected).
func TestEndpointSelection_LocalDefault(t *testing.T) {
	t.Setenv("PRISM_DOCKER_HOST", "")
	// Pin a deterministic local default so the assertion does not depend on a
	// developer's ambient DOCKER_HOST.
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")

	cfg := RemoteConfigFromEnv()
	require.False(t, cfg.IsRemote(), "empty PRISM_DOCKER_HOST must yield a local config")

	r, err := NewDockerContainerRunnerWithConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, "unix:///var/run/docker.sock", r.daemonHost(),
		"without remote config the runner must use the local daemon")
}

// TestEndpointSelection_RemotePlaintext verifies that a tcp:// PRISM_DOCKER_HOST
// points the runner at that remote host, and that it beats DOCKER_HOST.
func TestEndpointSelection_RemotePlaintext(t *testing.T) {
	// Ambient DOCKER_HOST is intentionally different to prove config wins.
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	t.Setenv("PRISM_DOCKER_HOST", "tcp://10.0.0.5:2375")

	cfg := RemoteConfigFromEnv()
	require.True(t, cfg.IsRemote())
	require.False(t, cfg.useTLS(), "no TLS triple → plaintext")

	r, err := NewDockerContainerRunnerWithConfig(cfg)
	require.NoError(t, err)
	assert.Equal(t, "tcp://10.0.0.5:2375", r.daemonHost(),
		"remote config must override DOCKER_HOST and target the remote daemon")
}

// TestEndpointSelection_RemoteTLS verifies that a full TLS triple is accepted
// and the runner targets the TLS port. We use bogus cert paths: client option
// construction loads them lazily, so a non-existent path surfaces only on dial,
// not on construction — which keeps this test daemon-free. We therefore assert
// via the pure clientOpts path that TLS is selected without error wiring.
func TestEndpointSelection_RemoteTLS(t *testing.T) {
	ca, cert, key := writeFakeTLS(t)
	cfg := RemoteConfig{
		Host:    "tcp://10.0.0.5:2376",
		TLSCA:   ca,
		TLSCert: cert,
		TLSKey:  key,
	}
	require.True(t, cfg.IsRemote())
	require.True(t, cfg.useTLS())

	opts, err := cfg.clientOpts()
	require.NoError(t, err, "valid TLS triple must build options")
	// FromEnv + APIVersionNegotiation + WithHost + WithTLSClientConfig = 4 opts.
	assert.Len(t, opts, 4, "remote+TLS must add WithHost and WithTLSClientConfig")
}

// TestEndpointSelection_PartialTLSRejected verifies that supplying some but not
// all of the TLS triple is rejected rather than silently downgraded.
func TestEndpointSelection_PartialTLSRejected(t *testing.T) {
	cfg := RemoteConfig{
		Host:  "tcp://10.0.0.5:2376",
		TLSCA: "/tmp/ca.pem", // cert + key missing
	}
	_, err := cfg.clientOpts()
	require.Error(t, err, "partial TLS config must be rejected")
	assert.Contains(t, err.Error(), "TLS requires all of")
}

// TestClientOpts_LocalNoOverride verifies the local path adds no host/TLS opts.
func TestClientOpts_LocalNoOverride(t *testing.T) {
	opts, err := RemoteConfig{}.clientOpts()
	require.NoError(t, err)
	// FromEnv + APIVersionNegotiation only.
	assert.Len(t, opts, 2, "local config must not add WithHost/WithTLS opts")
}

// writeFakeTLS creates three placeholder files and returns their paths. The
// Docker SDK does not read them at construction time, so their content is
// irrelevant for the selector test.
func writeFakeTLS(t *testing.T) (ca, cert, key string) {
	t.Helper()
	dir := t.TempDir()
	ca = dir + "/ca.pem"
	cert = dir + "/cert.pem"
	key = dir + "/key.pem"
	for _, p := range []string{ca, cert, key} {
		require.NoError(t, os.WriteFile(p, []byte("placeholder"), 0o600))
	}
	return ca, cert, key
}
