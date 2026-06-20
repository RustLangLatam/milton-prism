package application_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"milton_prism/core/worker/generation/application"
	workerdomain "milton_prism/core/worker/generation/domain"
	"milton_prism/core/worker/generation/ports"
)

// ── extractErrorVarNames ─────────────────────────────────────────────────────

func TestExtractErrorVarNames_SingleVar(t *testing.T) {
	t.Parallel()
	content := `package message_error

var articlesErrorMessages = map[string]string{
	"ART101": "Article not found.",
}
`
	got := application.ExtractErrorVarNames(content)
	assert.Equal(t, []string{"articlesErrorMessages"}, got)
}

func TestExtractErrorVarNames_MultipleVars(t *testing.T) {
	t.Parallel()
	content := `package message_error

var dbErrorMessages = map[string]string{"DB001": "db error"}

var commonErrorMessages = map[string]string{"COM001": "common error"}
`
	got := application.ExtractErrorVarNames(content)
	assert.ElementsMatch(t, []string{"dbErrorMessages", "commonErrorMessages"}, got)
}

func TestExtractErrorVarNames_NoVars(t *testing.T) {
	t.Parallel()
	got := application.ExtractErrorVarNames("package message_error\n// no vars here\n")
	assert.Empty(t, got)
}

// ── buildMessageErrorGo ──────────────────────────────────────────────────────

func TestBuildMessageErrorGo_ContainsAllVars(t *testing.T) {
	t.Parallel()
	varNames := map[string]struct{}{
		"authErrorMessages":       {},
		"articlesErrorMessages":   {},
		"profileErrorMessages":    {},
		"migrationErrorMessages":  {},
	}
	got := application.BuildMessageErrorGo(varNames)

	assert.Contains(t, got, "package message_error")
	assert.Contains(t, got, "func lookupErrorMessage(code string) (string, bool)")
	assert.Contains(t, got, "authErrorMessages,")
	assert.Contains(t, got, "articlesErrorMessages,")
	assert.Contains(t, got, "profileErrorMessages,")
	assert.Contains(t, got, "migrationErrorMessages,")
	assert.Contains(t, got, "func HandlerErrorMessage(")
	assert.Contains(t, got, "func formatErrorMessage(")
	assert.Contains(t, got, "func containsInternalUppercase(")
}

func TestBuildMessageErrorGo_SortedDeterministic(t *testing.T) {
	t.Parallel()
	varNames := map[string]struct{}{
		"zServiceErrorMessages": {},
		"aServiceErrorMessages": {},
		"mServiceErrorMessages": {},
	}
	got := application.BuildMessageErrorGo(varNames)

	// All three vars appear and are sorted alphabetically.
	idxA := strings.Index(got, "aServiceErrorMessages,")
	idxM := strings.Index(got, "mServiceErrorMessages,")
	idxZ := strings.Index(got, "zServiceErrorMessages,")
	assert.True(t, idxA < idxM && idxM < idxZ, "entries must be sorted alphabetically")
}

func TestBuildMessageErrorGo_EmptyVarNames(t *testing.T) {
	t.Parallel()
	got := application.BuildMessageErrorGo(map[string]struct{}{})

	// Static content still present; lookup slice is empty.
	assert.Contains(t, got, "package message_error")
	assert.Contains(t, got, "func lookupErrorMessage(code string) (string, bool)")
	assert.Contains(t, got, "maps := []map[string]string{")
	// The slice body should have only the closing brace — no variable entries.
	assert.NotContains(t, got, "ErrorMessages,")
}

// ── scanExistingErrorVarNames ────────────────────────────────────────────────

func TestScanExistingErrorVarNames_ReadsFilesFromDisk(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	errorDir := filepath.Join(dir, "pkg", "gateway", "common", "error")
	require.NoError(t, os.MkdirAll(errorDir, 0755))

	writeErrorFile(t, errorDir, "auth_errors.go", "package message_error\nvar authErrorMessages = map[string]string{}\n")
	writeErrorFile(t, errorDir, "identity_errors.go", "package message_error\nvar identityErrorMessages = map[string]string{}\n")
	// message_error.go itself must be skipped.
	writeErrorFile(t, errorDir, "message_error.go", "package message_error\nvar shouldBeSkipped = map[string]string{}\n")

	got := application.ScanExistingErrorVarNames(dir)

	assert.Contains(t, got, "authErrorMessages")
	assert.Contains(t, got, "identityErrorMessages")
	assert.NotContains(t, got, "shouldBeSkipped", "message_error.go must not be scanned")
}

func TestScanExistingErrorVarNames_MissingDirReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := application.ScanExistingErrorVarNames("/nonexistent/path/that/does/not/exist")
	assert.Empty(t, got)
}

// ── assembleErrorAggregator (end-to-end via Pipeline.Run) ───────────────────

// TestAssembleErrorAggregator_NoMIG211 simulates a two-service migration where
// each agent generates its own *_errors.go artifact (but NOT message_error.go,
// as required by the updated prompt). It verifies that the pipeline:
//   - Produces exactly one message_error.go under the "__pipeline__" service.
//   - The file contains all service variable names.
//   - No other service artifact claims message_error.go (no MIG211 conflict).
func TestAssembleErrorAggregator_NoMIG211(t *testing.T) {
	t.Parallel()

	// Monorepo root with platform error files already on disk.
	monorepoRoot := t.TempDir()
	errorDir := filepath.Join(monorepoRoot, "pkg", "gateway", "common", "error")
	require.NoError(t, os.MkdirAll(errorDir, 0755))
	writeErrorFile(t, errorDir, "auth_errors.go",    "package message_error\nvar authErrorMessages = map[string]string{}\n")
	writeErrorFile(t, errorDir, "common_errors.go",  "package message_error\nvar dbErrorMessages = map[string]string{}\nvar commonErrorMessages = map[string]string{}\n")
	writeErrorFile(t, errorDir, "identity_errors.go","package message_error\nvar identityErrorMessages = map[string]string{}\n")

	// Two services — pre-populated as done so the invoker is never called.
	// Their artifacts include *_errors.go only (no message_error.go).
	articlesArtifacts := []workerdomain.FileArtifact{
		{
			Path:    "core/services/articles/domain/domain.go",
			Content: []byte("package domain\n"),
		},
		{
			Path:    "pkg/gateway/common/error/articles_errors.go",
			Content: []byte("package message_error\nvar articlesErrorMessages = map[string]string{\"ART101\": \"article not found.\"}\n"),
		},
	}
	profileArtifacts := []workerdomain.FileArtifact{
		{
			Path:    "core/services/profile/domain/domain.go",
			Content: []byte("package domain\n"),
		},
		{
			Path:    "pkg/gateway/common/error/profile_errors.go",
			Content: []byte("package message_error\nvar profileErrorMessages = map[string]string{\"PRF101\": \"profile not found.\"}\n"),
		},
	}

	store := newMockStore(
		workerdomain.ServiceGenerationRecord{MigrationID: 99, ServiceName: "articles", Status: workerdomain.ServiceStatusDone, GatesPassed: true},
		workerdomain.ServiceGenerationRecord{MigrationID: 99, ServiceName: "profile", Status: workerdomain.ServiceStatusDone, GatesPassed: true},
	)
	ctx := context.Background()
	require.NoError(t, store.UpsertArtifacts(ctx, 99, "articles", articlesArtifacts))
	require.NoError(t, store.UpsertArtifacts(ctx, 99, "profile", profileArtifacts))

	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{
		{Name: "articles", ErrorPrefix: "ART"},
		{Name: "profile", ErrorPrefix: "PRF"},
	}}
	inv := &mockInvoker{} // never called — all services already done

	pipeline := application.NewPipeline(reader, store, updater, inv, monorepoRoot)
	err := pipeline.Run(ctx, workerdomain.JobPayload{MigrationID: 99})
	require.NoError(t, err)

	// The pipeline must have produced exactly one message_error.go artifact.
	pipelineArtifacts, err := store.ListArtifacts(ctx, 99, "__pipeline__")
	require.NoError(t, err)
	require.Len(t, pipelineArtifacts, 1, "pipeline must produce exactly one artifact (message_error.go)")
	assert.Equal(t, "pkg/gateway/common/error/message_error.go", pipelineArtifacts[0].Path)

	content := string(pipelineArtifacts[0].Content)
	assert.Contains(t, content, "articlesErrorMessages,")
	assert.Contains(t, content, "profileErrorMessages,")
	assert.Contains(t, content, "authErrorMessages,")
	assert.Contains(t, content, "commonErrorMessages,")
	assert.Contains(t, content, "dbErrorMessages,")
	assert.Contains(t, content, "identityErrorMessages,")

	// Verify NO other service artifact claims message_error.go — which would
	// cause MIG211 (same path, multiple services, potentially different content).
	for _, svc := range []string{"articles", "profile"} {
		arts, err := store.ListArtifacts(ctx, 99, svc)
		require.NoError(t, err)
		for _, a := range arts {
			assert.NotEqual(t, "pkg/gateway/common/error/message_error.go", a.Path,
				"service %s must not claim message_error.go", svc)
		}
	}

	// Migration advances to READY (all services done).
	assert.Equal(t, []uint64{99}, updater.readyCalls)
	assert.Empty(t, updater.failCalls)
}

// TestAssembleErrorAggregator_PartialFailure verifies that even when one service
// fails, the pipeline still assembles message_error.go for the successful ones,
// and advances the migration to FAILED (not READY).
func TestAssembleErrorAggregator_PartialFailure(t *testing.T) {
	t.Parallel()

	monorepoRoot := t.TempDir()
	errorDir := filepath.Join(monorepoRoot, "pkg", "gateway", "common", "error")
	require.NoError(t, os.MkdirAll(errorDir, 0755))
	writeErrorFile(t, errorDir, "auth_errors.go", "package message_error\nvar authErrorMessages = map[string]string{}\n")

	// articles: pre-populated as done (invoker will skip it).
	// profile: NOT pre-populated — invoker returns failure so it ends up failed.
	store := newMockStore(
		workerdomain.ServiceGenerationRecord{MigrationID: 100, ServiceName: "articles", Status: workerdomain.ServiceStatusDone, GatesPassed: true},
	)
	ctx := context.Background()
	require.NoError(t, store.UpsertArtifacts(ctx, 100, "articles", []workerdomain.FileArtifact{
		{
			Path:    "pkg/gateway/common/error/articles_errors.go",
			Content: []byte("package message_error\nvar articlesErrorMessages = map[string]string{}\n"),
		},
	}))

	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{
		{Name: "articles", ErrorPrefix: "ART"},
		{Name: "profile", ErrorPrefix: "PRF"},
	}}
	// Invoker only called for profile (articles is already done). Fails it.
	inv := &mockInvoker{
		results: map[string]ports.InvokeResult{
			"profile": {GatesPassed: false, ExitCode: 1, FailureReason: "build failed"},
		},
	}

	pipeline := application.NewPipeline(reader, store, updater, inv, monorepoRoot)
	err := pipeline.Run(ctx, workerdomain.JobPayload{MigrationID: 100})
	require.NoError(t, err)

	// Aggregator still runs — includes only the successful service and platform maps.
	pipelineArtifacts, err := store.ListArtifacts(ctx, 100, "__pipeline__")
	require.NoError(t, err)
	require.Len(t, pipelineArtifacts, 1)

	content := string(pipelineArtifacts[0].Content)
	assert.Contains(t, content, "articlesErrorMessages,")
	assert.Contains(t, content, "authErrorMessages,")
	assert.NotContains(t, content, "profileErrorMessages,", "failed service must not appear in aggregator")

	// Migration must be FAILED.
	assert.Equal(t, []uint64{100}, updater.failCalls)
	assert.Empty(t, updater.readyCalls)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeErrorFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
}
