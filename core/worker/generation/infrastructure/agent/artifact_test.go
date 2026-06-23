package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"milton_prism/core/worker/generation/infrastructure/agent"
)

// TestCaptureArtifacts_SizeCapDropsOversized confirms that files exceeding
// maxArtifactBytes (1 MiB) are dropped with a warning and never included in
// the returned slice — preventing them from reaching MongoDB's 16 MB limit.
func TestCaptureArtifacts_SizeCapDropsOversized(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Small Go source file — must be captured.
	smallContent := []byte("package main\n\nfunc main() {}\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), smallContent, 0644))

	// Oversized fake binary (1 MiB + 1 byte) — must be silently dropped.
	bigContent := make([]byte, (1<<20)+1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "analysis-worker"), bigContent, 0755))

	paths := []string{"main.go", "analysis-worker"}
	artifacts := agent.CaptureArtifacts(dir, paths)

	require.Len(t, artifacts, 1, "only the small file must be captured")
	assert.Equal(t, "main.go", artifacts[0].Path)
	assert.Equal(t, smallContent, artifacts[0].Content)
}

// TestCaptureArtifacts_AllSmallFiles confirms that when all files are within
// the size threshold, every one is captured.
func TestCaptureArtifacts_AllSmallFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	files := map[string][]byte{
		"domain.go":  []byte("package domain"),
		"service.go": []byte("package application"),
		"wire.go":    []byte("package user"),
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), content, 0644))
	}

	paths := []string{"domain.go", "service.go", "wire.go"}
	artifacts := agent.CaptureArtifacts(dir, paths)

	assert.Len(t, artifacts, 3)
}

// TestCaptureArtifacts_MissingFileSkipped confirms that a file listed in the
// diff but no longer readable (e.g., deleted by the agent) is skipped without
// aborting the capture of the remaining files.
func TestCaptureArtifacts_MissingFileSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "present.go"), []byte("package x"), 0644))
	// "missing.go" deliberately not created.

	artifacts := agent.CaptureArtifacts(dir, []string{"present.go", "missing.go"})

	require.Len(t, artifacts, 1)
	assert.Equal(t, "present.go", artifacts[0].Path)
}

// TestCaptureArtifacts_ExcludesNonSourceTrees confirms DEFECT 2: virtualenv,
// site-packages, __pycache__, *.pyc, node_modules and wheel/egg metadata are
// dropped, while real generated source under the same service is kept. This is
// the fix for the "profile generated 5759 artifacts" contamination.
func TestCaptureArtifacts_ExcludesNonSourceTrees(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	write := func(rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
		require.NoError(t, os.WriteFile(p, []byte("x = 1\n"), 0644))
	}

	keep := []string{
		"python/services/profile/main.py",
		"python/services/profile/domain/profile.py",
		"protobuf/proto/milton_prism/services/profile/v1/profile_service.proto",
		// Rust source must be captured (Cargo.toml is NOT a lockfile).
		"rust/services/user/src/main.rs",
		"rust/services/user/Cargo.toml",
	}
	drop := []string{
		".venv/lib/python3.12/site-packages/flask/app.py",
		"venv/bin/activate",
		"python/services/profile/__pycache__/main.cpython-312.pyc",
		"python/services/profile/domain/profile.pyc",
		"node_modules/left-pad/index.js",
		".mypy_cache/3.12/foo.json",
		"python/services/profile/flask-3.0.0.dist-info/METADATA",
		"some.egg-info/PKG-INFO",
		// Rust/cargo build output and lockfile must never be captured.
		"rust/target/debug/user",
		"rust/services/user/target/release/deps/user.rlib",
		"rust/Cargo.lock",
	}

	var paths []string
	for _, p := range keep {
		write(p)
		paths = append(paths, filepath.FromSlash(p))
	}
	for _, p := range drop {
		write(p)
		paths = append(paths, filepath.FromSlash(p))
	}

	artifacts := agent.CaptureArtifacts(dir, paths)

	require.Len(t, artifacts, len(keep), "only real generated source must be captured")
	got := map[string]bool{}
	for _, a := range artifacts {
		got[filepath.ToSlash(a.Path)] = true
	}
	for _, p := range keep {
		assert.True(t, got[p], "expected to keep %q", p)
	}
}

// TestCaptureArtifacts_ExcludesCargoHomeRegistry confirms DEFECT 4: when the
// Rust agent's `cargo build` materialises the cargo home (CARGO_HOME / .cargo)
// inside the workspace, the entire crate registry — index + every downloaded
// crate's full source tree (.cargo/registry/src/…/<crate>/…) plus package-cache
// locks — is dropped, while the real generated Rust source under rust/ is kept.
// mig38 persisted 8552 such registry files; this guard is the capture-time fix.
// A service legitimately NAMED "registry" (rust/services/registry/…) must still
// be kept — the .cargo segment, not a bare "registry" segment, is what's matched.
func TestCaptureArtifacts_ExcludesCargoHomeRegistry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	write := func(rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
		require.NoError(t, os.WriteFile(p, []byte("// x\n"), 0644))
	}

	keep := []string{
		"rust/services/app/src/main.rs",
		"rust/services/app/Cargo.toml",
		"rust/shared/src/config.rs",
		"protobuf/proto/milton_prism/services/app/v1/app_service.proto",
		// A service whose NAME is "registry" must not be dropped by mistake.
		"rust/services/registry/src/main.rs",
	}
	drop := []string{
		".cargo/.package-cache",
		".cargo/registry/CACHEDIR.TAG",
		".cargo/registry/index/index.crates.io-1949cf8c6b5b557f/.cache/3/h/hex",
		".cargo/registry/src/index.crates.io-1949cf8c6b5b557f/tokio-1.40.0/src/lib.rs",
		".cargo/registry/src/index.crates.io-1949cf8c6b5b557f/thiserror-2.0.18/build/probe.rs",
		".rustup/toolchains/stable/lib/rustlib/x.rs",
		"rust/services/app/target/debug/deps/app.rlib",
		"rust/services/app/target/debug/app.rmeta",
	}

	var paths []string
	for _, p := range keep {
		write(p)
		paths = append(paths, filepath.FromSlash(p))
	}
	for _, p := range drop {
		write(p)
		paths = append(paths, filepath.FromSlash(p))
	}

	artifacts := agent.CaptureArtifacts(dir, paths)

	require.Len(t, artifacts, len(keep), "only real generated source must be captured")
	got := map[string]bool{}
	for _, a := range artifacts {
		got[filepath.ToSlash(a.Path)] = true
	}
	for _, p := range keep {
		assert.True(t, got[p], "expected to keep %q", p)
	}
}

// TestCaptureArtifacts_DropsNonUTF8 confirms DEFECT 3 defence at the collector:
// a file with non-UTF-8 content is dropped so it can never be persisted and
// later break the gRPC marshal of the artifacts response.
func TestCaptureArtifacts_DropsNonUTF8(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644))
	// Invalid UTF-8 byte sequence (e.g. a small compiled blob that slipped in).
	require.NoError(t, os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0xff, 0xfe, 0x00, 0x80}, 0644))

	artifacts := agent.CaptureArtifacts(dir, []string{"main.go", "blob.bin"})

	require.Len(t, artifacts, 1)
	assert.Equal(t, "main.go", artifacts[0].Path)
}
