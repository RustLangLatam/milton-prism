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
	fixtureComposerWithLock = "testdata/fixture-composer/with-lock"
	fixtureComposerJsonOnly = "testdata/fixture-composer/json-only"
)

func newComposerParser() *adapters.ComposerManifestParser {
	return adapters.NewComposerManifestParser()
}

func depsByName(deps []workerdomain.Dependency) map[string]workerdomain.Dependency {
	m := make(map[string]workerdomain.Dependency, len(deps))
	for _, d := range deps {
		m[d.Package] = d
	}
	return m
}

// ── lock file (preferred path) ────────────────────────────────────────────────

func TestComposer_WithLock_ReturnsProdPackages(t *testing.T) {
	t.Parallel()
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerWithLock, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	// 4 prod packages (laravel, guzzle, symfony/console, codeigniter4); dev excluded.
	assert.Len(t, deps, 4)
}

func TestComposer_WithLock_ExcludesDevPackages(t *testing.T) {
	t.Parallel()
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerWithLock, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	m := depsByName(deps)
	assert.NotContains(t, m, "phpunit/phpunit", "dev dependency must be excluded")
}

func TestComposer_WithLock_PrefersResolvedVersions(t *testing.T) {
	t.Parallel()
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerWithLock, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	m := depsByName(deps)

	// Lock has the pinned version, not the constraint from composer.json.
	assert.Equal(t, "v11.0.0", m["laravel/framework"].Version,
		"version must come from lock (v11.0.0), not constraint (^11.0)")
	assert.Equal(t, "7.8.1", m["guzzlehttp/guzzle"].Version)
	assert.Equal(t, "v7.1.0", m["symfony/console"].Version)
}

func TestComposer_WithLock_FrameworkDetectedByName(t *testing.T) {
	t.Parallel()
	// laravel/framework and codeigniter4/framework both have type: "library" in
	// real composer.lock files; the name-based map must still yield "framework".
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerWithLock, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	m := depsByName(deps)
	assert.Equal(t, "framework", m["laravel/framework"].Category,
		"laravel/framework must be framework via name map even when type=library")
	assert.Equal(t, "framework", m["codeigniter4/framework"].Category,
		"codeigniter4/framework must be framework via name map even when type=library")
}

func TestComposer_WithLock_CategoryLibrary(t *testing.T) {
	t.Parallel()
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerWithLock, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	m := depsByName(deps)
	assert.Equal(t, "library", m["guzzlehttp/guzzle"].Category)
	assert.Equal(t, "library", m["symfony/console"].Category)
}

func TestComposer_WithLock_EcosystemIsComposer(t *testing.T) {
	t.Parallel()
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerWithLock, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	for _, d := range deps {
		assert.Equal(t, workerdomain.EcosystemComposer, d.Ecosystem)
	}
}

// ── json-only fallback ────────────────────────────────────────────────────────

func TestComposer_JsonOnly_ParsesRequireSection(t *testing.T) {
	t.Parallel()
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerJsonOnly, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	// symfony/framework-bundle and doctrine/orm (php, ext-json excluded)
	assert.Len(t, deps, 2)
	m := depsByName(deps)
	assert.Contains(t, m, "symfony/framework-bundle")
	assert.Contains(t, m, "doctrine/orm")
}

func TestComposer_JsonOnly_SkipsPlatformRequirements(t *testing.T) {
	t.Parallel()
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerJsonOnly, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	m := depsByName(deps)
	assert.NotContains(t, m, "php", "platform req php must be skipped")
	assert.NotContains(t, m, "ext-json", "platform req ext-json must be skipped")
}

func TestComposer_JsonOnly_SkipsDevSection(t *testing.T) {
	t.Parallel()
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerJsonOnly, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	m := depsByName(deps)
	assert.NotContains(t, m, "symfony/phpunit-bridge", "require-dev must be excluded")
}

func TestComposer_JsonOnly_StripsConstraintOperator(t *testing.T) {
	t.Parallel()
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerJsonOnly, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	m := depsByName(deps)
	// "^7.0" → "7.0"
	assert.Equal(t, "7.0", m["symfony/framework-bundle"].Version,
		"^ constraint must be stripped to yield a bare version")
	assert.Equal(t, "3.0", m["doctrine/orm"].Version)
}

func TestComposer_JsonOnly_FrameworkDetectedByName(t *testing.T) {
	t.Parallel()
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerJsonOnly, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	m := depsByName(deps)
	// symfony/framework-bundle is in the name map → "framework" even without lock type info.
	assert.Equal(t, "framework", m["symfony/framework-bundle"].Category,
		"symfony/framework-bundle must be detected as framework by name even in json-only mode")
	// doctrine/orm is not a framework.
	assert.Equal(t, "library", m["doctrine/orm"].Category)
}

// ── no manifest ───────────────────────────────────────────────────────────────

func TestComposer_NoFiles_ReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	deps, err := newComposerParser().Parse(context.Background(), dir, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	assert.Nil(t, deps)
}

// ── lockfile takes priority over json ─────────────────────────────────────────

func TestComposer_WithLock_LockTakesPriorityOverJson(t *testing.T) {
	t.Parallel()
	// The with-lock fixture has both files. The json requires laravel/framework ^11.0
	// but the lock pins it to v11.0.0. If json were parsed, version would be "11.0"
	// (stripped constraint). Lock version is "v11.0.0".
	deps, err := newComposerParser().Parse(context.Background(), fixtureComposerWithLock, workerdomain.EcosystemComposer)
	require.NoError(t, err)
	m := depsByName(deps)
	assert.Equal(t, "v11.0.0", m["laravel/framework"].Version,
		"must be the pinned lock version, not the stripped json constraint")
}
