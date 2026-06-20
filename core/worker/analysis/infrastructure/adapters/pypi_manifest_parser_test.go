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
	fixturePyPIWithPoetry       = "testdata/fixture-pypi/with-poetry"
	fixturePyPIRequirementsOnly = "testdata/fixture-pypi/requirements-only"
	fixturePyPICombined         = "testdata/fixture-pypi/combined-sources"
)

func newPyPIParser() *adapters.PyPIManifestParser {
	return adapters.NewPyPIManifestParser()
}

func pypiDepsByName(deps []workerdomain.Dependency) map[string]workerdomain.Dependency {
	m := make(map[string]workerdomain.Dependency, len(deps))
	for _, d := range deps {
		m[d.Package] = d
	}
	return m
}

// ── poetry.lock (preferred path) ──────────────────────────────────────────────

func TestPyPI_WithPoetry_ReturnsThreeProdPackages(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIWithPoetry, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	// django, requests, psycopg2 from groups=["main"]; pytest excluded (groups=["dev"]).
	assert.Len(t, deps, 3)
}

func TestPyPI_WithPoetry_ExcludesDevPackage(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIWithPoetry, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)
	assert.NotContains(t, m, "pytest", "dev-only package must be excluded")
}

func TestPyPI_WithPoetry_PinnedVersionsFromLock(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIWithPoetry, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)
	// Lock has resolved versions, not pyproject.toml constraints.
	assert.Equal(t, "4.2.13", m["django"].Version)
	assert.Equal(t, "2.31.0", m["requests"].Version)
	assert.Equal(t, "2.9.9", m["psycopg2"].Version)
}

func TestPyPI_WithPoetry_LockTakesPriorityOverPyproject(t *testing.T) {
	t.Parallel()
	// pyproject.toml declares django "^4.2"; lock pins it to "4.2.13".
	// stripPythonConstraint("^4.2") → "4.2". Lock gives "4.2.13".
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIWithPoetry, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)
	assert.Equal(t, "4.2.13", m["django"].Version,
		"must be the pinned lock version, not the stripped pyproject constraint")
}

func TestPyPI_WithPoetry_DjangoCategoryIsFramework(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIWithPoetry, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)
	assert.Equal(t, "framework", m["django"].Category)
}

func TestPyPI_WithPoetry_LibraryCategory(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIWithPoetry, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)
	assert.Equal(t, "library", m["requests"].Category)
	assert.Equal(t, "library", m["psycopg2"].Category)
}

func TestPyPI_WithPoetry_EcosystemIsPyPI(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIWithPoetry, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	for _, d := range deps {
		assert.Equal(t, workerdomain.EcosystemPyPI, d.Ecosystem)
	}
}

// ── requirements.txt fallback ─────────────────────────────────────────────────

func TestPyPI_RequirementsTxt_ReturnsThreePackages(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIRequirementsOnly, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	// flask, sqlalchemy, celery; comment/flag lines skipped.
	assert.Len(t, deps, 3)
}

func TestPyPI_RequirementsTxt_SkipsFlagLines(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIRequirementsOnly, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)
	// "-r requirements-dev.txt" and "--index-url" lines must not produce entries.
	assert.NotContains(t, m, "-r")
	assert.NotContains(t, m, "--index-url")
}

func TestPyPI_RequirementsTxt_PinOperatorStripped(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIRequirementsOnly, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)
	// "flask==2.3.3" → "2.3.3"
	assert.Equal(t, "2.3.3", m["flask"].Version)
}

func TestPyPI_RequirementsTxt_CompoundConstraintStrippedToLowerBound(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIRequirementsOnly, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)
	// "sqlalchemy>=2.0.0,<3.0.0" → lower bound "2.0.0"
	assert.Equal(t, "2.0.0", m["sqlalchemy"].Version)
}

func TestPyPI_RequirementsTxt_CompatibleReleaseStripped(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIRequirementsOnly, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)
	// "celery~=5.3.0" → "5.3.0"
	assert.Equal(t, "5.3.0", m["celery"].Version)
}

func TestPyPI_RequirementsTxt_FlaskIsFramework(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPIRequirementsOnly, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)
	assert.Equal(t, "framework", m["flask"].Category)
}

// ── combined sources: Pipfile wildcards + requirements includes ────────────────

// TestPyPI_Combined_PinFromRequirementsOverridesPipfileWildcard is the gate-block
// test for the Conduit bug: Pipfile has all packages as "*" (no version),
// requirements.txt redirects to requirements/prod.txt which has exact pins.
// After the fix, detectedVersion must be the pinned version, not empty.
func TestPyPI_Combined_PinFromRequirementsOverridesPipfileWildcard(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPICombined, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)

	// The Pipfile introduces these packages as "*" (no version); the merge must
	// update their versions from the pinned entries in requirements/prod.txt.
	// Package names in the result map use the Pipfile's casing (processed first).
	assert.Equal(t, "1.1.9", m["sqlalchemy"].Version,
		"sqlalchemy: requirements/prod.txt pin (1.1.9) must override Pipfile \"*\"")
	assert.Equal(t, "2.2", m["Flask-SQLAlchemy"].Version,
		"Flask-SQLAlchemy: requirements/prod.txt pin (2.2) must override Pipfile \"*\"")
}

// TestPyPI_Combined_IncludeFollowed verifies that -r include directives in
// requirements.txt are resolved and the referenced file is parsed so that its
// pinned versions can override wildcard entries from the Pipfile.
func TestPyPI_Combined_IncludeFollowed(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPICombined, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)

	// If the -r include were not followed, sqlalchemy and Flask-SQLAlchemy would
	// have empty Version (Pipfile "*"). A non-empty Version proves the include file
	// was read and its pinned values were merged in.
	assert.NotEmpty(t, m["sqlalchemy"].Version, "sqlalchemy version from included prod.txt must not be empty")
	assert.NotEmpty(t, m["Flask-SQLAlchemy"].Version, "Flask-SQLAlchemy version from included prod.txt must not be empty")
}

// TestPyPI_Combined_GenuinelyUnpinnedStaysEmpty verifies that packages with no
// version in any source (Werkzeug and Flask are "*" in Pipfile and bare names
// in prod.txt) retain detectedVersion="" — that is the correct signal for
// "genuinely unpinned, show latest as reference".
func TestPyPI_Combined_GenuinelyUnpinnedStaysEmpty(t *testing.T) {
	t.Parallel()
	deps, err := newPyPIParser().Parse(context.Background(), fixturePyPICombined, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	m := pypiDepsByName(deps)

	// werkzeug and flask are unpinned in every source: "*" in Pipfile, bare name in prod.txt.
	assert.Equal(t, "", m["werkzeug"].Version,
		"werkzeug is unpinned in every source — detectedVersion must stay empty")
	assert.Equal(t, "", m["flask"].Version,
		"flask is unpinned in every source — detectedVersion must stay empty")
}

// ── no manifest ───────────────────────────────────────────────────────────────

func TestPyPI_NoFiles_ReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	deps, err := newPyPIParser().Parse(context.Background(), dir, workerdomain.EcosystemPyPI)
	require.NoError(t, err)
	assert.Nil(t, deps)
}
