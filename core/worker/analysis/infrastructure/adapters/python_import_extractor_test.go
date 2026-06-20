package adapters_test

import (
	"context"
	"testing"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fixturePyBasic    = "testdata/fixture-python/basic"
	fixturePyRelative = "testdata/fixture-python/relative"
	fixturePyFlask    = "testdata/fixture-python/flask"
)

func newExtractor() *adapters.PythonImportExtractor {
	return adapters.NewPythonImportExtractor()
}

// indexByModule returns a map from Module → []RawImport for easy test lookups.
func indexByModule(imports []workerdomain.RawImport) map[string][]workerdomain.RawImport {
	m := make(map[string][]workerdomain.RawImport)
	for _, imp := range imports {
		m[imp.Module] = append(m[imp.Module], imp)
	}
	return m
}

// firstByModule returns the first RawImport with the given module, or a zero value.
func firstByModule(imports []workerdomain.RawImport, module string) (workerdomain.RawImport, bool) {
	for _, imp := range imports {
		if imp.Module == module {
			return imp, true
		}
	}
	return workerdomain.RawImport{}, false
}

// ── import_statement: `import x` forms ───────────────────────────────────────

func TestPyExtractor_ImportSimple(t *testing.T) {
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyBasic)
	require.NoError(t, err)
	imp, ok := firstByModule(imports, "os")
	require.True(t, ok, "expected import for 'os'")
	assert.False(t, imp.IsRelative)
	assert.Equal(t, 0, imp.RelativeLevel)
	assert.Equal(t, []string{"os"}, imp.Names)
}

func TestPyExtractor_ImportDottedName(t *testing.T) {
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyBasic)
	require.NoError(t, err)
	imp, ok := firstByModule(imports, "a.b.c")
	require.True(t, ok, "expected import for 'a.b.c'")
	assert.False(t, imp.IsRelative)
	assert.Equal(t, []string{"a.b.c"}, imp.Names)
}

func TestPyExtractor_ImportAlias(t *testing.T) {
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyBasic)
	require.NoError(t, err)
	// `import a.b.c as abc` → Module="a.b.c", Names=["abc"]
	imp, ok := firstByModule(imports, "a.b.c")
	require.True(t, ok)
	// There are two entries for a.b.c: the plain import and the aliased one.
	byMod := indexByModule(imports)
	abcEntries := byMod["a.b.c"]
	require.Len(t, abcEntries, 2)
	names := make(map[string]bool)
	for _, e := range abcEntries {
		for _, n := range e.Names {
			names[n] = true
		}
	}
	assert.True(t, names["a.b.c"], "plain import should have Names=[a.b.c]")
	assert.True(t, names["abc"], "aliased import should have Names=[abc]")
	_ = imp
}

func TestPyExtractor_ImportMultiple(t *testing.T) {
	// `import alpha, beta` → two separate RawImport entries.
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyBasic)
	require.NoError(t, err)
	_, hasAlpha := firstByModule(imports, "alpha")
	_, hasBeta := firstByModule(imports, "beta")
	assert.True(t, hasAlpha, "expected import for 'alpha'")
	assert.True(t, hasBeta, "expected import for 'beta'")
}

// ── import_from_statement: `from x import y` forms ───────────────────────────

func TestPyExtractor_FromImport_SingleName(t *testing.T) {
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyBasic)
	require.NoError(t, err)
	// from a.b import c — first occurrence
	byMod := indexByModule(imports)
	abEntries := byMod["a.b"]
	require.NotEmpty(t, abEntries)
	// At least one entry with Names containing "c".
	found := false
	for _, e := range abEntries {
		for _, n := range e.Names {
			if n == "c" {
				found = true
			}
		}
	}
	assert.True(t, found, "expected Names to contain 'c' for 'from a.b import c'")
}

func TestPyExtractor_FromImport_MultipleNames(t *testing.T) {
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyBasic)
	require.NoError(t, err)
	// `from a.b import c, d` → Module="a.b", Names contains "c" and "d"
	byMod := indexByModule(imports)
	abEntries := byMod["a.b"]
	require.NotEmpty(t, abEntries)
	collected := make(map[string]bool)
	for _, e := range abEntries {
		for _, n := range e.Names {
			collected[n] = true
		}
	}
	assert.True(t, collected["c"], "expected 'c' in names for 'from a.b import c, d'")
	assert.True(t, collected["d"], "expected 'd' in names for 'from a.b import c, d'")
}

func TestPyExtractor_FromImport_Parenthesized(t *testing.T) {
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyBasic)
	require.NoError(t, err)
	// `from a.b import (c, d)` — same output as the comma form
	byMod := indexByModule(imports)
	abEntries := byMod["a.b"]
	collected := make(map[string]bool)
	for _, e := range abEntries {
		for _, n := range e.Names {
			collected[n] = true
		}
	}
	assert.True(t, collected["c"] && collected["d"],
		"parenthesized import must produce the same names as comma form")
}

func TestPyExtractor_FromImport_Alias(t *testing.T) {
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyBasic)
	require.NoError(t, err)
	// `from a.b import c as ce, d` → Names should contain "ce" (alias) and "d"
	byMod := indexByModule(imports)
	abEntries := byMod["a.b"]
	collected := make(map[string]bool)
	for _, e := range abEntries {
		for _, n := range e.Names {
			collected[n] = true
		}
	}
	assert.True(t, collected["ce"], "aliased name 'ce' must appear in Names")
}

func TestPyExtractor_FromImport_OsPath(t *testing.T) {
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyBasic)
	require.NoError(t, err)
	imp, ok := firstByModule(imports, "os.path")
	require.True(t, ok, "expected import for 'os.path'")
	assert.False(t, imp.IsRelative)
	assert.Contains(t, imp.Names, "join")
	assert.Contains(t, imp.Names, "exists")
}

// ── relative imports ──────────────────────────────────────────────────────────

func TestPyExtractor_Relative_Level1_Bare(t *testing.T) {
	t.Parallel()
	// `from . import utils` → IsRelative=true, RelativeLevel=1, Module="", Names=["utils"]
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyRelative)
	require.NoError(t, err)

	var found *workerdomain.RawImport
	for i := range imports {
		if imports[i].IsRelative && imports[i].RelativeLevel == 1 && imports[i].Module == "" {
			found = &imports[i]
			break
		}
	}
	require.NotNil(t, found, "expected bare level-1 relative import (from . import utils)")
	assert.Equal(t, []string{"utils"}, found.Names)
}

func TestPyExtractor_Relative_Level2_Bare(t *testing.T) {
	t.Parallel()
	// `from .. import config` → IsRelative=true, RelativeLevel=2, Module="", Names=["config"]
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyRelative)
	require.NoError(t, err)

	var found *workerdomain.RawImport
	for i := range imports {
		if imports[i].IsRelative && imports[i].RelativeLevel == 2 && imports[i].Module == "" {
			found = &imports[i]
			break
		}
	}
	require.NotNil(t, found, "expected bare level-2 relative import (from .. import config)")
	assert.Equal(t, []string{"config"}, found.Names)
}

func TestPyExtractor_Relative_Level1_WithModule(t *testing.T) {
	t.Parallel()
	// `from .models import User` → IsRelative=true, RelativeLevel=1, Module="models", Names=["User"]
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyRelative)
	require.NoError(t, err)

	var found *workerdomain.RawImport
	for i := range imports {
		if imports[i].IsRelative && imports[i].RelativeLevel == 1 && imports[i].Module == "models" {
			found = &imports[i]
			break
		}
	}
	require.NotNil(t, found, "expected relative import with module 'models'")
	assert.Equal(t, []string{"User"}, found.Names)
}

func TestPyExtractor_Relative_Level2_WithModule(t *testing.T) {
	t.Parallel()
	// `from ..pkg import helpers` → IsRelative=true, RelativeLevel=2, Module="pkg"
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyRelative)
	require.NoError(t, err)

	var found *workerdomain.RawImport
	for i := range imports {
		if imports[i].IsRelative && imports[i].RelativeLevel == 2 && imports[i].Module == "pkg" {
			found = &imports[i]
			break
		}
	}
	require.NotNil(t, found, "expected relative import with module 'pkg' at level 2")
	assert.Equal(t, []string{"helpers"}, found.Names)
}

func TestPyExtractor_Relative_Level3(t *testing.T) {
	t.Parallel()
	// `from ...deep import constants` → RelativeLevel=3, Module="deep"
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyRelative)
	require.NoError(t, err)

	var found *workerdomain.RawImport
	for i := range imports {
		if imports[i].IsRelative && imports[i].RelativeLevel == 3 && imports[i].Module == "deep" {
			found = &imports[i]
			break
		}
	}
	require.NotNil(t, found, "expected relative import with module 'deep' at level 3")
	assert.Equal(t, []string{"constants"}, found.Names)
}

func TestPyExtractor_Relative_IsRelativeFlag(t *testing.T) {
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyRelative)
	require.NoError(t, err)
	for _, imp := range imports {
		assert.True(t, imp.IsRelative, "all imports in the relative fixture must be relative: %+v", imp)
	}
}

// ── ImportingFile ─────────────────────────────────────────────────────────────

func TestPyExtractor_ImportingFile_RelativePath(t *testing.T) {
	t.Parallel()
	imports, _, err := newExtractor().ExtractImports(context.Background(), fixturePyBasic)
	require.NoError(t, err)
	require.NotEmpty(t, imports)
	// All ImportingFile values must be relative paths (no leading slash).
	for _, imp := range imports {
		assert.False(t, len(imp.ImportingFile) > 0 && imp.ImportingFile[0] == '/',
			"ImportingFile must be relative, got: %s", imp.ImportingFile)
	}
}

// ── Flask blueprint signals ───────────────────────────────────────────────────

func TestPyExtractor_Blueprint_DetectsTwoBlueprints(t *testing.T) {
	t.Parallel()
	_, blueprints, err := newExtractor().ExtractImports(context.Background(), fixturePyFlask)
	require.NoError(t, err)
	assert.Len(t, blueprints, 2, "expected two Blueprint() definitions")
}

func TestPyExtractor_Blueprint_MainName(t *testing.T) {
	t.Parallel()
	_, blueprints, err := newExtractor().ExtractImports(context.Background(), fixturePyFlask)
	require.NoError(t, err)
	byName := make(map[string]workerdomain.BlueprintInfo)
	for _, bp := range blueprints {
		byName[bp.Name] = bp
	}
	main, ok := byName["main"]
	require.True(t, ok, "expected blueprint named 'main'")
	assert.Equal(t, "/", main.URLPrefix)
}

func TestPyExtractor_Blueprint_APIName(t *testing.T) {
	t.Parallel()
	_, blueprints, err := newExtractor().ExtractImports(context.Background(), fixturePyFlask)
	require.NoError(t, err)
	byName := make(map[string]workerdomain.BlueprintInfo)
	for _, bp := range blueprints {
		byName[bp.Name] = bp
	}
	api, ok := byName["api"]
	require.True(t, ok, "expected blueprint named 'api'")
	assert.Equal(t, "/api", api.URLPrefix)
}

func TestPyExtractor_Blueprint_FileIsSet(t *testing.T) {
	t.Parallel()
	_, blueprints, err := newExtractor().ExtractImports(context.Background(), fixturePyFlask)
	require.NoError(t, err)
	for _, bp := range blueprints {
		assert.NotEmpty(t, bp.File, "Blueprint.File must be set for: %s", bp.Name)
	}
}

// ── empty workspace ───────────────────────────────────────────────────────────

func TestPyExtractor_EmptyDir_ReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	imports, blueprints, err := newExtractor().ExtractImports(context.Background(), dir)
	require.NoError(t, err)
	assert.Nil(t, imports)
	assert.Nil(t, blueprints)
}
