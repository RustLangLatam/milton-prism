package adapters

import (
	"context"
	"sort"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// conduitEdges mirrors the Conduit fixture in cascade_clusterer_test.go so
// that the analysis-worker classifier can be validated against the same graph
// the decomposition-worker DeterministicInfraDetector processes.
var conduitClassifierEdges = []*analysisdomain.DependencyEdge{
	{FromModule: "conduit.articles.views", ToModule: "conduit.articles.models", Weight: 3},
	{FromModule: "conduit.articles.models", ToModule: "conduit.database", Weight: 6},
	{FromModule: "conduit.articles.models", ToModule: "conduit.profile.models", Weight: 1},
	{FromModule: "conduit.profile.views", ToModule: "conduit.profile.models", Weight: 2},
	{FromModule: "conduit.profile.models", ToModule: "conduit.database", Weight: 5},
	{FromModule: "conduit.profile.models", ToModule: "conduit.user.models", Weight: 1},
	{FromModule: "conduit.user.views", ToModule: "conduit.user.models", Weight: 2},
	{FromModule: "conduit.user.models", ToModule: "conduit.database", Weight: 4},
}

// notiplanClassifierEdges mirrors the notiplan fixture in cascade_clusterer_test.go.
var notiplanClassifierEdges = []*analysisdomain.DependencyEdge{
	{FromModule: "backend.202222ingeteam_backend", ToModule: "backend.funcs", Weight: 1},
	{FromModule: "backend.202222ingeteam_backend", ToModule: "backend.parametros_iniciales", Weight: 1},
	{FromModule: "backend.202222ingeteam_backend", ToModule: "backend.plant_table_report", Weight: 2},
	{FromModule: "backend.202222ingeteam_backend", ToModule: "backend.var", Weight: 1},
	{FromModule: "backend.create_tables", ToModule: "backend.var", Weight: 2},
	{FromModule: "backend.funcs", ToModule: "backend.parametros_iniciales", Weight: 2},
	{FromModule: "backend.funcs", ToModule: "backend.var", Weight: 3},
	{FromModule: "backend.ingeteam_backend", ToModule: "backend.funcs", Weight: 1},
	{FromModule: "backend.ingeteam_backend", ToModule: "backend.parametros_iniciales", Weight: 1},
	{FromModule: "backend.ingeteam_backend", ToModule: "backend.plant_table_report", Weight: 2},
	{FromModule: "backend.ingeteam_backend", ToModule: "backend.var", Weight: 1},
	{FromModule: "backend.itxi", ToModule: "backend.var", Weight: 1},
	{FromModule: "backend.itxi-ikusi", ToModule: "backend.var", Weight: 1},
	{FromModule: "backend.op_disp", ToModule: "backend.var", Weight: 2},
	{FromModule: "backend.operario_disp", ToModule: "backend.var", Weight: 2},
	{FromModule: "backend.parametros_iniciales", ToModule: "backend.op_disp", Weight: 1},
	{FromModule: "backend.parametros_iniciales", ToModule: "backend.operario_disp", Weight: 1},
	{FromModule: "backend.parametros_iniciales", ToModule: "backend.var", Weight: 2},
	{FromModule: "backend.plant_table_report", ToModule: "backend.funcs", Weight: 8},
	{FromModule: "backend.plant_table_report", ToModule: "backend.var", Weight: 2},
	{FromModule: "backend.reset_tareas", ToModule: "backend.var", Weight: 1},
	{FromModule: "backend.session", ToModule: "backend.var", Weight: 1},
	{FromModule: "backend.tests.test_generate_card_json", ToModule: "backend.funcs", Weight: 1},
	{FromModule: "backend.tests.test_generate_card_json", ToModule: "backend.ingeteam_backend", Weight: 1},
	{FromModule: "backend.tests.test_sync_servidor", ToModule: "backend.funcs", Weight: 1},
	{FromModule: "backend.tests.test_sync_servidor", ToModule: "backend.ingeteam_backend", Weight: 1},
}

// TestDeterministicModuleClassifier_Conduit_BlueprintPath verifies the
// blueprint-group path on the Conduit fixture. Expected output matches what
// DeterministicInfraDetector in the decomposition worker produces on the same
// edges: 6 domain modules (articles + profile + user sub-modules) and 1 infra
// module (conduit.database). No test modules. StructuralFallback=false.
func TestDeterministicModuleClassifier_Conduit_BlueprintPath(t *testing.T) {
	t.Parallel()

	cls := NewDeterministicModuleClassifier()
	result, err := cls.Classify(context.Background(), conduitClassifierEdges)
	require.NoError(t, err)

	assert.False(t, result.GetStructuralFallback(), "Conduit has blueprint groups — must NOT use fallback")
	assert.Empty(t, result.GetTestModules(), "no test modules in Conduit fixture")

	sort.Strings(result.DomainModules)
	sort.Strings(result.InfraModules)

	assert.ElementsMatch(t, []string{
		"conduit.articles.models",
		"conduit.articles.views",
		"conduit.profile.models",
		"conduit.profile.views",
		"conduit.user.models",
		"conduit.user.views",
	}, result.GetDomainModules(), "Conduit domain modules must match decomp worker output")

	assert.ElementsMatch(t, []string{
		"conduit.database",
	}, result.GetInfraModules(), "Conduit infra modules must match decomp worker output")
}

// TestDeterministicModuleClassifier_Notiplan_StructuralFallback verifies the
// fan-in structural-fallback path on the notiplan fixture. Expected output:
// StructuralFallback=true, backend.var and backend.funcs as infra (fan-in > 3),
// backend.tests.* modules as test (nested test indicator), 11 domain modules.
func TestDeterministicModuleClassifier_Notiplan_StructuralFallback(t *testing.T) {
	t.Parallel()

	cls := NewDeterministicModuleClassifier()
	result, err := cls.Classify(context.Background(), notiplanClassifierEdges)
	require.NoError(t, err)

	assert.True(t, result.GetStructuralFallback(), "notiplan must use structural fallback — no blueprint groups")

	// The two test modules in the fixture edges must be classified as test,
	// even though their first path component is "backend" (not "test").
	assert.ElementsMatch(t, []string{
		"backend.tests.test_generate_card_json",
		"backend.tests.test_sync_servidor",
	}, result.GetTestModules(), "backend.tests.* modules must be classified as test")

	assert.ElementsMatch(t, []string{
		"backend.funcs",
		"backend.var",
	}, result.GetInfraModules(), "backend.funcs and backend.var must be infra (fan-in > 3)")

	// 15 total modules − 2 infra − 2 test = 11 domain.
	assert.Len(t, result.GetDomainModules(), 11,
		"11 domain modules expected (15 total − 2 high-fan-in infra − 2 test)")

	// Spot-check: singleton modules with fan-in ≤ threshold must be domain.
	domainSet := make(map[string]bool)
	for _, m := range result.GetDomainModules() {
		domainSet[m] = true
	}
	for _, m := range []string{
		"backend.202222ingeteam_backend",
		"backend.create_tables",
		"backend.itxi",
		"backend.itxi-ikusi",
		"backend.reset_tareas",
		"backend.session",
	} {
		assert.True(t, domainSet[m], "singleton module %s must be domain", m)
	}
}

// TestDeterministicModuleClassifier_EmptyGraph returns an empty classification
// with no fallback when there are no edges.
func TestDeterministicModuleClassifier_EmptyGraph(t *testing.T) {
	t.Parallel()

	cls := NewDeterministicModuleClassifier()
	result, err := cls.Classify(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.GetDomainModules())
	assert.Empty(t, result.GetInfraModules())
	assert.Empty(t, result.GetTestModules())
}

// TestDeterministicModuleClassifier_TestModulesExcluded verifies that modules
// whose first path component is "tests", "test", "spec", or "test_*" are
// classified as TEST, not domain or infra.
func TestDeterministicModuleClassifier_TestModulesExcluded(t *testing.T) {
	t.Parallel()

	edges := []*analysisdomain.DependencyEdge{
		{FromModule: "tests.unit.orders", ToModule: "orders.models", Weight: 1},
		{FromModule: "test.helpers", ToModule: "orders.models", Weight: 1},
		{FromModule: "spec.orders", ToModule: "orders.models", Weight: 1},
		{FromModule: "test_orders", ToModule: "orders.models", Weight: 1},
		// domain module that has a domain indicator suffix
		{FromModule: "orders.views", ToModule: "orders.models", Weight: 2},
	}

	cls := NewDeterministicModuleClassifier()
	result, err := cls.Classify(context.Background(), edges)
	require.NoError(t, err)
	assert.False(t, result.GetStructuralFallback())
	assert.ElementsMatch(t, []string{"tests.unit.orders", "test.helpers", "spec.orders", "test_orders"},
		result.GetTestModules())
	assert.ElementsMatch(t, []string{"orders.models", "orders.views"}, result.GetDomainModules())
	assert.Empty(t, result.GetInfraModules())
}
