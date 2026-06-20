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
	fixtureRubyGemsWithLockfile = "testdata/fixture-rubygems/with-lockfile"
	fixtureRubyGemsGemfileOnly  = "testdata/fixture-rubygems/gemfile-only"
)

func newRubyGemsParser() *adapters.RubyGemsManifestParser {
	return adapters.NewRubyGemsManifestParser()
}

func rubyDepsByName(deps []workerdomain.Dependency) map[string]workerdomain.Dependency {
	m := make(map[string]workerdomain.Dependency, len(deps))
	for _, d := range deps {
		m[d.Package] = d
	}
	return m
}

// ── Gemfile.lock (preferred path) ─────────────────────────────────────────────

func TestRubyGems_WithLock_ReturnsProdGems(t *testing.T) {
	t.Parallel()
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsWithLockfile, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	// Specs: pg, rails, railties, rake, rspec-rails, rspec-core.
	// rspec-rails is in :test group → excluded.
	// rspec-core is a transitive of rspec-rails. Tier 1 does not trace
	// test transitives, so it may appear; the critical assertion is that
	// rspec-rails itself (the test direct dep) is excluded.
	m := rubyDepsByName(deps)
	assert.Contains(t, m, "rails")
	assert.Contains(t, m, "pg")
}

func TestRubyGems_WithLock_ExcludesDirectDevGem(t *testing.T) {
	t.Parallel()
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsWithLockfile, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	m := rubyDepsByName(deps)
	assert.NotContains(t, m, "rspec-rails",
		"direct test gem from Gemfile :test group must be excluded")
}

func TestRubyGems_WithLock_PinnedVersionFromLock(t *testing.T) {
	t.Parallel()
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsWithLockfile, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	m := rubyDepsByName(deps)
	// Gemfile declares "~> 7.1" → stripped = "7.1"; lock pins to "7.1.3".
	assert.Equal(t, "7.1.3", m["rails"].Version,
		"must be the pinned lock version, not the stripped Gemfile constraint")
	assert.Equal(t, "1.5.6", m["pg"].Version)
}

func TestRubyGems_WithLock_LockTakesPriorityOverGemfile(t *testing.T) {
	t.Parallel()
	// If only the Gemfile were parsed, rails version would be "7.1" (stripped ~> 7.1).
	// The lock gives "7.1.3". Verifies lock path is taken.
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsWithLockfile, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	m := rubyDepsByName(deps)
	assert.Equal(t, "7.1.3", m["rails"].Version)
}

func TestRubyGems_WithLock_RailsIsFramework(t *testing.T) {
	t.Parallel()
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsWithLockfile, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	m := rubyDepsByName(deps)
	assert.Equal(t, "framework", m["rails"].Category)
}

func TestRubyGems_WithLock_PgIsLibrary(t *testing.T) {
	t.Parallel()
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsWithLockfile, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	m := rubyDepsByName(deps)
	assert.Equal(t, "library", m["pg"].Category)
}

func TestRubyGems_WithLock_EcosystemIsRubyGems(t *testing.T) {
	t.Parallel()
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsWithLockfile, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	for _, d := range deps {
		assert.Equal(t, workerdomain.EcosystemRubyGems, d.Ecosystem)
	}
}

// ── Gemfile-only fallback ─────────────────────────────────────────────────────

func TestRubyGems_GemfileOnly_ParsesProdGems(t *testing.T) {
	t.Parallel()
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsGemfileOnly, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	// sinatra and sequel from top level; minitest from :development,:test excluded.
	assert.Len(t, deps, 2)
	m := rubyDepsByName(deps)
	assert.Contains(t, m, "sinatra")
	assert.Contains(t, m, "sequel")
}

func TestRubyGems_GemfileOnly_ExcludesDevTestGems(t *testing.T) {
	t.Parallel()
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsGemfileOnly, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	m := rubyDepsByName(deps)
	assert.NotContains(t, m, "minitest", "gems in :development, :test group must be excluded")
}

func TestRubyGems_GemfileOnly_StripsConstraint(t *testing.T) {
	t.Parallel()
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsGemfileOnly, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	m := rubyDepsByName(deps)
	// "~> 3.1" → "3.1", "~> 5.78" → "5.78"
	assert.Equal(t, "3.1", m["sinatra"].Version)
	assert.Equal(t, "5.78", m["sequel"].Version)
}

func TestRubyGems_GemfileOnly_SinatraIsFramework(t *testing.T) {
	t.Parallel()
	deps, err := newRubyGemsParser().Parse(context.Background(), fixtureRubyGemsGemfileOnly, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	m := rubyDepsByName(deps)
	assert.Equal(t, "framework", m["sinatra"].Category)
}

// ── no manifest ───────────────────────────────────────────────────────────────

func TestRubyGems_NoFiles_ReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	deps, err := newRubyGemsParser().Parse(context.Background(), dir, workerdomain.EcosystemRubyGems)
	require.NoError(t, err)
	assert.Nil(t, deps)
}
