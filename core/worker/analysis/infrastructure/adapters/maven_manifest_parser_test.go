package adapters_test

import (
	"context"
	"testing"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureMavenWithSpring = "testdata/fixture-maven/with-spring"

func newMavenParser() *adapters.MavenManifestParser {
	return adapters.NewMavenManifestParser()
}

func mavenDepsByName(deps []workerdomain.Dependency) map[string]workerdomain.Dependency {
	m := make(map[string]workerdomain.Dependency, len(deps))
	for _, d := range deps {
		m[d.Package] = d
	}
	return m
}

// ── production scope filtering ────────────────────────────────────────────────

func TestMaven_WithSpring_ReturnsFourProdDeps(t *testing.T) {
	t.Parallel()
	deps, err := newMavenParser().Parse(context.Background(), fixtureMavenWithSpring, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	// spring-boot-starter-web (compile), guava (compile), hibernate-core (compile),
	// jakarta.servlet-api (provided) = 4. test and system excluded.
	assert.Len(t, deps, 4)
}

func TestMaven_WithSpring_ExcludesTestScope(t *testing.T) {
	t.Parallel()
	deps, err := newMavenParser().Parse(context.Background(), fixtureMavenWithSpring, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	m := mavenDepsByName(deps)
	assert.NotContains(t, m, "org.junit.jupiter:junit-jupiter", "test-scope dep must be excluded")
}

func TestMaven_WithSpring_ExcludesSystemScope(t *testing.T) {
	t.Parallel()
	deps, err := newMavenParser().Parse(context.Background(), fixtureMavenWithSpring, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	m := mavenDepsByName(deps)
	assert.NotContains(t, m, "com.example:legacy-lib", "system-scope dep must be excluded")
}

func TestMaven_WithSpring_IncludesProvidedScope(t *testing.T) {
	t.Parallel()
	deps, err := newMavenParser().Parse(context.Background(), fixtureMavenWithSpring, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	m := mavenDepsByName(deps)
	assert.Contains(t, m, "jakarta.servlet:jakarta.servlet-api",
		"provided-scope dep must be included (production system depends on it)")
}

// ── package name format ───────────────────────────────────────────────────────

func TestMaven_WithSpring_PackageNameIsGroupColonArtifact(t *testing.T) {
	t.Parallel()
	deps, err := newMavenParser().Parse(context.Background(), fixtureMavenWithSpring, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	m := mavenDepsByName(deps)
	assert.Contains(t, m, "org.springframework.boot:spring-boot-starter-web")
	assert.Contains(t, m, "com.google.guava:guava")
}

// ── version handling ──────────────────────────────────────────────────────────

func TestMaven_WithSpring_LiteralVersionPreserved(t *testing.T) {
	t.Parallel()
	deps, err := newMavenParser().Parse(context.Background(), fixtureMavenWithSpring, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	m := mavenDepsByName(deps)
	assert.Equal(t, "3.2.5", m["org.springframework.boot:spring-boot-starter-web"].Version)
	assert.Equal(t, "33.2.0-jre", m["com.google.guava:guava"].Version)
}

func TestMaven_WithSpring_PropertyVersionBecomesEmpty(t *testing.T) {
	t.Parallel()
	deps, err := newMavenParser().Parse(context.Background(), fixtureMavenWithSpring, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	m := mavenDepsByName(deps)
	// ${hibernate.version} cannot be resolved without full POM inheritance; stored as "".
	assert.Equal(t, "", m["org.hibernate.orm:hibernate-core"].Version,
		"property reference must become empty string")
}

// ── category classification ───────────────────────────────────────────────────

func TestMaven_WithSpring_SpringBootCategoryIsFramework(t *testing.T) {
	t.Parallel()
	deps, err := newMavenParser().Parse(context.Background(), fixtureMavenWithSpring, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	m := mavenDepsByName(deps)
	assert.Equal(t, "framework", m["org.springframework.boot:spring-boot-starter-web"].Category)
}

func TestMaven_WithSpring_NonSpringCategoryIsLibrary(t *testing.T) {
	t.Parallel()
	deps, err := newMavenParser().Parse(context.Background(), fixtureMavenWithSpring, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	m := mavenDepsByName(deps)
	assert.Equal(t, "library", m["com.google.guava:guava"].Category)
	assert.Equal(t, "library", m["org.hibernate.orm:hibernate-core"].Category)
	assert.Equal(t, "library", m["jakarta.servlet:jakarta.servlet-api"].Category)
}

// ── ecosystem tag ─────────────────────────────────────────────────────────────

func TestMaven_WithSpring_EcosystemIsMaven(t *testing.T) {
	t.Parallel()
	deps, err := newMavenParser().Parse(context.Background(), fixtureMavenWithSpring, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	for _, d := range deps {
		assert.Equal(t, workerdomain.EcosystemMaven, d.Ecosystem)
	}
}

// ── no pom.xml ────────────────────────────────────────────────────────────────

func TestMaven_NoPom_ReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	deps, err := newMavenParser().Parse(context.Background(), dir, workerdomain.EcosystemMaven)
	require.NoError(t, err)
	assert.Nil(t, deps)
}
