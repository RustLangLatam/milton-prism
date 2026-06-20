package adapters_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixturePyCards = "testdata/fixture-python/cards"

// edgeIndex returns a map from (from, to) → *DependencyEdge for fast lookups.
func edgeIndex(edges []*analysisdomain.DependencyEdge) map[[2]string]*analysisdomain.DependencyEdge {
	m := make(map[[2]string]*analysisdomain.DependencyEdge, len(edges))
	for _, e := range edges {
		m[[2]string{e.GetFromModule(), e.GetToModule()}] = e
	}
	return m
}

// sortedFromModules returns the distinct FromModule values, sorted.
func sortedFromModules(edges []*analysisdomain.DependencyEdge) []string {
	seen := make(map[string]bool)
	for _, e := range edges {
		seen[e.GetFromModule()] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── basic properties ──────────────────────────────────────────────────────────

func TestPyAnalyzer_Language(t *testing.T) {
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	assert.Equal(t, "Python", a.Language())
}

func TestPyAnalyzer_FrameworkProfile_Flask(t *testing.T) {
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	profile := a.FrameworkProfile()
	assert.Equal(t, "Flask", profile.Framework)
}

// ── dependency graph from the miniproject fixture ─────────────────────────────

func TestPyAnalyzer_Graph_HasExpectedEdges(t *testing.T) {
	// The miniproject has these internal edges after resolution:
	//   flat_util      → myapp              (import myapp)
	//   myapp          → myapp.views        (from . import views)
	//   myapp.models   → myapp.models.user  (from .user import User)
	//   myapp.views    → myapp.models       (×2: from myapp import models + from myapp.models import User)
	//   myapp.views    → myapp.models.user  (from .models import user)
	//   service.handler→ service.utils      (×2: from service import utils + from .utils import helper)
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), fixturePyMiniproject)
	require.NoError(t, err)
	require.NotEmpty(t, edges)

	idx := edgeIndex(edges)

	// Verify each expected internal edge exists.
	type expected struct{ from, to string }
	internals := []expected{
		{"flat_util", "myapp"},
		{"myapp", "myapp.views"},
		{"myapp.models", "myapp.models.user"},
		{"myapp.views", "myapp.models"},
		{"myapp.views", "myapp.models.user"},
		{"service.handler", "service.utils"},
	}
	for _, ex := range internals {
		_, ok := idx[[2]string{ex.from, ex.to}]
		assert.True(t, ok, "expected edge %s → %s not found in graph", ex.from, ex.to)
	}
}

func TestPyAnalyzer_Graph_ExternalImportsProduceNoEdges(t *testing.T) {
	// "requests" and "os" are external; they must not appear as ToModule.
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), fixturePyMiniproject)
	require.NoError(t, err)

	for _, e := range edges {
		assert.NotEqual(t, "requests", e.GetToModule(),
			"external 'requests' must not appear as ToModule")
		assert.NotEqual(t, "os", e.GetToModule(),
			"external 'os' must not appear as ToModule")
	}
}

func TestPyAnalyzer_Graph_Weights(t *testing.T) {
	// Coupling weights:
	//   myapp.views → myapp.models:       2 (from myapp import models + from myapp.models import User)
	//   service.handler → service.utils:  2 (from service import utils + from .utils import helper)
	//   other edges:                      1
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), fixturePyMiniproject)
	require.NoError(t, err)

	idx := edgeIndex(edges)

	viewsToModels := idx[[2]string{"myapp.views", "myapp.models"}]
	require.NotNil(t, viewsToModels, "myapp.views → myapp.models edge must exist")
	assert.Equal(t, uint32(2), viewsToModels.GetWeight(),
		"myapp.views → myapp.models weight must be 2 (two distinct import statements)")

	handlerToUtils := idx[[2]string{"service.handler", "service.utils"}]
	require.NotNil(t, handlerToUtils, "service.handler → service.utils edge must exist")
	assert.Equal(t, uint32(2), handlerToUtils.GetWeight(),
		"service.handler → service.utils weight must be 2 (absolute + relative)")

	// All other edges have weight 1.
	for _, e := range edges {
		from, to := e.GetFromModule(), e.GetToModule()
		if (from == "myapp.views" && to == "myapp.models") ||
			(from == "service.handler" && to == "service.utils") {
			continue
		}
		assert.Equal(t, uint32(1), e.GetWeight(),
			"unexpected weight for edge %s → %s", from, to)
	}
}

func TestPyAnalyzer_Graph_NoSelfEdges(t *testing.T) {
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), fixturePyMiniproject)
	require.NoError(t, err)
	for _, e := range edges {
		assert.NotEqual(t, e.GetFromModule(), e.GetToModule(),
			"self-edge must not appear: %s → %s", e.GetFromModule(), e.GetToModule())
	}
}

func TestPyAnalyzer_Graph_DeterministicOrder(t *testing.T) {
	// Running ResolveImports twice must produce the same ordered slice.
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	first, err := a.ResolveImports(context.Background(), fixturePyMiniproject)
	require.NoError(t, err)
	second, err := a.ResolveImports(context.Background(), fixturePyMiniproject)
	require.NoError(t, err)

	require.Len(t, second, len(first), "second run must return same number of edges")
	for i := range first {
		assert.Equal(t, first[i].GetFromModule(), second[i].GetFromModule())
		assert.Equal(t, first[i].GetToModule(), second[i].GetToModule())
		assert.Equal(t, first[i].GetWeight(), second[i].GetWeight())
	}
}

func TestPyAnalyzer_EmptyWorkspace_ReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := adapters.NewPythonLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), dir)
	require.NoError(t, err)
	assert.Nil(t, edges)
}

// ── ExtractCards: module card fixture ────────────────────────────────────────

// cardIndex builds a map from module name → *analysisdomain.ModuleCard.
func cardIndex(cards []*analysisdomain.ModuleCard) map[string]*analysisdomain.ModuleCard {
	m := make(map[string]*analysisdomain.ModuleCard, len(cards))
	for _, c := range cards {
		m[c.GetModule()] = c
	}
	return m
}

func TestPyAnalyzer_ExtractCards_ReturnsOneCard(t *testing.T) {
	// The cards fixture has exactly one .py file (mymodule.py).
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	cards, _, err := a.ExtractCards(context.Background(), fixturePyCards)
	require.NoError(t, err)
	require.Len(t, cards, 1, "expected exactly one module card")
}

func TestPyAnalyzer_ExtractCards_Functions(t *testing.T) {
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	cards, _, err := a.ExtractCards(context.Background(), fixturePyCards)
	require.NoError(t, err)
	idx := cardIndex(cards)
	card := idx["mymodule"]
	require.NotNil(t, card, "mymodule card must be present")
	// get_user, update_user are plain defs; list_users, user_detail are decorated.
	assert.ElementsMatch(t, []string{"get_user", "update_user", "list_users", "user_detail"}, card.GetFunctions())
}

func TestPyAnalyzer_ExtractCards_Classes(t *testing.T) {
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	cards, _, err := a.ExtractCards(context.Background(), fixturePyCards)
	require.NoError(t, err)
	idx := cardIndex(cards)
	card := idx["mymodule"]
	require.NotNil(t, card)
	assert.ElementsMatch(t, []string{"UserService", "OrderService"}, card.GetClasses())
}

func TestPyAnalyzer_ExtractCards_ModuleLevelState_MutableOnly(t *testing.T) {
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	cards, _, err := a.ExtractCards(context.Background(), fixturePyCards)
	require.NoError(t, err)
	idx := cardIndex(cards)
	card := idx["mymodule"]
	require.NotNil(t, card)
	state := card.GetModuleLevelState()
	// counter, _cache, users are mutable (have lowercase letters).
	assert.Contains(t, state, "counter")
	assert.Contains(t, state, "_cache")
	assert.Contains(t, state, "users")
	// MAX_SIZE and DB_URL are ALL_CAPS constants — must be excluded.
	assert.NotContains(t, state, "MAX_SIZE")
	assert.NotContains(t, state, "DB_URL")
	// bp is a Blueprint assignment — excluded from state.
	assert.NotContains(t, state, "bp")
}

func TestPyAnalyzer_ExtractCards_Routes(t *testing.T) {
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	cards, _, err := a.ExtractCards(context.Background(), fixturePyCards)
	require.NoError(t, err)
	idx := cardIndex(cards)
	card := idx["mymodule"]
	require.NotNil(t, card)
	routes := card.GetRoutes()
	require.Len(t, routes, 2, "expected exactly 2 routes")

	byPath := make(map[string]*analysisdomain.RouteInfo, len(routes))
	for _, r := range routes {
		byPath[r.GetPath()] = r
	}

	r1 := byPath["/users"]
	require.NotNil(t, r1, "/users route must be present")
	assert.Equal(t, "list_users", r1.GetHandler())
	assert.Equal(t, "GET", r1.GetMethod())

	r2 := byPath["/users/<int:user_id>"]
	require.NotNil(t, r2, "/users/<int:user_id> route must be present")
	assert.Equal(t, "user_detail", r2.GetHandler())
	// methods=["GET","PUT"] → comma-joined; order matches the list literal.
	assert.True(t, strings.Contains(r2.GetMethod(), "GET"), "method must include GET")
	assert.True(t, strings.Contains(r2.GetMethod(), "PUT"), "method must include PUT")
}

func TestPyAnalyzer_ExtractCards_DocstringHead(t *testing.T) {
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	cards, _, err := a.ExtractCards(context.Background(), fixturePyCards)
	require.NoError(t, err)
	idx := cardIndex(cards)
	card := idx["mymodule"]
	require.NotNil(t, card)
	// Fixture docstring: "Card extraction fixture — functions, classes, state, and routes."
	assert.Contains(t, card.GetDocstringHead(), "Card extraction fixture")
}

func TestPyAnalyzer_ExtractCards_LOCPositive(t *testing.T) {
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	cards, _, err := a.ExtractCards(context.Background(), fixturePyCards)
	require.NoError(t, err)
	idx := cardIndex(cards)
	card := idx["mymodule"]
	require.NotNil(t, card)
	assert.Greater(t, card.GetLoc(), uint32(0), "LOC must be positive")
}

func TestPyAnalyzer_ExtractCards_Blueprints(t *testing.T) {
	// The fixture has bp = Blueprint("cards", __name__) with no register_blueprint.
	t.Parallel()
	a := adapters.NewPythonLanguageAnalyzer()
	_, blueprints, err := a.ExtractCards(context.Background(), fixturePyCards)
	require.NoError(t, err)
	require.Len(t, blueprints, 1, "expected exactly one blueprint")
	assert.Equal(t, "cards", blueprints[0].GetName())
	// No register_blueprint call in the fixture → url_prefix is empty.
	assert.Empty(t, blueprints[0].GetUrlPrefix())
}

func TestPyAnalyzer_ExtractCards_EmptyWorkspace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := adapters.NewPythonLanguageAnalyzer()
	cards, bps, err := a.ExtractCards(context.Background(), dir)
	require.NoError(t, err)
	assert.Empty(t, cards)
	assert.Empty(t, bps)
}
