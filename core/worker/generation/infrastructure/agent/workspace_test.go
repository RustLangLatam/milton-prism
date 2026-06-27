package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"milton_prism/core/worker/generation/infrastructure/agent"
)

// TestCopyMonorepo_ExcludesDirectories verifies that workspaceExcludes dirs
// (.git, .frontend, frontend, infra, bin) are never copied.
func TestCopyMonorepo_ExcludesDirectories(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := t.TempDir()

	// Directories that must be excluded.
	excludedDirs := []string{
		".git",
		".frontend/app/node_modules",
		"frontend/src",
		"infra/docker",
		"bin",
	}
	for _, d := range excludedDirs {
		require.NoError(t, os.MkdirAll(filepath.Join(src, d), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(src, d, "file.txt"), []byte("data"), 0644))
	}

	// Files and dirs that must be included.
	require.NoError(t, os.MkdirAll(filepath.Join(src, "core", "services"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "go.mod"), []byte("module test"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "core", "services", "domain.go"), []byte("package services"), 0644))

	require.NoError(t, agent.CopyMonorepo(src, dst))

	// Each top-level excluded dir must not appear in the workspace at all.
	assert.NoDirExists(t, filepath.Join(dst, ".git"))
	assert.NoDirExists(t, filepath.Join(dst, ".frontend"))
	assert.NoDirExists(t, filepath.Join(dst, "frontend"))
	assert.NoDirExists(t, filepath.Join(dst, "infra"))
	assert.NoDirExists(t, filepath.Join(dst, "bin"))

	// Included files must exist.
	assert.FileExists(t, filepath.Join(dst, "go.mod"))
	assert.FileExists(t, filepath.Join(dst, "core", "services", "domain.go"))
}

// TestCopyMonorepo_SizeCapExcludesLargeFiles verifies the universal 512 KiB cap:
// any file exceeding the threshold is skipped regardless of location or name,
// while files just under the threshold (and standard source files) are kept.
func TestCopyMonorepo_SizeCapExcludesLargeFiles(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := t.TempDir()

	// A large file inside a subdirectory — must not be copied.
	require.NoError(t, os.MkdirAll(filepath.Join(src, "core", "services", "articles"), 0755))
	bigContent := make([]byte, 513*1024) // 513 KiB > 512 KiB cap
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "core", "services", "articles", "compiled.so"),
		bigContent, 0644,
	))

	// A file right at the threshold (512 KiB exactly) — must be copied.
	exactContent := make([]byte, 512*1024)
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "core", "services", "articles", "boundary.go"),
		exactContent, 0644,
	))

	// A normal source file well under the cap — must be copied.
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "go.mod"),
		[]byte("module test\ngo 1.25\n"),
		0644,
	))

	require.NoError(t, agent.CopyMonorepo(src, dst))

	assert.NoFileExists(t, filepath.Join(dst, "core", "services", "articles", "compiled.so"),
		"file > 512 KiB must be excluded by size cap")
	assert.FileExists(t, filepath.Join(dst, "core", "services", "articles", "boundary.go"),
		"file == 512 KiB (at threshold) must be copied")
	assert.FileExists(t, filepath.Join(dst, "go.mod"),
		"normal source file must be copied")
}

// TestCopyMonorepo_ExcludesRootBinaries verifies that executable binaries and
// archive files at the monorepo root are not copied into the workspace.
func TestCopyMonorepo_ExcludesRootBinaries(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := t.TempDir()

	// Root-level executable (simulate compiled Go binary).
	require.NoError(t, os.WriteFile(filepath.Join(src, "analysis-worker"), []byte("ELF"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "generation-worker"), []byte("ELF"), 0755))

	// Root-level archives.
	require.NoError(t, os.WriteFile(filepath.Join(src, "backup.zip"), []byte("PK"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "backup.tar.bz"), []byte("BZ"), 0644))

	// Root-level text files that must be kept.
	require.NoError(t, os.WriteFile(filepath.Join(src, "go.mod"), []byte("module test"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "go.sum"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "Makefile"), []byte("all:"), 0644))

	// Executable inside a subdirectory — must be kept (only root-level excluded).
	require.NoError(t, os.MkdirAll(filepath.Join(src, "scripts"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "scripts", "build.sh"), []byte("#!/bin/sh"), 0755))

	require.NoError(t, agent.CopyMonorepo(src, dst))

	// Root-level binaries and archives must not exist.
	assert.NoFileExists(t, filepath.Join(dst, "analysis-worker"))
	assert.NoFileExists(t, filepath.Join(dst, "generation-worker"))
	assert.NoFileExists(t, filepath.Join(dst, "backup.zip"))
	assert.NoFileExists(t, filepath.Join(dst, "backup.tar.bz"))

	// Text files at root must be kept.
	assert.FileExists(t, filepath.Join(dst, "go.mod"))
	assert.FileExists(t, filepath.Join(dst, "go.sum"))
	assert.FileExists(t, filepath.Join(dst, "Makefile"))

	// Executable inside subdirectory must be kept.
	assert.FileExists(t, filepath.Join(dst, "scripts", "build.sh"))
}

// TestWriteCombinedPrompt_GoProfile asserts the assembled prompt for the Go
// profile references the Go profile doc and a Go build/test workflow.
func TestWriteCombinedPrompt_GoProfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p, err := agent.WriteCombinedPrompt(dir,
		"docs/prism/milton-prism-service-generator-prompt.md",
		"articles", "ART", "go", "grpc", "", "none", "", "", "spec", "proto")
	require.NoError(t, err)
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "complete Go microservice")
	assert.Contains(t, s, "milton-prism-go-profile.md")
	assert.Contains(t, s, "go build")
	assert.NotContains(t, s, "milton-prism-python-profile.md")
}

// TestWriteCombinedPrompt_PythonProfile asserts the assembled prompt for the
// python profile is parametrised: it references the Python profile doc, a
// Python tooling workflow, and the python-specific intro — not Go's. This
// guards the regression where the combined prompt hardcoded "Go microservice"
// and the Go profile doc regardless of the output profile.
func TestWriteCombinedPrompt_PythonProfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p, err := agent.WriteCombinedPrompt(dir,
		"docs/prism/milton-prism-service-generator-prompt-python.md",
		"articles", "ART", "python", "grpc", "", "none", "", "", "spec", "proto")
	require.NoError(t, err)
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "complete Python microservice")
	assert.Contains(t, s, "milton-prism-python-profile.md")
	assert.Contains(t, s, "pytest")
	assert.NotContains(t, s, "complete Go microservice")
	assert.NotContains(t, s, "milton-prism-go-profile.md")
}

// TestWriteCombinedPrompt_AuthJWT asserts the JWT auth section is injected with the
// idiomatic library per profile, the .env-driven / no-hardcode rule, and (for HS256)
// the symmetric-secret algorithm guidance.
func TestWriteCombinedPrompt_AuthJWT(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p, err := agent.WriteCombinedPrompt(dir,
		"docs/prism/milton-prism-service-generator-prompt-python.md",
		"articles", "ART", "python", "http", "", "jwt", "HS256", "", "spec", "proto")
	require.NoError(t, err)
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "## Auth / Validation: JWT")
	assert.Contains(t, s, "PyJWT")
	assert.Contains(t, s, "Authorization: Bearer")
	assert.Contains(t, s, "NEVER hardcode")
	assert.Contains(t, s, "SYMMETRIC secret (HS256)")
	assert.Contains(t, s, ".env.example")
}

// TestWriteCombinedPrompt_AuthNone asserts the none scheme emits an explicit
// "do not invent auth" note and no JWT library reference.
func TestWriteCombinedPrompt_AuthNone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p, err := agent.WriteCombinedPrompt(dir,
		"docs/prism/milton-prism-service-generator-prompt.md",
		"articles", "ART", "go", "grpc", "", "none", "", "", "spec", "proto")
	require.NoError(t, err)
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "## Auth / Validation: none")
	assert.Contains(t, s, "Do NOT invent an auth layer")
	assert.NotContains(t, s, "golang-jwt")
}

// TestWriteCombinedPrompt_AuthDetectedNotGenerated asserts a detected-but-not-v1
// scheme (oauth2) yields an honest "NOT generated" note and forbids guessing.
func TestWriteCombinedPrompt_AuthDetectedNotGenerated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p, err := agent.WriteCombinedPrompt(dir,
		"docs/prism/milton-prism-service-generator-prompt.md",
		"articles", "ART", "go", "grpc", "", "oauth2", "", "", "spec", "proto")
	require.NoError(t, err)
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	s := string(b)
	assert.Contains(t, s, "oauth2")
	assert.Contains(t, s, "NOT generated")
	assert.Contains(t, s, "MUST NOT guess")
}
