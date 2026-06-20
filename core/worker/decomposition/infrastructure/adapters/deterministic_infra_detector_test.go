package adapters

import (
	"context"
	"sort"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
)

// conduitEdges reproduces the 50-edge dependency graph of
// gothinkster/flask-realworld-example-app (Conduit) as extracted by the
// analysis pipeline. It is the acceptance-test fixture for the InfraDetector.
var conduitEdges = []workerdomain.Edge{
	{From: "conduit.app", To: "conduit.extensions", Weight: 6},
	{From: "conduit.articles.models", To: "conduit.database", Weight: 6},
	{From: "conduit.profile.models", To: "conduit.database", Weight: 5},
	{From: "conduit.articles.views", To: "conduit.articles.serializers", Weight: 4},
	{From: "conduit.user.models", To: "conduit.database", Weight: 4},
	{From: "conduit.articles.views", To: "conduit.articles.models", Weight: 3},
	{From: "tests.test_models", To: "conduit.articles.models", Weight: 3},
	{From: "autoapp", To: "conduit.settings", Weight: 2},
	{From: "conduit.extensions", To: "conduit.utils", Weight: 2},
	{From: "tests.test_config", To: "conduit.settings", Weight: 2},
	{From: "conduit.app", To: "conduit.articles", Weight: 1},
	{From: "conduit.app", To: "conduit.profile", Weight: 1},
	{From: "conduit.app", To: "conduit.user", Weight: 1},
	{From: "conduit.app", To: "conduit.settings", Weight: 1},
	{From: "conduit.app", To: "conduit.commands", Weight: 1},
	{From: "conduit.articles", To: "conduit.articles.views", Weight: 1},
	{From: "conduit.articles", To: "conduit.articles.models", Weight: 1},
	{From: "conduit.articles.models", To: "conduit.profile.models", Weight: 1},
	{From: "conduit.articles.models", To: "conduit.user.models", Weight: 1},
	{From: "conduit.articles.serializers", To: "conduit.profile.serializers", Weight: 1},
	{From: "conduit.articles.views", To: "conduit.articles", Weight: 1},
	{From: "conduit.articles.views", To: "conduit.exceptions", Weight: 1},
	{From: "conduit.database", To: "conduit.extensions", Weight: 1},
	{From: "conduit.database", To: "conduit.compat", Weight: 1},
	{From: "conduit.extensions", To: "conduit.database", Weight: 1},
	{From: "conduit.profile", To: "conduit.profile.views", Weight: 1},
	{From: "conduit.profile.models", To: "conduit.user.models", Weight: 1},
	{From: "conduit.profile.serializers", To: "conduit.user.models", Weight: 1},
	{From: "conduit.profile.views", To: "conduit.profile.serializers", Weight: 1},
	{From: "conduit.profile.views", To: "conduit.exceptions", Weight: 1},
	{From: "conduit.user", To: "conduit.user.views", Weight: 1},
	{From: "conduit.user.models", To: "conduit.extensions", Weight: 1},
	{From: "conduit.user.serializers", To: "conduit.user.models", Weight: 1},
	{From: "conduit.user.views", To: "conduit.user.serializers", Weight: 1},
	{From: "conduit.user.views", To: "conduit.user.models", Weight: 1},
	{From: "conduit.user.views", To: "conduit.exceptions", Weight: 1},
	{From: "conduit.utils", To: "conduit.user.models", Weight: 1},
	{From: "tests.conftest", To: "conduit.app", Weight: 1},
	{From: "tests.conftest", To: "conduit.database", Weight: 1},
	{From: "tests.conftest", To: "conduit.settings", Weight: 1},
	{From: "tests.conftest", To: "conduit.profile.models", Weight: 1},
	{From: "tests.factories", To: "conduit.articles.models", Weight: 1},
	{From: "tests.factories", To: "conduit.database", Weight: 1},
	{From: "tests.factories", To: "conduit.user.models", Weight: 1},
	{From: "tests.test_articles", To: "conduit.profile.serializers", Weight: 1},
	{From: "tests.test_authentication", To: "conduit.exceptions", Weight: 1},
	{From: "tests.test_models", To: "conduit.profile.models", Weight: 1},
	{From: "tests.test_profile", To: "conduit.exceptions", Weight: 1},
	{From: "tests.test_config", To: "conduit.app", Weight: 1},
	{From: "autoapp", To: "conduit.app", Weight: 1},
}

func sortedModuleNames(mods []workerdomain.Module) []string {
	names := make([]string, len(mods))
	for i, m := range mods {
		names[i] = string(m)
	}
	sort.Strings(names)
	return names
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestDeterministicInfraDetector_Conduit is the primary acceptance test.
// It verifies that on the Conduit graph the detector produces exactly the
// expected INFRA / DOMAIN / TEST partition described in the spec §7.
func TestDeterministicInfraDetector_Conduit(t *testing.T) {
	graph := &workerdomain.Graph{Edges: conduitEdges}
	det := NewDeterministicInfraDetector()
	cls, err := det.Detect(context.Background(), graph)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	infra := sortedModuleNames(cls.Infra)
	domain := sortedModuleNames(cls.Domain)
	tests := sortedModuleNames(cls.Tests)

	t.Logf("INFRA  (%d): %v", len(infra), infra)
	t.Logf("DOMAIN (%d): %v", len(domain), domain)
	t.Logf("TESTS  (%d): %v", len(tests), tests)

	// --- INFRA assertions ---
	expectedInfra := []string{
		"autoapp",
		"conduit.app",
		"conduit.commands",
		"conduit.compat",
		"conduit.database",
		"conduit.exceptions",
		"conduit.extensions",
		"conduit.settings",
		"conduit.utils",
	}
	for _, want := range expectedInfra {
		if !contains(infra, want) {
			t.Errorf("expected %q in INFRA, got INFRA=%v", want, infra)
		}
	}

	// --- DOMAIN assertions ---
	expectedDomain := []string{
		"conduit.articles",
		"conduit.articles.models",
		"conduit.articles.serializers",
		"conduit.articles.views",
		"conduit.profile",
		"conduit.profile.models",
		"conduit.profile.serializers",
		"conduit.profile.views",
		"conduit.user",
		"conduit.user.models",
		"conduit.user.serializers",
		"conduit.user.views",
	}
	for _, want := range expectedDomain {
		if !contains(domain, want) {
			t.Errorf("expected %q in DOMAIN, got DOMAIN=%v", want, domain)
		}
	}

	// --- TEST assertions ---
	for _, m := range cls.Tests {
		name := string(m)
		if !isTestModule(name) {
			t.Errorf("module %q in TEST but isTestModule() returned false", name)
		}
	}
	if len(tests) == 0 {
		t.Error("expected at least one test module")
	}

	// --- No module in both INFRA and DOMAIN ---
	infraSet := make(map[string]bool, len(infra))
	for _, s := range infra {
		infraSet[s] = true
	}
	for _, s := range domain {
		if infraSet[s] {
			t.Errorf("module %q appears in both INFRA and DOMAIN", s)
		}
	}
}

// TestDeterministicInfraDetector_EmptyGraph returns an empty classification
// without error for an empty graph.
func TestDeterministicInfraDetector_EmptyGraph(t *testing.T) {
	det := NewDeterministicInfraDetector()
	cls, err := det.Detect(context.Background(), &workerdomain.Graph{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cls.Infra)+len(cls.Domain)+len(cls.Tests) != 0 {
		t.Errorf("expected empty classification for empty graph, got infra=%d domain=%d tests=%d",
			len(cls.Infra), len(cls.Domain), len(cls.Tests))
	}
}

// TestDeterministicInfraDetector_SingleBlueprint verifies that a minimal graph
// with a single blueprint (one .models module) classifies its members as DOMAIN
// and any utility as INFRA.
func TestDeterministicInfraDetector_SingleBlueprint(t *testing.T) {
	edges := []workerdomain.Edge{
		{From: "myapp.articles.views", To: "myapp.articles.models", Weight: 2},
		{From: "myapp.articles.models", To: "myapp.database", Weight: 3},
		{From: "myapp.articles.models", To: "myapp.config", Weight: 1},
	}
	det := NewDeterministicInfraDetector()
	cls, err := det.Detect(context.Background(), &workerdomain.Graph{Edges: edges})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	domain := sortedModuleNames(cls.Domain)
	infra := sortedModuleNames(cls.Infra)

	for _, want := range []string{"myapp.articles.views", "myapp.articles.models"} {
		if !contains(domain, want) {
			t.Errorf("expected %q in DOMAIN, got DOMAIN=%v", want, domain)
		}
	}
	for _, want := range []string{"myapp.database", "myapp.config"} {
		if !contains(infra, want) {
			t.Errorf("expected %q in INFRA, got INFRA=%v", want, infra)
		}
	}
}

// TestDeterministicInfraDetector_Conduit_NoRegressionAfterFallbackAdd verifies
// that adding the structural fallback does not change Conduit's output: blueprint
// groups are found (conduit.articles, conduit.profile, conduit.user), so the
// fallback is NOT triggered and StructuralFallback remains false.
func TestDeterministicInfraDetector_Conduit_NoRegressionAfterFallbackAdd(t *testing.T) {
	graph := &workerdomain.Graph{Edges: conduitEdges}
	det := NewDeterministicInfraDetector()
	cls, err := det.Detect(context.Background(), graph)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if cls.StructuralFallback {
		t.Error("StructuralFallback must be false when blueprint groups exist (Conduit has 3)")
	}
	if len(cls.Domain) == 0 {
		t.Error("Conduit must have domain modules")
	}
}

// notionplanEdges is a synthetic graph that mimics codebases without Flask blueprint
// structure: no module has a domain-indicator suffix (models/views/serializers/etc.).
// Two modules (state.var, state.funcs) act as high-fan-in shared state hubs,
// imported by 9 of the 12 non-test modules — well above the hub threshold.
var notiplanLikeEdges = func() []workerdomain.Edge {
	// 12 non-test modules total. Hub threshold = max(2, 12/4) = 3.
	// backend.var and backend.funcs each have fan-in >= 9 → INFRA.
	// The remaining modules have fan-in 0–2 → DOMAIN.
	// NOTE: none of these names end in a domainIndicatorSuffix (models/views/etc.)
	// so no blueprint groups are found and the structural fallback activates.
	modules := []string{
		"backend.adapter",     // replaces "backend.api" — "api" is a domain suffix
		"backend.config",
		"backend.create_tables",
		"backend.dispatcher",
		"backend.entry",
		"backend.funcs", // hub: imported by 9+ modules
		"backend.op_disp",
		"backend.parametros",
		"backend.plant_report",
		"backend.session",
		"backend.utils",
		"backend.var", // hub: imported by 9+ modules
	}
	var edges []workerdomain.Edge
	// Make var and funcs high-fan-in hubs.
	for _, src := range modules {
		if src != "backend.var" {
			edges = append(edges, workerdomain.Edge{From: workerdomain.Module(src), To: "backend.var", Weight: 1})
		}
		if src != "backend.funcs" && src != "backend.var" {
			edges = append(edges, workerdomain.Edge{From: workerdomain.Module(src), To: "backend.funcs", Weight: 1})
		}
	}
	// Sparse cross-connections among non-hub modules.
	edges = append(edges,
		workerdomain.Edge{From: "backend.entry", To: "backend.adapter", Weight: 2},
		workerdomain.Edge{From: "backend.entry", To: "backend.session", Weight: 1},
		workerdomain.Edge{From: "backend.adapter", To: "backend.dispatcher", Weight: 1},
		workerdomain.Edge{From: "backend.dispatcher", To: "backend.op_disp", Weight: 1},
	)
	return edges
}()

// TestDeterministicInfraDetector_StructuralFallback_NoBlueprints verifies the
// fallback path: when no blueprint groups are found, fan-in hubs become INFRA
// and the remaining non-test modules become DOMAIN (non-empty).
func TestDeterministicInfraDetector_StructuralFallback_NoBlueprints(t *testing.T) {
	graph := &workerdomain.Graph{Edges: notiplanLikeEdges}
	det := NewDeterministicInfraDetector()
	cls, err := det.Detect(context.Background(), graph)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	infra := sortedModuleNames(cls.Infra)
	domain := sortedModuleNames(cls.Domain)
	t.Logf("StructuralFallback=%v INFRA(%d)=%v DOMAIN(%d)=%v",
		cls.StructuralFallback, len(infra), infra, len(domain), domain)

	if !cls.StructuralFallback {
		t.Error("StructuralFallback must be true when no blueprint groups are found")
	}

	// State hubs must be classified as INFRA.
	for _, hub := range []string{"backend.var", "backend.funcs"} {
		if !contains(infra, hub) {
			t.Errorf("expected hub %q in INFRA, got INFRA=%v", hub, infra)
		}
		if contains(domain, hub) {
			t.Errorf("hub %q must not be in DOMAIN", hub)
		}
	}

	// Domain must be non-empty: structural fallback must leave something to cluster.
	if len(cls.Domain) == 0 {
		t.Error("DOMAIN must be non-empty after structural fallback")
	}

	// No module in both INFRA and DOMAIN.
	infraSet := make(map[string]bool, len(infra))
	for _, s := range infra {
		infraSet[s] = true
	}
	for _, s := range domain {
		if infraSet[s] {
			t.Errorf("module %q in both INFRA and DOMAIN", s)
		}
	}
}

// TestDeterministicInfraDetector_NestedTestModules verifies that modules like
// "backend.tests.conftest" (test indicator in a non-first component) are
// classified as TEST and excluded from both domain and infra.
func TestDeterministicInfraDetector_NestedTestModules(t *testing.T) {
	edges := []workerdomain.Edge{
		// A notiplan-like graph where two test files import the hubs.
		{From: "backend.tests.test_foo", To: "backend.var", Weight: 1},
		{From: "backend.tests.test_bar", To: "backend.funcs", Weight: 1},
		{From: "backend.adapter", To: "backend.var", Weight: 2},
		{From: "backend.adapter", To: "backend.funcs", Weight: 2},
		{From: "backend.session", To: "backend.var", Weight: 1},
	}
	det := NewDeterministicInfraDetector()
	cls, err := det.Detect(context.Background(), &workerdomain.Graph{Edges: edges})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	tests := sortedModuleNames(cls.Tests)
	if len(tests) != 2 {
		t.Errorf("expected 2 test modules, got %v", tests)
	}
	for _, want := range []string{"backend.tests.test_bar", "backend.tests.test_foo"} {
		if !contains(tests, want) {
			t.Errorf("expected %q in Tests, got Tests=%v", want, tests)
		}
	}
	// Test modules must not appear in infra or domain.
	infra := sortedModuleNames(cls.Infra)
	domain := sortedModuleNames(cls.Domain)
	for _, tm := range tests {
		if contains(infra, tm) {
			t.Errorf("test module %q must not be in INFRA", tm)
		}
		if contains(domain, tm) {
			t.Errorf("test module %q must not be in DOMAIN", tm)
		}
	}
}

// TestIsTestModule verifies that isTestModule detects test indicators in any
// path component, not just the first one.
func TestIsTestModule(t *testing.T) {
	yes := []string{
		"tests.test_models",                              // first component
		"test.helpers",                                   // first component alt
		"spec.user",                                      // spec first component
		"backend.tests.conftest",                         // nested — first="backend"
		"backend.tests.test_agrupaciones_state_mapping",  // nested test_* suffix
		"backend.tests.test_sync_servidor",               // nested test_* suffix
		"myapp.test_helpers",                             // test_ prefix mid-path
		"myapp.models_test",                              // _test suffix mid-path
	}
	no := []string{
		"backend.contestant",    // "test" is a substring, not a whole component
		"backend.context",       // "test" substring
		"backend.funcs",
		"conduit.articles",
		"myapp.attestation",     // "test" substring inside "attestation"
		`BookStack\Entities\Models\Page`,
	}
	for _, m := range yes {
		if !isTestModule(m) {
			t.Errorf("isTestModule(%q) = false, want true", m)
		}
	}
	for _, m := range no {
		if isTestModule(m) {
			t.Errorf("isTestModule(%q) = true, want false", m)
		}
	}
}

// TestGroupOf verifies the group-prefix helper covers Python and PHP edge cases.
func TestGroupOf(t *testing.T) {
	cases := []struct {
		module string
		want   string
	}{
		// Python (dot-separated).
		{"autoapp", "autoapp"},
		{"conduit.database", "conduit.database"},
		{"conduit.articles.views", "conduit.articles"},
		{"conduit.articles.models", "conduit.articles"},
		{"a.b.c.d", "a.b"},
		// PHP (backslash-separated) — groupOf must use \ as the separator and
		// return the top-two namespace segments so the blueprint bias groups
		// all BookStack\Entities\* together (not 258 singletons).
		{`BookStack\Entities\Models\Entity`, `BookStack\Entities`},
		{`BookStack\Entities\Models\Page`, `BookStack\Entities`},
		{`BookStack\Entities\Tools\PageContent`, `BookStack\Entities`},
		{`BookStack\Access\Guards\AsyncExternalBaseSessionGuard`, `BookStack\Access`},
		{`BookStack\Users\Models\User`, `BookStack\Users`},
		{`BookStack\Permissions\PermissionApplicator`, `BookStack\Permissions`},
		{`BookStack\Users`, `BookStack\Users`}, // depth ≤ 2 → module itself
	}
	for _, tc := range cases {
		got := groupOf(tc.module)
		if got != tc.want {
			t.Errorf("groupOf(%q) = %q, want %q", tc.module, got, tc.want)
		}
	}
}
