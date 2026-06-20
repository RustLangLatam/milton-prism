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
	fixtureNpmWithLockfile = "testdata/fixture-npm/with-lockfile"
	fixtureNpmJsonOnly     = "testdata/fixture-npm/json-only"
)

func newNpmParser() *adapters.NpmManifestParser {
	return adapters.NewNpmManifestParser()
}

func npmDepsByName(deps []workerdomain.Dependency) map[string]workerdomain.Dependency {
	m := make(map[string]workerdomain.Dependency, len(deps))
	for _, d := range deps {
		m[d.Package] = d
	}
	return m
}

// ── lock file (preferred path) ────────────────────────────────────────────────

func TestNpm_WithLock_ReturnsProdDepsOnly(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmWithLockfile, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	// 3 prod packages in the lock; 2 dev excluded.
	assert.Len(t, deps, 3)
}

func TestNpm_WithLock_ExcludesJest(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmWithLockfile, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	m := npmDepsByName(deps)
	assert.NotContains(t, m, "jest", "dev dependency must be excluded")
}

func TestNpm_WithLock_ExcludesTypesNode(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmWithLockfile, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	m := npmDepsByName(deps)
	assert.NotContains(t, m, "@types/node", "dev dependency must be excluded")
}

func TestNpm_WithLock_PinnedVersionsFromLock(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmWithLockfile, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	m := npmDepsByName(deps)
	// Lock has resolved versions, not the constraints in package.json.
	assert.Equal(t, "4.18.2", m["express"].Version)
	assert.Equal(t, "4.17.21", m["lodash"].Version)
	assert.Equal(t, "8.11.3", m["pg"].Version)
}

func TestNpm_WithLock_ExpressIsFramework(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmWithLockfile, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	m := npmDepsByName(deps)
	assert.Equal(t, "framework", m["express"].Category)
}

func TestNpm_WithLock_LibraryCategory(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmWithLockfile, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	m := npmDepsByName(deps)
	assert.Equal(t, "library", m["lodash"].Category)
	assert.Equal(t, "library", m["pg"].Category)
}

func TestNpm_WithLock_EcosystemIsNpm(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmWithLockfile, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	for _, d := range deps {
		assert.Equal(t, workerdomain.EcosystemNpm, d.Ecosystem)
	}
}

func TestNpm_WithLock_LockTakesPriorityOverJson(t *testing.T) {
	t.Parallel()
	// package.json declares express "^4.18.0"; lock pins it to "4.18.2".
	// If json were parsed, stripNpmConstraint("^4.18.0") → "4.18.0" (not "4.18.2").
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmWithLockfile, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	m := npmDepsByName(deps)
	assert.Equal(t, "4.18.2", m["express"].Version,
		"must be the pinned lock version, not the stripped json constraint")
}

// ── json-only fallback ────────────────────────────────────────────────────────

func TestNpm_JsonOnly_ParsesDependencies(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmJsonOnly, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	// react and axios from dependencies; typescript from devDependencies excluded.
	assert.Len(t, deps, 2)
	m := npmDepsByName(deps)
	assert.Contains(t, m, "react")
	assert.Contains(t, m, "axios")
}

func TestNpm_JsonOnly_SkipsDevDeps(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmJsonOnly, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	m := npmDepsByName(deps)
	assert.NotContains(t, m, "typescript", "devDependencies must be excluded")
}

func TestNpm_JsonOnly_StripsCaretConstraint(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmJsonOnly, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	m := npmDepsByName(deps)
	// "^18.2.0" → "18.2.0", "^1.6.0" → "1.6.0"
	assert.Equal(t, "18.2.0", m["react"].Version)
	assert.Equal(t, "1.6.0", m["axios"].Version)
}

func TestNpm_JsonOnly_ReactIsFramework(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmJsonOnly, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	m := npmDepsByName(deps)
	assert.Equal(t, "framework", m["react"].Category)
}

func TestNpm_JsonOnly_AxiosIsLibrary(t *testing.T) {
	t.Parallel()
	deps, err := newNpmParser().Parse(context.Background(), fixtureNpmJsonOnly, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	m := npmDepsByName(deps)
	assert.Equal(t, "library", m["axios"].Category)
}

// ── no manifest ───────────────────────────────────────────────────────────────

func TestNpm_NoFiles_ReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	deps, err := newNpmParser().Parse(context.Background(), dir, workerdomain.EcosystemNpm)
	require.NoError(t, err)
	assert.Nil(t, deps)
}
