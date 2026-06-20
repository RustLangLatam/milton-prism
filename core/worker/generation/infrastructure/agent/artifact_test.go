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
