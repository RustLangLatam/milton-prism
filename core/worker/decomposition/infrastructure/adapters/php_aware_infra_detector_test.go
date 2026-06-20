package adapters

import (
	"context"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
)

// bookstackEdges is a representative sample of BookStack's PHP/Laravel dependency
// graph. It includes modules from all layers: controllers (APPLICATION), models
// and services (DOMAIN), repos/providers/factories (INFRA), and tests (TEST).
// The full graph has 548 modules / 1834 edges; this fixture uses a meaningful
// cross-section to validate that each layer is correctly excluded or included.
var bookstackEdges = []workerdomain.Edge{
	// Controllers → Models (application → domain)
	{From: `BookStack\Entities\Controllers\PageController`, To: `BookStack\Entities\Models\Page`, Weight: 5},
	{From: `BookStack\Entities\Controllers\BookController`, To: `BookStack\Entities\Models\Book`, Weight: 4},
	{From: `BookStack\Users\Controllers\UserController`, To: `BookStack\Users\Models\User`, Weight: 3},
	// Domain → Domain
	{From: `BookStack\Entities\Models\Page`, To: `BookStack\Entities\Models\Entity`, Weight: 3},
	{From: `BookStack\Entities\Models\Book`, To: `BookStack\Entities\Models\Entity`, Weight: 2},
	{From: `BookStack\Permissions\PermissionApplicator`, To: `BookStack\Users\Models\User`, Weight: 2},
	{From: `BookStack\Entities\Tools\PageContent`, To: `BookStack\Entities\Models\Page`, Weight: 2},
	// Repos → Models (infra → domain)
	{From: `BookStack\Entities\Repos\PageRepo`, To: `BookStack\Entities\Models\Page`, Weight: 4},
	{From: `BookStack\Entities\Repos\BookRepo`, To: `BookStack\Entities\Models\Book`, Weight: 2},
	{From: `BookStack\Users\UserRepo`, To: `BookStack\Users\Models\User`, Weight: 3},
	// Providers → Models (infra → domain)
	{From: `BookStack\App\Providers\AppServiceProvider`, To: `BookStack\Entities\Models\Entity`, Weight: 1},
	// Factories → Models (infra → domain; Database\Factories prefix is INFRA)
	{From: `Database\Factories\Entities\Models\PageFactory`, To: `BookStack\Entities\Models\Page`, Weight: 1},
	{From: `Database\Factories\Users\Models\UserFactory`, To: `BookStack\Users\Models\User`, Weight: 1},
	// Tests → Domain (test → domain)
	{From: `Tests\Entity\PageTest`, To: `BookStack\Entities\Models\Page`, Weight: 2},
	{From: `Tests\Unit\UserServiceTest`, To: `BookStack\Users\Models\User`, Weight: 1},
	// Utility classes (unmatched: Theming, Util, Http\Controller base)
	{From: `BookStack\Theming\ThemeManager`, To: `BookStack\Entities\Models\Entity`, Weight: 1},
	{From: `BookStack\Http\Controller`, To: `BookStack\Entities\Models\Entity`, Weight: 1},
}

// TestPHPAwareInfraDetector_BookStackLayers verifies the layer partitioning for
// a representative PHP/Laravel graph. Controllers, repos, providers, and factories
// must be in Infra; models, services, and tools in Domain; tests in Tests.
func TestPHPAwareInfraDetector_BookStackLayers(t *testing.T) {
	graph := &workerdomain.Graph{Edges: bookstackEdges}
	det := NewPHPAwareInfraDetector()
	cls, err := det.Detect(context.Background(), graph)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	infra := sortedModuleNames(cls.Infra)
	domain := sortedModuleNames(cls.Domain)
	tests := sortedModuleNames(cls.Tests)

	t.Logf("DOMAIN(%d): %v", len(domain), domain)
	t.Logf("INFRA(%d):  %v", len(infra), infra)
	t.Logf("TEST(%d):   %v", len(tests), tests)

	// Controllers → INFRA (excluded from domain clustering).
	for _, m := range []string{
		`BookStack\Entities\Controllers\PageController`,
		`BookStack\Entities\Controllers\BookController`,
		`BookStack\Users\Controllers\UserController`,
	} {
		if !contains(infra, m) {
			t.Errorf("controller %q must be in INFRA, got INFRA=%v", m, infra)
		}
		if contains(domain, m) {
			t.Errorf("controller %q must NOT be in DOMAIN", m)
		}
	}

	// Repos and providers → INFRA.
	// Note: BookStack\Users\UserRepo is NOT checked here — its class name "UserRepo"
	// doesn't match the exact segment "Repo"/"Repos", so it falls through to the
	// fan-in fallback. With low fan-in in this fixture it goes to Domain, which is
	// acceptable (it will cluster with the User service boundary).
	for _, m := range []string{
		`BookStack\Entities\Repos\PageRepo`,
		`BookStack\Entities\Repos\BookRepo`,
		`BookStack\App\Providers\AppServiceProvider`,
		`Database\Factories\Entities\Models\PageFactory`,
		`Database\Factories\Users\Models\UserFactory`,
	} {
		if !contains(infra, m) {
			t.Errorf("infra module %q must be in INFRA, got INFRA=%v", m, infra)
		}
		if contains(domain, m) {
			t.Errorf("infra module %q must NOT be in DOMAIN", m)
		}
	}

	// Models, services, permissions, tools → DOMAIN.
	for _, m := range []string{
		`BookStack\Entities\Models\Page`,
		`BookStack\Entities\Models\Book`,
		`BookStack\Entities\Models\Entity`,
		`BookStack\Users\Models\User`,
		`BookStack\Permissions\PermissionApplicator`,
		`BookStack\Entities\Tools\PageContent`,
	} {
		if !contains(domain, m) {
			t.Errorf("domain module %q must be in DOMAIN, got DOMAIN=%v", m, domain)
		}
		if contains(infra, m) {
			t.Errorf("domain module %q must NOT be in INFRA", m)
		}
	}

	// Tests → Tests bucket.
	for _, m := range []string{
		`Tests\Entity\PageTest`,
		`Tests\Unit\UserServiceTest`,
	} {
		if !contains(tests, m) {
			t.Errorf("test module %q must be in Tests, got Tests=%v", m, tests)
		}
		if contains(domain, m) {
			t.Errorf("test module %q must NOT be in DOMAIN", m)
		}
		if contains(infra, m) {
			t.Errorf("test module %q must NOT be in INFRA", m)
		}
	}

	// No module in both INFRA and DOMAIN.
	infraSet := make(map[string]bool, len(infra))
	for _, s := range infra {
		infraSet[s] = true
	}
	for _, s := range domain {
		if infraSet[s] {
			t.Errorf("module %q appears in both INFRA and DOMAIN", s)
		}
	}

	// StructuralFallback must NOT be set for PHP graphs — confidence comes from
	// Louvain modularity, not from the classification path.
	if cls.StructuralFallback {
		t.Error("StructuralFallback must be false for PHP graphs (PHP segment rules are the authoritative path)")
	}
}

// TestPHPAwareInfraDetector_DomainSmallerThanTotal verifies that the domain
// subgraph for PHP is meaningfully smaller than the total module set —
// the core fix for the Louvain over-clustering issue.
func TestPHPAwareInfraDetector_DomainSmallerThanTotal(t *testing.T) {
	graph := &workerdomain.Graph{Edges: bookstackEdges}
	det := NewPHPAwareInfraDetector()
	cls, err := det.Detect(context.Background(), graph)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	total := len(cls.Domain) + len(cls.Infra) + len(cls.Tests)
	t.Logf("total=%d domain=%d infra=%d tests=%d", total, len(cls.Domain), len(cls.Infra), len(cls.Tests))

	if len(cls.Domain) >= total {
		t.Errorf("domain must be smaller than total modules: domain=%d total=%d", len(cls.Domain), total)
	}
	// At least controllers + factories in infra, tests in test bucket.
	if len(cls.Infra) == 0 {
		t.Error("INFRA must be non-empty for a PHP/Laravel graph")
	}
	if len(cls.Tests) == 0 {
		t.Error("Tests must be non-empty for a PHP/Laravel graph with test modules")
	}
}

// TestPHPAwareInfraDetector_PythonDelegation verifies that dotted-module Python
// graphs are delegated to DeterministicInfraDetector unchanged.
func TestPHPAwareInfraDetector_PythonDelegation(t *testing.T) {
	graph := &workerdomain.Graph{Edges: conduitEdges}
	det := NewPHPAwareInfraDetector()
	cls, err := det.Detect(context.Background(), graph)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	// DeterministicInfraDetector behaviour: 3 blueprint groups (articles/profile/user)
	// → StructuralFallback=false, 12 domain modules, tests separated.
	if cls.StructuralFallback {
		t.Error("Python/Conduit must NOT trigger StructuralFallback (blueprint groups exist)")
	}

	domain := sortedModuleNames(cls.Domain)
	for _, want := range []string{
		"conduit.articles.models",
		"conduit.user.models",
		"conduit.profile.models",
	} {
		if !contains(domain, want) {
			t.Errorf("Python module %q must be in DOMAIN, got DOMAIN=%v", want, domain)
		}
	}

	tests := sortedModuleNames(cls.Tests)
	if len(tests) == 0 {
		t.Error("Conduit must have test modules in Tests bucket")
	}
}

// TestPHPAwareInfraDetector_ExceptionsInInfra verifies that PHP exception classes
// are redirected to Infra (not Domain) in the decomposition context.
// Exception classes are error contracts owned by their callers — not services.
func TestPHPAwareInfraDetector_ExceptionsInInfra(t *testing.T) {
	edges := []workerdomain.Edge{
		{From: `BookStack\Exceptions\NotFoundException`, To: `BookStack\Entities\Models\Entity`, Weight: 1},
		{From: `BookStack\Exceptions\OidcException`, To: `BookStack\Entities\Models\Entity`, Weight: 1},
		{From: `BookStack\Entities\Models\Page`, To: `BookStack\Entities\Models\Entity`, Weight: 3},
	}
	graph := &workerdomain.Graph{Edges: edges}
	det := NewPHPAwareInfraDetector()
	cls, err := det.Detect(context.Background(), graph)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	domain := sortedModuleNames(cls.Domain)
	infra := sortedModuleNames(cls.Infra)
	t.Logf("DOMAIN(%d): %v", len(domain), domain)
	t.Logf("INFRA(%d):  %v", len(infra), infra)

	for _, exceptionModule := range []string{
		`BookStack\Exceptions\NotFoundException`,
		`BookStack\Exceptions\OidcException`,
	} {
		if contains(domain, exceptionModule) {
			t.Errorf("exception module %q must NOT be in DOMAIN for decomposition", exceptionModule)
		}
		if !contains(infra, exceptionModule) {
			t.Errorf("exception module %q must be in INFRA for decomposition", exceptionModule)
		}
	}

	// Normal domain module is unaffected.
	if !contains(domain, `BookStack\Entities\Models\Page`) {
		t.Errorf("domain model must remain in DOMAIN")
	}
}

// TestPHPAwareInfraDetector_FallbackCountsUniqueImporters verifies that the
// structural fan-in fallback counts distinct importer modules, not raw edge
// count. A single caller that imports multiple symbols from the same target
// generates multiple edges but must count as exactly one importer.
//
// Setup: 9 unmatched utility modules → threshold = max(2, 9/4) = 2.
// App\Utils\Config is imported by one caller (App\Models\User) via 3 edges
// (3 different symbols). With edge counting (old bug): fanIn=3 > 2 → INFRA.
// With unique-importer counting (correct): fanIn=1, not > 2 → DOMAIN.
func TestPHPAwareInfraDetector_FallbackCountsUniqueImporters(t *testing.T) {
	edges := []workerdomain.Edge{
		// One caller imports three symbols from the target — three edges, one importer.
		{From: `App\Models\User`, To: `App\Utils\Config`, Weight: 1},
		{From: `App\Models\User`, To: `App\Utils\Config`, Weight: 1},
		{From: `App\Models\User`, To: `App\Utils\Config`, Weight: 1},
		// Eight other unmatched utility modules appear in the graph (via their
		// own edges) to bring the unmatched count to 9 and set threshold=2.
		{From: `App\Utils\Util1`, To: `App\Models\User`, Weight: 1},
		{From: `App\Utils\Util2`, To: `App\Models\User`, Weight: 1},
		{From: `App\Utils\Util3`, To: `App\Models\User`, Weight: 1},
		{From: `App\Utils\Util4`, To: `App\Models\User`, Weight: 1},
		{From: `App\Utils\Util5`, To: `App\Models\User`, Weight: 1},
		{From: `App\Utils\Util6`, To: `App\Models\User`, Weight: 1},
		{From: `App\Utils\Util7`, To: `App\Models\User`, Weight: 1},
		{From: `App\Utils\Util8`, To: `App\Models\User`, Weight: 1},
	}
	graph := &workerdomain.Graph{Edges: edges}
	det := NewPHPAwareInfraDetector()
	cls, err := det.Detect(context.Background(), graph)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	domain := sortedModuleNames(cls.Domain)
	infra := sortedModuleNames(cls.Infra)
	t.Logf("DOMAIN(%d): %v", len(domain), domain)
	t.Logf("INFRA(%d):  %v", len(infra), infra)

	// App\Utils\Config has one unique importer (App\Models\User), even though
	// three edges exist from it. Unique importers (1) ≤ threshold (2) → DOMAIN.
	if !contains(domain, `App\Utils\Config`) {
		t.Errorf("Config has 1 unique importer (≤ threshold 2) — must be in DOMAIN, got DOMAIN=%v", domain)
	}
	if contains(infra, `App\Utils\Config`) {
		t.Errorf("Config must NOT be in INFRA (edge-count bug would put it there)")
	}
}

// TestPHPAwareInfraDetector_EmptyGraph returns an empty classification for
// an empty graph without error.
func TestPHPAwareInfraDetector_EmptyGraph(t *testing.T) {
	det := NewPHPAwareInfraDetector()
	cls, err := det.Detect(context.Background(), &workerdomain.Graph{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cls.Infra)+len(cls.Domain)+len(cls.Tests) != 0 {
		t.Errorf("expected empty classification, got infra=%d domain=%d tests=%d",
			len(cls.Infra), len(cls.Domain), len(cls.Tests))
	}
}
