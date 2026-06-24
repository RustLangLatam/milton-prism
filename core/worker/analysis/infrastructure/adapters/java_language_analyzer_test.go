package adapters_test

import (
	"context"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureJava = "testdata/fixture-java"

// ── basic properties ──────────────────────────────────────────────────────────

func TestJavaAnalyzer_Language(t *testing.T) {
	t.Parallel()
	a := adapters.NewJavaLanguageAnalyzer()
	assert.Equal(t, "Java", a.Language())
}

func TestJavaAnalyzer_FrameworkProfile_Spring(t *testing.T) {
	t.Parallel()
	a := adapters.NewJavaLanguageAnalyzer()
	assert.Equal(t, "Spring", a.FrameworkProfile().Framework)
}

// ── (b) resolver: intra-repo edges with weights ───────────────────────────────

func TestJavaAnalyzer_Graph_IntraRepoEdges(t *testing.T) {
	t.Parallel()
	a := adapters.NewJavaLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), fixtureJava)
	require.NoError(t, err)
	require.NotEmpty(t, edges)

	idx := javaEdgeIndex(edges)

	// Controller imports Service and Model.
	assertEdge(t, idx, "com.acme.web.UserController", "com.acme.service.UserService", 1)
	assertEdge(t, idx, "com.acme.web.UserController", "com.acme.model.User", 1)
	// Service imports Repo and Model.
	assertEdge(t, idx, "com.acme.service.UserService", "com.acme.repo.UserRepository", 1)
	assertEdge(t, idx, "com.acme.service.UserService", "com.acme.model.User", 1)
	// Repo imports Model (java.util.* is external → no edge).
	assertEdge(t, idx, "com.acme.repo.UserRepository", "com.acme.model.User", 1)

	// No edge may target a JDK or third-party type.
	for k := range idx {
		assert.NotContains(t, k[1], "java.util", "JDK imports must not be graph edges")
		assert.NotContains(t, k[1], "org.springframework", "third-party imports must not be graph edges")
	}
	// The generated file under target/ must never appear as a node.
	for k := range idx {
		assert.NotContains(t, k[0], "generated")
		assert.NotContains(t, k[1], "generated")
	}
}

// ── (c) ExtractCards: module cards + route blueprints ─────────────────────────

func TestJavaAnalyzer_ExtractCards(t *testing.T) {
	t.Parallel()
	a := adapters.NewJavaLanguageAnalyzer()
	cards, blueprints, err := a.ExtractCards(context.Background(), fixtureJava)
	require.NoError(t, err)

	cardByModule := map[string]*analysisdomain.ModuleCard{}
	for _, c := range cards {
		cardByModule[c.GetModule()] = c
	}

	// target/ generated file must not produce a card.
	_, leaked := cardByModule["com.acme.generated.Generated"]
	assert.False(t, leaked, "files under target/ must be skipped")

	ctrl := cardByModule["com.acme.web.UserController"]
	require.NotNil(t, ctrl, "controller module card must exist")
	assert.Equal(t, []string{"class:UserController"}, ctrl.GetClasses())
	assert.ElementsMatch(t, []string{"getUser", "createUser"}, ctrl.GetFunctions())

	// Routes: GET /api/users/{id} and POST /api/users, with the class prefix applied.
	routes := map[string]string{} // method+path → handler
	for _, r := range ctrl.GetRoutes() {
		routes[r.GetMethod()+" "+r.GetPath()] = r.GetHandler()
	}
	assert.Equal(t, "getUser", routes["GET /api/users/{id}"])
	assert.Equal(t, "createUser", routes["POST /api/users"])

	repo := cardByModule["com.acme.repo.UserRepository"]
	require.NotNil(t, repo)
	assert.Contains(t, repo.GetModuleLevelState(), "instanceCount")

	// Blueprint: one per controller, carrying the URL prefix.
	require.Len(t, blueprints, 1)
	assert.Equal(t, "UserController", blueprints[0].GetName())
	assert.Equal(t, "/api/users", blueprints[0].GetUrlPrefix())
}

// ── helpers ───────────────────────────────────────────────────────────────────

func javaEdgeIndex(edges []*analysisdomain.DependencyEdge) map[[2]string]*analysisdomain.DependencyEdge {
	m := make(map[[2]string]*analysisdomain.DependencyEdge, len(edges))
	for _, e := range edges {
		m[[2]string{e.GetFromModule(), e.GetToModule()}] = e
	}
	return m
}

func assertEdge(t *testing.T, idx map[[2]string]*analysisdomain.DependencyEdge, from, to string, weight uint32) {
	t.Helper()
	e, ok := idx[[2]string{from, to}]
	require.Truef(t, ok, "expected edge %s → %s", from, to)
	assert.Equalf(t, weight, e.GetWeight(), "weight for %s → %s", from, to)
}
