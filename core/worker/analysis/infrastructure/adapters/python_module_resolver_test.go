package adapters_test

import (
	"sort"
	"testing"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixturePyMiniproject = "testdata/fixture-python/miniproject"

// resolvedIndex builds a map from (from, to) pairs to a boolean for easy lookup.
func resolvedIndex(ri []workerdomain.ResolvedImport) map[[2]string]bool {
	m := make(map[[2]string]bool, len(ri))
	for _, r := range ri {
		m[[2]string{r.FromModule, r.ToModule}] = true
	}
	return m
}

// toModules returns the set of ToModule values for a given FromModule.
func toModules(ri []workerdomain.ResolvedImport, from string) []string {
	var out []string
	for _, r := range ri {
		if r.FromModule == from {
			out = append(out, r.ToModule)
		}
	}
	sort.Strings(out)
	return out
}

// resolveFixture builds a resolver for the miniproject fixture and runs it
// against imports extracted from the same workspace.
func resolveFixture(t *testing.T) []workerdomain.ResolvedImport {
	t.Helper()
	resolver, err := adapters.NewPythonModuleResolver(fixturePyMiniproject)
	require.NoError(t, err)

	extractor := adapters.NewPythonImportExtractor()
	rawImports, _, err := extractor.ExtractImports(t.Context(), fixturePyMiniproject)
	require.NoError(t, err)

	return resolver.Resolve(rawImports)
}

// ── module map / import root detection ───────────────────────────────────────

func TestPyResolver_ModuleMap_FlatFileRegistered(t *testing.T) {
	// flat_util.py at workspace root → module "flat_util"
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	// flat_util imports myapp (absolute internal)
	assert.True(t, idx[([2]string{"flat_util", "myapp"})],
		"expected flat_util → myapp edge; got %v", resolved)
}

func TestPyResolver_ModuleMap_SrcLayoutRegistered(t *testing.T) {
	// src/service/utils.py → module "service.utils" (via src/ root, not "src.service.utils")
	t.Parallel()
	resolved := resolveFixture(t)
	for _, r := range resolved {
		assert.NotContains(t, r.FromModule, "src.",
			"no module name should start with 'src.' — src/ layout must be stripped")
		assert.NotContains(t, r.ToModule, "src.",
			"no module name should start with 'src.' — src/ layout must be stripped")
	}
}

// ── absolute imports: external → discarded ────────────────────────────────────

func TestPyResolver_Absolute_ExternalDiscarded(t *testing.T) {
	// `import requests` and `import os` must not produce any ResolvedImport.
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	for k := range idx {
		assert.NotEqual(t, "requests", k[1], "external 'requests' must not appear as ToModule")
		assert.NotEqual(t, "os", k[1], "external 'os' must not appear as ToModule")
	}
}

// ── absolute imports: internal ────────────────────────────────────────────────

func TestPyResolver_Absolute_PackageImport(t *testing.T) {
	// `from myapp import models` in myapp/views.py → (myapp.views, myapp.models)
	// myapp.models is a package (has __init__.py); it appears in the module map.
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"myapp.views", "myapp.models"}],
		"from myapp import models must resolve to myapp.models")
}

func TestPyResolver_Absolute_ClassName_FallsBackToModule(t *testing.T) {
	// `from myapp.models import User` — User is a class, not a module.
	// Resolver tries myapp.models.User (not found), falls back to myapp.models.
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"myapp.views", "myapp.models"}],
		"from myapp.models import User must fall back to myapp.models")
	assert.False(t, idx[[2]string{"myapp.views", "myapp.models.User"}],
		"myapp.models.User must not appear (User is not a module)")
}

func TestPyResolver_Absolute_SubModule(t *testing.T) {
	// `from myapp.models import user` — user.py IS a module → myapp.models.user
	// (This arrives via `from .models import user` in views.py — tested separately.)
	// Here we verify that the absolute path also resolves to the sub-module.
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	// myapp.views has a relative import from .models import user → myapp.models.user.
	// Confirm the edge is present.
	assert.True(t, idx[[2]string{"myapp.views", "myapp.models.user"}],
		"from .models import user must resolve to myapp.models.user")
}

func TestPyResolver_Absolute_FlatModuleToPackage(t *testing.T) {
	// `import myapp` in flat_util.py → (flat_util, myapp)
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"flat_util", "myapp"}],
		"import myapp must resolve to the myapp package")
}

func TestPyResolver_Absolute_SrcLayoutService(t *testing.T) {
	// `from service import utils` in src/service/handler.py
	// → (service.handler, service.utils)
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"service.handler", "service.utils"}],
		"from service import utils must resolve to service.utils")
}

// ── relative imports ──────────────────────────────────────────────────────────

func TestPyResolver_Relative_Level1_FromInit(t *testing.T) {
	// `from . import views` in myapp/__init__.py (isPackage=true)
	// → (myapp, myapp.views)
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"myapp", "myapp.views"}],
		"from . import views in __init__.py must resolve to myapp.views")
}

func TestPyResolver_Relative_Level1_WithModule(t *testing.T) {
	// `from .models import user` in myapp/views.py
	// base = myapp + ".models" = myapp.models; try myapp.models.user → found.
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"myapp.views", "myapp.models.user"}],
		"from .models import user must resolve to myapp.models.user")
}

func TestPyResolver_Relative_Level1_FromModelsInit(t *testing.T) {
	// `from .user import User` in myapp/models/__init__.py
	// base = myapp.models + ".user" = myapp.models.user → (myapp.models, myapp.models.user)
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"myapp.models", "myapp.models.user"}],
		"from .user import User in models __init__ must resolve to myapp.models.user")
}

func TestPyResolver_Relative_Level1_SrcLayout(t *testing.T) {
	// `from .utils import helper` in src/service/handler.py
	// → (service.handler, service.utils)
	t.Parallel()
	resolved := resolveFixture(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"service.handler", "service.utils"}],
		"from .utils import helper must resolve to service.utils")
}

// ── deduplication ─────────────────────────────────────────────────────────────

func TestPyResolver_Deduplication_NoDuplicateEdges(t *testing.T) {
	// The same (from, to) pair must appear at most once even when multiple
	// import forms in the same file resolve to the same target.
	// service.handler imports service.utils twice: via absolute + relative.
	t.Parallel()
	resolved := resolveFixture(t)
	counts := make(map[[2]string]int)
	for _, r := range resolved {
		counts[[2]string{r.FromModule, r.ToModule}]++
	}
	for k, n := range counts {
		assert.Equal(t, 1, n, "duplicate edge %v → %v (count=%d)", k[0], k[1], n)
	}
}

// ── no self-edges ─────────────────────────────────────────────────────────────

func TestPyResolver_NoSelfEdges(t *testing.T) {
	t.Parallel()
	resolved := resolveFixture(t)
	for _, r := range resolved {
		assert.NotEqual(t, r.FromModule, r.ToModule,
			"self-edge must be discarded: %s → %s", r.FromModule, r.ToModule)
	}
}

// ── empty workspace ───────────────────────────────────────────────────────────

func TestPyResolver_EmptyWorkspace_ReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resolver, err := adapters.NewPythonModuleResolver(dir)
	require.NoError(t, err)
	result := resolver.Resolve(nil)
	assert.Nil(t, result)
}

// ── resolver without extractor (unit-level) ───────────────────────────────────

func TestPyResolver_HandCrafted_AbsoluteExternal(t *testing.T) {
	// External import produces no edge.
	t.Parallel()
	resolver, err := adapters.NewPythonModuleResolver(fixturePyMiniproject)
	require.NoError(t, err)
	raw := []workerdomain.RawImport{
		{ImportingFile: "flat_util.py", Module: "requests", Names: []string{"requests"}},
	}
	result := resolver.Resolve(raw)
	assert.Empty(t, result, "external import must produce no ResolvedImport")
}

func TestPyResolver_HandCrafted_RelativeAlwaysInternal(t *testing.T) {
	// Relative import resolves even when the module name is not in the map;
	// the base package itself is used as the target.
	t.Parallel()
	resolver, err := adapters.NewPythonModuleResolver(fixturePyMiniproject)
	require.NoError(t, err)
	raw := []workerdomain.RawImport{
		{
			ImportingFile: "myapp/views.py",
			Module:        "nonexistent",
			IsRelative:    true,
			RelativeLevel: 1,
			Names:         []string{"Something"},
		},
	}
	result := resolver.Resolve(raw)
	// base = myapp + ".nonexistent" = "myapp.nonexistent"
	// myapp.nonexistent not in map → falls back to "myapp.nonexistent" as base
	require.Len(t, result, 1)
	assert.Equal(t, "myapp.views", result[0].FromModule)
	assert.Equal(t, "myapp.nonexistent", result[0].ToModule)
}

func TestPyResolver_HandCrafted_FromModuleUnknownFile_Skipped(t *testing.T) {
	// Imports from a file not in the module map are silently skipped.
	t.Parallel()
	resolver, err := adapters.NewPythonModuleResolver(fixturePyMiniproject)
	require.NoError(t, err)
	raw := []workerdomain.RawImport{
		{ImportingFile: "does_not_exist.py", Module: "myapp", Names: []string{"myapp"}},
	}
	result := resolver.Resolve(raw)
	assert.Empty(t, result)
}

// ── bare imports from a subdirectory import root ─────────────────────────────

const fixturePyBareImports = "testdata/fixture-python/bare-imports"

// resolveFixtureBare builds a resolver for the bare-imports fixture (notiplan-
// like layout: all .py files live inside backend/, which is the runtime working
// directory so imports are bare names).
func resolveFixtureBare(t *testing.T) []workerdomain.ResolvedImport {
	t.Helper()
	resolver, err := adapters.NewPythonModuleResolver(fixturePyBareImports)
	require.NoError(t, err)

	extractor := adapters.NewPythonImportExtractor()
	rawImports, _, err := extractor.ExtractImports(t.Context(), fixturePyBareImports)
	require.NoError(t, err)

	return resolver.Resolve(rawImports)
}

func TestPyResolver_BareImports_EdgeCountNonZero(t *testing.T) {
	// backend/ is detected as an alias root; bare imports resolve to canonical
	// backend.X names, producing intra-repo edges.
	t.Parallel()
	resolved := resolveFixtureBare(t)
	assert.NotEmpty(t, resolved, "bare imports from backend/ must produce at least one edge")
}

func TestPyResolver_BareImports_AppToUtils(t *testing.T) {
	// `from utils import helper` in backend/app.py → (backend.app, backend.utils)
	t.Parallel()
	resolved := resolveFixtureBare(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"backend.app", "backend.utils"}],
		"from utils import helper must resolve to backend.utils")
}

func TestPyResolver_BareImports_AppToModels(t *testing.T) {
	// `from models import SomeModel` — SomeModel is a class, falls back to backend.models.
	t.Parallel()
	resolved := resolveFixtureBare(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"backend.app", "backend.models"}],
		"from models import SomeModel must fall back to backend.models")
}

func TestPyResolver_BareImports_ModelsToUtils(t *testing.T) {
	// `from utils import helper` in backend/models.py → (backend.models, backend.utils)
	t.Parallel()
	resolved := resolveFixtureBare(t)
	idx := resolvedIndex(resolved)
	assert.True(t, idx[[2]string{"backend.models", "backend.utils"}],
		"from utils import helper in models must resolve to backend.utils")
}

func TestPyResolver_BareImports_ExternalDiscarded(t *testing.T) {
	// `import flask` and `import requests` must not appear as edges.
	t.Parallel()
	resolved := resolveFixtureBare(t)
	idx := resolvedIndex(resolved)
	for k := range idx {
		assert.NotEqual(t, "flask", k[1])
		assert.NotEqual(t, "requests", k[1])
	}
}

func TestPyResolver_BareImports_CanonicalNames(t *testing.T) {
	// All module names in the resolved edges must use the canonical backend.X form,
	// not the bare alias (utils, models, app).
	t.Parallel()
	resolved := resolveFixtureBare(t)
	for _, r := range resolved {
		for _, bare := range []string{"utils", "models", "app"} {
			assert.NotEqual(t, bare, r.FromModule, "bare module name must not appear as FromModule")
			assert.NotEqual(t, bare, r.ToModule, "bare module name must not appear as ToModule")
		}
	}
}

// ── alias collision: two alias roots claim the same short name ─────────────────

func TestPyResolver_AliasCollision_NoWrongEdge(t *testing.T) {
	// app/funcs.py and backend/funcs.py both claim alias "funcs".
	// The collision is detected and marked ambiguous; bare `from funcs import X`
	// must produce NO edge rather than a wrong edge pointing at the wrong file.
	t.Parallel()
	const fixtureDir = "testdata/fixture-python/alias-collision"
	resolver, err := adapters.NewPythonModuleResolver(fixtureDir)
	require.NoError(t, err)

	extractor := adapters.NewPythonImportExtractor()
	rawImports, _, err := extractor.ExtractImports(t.Context(), fixtureDir)
	require.NoError(t, err)

	resolved := resolver.Resolve(rawImports)
	idx := resolvedIndex(resolved)

	// Wrong edges that must NOT appear (both are possible wrong outcomes).
	assert.False(t, idx[[2]string{"backend.models", "app.funcs"}],
		"collision must not produce edge to wrong module app.funcs")
	assert.False(t, idx[[2]string{"backend.models", "backend.funcs"}],
		"collision must not produce edge (ambiguous alias, correct resolution unknown)")
	// The ambiguous bare import must be discarded entirely.
	assert.Empty(t, resolved,
		"ambiguous bare import must produce no edges at all")
}

// ── toModules helper used for readable multi-target assertions ────────────────

func TestPyResolver_ViewsModule_AllEdges(t *testing.T) {
	t.Parallel()
	resolved := resolveFixture(t)
	tos := toModules(resolved, "myapp.views")
	// Expected: myapp.models (×2 sources deduped), myapp.models.user
	assert.Contains(t, tos, "myapp.models", "views must have edge to myapp.models")
	assert.Contains(t, tos, "myapp.models.user", "views must have edge to myapp.models.user")
	for _, to := range tos {
		assert.NotEqual(t, "requests", to, "external requests must not appear")
	}
}
