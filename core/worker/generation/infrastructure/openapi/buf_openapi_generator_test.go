package openapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"milton_prism/core/worker/generation/ports"
)

// fakeRunner simulates the agent container. It records the run request and,
// when simulateBuf is set, mimics protoc-gen-openapi by writing a canned
// docs/openapi.yaml into the workspace bind-mount host path.
type fakeRunner struct {
	networks    []string
	gotReq      ports.RunRequest
	simulateBuf bool
	doc         string
	exitCode    int
}

func (f *fakeRunner) EnsureNetwork(_ context.Context, name string) error {
	f.networks = append(f.networks, name)
	return nil
}

func (f *fakeRunner) Run(_ context.Context, req ports.RunRequest) (ports.RunResult, error) {
	f.gotReq = req
	if f.simulateBuf && f.exitCode == 0 {
		// The first bind mount is "<hostWorkspace>:/workspace:rw".
		host := strings.SplitN(req.BindMounts[0], ":", 2)[0]
		out := filepath.Join(host, "docs", "openapi.yaml")
		if err := os.MkdirAll(filepath.Dir(out), 0o777); err != nil {
			return ports.RunResult{ExitCode: 1, Stderr: err.Error()}, nil
		}
		if err := os.WriteFile(out, []byte(f.doc), 0o644); err != nil {
			return ports.RunResult{ExitCode: 1, Stderr: err.Error()}, nil
		}
	}
	return ports.RunResult{ExitCode: f.exitCode}, nil
}

// TestGenerate_HappyPath proves the generator copies the base, overlays protos,
// runs buf from protobuf/ with the deliverable template, and returns the bytes
// of the produced docs/openapi.yaml.
func TestGenerate_HappyPath(t *testing.T) {
	base := t.TempDir()
	// Minimal monorepo base: a buf root and the deliverable template.
	require.NoError(t, os.MkdirAll(filepath.Join(base, "protobuf"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(base, "protobuf", "buf.yaml"), []byte("version: v2\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(base, "protobuf", deliverableTemplate), []byte("version: v2\n"), 0o644))

	const wantDoc = "openapi: 3.0.3\ninfo:\n  title: Deliverable\n"
	runner := &fakeRunner{simulateBuf: true, doc: wantDoc}
	gen := NewBufOpenAPIGenerator(runner)

	protos := []ports.ProtoArtifact{
		{Path: "protobuf/proto/milton_prism/services/articles/v1/articles_service.proto", Content: []byte("syntax = \"proto3\";\n")},
		{Path: "protobuf/proto/milton_prism/types/articles/v1/articles.proto", Content: []byte("syntax = \"proto3\";\n")},
	}

	doc, err := gen.Generate(context.Background(), base, protos)
	require.NoError(t, err)
	assert.Equal(t, wantDoc, string(doc))

	// buf must run from /workspace/protobuf with the deliverable template.
	require.Len(t, runner.gotReq.Command, 3)
	cmd := runner.gotReq.Command[2]
	assert.Contains(t, cmd, "cd /workspace/protobuf")
	assert.Contains(t, cmd, "buf generate proto --template "+deliverableTemplate)
	assert.Equal(t, []string{generationNetworkName}, runner.networks)

	// BUG 1: emission must be SCOPED to the migration's generated protos via
	// --path (relative to the buf root protobuf/), so the spec covers only the
	// generated service — not every proto in the tree.
	assert.Contains(t, cmd, "--path proto/milton_prism/services/articles/v1/articles_service.proto")
	assert.Contains(t, cmd, "--path proto/milton_prism/types/articles/v1/articles.proto")
}

// TestGenerate_ScopesToGeneratedProtos proves the buf command is scoped with one
// --path per generated proto (relative to protobuf/) and carries no --path for
// protos outside the migration's artifact set, so emission does not cover the
// whole proto tree (platform gateway API).
func TestGenerate_ScopesToGeneratedProtos(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "protobuf"), 0o755))

	runner := &fakeRunner{simulateBuf: true, doc: "openapi: 3.0.3\n"}
	gen := NewBufOpenAPIGenerator(runner)

	protos := []ports.ProtoArtifact{
		{Path: "protobuf/proto/milton_prism/services/user/v1/user_service.proto", Content: []byte("x")},
		{Path: "protobuf/proto/milton_prism/types/user/v1/user.proto", Content: []byte("x")},
		// Duplicate path must be deduped to a single --path.
		{Path: "protobuf/proto/milton_prism/types/user/v1/user.proto", Content: []byte("x")},
	}
	_, err := gen.Generate(context.Background(), base, protos)
	require.NoError(t, err)

	cmd := runner.gotReq.Command[2]
	// Exactly two --path flags (deduped), each rooted at proto/ not protobuf/.
	assert.Equal(t, 2, strings.Count(cmd, " --path "), "one --path per unique generated proto")
	assert.Contains(t, cmd, "--path proto/milton_prism/services/user/v1/user_service.proto")
	assert.Contains(t, cmd, "--path proto/milton_prism/types/user/v1/user.proto")
	// Must NOT pass the whole input dir un-scoped (only `proto` as the buf input).
	assert.NotContains(t, cmd, "--path protobuf/")
	// No path for an analysis/migration/etc service the migration did not generate.
	assert.NotContains(t, cmd, "analysis")
	assert.NotContains(t, cmd, "migration")
}

// TestGenerate_OverlaysProtos proves overlaid protos are written into the
// workspace copy (exercised indirectly: the fake runner cannot see them, so we
// re-run with a runner that inspects the copied tree).
func TestGenerate_OverlaysProtos(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "protobuf"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(base, "protobuf", "buf.yaml"), []byte("v2\n"), 0o644))

	var seenProtoContent string
	inspect := &inspectRunner{onRun: func(req ports.RunRequest) {
		host := strings.SplitN(req.BindMounts[0], ":", 2)[0]
		b, err := os.ReadFile(filepath.Join(host,
			"protobuf", "proto", "milton_prism", "services", "x", "v1", "x.proto"))
		if err == nil {
			seenProtoContent = string(b)
		}
		// Write the doc so Generate succeeds.
		out := filepath.Join(host, "docs", "openapi.yaml")
		_ = os.MkdirAll(filepath.Dir(out), 0o777)
		_ = os.WriteFile(out, []byte("openapi: 3.0.3\n"), 0o644)
	}}

	gen := NewBufOpenAPIGenerator(inspect)
	_, err := gen.Generate(context.Background(), base, []ports.ProtoArtifact{
		{Path: "protobuf/proto/milton_prism/services/x/v1/x.proto", Content: []byte("PROTO_BODY")},
	})
	require.NoError(t, err)
	assert.Equal(t, "PROTO_BODY", seenProtoContent, "generated proto must be overlaid into the workspace")
}

// TestGenerate_NoProtos rejects an empty proto set.
func TestGenerate_NoProtos(t *testing.T) {
	gen := NewBufOpenAPIGenerator(&fakeRunner{})
	_, err := gen.Generate(context.Background(), t.TempDir(), nil)
	require.Error(t, err)
}

// TestGenerate_BufFailure surfaces a non-zero buf exit as an error.
func TestGenerate_BufFailure(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "protobuf"), 0o755))
	runner := &fakeRunner{exitCode: 1}
	gen := NewBufOpenAPIGenerator(runner)
	_, err := gen.Generate(context.Background(), base, []ports.ProtoArtifact{
		{Path: "protobuf/proto/milton_prism/services/x/v1/x.proto", Content: []byte("x")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit=1")
}

// TestGenerate_RejectsUnsafePath blocks path traversal in artifact paths.
func TestGenerate_RejectsUnsafePath(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "protobuf"), 0o755))
	gen := NewBufOpenAPIGenerator(&fakeRunner{simulateBuf: true, doc: "x"})
	_, err := gen.Generate(context.Background(), base, []ports.ProtoArtifact{
		{Path: "../../etc/evil.proto", Content: []byte("x")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe artifact path")
}

// TestShouldExclude documents the workspace copy exclusions.
func TestShouldExclude(t *testing.T) {
	excluded := []string{
		".git", ".git/config", "frontend/app.js", "infra/x", "bin/worker", "node_modules/x",
		// DEFECT 1: the platform service-proto tree must be dropped so its
		// endpoints never enter the openapi FileDescriptorSet.
		"protobuf/proto/milton_prism/services",
		"protobuf/proto/milton_prism/services/migration/v1/migration_service.proto",
		"protobuf/proto/milton_prism/services/articles/v1/articles_service.proto",
		// DEFECT 1 real cause: the committed PLATFORM docs/openapi.yaml must be
		// dropped so buf writes a fresh deliverable-scoped spec.
		"docs/openapi.yaml",
	}
	kept := []string{
		".", "protobuf/buf.yaml", "core/x.go",
		// docs/ itself stays (buf needs it); only the stale spec file is dropped.
		"docs", "docs/README.md",
		// types/** stay: they define no services and are needed as imports.
		"protobuf/proto/milton_prism/types/articles/v1/articles.proto",
		"protobuf/proto/milton_prism/types/pagination/v1/pagination.proto",
	}
	for _, p := range excluded {
		assert.True(t, shouldExclude(filepath.FromSlash(p)), "should exclude %q", p)
	}
	for _, p := range kept {
		assert.False(t, shouldExclude(filepath.FromSlash(p)), "should keep %q", p)
	}
}

// inspectRunner runs a caller-provided hook on Run.
type inspectRunner struct {
	onRun func(ports.RunRequest)
}

func (r *inspectRunner) EnsureNetwork(_ context.Context, _ string) error { return nil }
func (r *inspectRunner) Run(_ context.Context, req ports.RunRequest) (ports.RunResult, error) {
	if r.onRun != nil {
		r.onRun(req)
	}
	return ports.RunResult{ExitCode: 0}, nil
}
