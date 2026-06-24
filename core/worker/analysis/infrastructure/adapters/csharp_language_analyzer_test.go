package adapters_test

import (
	"context"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureCSharp = "testdata/fixture-csharp"

func TestCSharpAnalyzer_Language(t *testing.T) {
	t.Parallel()
	a := adapters.NewCSharpLanguageAnalyzer()
	assert.Equal(t, "C#", a.Language())
}

func TestCSharpAnalyzer_FrameworkProfile_AspNet(t *testing.T) {
	t.Parallel()
	a := adapters.NewCSharpLanguageAnalyzer()
	assert.Equal(t, "ASP.NET Core", a.FrameworkProfile().Framework)
}

// ── (b) resolver: intra-repo namespace edges with weights ─────────────────────

func TestCSharpAnalyzer_Graph_IntraRepoEdges(t *testing.T) {
	t.Parallel()
	a := adapters.NewCSharpLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), fixtureCSharp)
	require.NoError(t, err)
	require.NotEmpty(t, edges)

	idx := javaEdgeIndex(edges) // reuse the (from,to)→edge indexer from the Java test

	// Controller uses Acme.Services and Acme.Models (Sys alias of System.Text is external).
	assertEdge(t, idx, "Acme.Controllers.UserController", "Acme.Services.UserService", 1)
	assertEdge(t, idx, "Acme.Controllers.UserController", "Acme.Models.User", 1)
	// Service uses Acme.Data and Acme.Models.
	assertEdge(t, idx, "Acme.Services.UserService", "Acme.Data.UserRepository", 1)
	assertEdge(t, idx, "Acme.Services.UserService", "Acme.Models.User", 1)
	// Repository uses Acme.Models (System.Collections.Generic is external → no edge).
	assertEdge(t, idx, "Acme.Data.UserRepository", "Acme.Models.User", 1)

	for k := range idx {
		assert.NotContains(t, k[1], "System", "BCL usings must not be graph edges")
	}
}

// ── (c) ExtractCards: module cards + route blueprints ─────────────────────────

func TestCSharpAnalyzer_ExtractCards(t *testing.T) {
	t.Parallel()
	a := adapters.NewCSharpLanguageAnalyzer()
	cards, blueprints, err := a.ExtractCards(context.Background(), fixtureCSharp)
	require.NoError(t, err)

	cardByModule := map[string]*analysisdomain.ModuleCard{}
	for _, c := range cards {
		cardByModule[c.GetModule()] = c
	}

	// obj/ generated file must not produce a card.
	_, leaked := cardByModule["Acme.Generated.Generated"]
	assert.False(t, leaked, "files under obj/ must be skipped")

	ctrl := cardByModule["Acme.Controllers.UserController"]
	require.NotNil(t, ctrl)
	assert.Equal(t, []string{"class:UserController"}, ctrl.GetClasses())

	routes := map[string]string{}
	for _, r := range ctrl.GetRoutes() {
		routes[r.GetMethod()+" "+r.GetPath()] = r.GetHandler()
	}
	assert.Equal(t, "GetUser", routes["GET /api/users/{id}"])
	assert.Equal(t, "CreateUser", routes["POST /api/users"])

	repo := cardByModule["Acme.Data.UserRepository"]
	require.NotNil(t, repo)
	assert.Contains(t, repo.GetModuleLevelState(), "instanceCount")

	// Minimal-API routes from Program.cs land on its file's card.
	prog := cardByModule["Program.cs"]
	require.NotNil(t, prog, "minimal-API Program.cs card must exist (identified by path)")
	progRoutes := map[string]bool{}
	for _, r := range prog.GetRoutes() {
		progRoutes[r.GetMethod()+" "+r.GetPath()] = true
	}
	assert.True(t, progRoutes["GET /health"])
	assert.True(t, progRoutes["POST /echo"])

	// Blueprints: one for the controller, one for the minimal-API file.
	bpByName := map[string]*analysisdomain.BlueprintInfo{}
	for _, b := range blueprints {
		bpByName[b.GetName()] = b
	}
	require.Contains(t, bpByName, "UserController")
	assert.Equal(t, "/api/users", bpByName["UserController"].GetUrlPrefix())
	assert.Contains(t, bpByName, "Program.cs")
}
