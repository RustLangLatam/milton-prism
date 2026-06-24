package adapters_test

import (
	"context"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureGo = "testdata/fixture-go"

const (
	modRoot    = "example.com/app"
	modModel   = "example.com/app/internal/model"
	modRepo    = "example.com/app/internal/repo"
	modService = "example.com/app/internal/service"
	modWeb     = "example.com/app/web"
)

// ── basic properties ──────────────────────────────────────────────────────────

func TestGoAnalyzer_Language(t *testing.T) {
	t.Parallel()
	a := adapters.NewGoLanguageAnalyzer()
	assert.Equal(t, "Go", a.Language())
}

func TestGoAnalyzer_FrameworkProfile(t *testing.T) {
	t.Parallel()
	a := adapters.NewGoLanguageAnalyzer()
	assert.Equal(t, "Go", a.FrameworkProfile().Framework)
}

// ── resolver: intra-repo edges with weights, ignoring stdlib + third-party ────

func goEdgeIndex(edges []*analysisdomain.DependencyEdge) map[[2]string]uint32 {
	idx := make(map[[2]string]uint32, len(edges))
	for _, e := range edges {
		idx[[2]string{e.GetFromModule(), e.GetToModule()}] = e.GetWeight()
	}
	return idx
}

func TestGoAnalyzer_Graph_IntraRepoEdges(t *testing.T) {
	t.Parallel()
	a := adapters.NewGoLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), fixtureGo)
	require.NoError(t, err)
	require.NotEmpty(t, edges)

	idx := goEdgeIndex(edges)

	// web imports service and model (gin, uuid, net/http are external → no edge).
	assert.Equal(t, uint32(1), idx[[2]string{modWeb, modService}], "web→service")
	assert.Equal(t, uint32(1), idx[[2]string{modWeb, modModel}], "web→model")
	// service imports repo (aliased) and model; uuid is third-party → no edge.
	// service→model has weight 2: user_service.go and user_service_test.go each
	// import model (the _test.go file is counted — parity with Python).
	assert.Equal(t, uint32(1), idx[[2]string{modService, modRepo}], "service→repo (aliased)")
	assert.Equal(t, uint32(2), idx[[2]string{modService, modModel}], "service→model (incl. _test.go)")
	// repo imports model; database/sql (stdlib) and lib/pq (blank, third-party) → no edge.
	assert.Equal(t, uint32(1), idx[[2]string{modRepo, modModel}], "repo→model")
	// root main imports service and web.
	assert.Equal(t, uint32(1), idx[[2]string{modRoot, modService}], "root→service")
	assert.Equal(t, uint32(1), idx[[2]string{modRoot, modWeb}], "root→web")

	// No edge may target stdlib or third-party packages.
	for k := range idx {
		assert.NotContains(t, k[1], "github.com", "third-party imports must not be graph edges")
		assert.NotEqual(t, "fmt", k[1])
		assert.NotEqual(t, "net/http", k[1])
		assert.NotEqual(t, "database/sql", k[1])
		assert.NotEqual(t, "time", k[1])
		// vendor/ file must never appear as a node.
		assert.NotContains(t, k[0], "fake")
		assert.NotContains(t, k[1], "fake")
	}
}

// ── ExtractCards: module cards + route blueprints ─────────────────────────────

func TestGoAnalyzer_ExtractCards(t *testing.T) {
	t.Parallel()
	a := adapters.NewGoLanguageAnalyzer()
	cards, blueprints, err := a.ExtractCards(context.Background(), fixtureGo)
	require.NoError(t, err)
	require.NotEmpty(t, cards)

	cardByFile := map[string]*analysisdomain.ModuleCard{}
	for _, c := range cards {
		cardByFile[c.GetFile()] = c
	}

	// vendor/ file must not produce a card.
	for _, c := range cards {
		assert.NotContains(t, c.GetFile(), "vendor/", "files under vendor/ must be skipped")
	}

	// model card: struct/interface/type kind prefixes, method "Recv.Method".
	model := cardByFile["internal/model/user.go"]
	require.NotNil(t, model)
	assert.Equal(t, modModel, model.GetModule())
	assert.ElementsMatch(t,
		[]string{"struct:User", "type:Role", "interface:Store"},
		model.GetClasses())
	assert.Contains(t, model.GetFunctions(), "User.Display")
	assert.Contains(t, model.GetFunctions(), "New")
	assert.Equal(t, "Package model holds the core domain entities.", model.GetDocstringHead())
	assert.Greater(t, model.GetLoc(), uint32(0))

	// repo card: file-scope mutable var becomes module-level state.
	repo := cardByFile["internal/repo/user_repo.go"]
	require.NotNil(t, repo)
	assert.Contains(t, repo.GetModuleLevelState(), "connCount")
	assert.Contains(t, repo.GetFunctions(), "PostgresRepo.Save")

	// web card: gin + net/http routes.
	web := cardByFile["web/router.go"]
	require.NotNil(t, web)
	assert.Contains(t, web.GetModuleLevelState(), "activeRequests")
	routes := map[string]string{} // method+path → handler
	for _, r := range web.GetRoutes() {
		routes[r.GetMethod()+" "+r.GetPath()] = r.GetHandler()
	}
	assert.Equal(t, "getUser", routes["GET /users/:id"])
	assert.Equal(t, "createUser", routes["POST /users"])
	assert.Equal(t, "deleteUser", routes["DELETE /users/:id"])
	assert.Equal(t, "healthHandler", routes["GET /health"], "net/http HandleFunc → GET")

	// _test.go files are included in the graph (parity with Python).
	testCard := cardByFile["internal/service/user_service_test.go"]
	require.NotNil(t, testCard, "_test.go files must produce a module card")
	assert.Equal(t, modService, testCard.GetModule())

	// Blueprint emitted for the file that registers routes.
	var webBP *analysisdomain.BlueprintInfo
	for _, bp := range blueprints {
		if bp.GetFile() == "web/router.go" {
			webBP = bp
		}
	}
	require.NotNil(t, webBP, "the route-bearing file must produce a blueprint")
	assert.Equal(t, modWeb, webBP.GetName())
}
