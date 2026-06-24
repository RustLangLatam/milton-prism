package adapters_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoResolver_NoGoMod_NoEdges verifies the resolver's clean degradation when
// no go.mod is present: import paths cannot be classified as internal, so no
// intra-repo edges are produced (the unregistered-hole contract is preserved at
// the resolver level — ResolveImports returns nil, not an error).
func TestGoResolver_NoGoMod_NoEdges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pkg"), 0o755))
	src := `package pkg

import "example.com/other/dep"

var _ = dep.Thing
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg", "a.go"), []byte(src), 0o644))

	a := adapters.NewGoLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), dir)
	require.NoError(t, err)
	assert.Empty(t, edges, "without go.mod, no import can be classified as internal")
}

// TestGoResolver_SamePackageNoSelfEdge verifies that two files in the same
// directory (same package import path) referencing each other do not produce a
// self-edge (from == to is collapsed).
func TestGoResolver_SamePackageNoSelfEdge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/single\n\ngo 1.25.0\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "core"), 0o755))
	// Two files in package core; one imports model, the other does not.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "model"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model", "m.go"),
		[]byte("package model\n\ntype T struct{}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "core", "a.go"),
		[]byte("package core\n\nimport \"example.com/single/model\"\n\nvar _ = model.T{}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "core", "b.go"),
		[]byte("package core\n\nfunc Helper() {}\n"), 0o644))

	a := adapters.NewGoLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), dir)
	require.NoError(t, err)
	idx := goEdgeIndex(edges)

	assert.Equal(t, uint32(1), idx[[2]string{"example.com/single/core", "example.com/single/model"}])
	for k := range idx {
		assert.NotEqual(t, k[0], k[1], "same-package references must not produce a self-edge")
	}
}
