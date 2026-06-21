package application_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"milton_prism/core/worker/generation/application"
	workerdomain "milton_prism/core/worker/generation/domain"
	"milton_prism/core/worker/generation/ports"
)

// ── mock AgentInvoker ─────────────────────────────────────────────────────────

type mockInvoker struct {
	mu        sync.Mutex
	results   map[string]ports.InvokeResult
	invokeErr map[string]error
	// invokeErrSeq lets tests inject a sequence of errors per service — the first
	// call dequeues seq[0], the second seq[1], etc. A nil entry means "succeed,
	// fall through to results". Once the sequence is exhausted the mock falls
	// through to invokeErr / results as usual.
	invokeErrSeq map[string][]error
	called       []string
	maxParallel  int64
	current      int64
}

func (m *mockInvoker) Invoke(_ context.Context, _ string, req ports.InvokeRequest) (ports.InvokeResult, error) {
	c := atomic.AddInt64(&m.current, 1)
	defer atomic.AddInt64(&m.current, -1)
	// Update observed maximum without a mutex — CAS loop.
	for {
		old := atomic.LoadInt64(&m.maxParallel)
		if c <= old {
			break
		}
		if atomic.CompareAndSwapInt64(&m.maxParallel, old, c) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond) // hold slot so concurrency is observable

	m.mu.Lock()
	m.called = append(m.called, req.ServiceName)
	// Consume one entry from the per-service error sequence (if any).
	var hasSeqEntry bool
	var seqErr error
	if seq, ok := m.invokeErrSeq[req.ServiceName]; ok && len(seq) > 0 {
		seqErr = seq[0]
		m.invokeErrSeq[req.ServiceName] = seq[1:]
		hasSeqEntry = true
	}
	m.mu.Unlock()

	if hasSeqEntry {
		if seqErr != nil {
			return ports.InvokeResult{}, seqErr
		}
		// nil entry → succeed with default/results (fall through below)
	} else if m.invokeErr != nil {
		if err, ok := m.invokeErr[req.ServiceName]; ok && err != nil {
			return ports.InvokeResult{}, err
		}
	}
	if m.results != nil {
		if r, ok := m.results[req.ServiceName]; ok {
			return r, nil
		}
	}
	return ports.InvokeResult{GatesPassed: true, ExitCode: 0, Success: true, TotalCostUSD: 1.0}, nil
}

// ── mock GenerationStore ──────────────────────────────────────────────────────

type mockStore struct {
	mu      sync.Mutex
	records map[string]workerdomain.ServiceGenerationRecord
	// artifacts is keyed by serviceName → path → artifact (upsert semantics).
	artifacts map[string]map[string]workerdomain.FileArtifact
}

func newMockStore(initial ...workerdomain.ServiceGenerationRecord) *mockStore {
	s := &mockStore{
		records:   make(map[string]workerdomain.ServiceGenerationRecord),
		artifacts: make(map[string]map[string]workerdomain.FileArtifact),
	}
	for _, r := range initial {
		s.records[r.ServiceName] = r
	}
	return s
}

func (s *mockStore) UpsertRecord(_ context.Context, rec workerdomain.ServiceGenerationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.ServiceName] = rec
	return nil
}

func (s *mockStore) ListRecords(_ context.Context, _ uint64) ([]workerdomain.ServiceGenerationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]workerdomain.ServiceGenerationRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	return out, nil
}

func (s *mockStore) UpsertArtifacts(_ context.Context, _ uint64, serviceName string, artifacts []workerdomain.FileArtifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.artifacts[serviceName] == nil {
		s.artifacts[serviceName] = make(map[string]workerdomain.FileArtifact)
	}
	for _, a := range artifacts {
		s.artifacts[serviceName][a.Path] = a
	}
	return nil
}

func (s *mockStore) ListArtifacts(_ context.Context, _ uint64, serviceName string) ([]workerdomain.FileArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	byPath := s.artifacts[serviceName]
	out := make([]workerdomain.FileArtifact, 0, len(byPath))
	for _, a := range byPath {
		out = append(out, a)
	}
	return out, nil
}

// ── mock MigrationStateUpdater ────────────────────────────────────────────────

type mockStateUpdater struct {
	mu         sync.Mutex
	readyCalls []uint64
	failCalls  []uint64
}

func (m *mockStateUpdater) MarkReady(_ context.Context, migrationID uint64) error {
	m.mu.Lock()
	m.readyCalls = append(m.readyCalls, migrationID)
	m.mu.Unlock()
	return nil
}

func (m *mockStateUpdater) MarkFailed(_ context.Context, migrationID uint64) error {
	m.mu.Lock()
	m.failCalls = append(m.failCalls, migrationID)
	m.mu.Unlock()
	return nil
}

// ── mock GenerationPackageReader ──────────────────────────────────────────────

type mockPackageReader struct {
	services []ports.ServiceSpec
}

func (r *mockPackageReader) ReadPackage(_ context.Context, migrationID uint64) (*ports.GenerationPackage, error) {
	return &ports.GenerationPackage{
		MigrationID:   migrationID,
		OutputProfile: "go",
		Services:      r.services,
	}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newPipeline(
	inv ports.AgentInvoker,
	store ports.GenerationStore,
	updater ports.MigrationStateUpdater,
	reader ports.GenerationPackageReader,
) *application.Pipeline {
	return application.NewPipeline(reader, store, updater, inv, "/workspace")
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestPipeline_AllServicesSucceed(t *testing.T) {
	t.Parallel()
	inv := &mockInvoker{
		results: map[string]ports.InvokeResult{
			"articles": {GatesPassed: true, Success: true, TotalCostUSD: 1.5},
			"profiles": {GatesPassed: true, Success: true, TotalCostUSD: 1.2},
		},
	}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{
		{Name: "articles", ErrorPrefix: "ART"},
		{Name: "profiles", ErrorPrefix: "PRF"},
	}}

	err := newPipeline(inv, store, updater, reader).Run(context.Background(), workerdomain.JobPayload{MigrationID: 1})
	require.NoError(t, err)

	assert.Len(t, inv.called, 2)
	assert.Contains(t, inv.called, "articles")
	assert.Contains(t, inv.called, "profiles")
	assert.Equal(t, []uint64{1}, updater.readyCalls, "migration must be marked READY once")
	assert.Empty(t, updater.failCalls)

	store.mu.Lock()
	defer store.mu.Unlock()
	for _, name := range []string{"articles", "profiles"} {
		rec := store.records[name]
		assert.Equal(t, workerdomain.ServiceStatusDone, rec.Status, "service=%s", name)
		assert.True(t, rec.GatesPassed, "service=%s", name)
	}
}

func TestPipeline_PartialFailure_A5Degradation(t *testing.T) {
	t.Parallel()
	inv := &mockInvoker{
		results: map[string]ports.InvokeResult{
			"articles": {GatesPassed: true, Success: true, TotalCostUSD: 1.5},
			"profiles": {GatesPassed: false, ExitCode: 1, FailureReason: "build failed"},
		},
	}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{
		{Name: "articles", ErrorPrefix: "ART"},
		{Name: "profiles", ErrorPrefix: "PRF"},
	}}

	err := newPipeline(inv, store, updater, reader).Run(context.Background(), workerdomain.JobPayload{MigrationID: 2})
	require.NoError(t, err, "partial failure must not return an error (A.5)")
	assert.Equal(t, []uint64{2}, updater.failCalls, "migration must be marked FAILED when any service fails")
	assert.Empty(t, updater.readyCalls, "READY must not be called when a service failed")

	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, workerdomain.ServiceStatusDone, store.records["articles"].Status)
	assert.Equal(t, workerdomain.ServiceStatusFailed, store.records["profiles"].Status)
	assert.Equal(t, "build failed", store.records["profiles"].FailureReason)
}

func TestPipeline_Idempotent_SkipsDoneServices(t *testing.T) {
	t.Parallel()
	inv := &mockInvoker{
		results: map[string]ports.InvokeResult{
			"articles": {GatesPassed: true, Success: true},
			"profiles": {GatesPassed: true, Success: true},
		},
	}
	// "articles" already done — only "profiles" should be invoked.
	store := newMockStore(workerdomain.ServiceGenerationRecord{
		MigrationID: 3, ServiceName: "articles",
		Status: workerdomain.ServiceStatusDone, GatesPassed: true,
	})
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{
		{Name: "articles", ErrorPrefix: "ART"},
		{Name: "profiles", ErrorPrefix: "PRF"},
	}}

	err := newPipeline(inv, store, updater, reader).Run(context.Background(), workerdomain.JobPayload{MigrationID: 3})
	require.NoError(t, err)

	assert.Equal(t, []string{"profiles"}, inv.called, "articles must not be re-invoked")
	assert.Equal(t, []uint64{3}, updater.readyCalls)
	assert.Empty(t, updater.failCalls)
}

func TestPipeline_ConcurrencyBound_A4(t *testing.T) {
	t.Parallel()
	inv := &mockInvoker{}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{
		{Name: "svc1"},
		{Name: "svc2"},
		{Name: "svc3"},
	}}

	p := application.NewPipeline(reader, store, updater, inv, "/workspace").WithConcurrency(2)
	err := p.Run(context.Background(), workerdomain.JobPayload{MigrationID: 4})
	require.NoError(t, err)

	assert.LessOrEqual(t, inv.maxParallel, int64(2), "never more than 2 services in parallel (A.4)")
	assert.Len(t, inv.called, 3)
	assert.Equal(t, []uint64{4}, updater.readyCalls)
	assert.Empty(t, updater.failCalls)
}

func TestPipeline_IncompleteService_MarkedFailedWithoutInvoker(t *testing.T) {
	t.Parallel()
	inv := &mockInvoker{}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{
		{Name: "svc1", Incomplete: true, IncompleteReason: "no profile for rust"},
	}}

	err := newPipeline(inv, store, updater, reader).Run(context.Background(), workerdomain.JobPayload{MigrationID: 5})
	require.NoError(t, err)

	assert.Empty(t, inv.called, "invoker must not be called for incomplete services")
	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, workerdomain.ServiceStatusFailed, store.records["svc1"].Status)
	assert.Equal(t, "no profile for rust", store.records["svc1"].FailureReason)
	assert.Equal(t, []uint64{5}, updater.failCalls, "incomplete service → migration marked FAILED")
	assert.Empty(t, updater.readyCalls)
}

func TestPipeline_ArtifactsAndRawResultPersistedToStore(t *testing.T) {
	t.Parallel()

	wantArtifacts := []workerdomain.FileArtifact{
		{Path: "core/services/articles/domain/domain.go", Content: []byte("package domain\n")},
		{Path: "core/services/articles/wire.go", Content: []byte("package articles\n")},
	}

	inv := &mockInvoker{
		results: map[string]ports.InvokeResult{
			"articles": {
				GatesPassed:    true,
				Success:        true,
				TotalCostUSD:   1.5,
				RawResult:      "generation complete",
				GeneratedFiles: []string{"core/services/articles/domain/domain.go", "core/services/articles/wire.go"},
				FileArtifacts:  wantArtifacts,
			},
		},
	}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{
		{Name: "articles", ErrorPrefix: "ART"},
	}}

	err := newPipeline(inv, store, updater, reader).Run(context.Background(), workerdomain.JobPayload{MigrationID: 10})
	require.NoError(t, err)

	store.mu.Lock()
	rec := store.records["articles"]
	store.mu.Unlock()

	assert.Equal(t, "generation complete", rec.AgentRawResult, "AgentRawResult must be persisted in the generation record")
	assert.Equal(t, 2, rec.GeneratedFileCount)
	assert.True(t, rec.GatesPassed)

	got, err := store.ListArtifacts(context.Background(), 10, "articles")
	require.NoError(t, err)
	require.Len(t, got, 2, "both artifacts must be persisted")

	byPath := make(map[string][]byte, len(got))
	for _, a := range got {
		byPath[a.Path] = a.Content
	}
	assert.Equal(t, []byte("package domain\n"), byPath["core/services/articles/domain/domain.go"])
	assert.Equal(t, []byte("package articles\n"), byPath["core/services/articles/wire.go"])
}

func TestPipeline_ArtifactUpsert_Idempotent(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	ctx := context.Background()

	first := []workerdomain.FileArtifact{
		{Path: "core/services/user/domain/domain.go", Content: []byte("v1\n")},
	}
	require.NoError(t, store.UpsertArtifacts(ctx, 11, "user", first))

	// Re-upsert with updated content — must overwrite, not append.
	second := []workerdomain.FileArtifact{
		{Path: "core/services/user/domain/domain.go", Content: []byte("v2\n")},
	}
	require.NoError(t, store.UpsertArtifacts(ctx, 11, "user", second))

	got, err := store.ListArtifacts(ctx, 11, "user")
	require.NoError(t, err)
	require.Len(t, got, 1, "re-upsert must overwrite, not duplicate")
	assert.Equal(t, []byte("v2\n"), got[0].Content, "content must reflect the latest upsert")
}

func TestPipeline_NoArtifacts_OnFailedService(t *testing.T) {
	t.Parallel()

	inv := &mockInvoker{
		results: map[string]ports.InvokeResult{
			"profiles": {GatesPassed: false, ExitCode: 1, FailureReason: "build failed"},
		},
	}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{
		{Name: "profiles", ErrorPrefix: "PRF"},
	}}

	err := newPipeline(inv, store, updater, reader).Run(context.Background(), workerdomain.JobPayload{MigrationID: 12})
	require.NoError(t, err)

	got, err := store.ListArtifacts(context.Background(), 12, "profiles")
	require.NoError(t, err)
	assert.Empty(t, got, "no artifacts must be stored for a failed service with no FileArtifacts")
}

// ── Fix F: service filter test ────────────────────────────────────────────────

// TestPipeline_ServiceFilter_OnlyGeneratesSubset verifies that when a
// ServiceFilter is set in the JobPayload, only those named services are invoked
// and the migration is marked READY based solely on those results.
func TestPipeline_ServiceFilter_OnlyGeneratesSubset(t *testing.T) {
	t.Parallel()
	inv := &mockInvoker{}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{
		{Name: "svc1", ErrorPrefix: "S1"},
		{Name: "svc2", ErrorPrefix: "S2"},
		{Name: "svc3", ErrorPrefix: "S3"},
	}}

	// Only generate svc1 and svc3 — svc2 must be silently skipped.
	payload := workerdomain.JobPayload{
		MigrationID:   30,
		ServiceFilter: []string{"svc1", "svc3"},
	}
	err := newPipeline(inv, store, updater, reader).Run(context.Background(), payload)
	require.NoError(t, err)

	assert.Contains(t, inv.called, "svc1", "svc1 must be generated")
	assert.Contains(t, inv.called, "svc3", "svc3 must be generated")
	assert.NotContains(t, inv.called, "svc2", "svc2 must be skipped by the filter")
	assert.Len(t, inv.called, 2, "exactly 2 services must be invoked")

	assert.Equal(t, []uint64{30}, updater.readyCalls, "migration must be READY when filtered services all succeed")
	assert.Empty(t, updater.failCalls)
}

// ── Fix C: per-service retry tests ───────────────────────────────────────────

// TestPipeline_Retry_SucceedsAfterTransientErrors verifies that a service
// succeeds when transient (infrastructure) errors occur on the first two
// attempts and the third attempt succeeds.
func TestPipeline_Retry_SucceedsAfterTransientErrors(t *testing.T) {
	t.Parallel()

	transient := errors.New("network timeout")
	inv := &mockInvoker{
		// First two Invoke calls return a transient error; third falls through to results.
		invokeErrSeq: map[string][]error{
			"svc": {transient, transient},
		},
		results: map[string]ports.InvokeResult{
			"svc": {GatesPassed: true, Success: true, TotalCostUSD: 1.0},
		},
	}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{{Name: "svc", ErrorPrefix: "SVC"}}}

	p := application.NewPipeline(reader, store, updater, inv, "/workspace").
		WithRetryBackoff(1 * time.Millisecond)
	err := p.Run(context.Background(), workerdomain.JobPayload{MigrationID: 20})
	require.NoError(t, err)

	// Invoked 3 times (2 transient + 1 success).
	inv.mu.Lock()
	callCount := 0
	for _, name := range inv.called {
		if name == "svc" {
			callCount++
		}
	}
	inv.mu.Unlock()
	assert.Equal(t, 3, callCount, "invoker must be called 3 times (2 transient + 1 success)")

	assert.Equal(t, []uint64{20}, updater.readyCalls, "migration must be READY after eventual success")
	assert.Empty(t, updater.failCalls)

	store.mu.Lock()
	rec := store.records["svc"]
	store.mu.Unlock()
	assert.Equal(t, workerdomain.ServiceStatusDone, rec.Status)
	assert.True(t, rec.GatesPassed)
}

// TestPipeline_Retry_PermanentAfterExhaustion verifies that a service is
// marked FAILED after all maxServiceAttempts (3) are exhausted with transient errors.
func TestPipeline_Retry_PermanentAfterExhaustion(t *testing.T) {
	t.Parallel()

	transient := errors.New("overloaded")
	inv := &mockInvoker{
		invokeErrSeq: map[string][]error{
			"svc": {transient, transient, transient},
		},
	}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{{Name: "svc", ErrorPrefix: "SVC"}}}

	p := application.NewPipeline(reader, store, updater, inv, "/workspace").
		WithRetryBackoff(1 * time.Millisecond)
	err := p.Run(context.Background(), workerdomain.JobPayload{MigrationID: 21})
	require.NoError(t, err, "exhausted retry must not bubble up (A.5)")

	inv.mu.Lock()
	callCount := 0
	for _, name := range inv.called {
		if name == "svc" {
			callCount++
		}
	}
	inv.mu.Unlock()
	assert.Equal(t, 3, callCount, "invoker must be called exactly maxServiceAttempts times")

	assert.Equal(t, []uint64{21}, updater.failCalls, "migration must be FAILED after exhaustion")
	assert.Empty(t, updater.readyCalls)

	store.mu.Lock()
	rec := store.records["svc"]
	store.mu.Unlock()
	assert.Equal(t, workerdomain.ServiceStatusFailed, rec.Status)
}

// TestPipeline_Retry_PermanentGatesFailure_NoRetry verifies that a permanent
// gates failure (code quality, not rate-limit) is not retried.
func TestPipeline_Retry_PermanentGatesFailure_NoRetry(t *testing.T) {
	t.Parallel()

	inv := &mockInvoker{
		results: map[string]ports.InvokeResult{
			"svc": {GatesPassed: false, ExitCode: 1, FailureReason: "undefined: SomeType"},
		},
	}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{{Name: "svc", ErrorPrefix: "SVC"}}}

	p := application.NewPipeline(reader, store, updater, inv, "/workspace").
		WithRetryBackoff(1 * time.Millisecond)
	err := p.Run(context.Background(), workerdomain.JobPayload{MigrationID: 22})
	require.NoError(t, err)

	inv.mu.Lock()
	callCount := 0
	for _, name := range inv.called {
		if name == "svc" {
			callCount++
		}
	}
	inv.mu.Unlock()
	assert.Equal(t, 1, callCount, "permanent failure must not be retried — invoker called once only")

	assert.Equal(t, []uint64{22}, updater.failCalls)
	assert.Empty(t, updater.readyCalls)

	store.mu.Lock()
	rec := store.records["svc"]
	store.mu.Unlock()
	assert.Equal(t, workerdomain.ServiceStatusFailed, rec.Status)
	assert.Equal(t, "undefined: SomeType", rec.FailureReason)
}
