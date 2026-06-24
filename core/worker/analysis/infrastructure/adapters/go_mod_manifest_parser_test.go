package adapters_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func goDepsByName(deps []workerdomain.Dependency) map[string]workerdomain.Dependency {
	m := make(map[string]workerdomain.Dependency, len(deps))
	for _, d := range deps {
		m[d.Package] = d
	}
	return m
}

// TestGoMod_ParsesDirectDeps_SkipsIndirect verifies the fixture go.mod yields
// only direct requires (block + single-line), with exact versions and the Go
// ecosystem, while // indirect entries are excluded.
func TestGoMod_ParsesDirectDeps_SkipsIndirect(t *testing.T) {
	t.Parallel()
	p := adapters.NewGoModManifestParser()
	deps, err := p.Parse(context.Background(), fixtureGo, workerdomain.EcosystemGoModules)
	require.NoError(t, err)

	m := goDepsByName(deps)

	// Direct: gin, lib/pq (block) + google/uuid (single-line require) = 3.
	require.Len(t, deps, 3)

	gin, ok := m["github.com/gin-gonic/gin"]
	require.True(t, ok)
	assert.Equal(t, "v1.10.0", gin.Version)
	assert.Equal(t, workerdomain.EcosystemGoModules, gin.Ecosystem)
	assert.False(t, gin.Approximate, "go.mod versions are exact")

	assert.Contains(t, m, "github.com/lib/pq")
	assert.Contains(t, m, "github.com/google/uuid")

	// Indirect deps must be excluded.
	assert.NotContains(t, m, "github.com/bytedance/sonic", "// indirect dep must be skipped")
	assert.NotContains(t, m, "golang.org/x/sys", "// indirect dep must be skipped")
}

// TestGoMod_AbsentReturnsNil verifies the absent-manifest contract: no go.mod
// → (nil, nil), never an error.
func TestGoMod_AbsentReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := adapters.NewGoModManifestParser()
	deps, err := p.Parse(context.Background(), dir, workerdomain.EcosystemGoModules)
	require.NoError(t, err)
	assert.Nil(t, deps)
}

// TestGoMod_OSVEcosystemStringIsGo pins that the ecosystem string handed to OSV
// is literally "Go" (OSV's Go-modules identifier).
func TestGoMod_OSVEcosystemStringIsGo(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Go", string(workerdomain.EcosystemGoModules))
}

// TestGoMod_SingleLineOnly exercises a go.mod with only single-line requires.
func TestGoMod_SingleLineOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "module example.com/x\n\ngo 1.25.0\n\nrequire github.com/foo/bar v0.1.0\nrequire github.com/baz/qux v2.3.4 // indirect\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644))

	p := adapters.NewGoModManifestParser()
	deps, err := p.Parse(context.Background(), dir, workerdomain.EcosystemGoModules)
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, "github.com/foo/bar", deps[0].Package)
	assert.Equal(t, "v0.1.0", deps[0].Version)
}
