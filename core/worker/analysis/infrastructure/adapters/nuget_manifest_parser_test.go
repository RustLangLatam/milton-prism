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
	fixtureNuGetModern = "testdata/fixture-nuget/modern"
	fixtureNuGetLegacy = "testdata/fixture-nuget/legacy"
)

func newNuGetParser() *adapters.NuGetManifestParser {
	return adapters.NewNuGetManifestParser()
}

func nugetDepsByName(deps []workerdomain.Dependency) map[string]workerdomain.Dependency {
	m := make(map[string]workerdomain.Dependency, len(deps))
	for _, d := range deps {
		m[d.Package] = d
	}
	return m
}

// ── modern *.csproj (preferred path) ─────────────────────────────────────────

func TestNuGet_Modern_FindsFourPackages(t *testing.T) {
	t.Parallel()
	deps, err := newNuGetParser().Parse(context.Background(), fixtureNuGetModern, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	// 4 PackageReference entries in Demo.csproj; packages.config ignored.
	assert.Len(t, deps, 4)
}

func TestNuGet_Modern_CsprojTakesPriorityOverPackagesConfig(t *testing.T) {
	t.Parallel()
	// modern/ has both Demo.csproj and packages.config. If packages.config were
	// read, "OldPackage" would appear; if .csproj is read, it won't.
	deps, err := newNuGetParser().Parse(context.Background(), fixtureNuGetModern, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	m := nugetDepsByName(deps)
	assert.NotContains(t, m, "OldPackage", ".csproj must take priority over packages.config")
	assert.Contains(t, m, "Microsoft.AspNetCore.OpenApi", ".csproj entries must be present")
}

func TestNuGet_Modern_AspNetCoreCategoryIsFramework(t *testing.T) {
	t.Parallel()
	deps, err := newNuGetParser().Parse(context.Background(), fixtureNuGetModern, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	m := nugetDepsByName(deps)
	assert.Equal(t, "framework", m["Microsoft.AspNetCore.OpenApi"].Category)
}

func TestNuGet_Modern_LibraryCategory(t *testing.T) {
	t.Parallel()
	deps, err := newNuGetParser().Parse(context.Background(), fixtureNuGetModern, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	m := nugetDepsByName(deps)
	assert.Equal(t, "library", m["Newtonsoft.Json"].Category)
	assert.Equal(t, "library", m["Dapper"].Category)
}

func TestNuGet_Modern_LiteralVersionPreserved(t *testing.T) {
	t.Parallel()
	deps, err := newNuGetParser().Parse(context.Background(), fixtureNuGetModern, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	m := nugetDepsByName(deps)
	assert.Equal(t, "8.0.0", m["Microsoft.AspNetCore.OpenApi"].Version)
	assert.Equal(t, "13.0.3", m["Newtonsoft.Json"].Version)
}

func TestNuGet_Modern_PropertyVersionBecomesEmpty(t *testing.T) {
	t.Parallel()
	deps, err := newNuGetParser().Parse(context.Background(), fixtureNuGetModern, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	m := nugetDepsByName(deps)
	// Version="$(EFCoreVersion)" is a Central Package Management property reference.
	assert.Equal(t, "", m["Microsoft.EntityFrameworkCore"].Version,
		"property reference must become empty (same convention as Maven parent BOM)")
}

func TestNuGet_Modern_EcosystemIsNuGet(t *testing.T) {
	t.Parallel()
	deps, err := newNuGetParser().Parse(context.Background(), fixtureNuGetModern, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	for _, d := range deps {
		assert.Equal(t, workerdomain.EcosystemNuGet, d.Ecosystem)
	}
}

// ── legacy packages.config ────────────────────────────────────────────────────

func TestNuGet_Legacy_ReturnsTwoProdPackages(t *testing.T) {
	t.Parallel()
	deps, err := newNuGetParser().Parse(context.Background(), fixtureNuGetLegacy, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	// Newtonsoft.Json + EntityFramework; NUnit excluded (developmentDependency=true).
	assert.Len(t, deps, 2)
}

func TestNuGet_Legacy_ExcludesDevelopmentDependency(t *testing.T) {
	t.Parallel()
	deps, err := newNuGetParser().Parse(context.Background(), fixtureNuGetLegacy, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	m := nugetDepsByName(deps)
	assert.NotContains(t, m, "NUnit", "developmentDependency=true must be excluded")
}

func TestNuGet_Legacy_VersionPreserved(t *testing.T) {
	t.Parallel()
	deps, err := newNuGetParser().Parse(context.Background(), fixtureNuGetLegacy, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	m := nugetDepsByName(deps)
	assert.Equal(t, "13.0.3", m["Newtonsoft.Json"].Version)
	assert.Equal(t, "6.4.4", m["EntityFramework"].Version)
}

// ── no manifest ───────────────────────────────────────────────────────────────

func TestNuGet_NoFiles_ReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	deps, err := newNuGetParser().Parse(context.Background(), dir, workerdomain.EcosystemNuGet)
	require.NoError(t, err)
	assert.Nil(t, deps)
}
