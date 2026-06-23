package application_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"milton_prism/core/worker/generation/application"
	workerdomain "milton_prism/core/worker/generation/domain"
	"milton_prism/core/worker/generation/ports"
)

// mockOpenAPIGenerator records the protos it was asked to render and returns a
// canned document. genErr, when set, simulates a buf failure.
type mockOpenAPIGenerator struct {
	mu        sync.Mutex
	calls     int
	gotProtos []ports.ProtoArtifact
	gotBase   string
	doc       []byte
	genErr    error
}

func (m *mockOpenAPIGenerator) Generate(_ context.Context, workspaceBase string, protos []ports.ProtoArtifact) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.gotBase = workspaceBase
	m.gotProtos = append([]ports.ProtoArtifact(nil), protos...)
	if m.genErr != nil {
		return nil, m.genErr
	}
	return m.doc, nil
}

// pipelineWithOpenAPI mirrors newPipeline but wires the OpenAPI generator.
func pipelineWithOpenAPI(
	inv ports.AgentInvoker,
	store ports.GenerationStore,
	updater ports.MigrationStateUpdater,
	reader ports.GenerationPackageReader,
	gen ports.OpenAPIGenerator,
) *application.Pipeline {
	return application.NewPipeline(reader, store, updater, inv, "/workspace").
		WithOpenAPIGenerator(gen)
}

// TestPipeline_AssembleOpenAPI_PersistsDocsArtifact proves the pipeline emits
// docs/openapi.yaml from the generated service protos and persists it under the
// __pipeline__ synthetic service. Only .proto artifacts under the services/types
// trees are forwarded to the generator; .go and other artifacts are ignored.
func TestPipeline_AssembleOpenAPI_PersistsDocsArtifact(t *testing.T) {
	t.Parallel()

	const svcProto = "protobuf/proto/milton_prism/services/articles/v1/articles_service.proto"
	const typeProto = "protobuf/proto/milton_prism/types/articles/v1/articles.proto"

	inv := &mockInvoker{
		results: map[string]ports.InvokeResult{
			"articles": {
				GatesPassed: true, Success: true,
				FileArtifacts: []workerdomain.FileArtifact{
					{Path: svcProto, Content: []byte("syntax = \"proto3\";\n")},
					{Path: typeProto, Content: []byte("syntax = \"proto3\";\n")},
					// Non-proto artifact: must NOT be forwarded to the generator.
					{Path: "core/services/articles/wire.go", Content: []byte("package articles\n")},
				},
			},
		},
	}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{{Name: "articles", ErrorPrefix: "ART"}}}
	gen := &mockOpenAPIGenerator{doc: []byte("openapi: 3.0.3\ninfo:\n  title: X\n")}

	err := pipelineWithOpenAPI(inv, store, updater, reader, gen).
		Run(context.Background(), workerdomain.JobPayload{MigrationID: 7})
	require.NoError(t, err)

	// Generator was invoked exactly once with the monorepo root and only the protos.
	assert.Equal(t, 1, gen.calls)
	assert.Equal(t, "/workspace", gen.gotBase)
	gotPaths := make([]string, 0, len(gen.gotProtos))
	for _, p := range gen.gotProtos {
		gotPaths = append(gotPaths, p.Path)
	}
	assert.ElementsMatch(t, []string{svcProto, typeProto}, gotPaths,
		"only service/type protos must be forwarded — no .go files")

	// docs/openapi.yaml persisted under the __pipeline__ service.
	arts, err := store.ListArtifacts(context.Background(), 7, "__pipeline__")
	require.NoError(t, err)
	var found bool
	for _, a := range arts {
		if a.Path == "docs/openapi.yaml" {
			found = true
			assert.Equal(t, gen.doc, a.Content)
		}
	}
	assert.True(t, found, "docs/openapi.yaml must be persisted as a __pipeline__ artifact")
}

// TestPipeline_AssembleOpenAPI_ProfileAgnostic proves the step runs for a
// non-Go profile (Python) too — the spec is derived from protos alone.
func TestPipeline_AssembleOpenAPI_ProfileAgnostic(t *testing.T) {
	t.Parallel()

	const svcProto = "protobuf/proto/milton_prism/services/orders/v1/orders_service.proto"

	inv := &mockInvoker{
		results: map[string]ports.InvokeResult{
			"orders": {
				GatesPassed: true, Success: true,
				FileArtifacts: []workerdomain.FileArtifact{
					{Path: svcProto, Content: []byte("syntax = \"proto3\";\n")},
				},
			},
		},
	}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{
		profile:  "python",
		services: []ports.ServiceSpec{{Name: "orders", ErrorPrefix: "ORD"}},
	}
	gen := &mockOpenAPIGenerator{doc: []byte("openapi: 3.0.3\n")}

	err := pipelineWithOpenAPI(inv, store, updater, reader, gen).
		Run(context.Background(), workerdomain.JobPayload{MigrationID: 8})
	require.NoError(t, err)

	assert.Equal(t, 1, gen.calls, "openapi step must run regardless of OutputProfile")
	arts, err := store.ListArtifacts(context.Background(), 8, "__pipeline__")
	require.NoError(t, err)
	require.Len(t, arts, 1)
	assert.Equal(t, "docs/openapi.yaml", arts[0].Path)
}

// TestPipeline_AssembleOpenAPI_GeneratorFailureIsNonFatal proves a buf failure
// degrades gracefully: no artifact is persisted, but the migration still
// reaches READY.
func TestPipeline_AssembleOpenAPI_GeneratorFailureIsNonFatal(t *testing.T) {
	t.Parallel()

	inv := &mockInvoker{
		results: map[string]ports.InvokeResult{
			"articles": {
				GatesPassed: true, Success: true,
				FileArtifacts: []workerdomain.FileArtifact{
					{Path: "protobuf/proto/milton_prism/services/articles/v1/articles_service.proto", Content: []byte("x")},
				},
			},
		},
	}
	store := newMockStore()
	updater := &mockStateUpdater{}
	reader := &mockPackageReader{services: []ports.ServiceSpec{{Name: "articles", ErrorPrefix: "ART"}}}
	gen := &mockOpenAPIGenerator{genErr: assertAnErr{}}

	err := pipelineWithOpenAPI(inv, store, updater, reader, gen).
		Run(context.Background(), workerdomain.JobPayload{MigrationID: 9})
	require.NoError(t, err)
	assert.Equal(t, []uint64{9}, updater.readyCalls, "migration still READY despite openapi failure")

	arts, _ := store.ListArtifacts(context.Background(), 9, "__pipeline__")
	for _, a := range arts {
		assert.NotEqual(t, "docs/openapi.yaml", a.Path, "no openapi artifact on generator failure")
	}
}

type assertAnErr struct{}

func (assertAnErr) Error() string { return "buf boom" }
