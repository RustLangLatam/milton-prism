package adapters

import (
	"context"
	"sort"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/shared/phpclassify"
)

// ---- phpLayerOf unit tests ----

func TestPHPLayerOf_LaravelControllers(t *testing.T) {
	cases := []string{
		`App\Http\Controllers\UserController`,
		`BookStack\Entities\Controllers\BookController`,
		`BookStack\Access\Controllers\AuthController`,
		`App\Http\Controllers\Api\v1\UserController`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "application" {
			t.Errorf("phpclassify.LayerOf(%q) = %q, want %q", fqn, got, "application")
		}
	}
}

func TestPHPLayerOf_SymfonyControllers(t *testing.T) {
	cases := []string{
		`App\Controller\UserController`,
		`App\Controller\Api\ProductController`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "application" {
			t.Errorf("phpclassify.LayerOf(%q) = %q, want %q", fqn, got, "application")
		}
	}
}

func TestPHPLayerOf_ConsoleAndMiddleware(t *testing.T) {
	cases := []struct {
		fqn  string
		want string
	}{
		{`App\Console\Commands\SendNotifications`, "application"},
		{`App\Console\Kernel`, "application"},
		{`App\Http\Middleware\Authenticate`, "application"},
		{`App\Http\Requests\StoreUserRequest`, "application"},
	}
	for _, tc := range cases {
		if got := phpclassify.LayerOf(tc.fqn); got != tc.want {
			t.Errorf("phpclassify.LayerOf(%q) = %q, want %q", tc.fqn, got, tc.want)
		}
	}
}

func TestPHPLayerOf_Models(t *testing.T) {
	cases := []string{
		`App\Models\User`,
		`BookStack\Entities\Models\Page`,
		`BookStack\Entities\Models\Book`,
		`BookStack\Users\Models\User`,
		`App\Entity\Product`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "domain" {
			t.Errorf("phpclassify.LayerOf(%q) = %q, want %q", fqn, got, "domain")
		}
	}
}

func TestPHPLayerOf_Services(t *testing.T) {
	cases := []string{
		`App\Services\UserService`,
		`BookStack\Entities\Tools\BookTools`,
		`App\Policies\UserPolicy`,
		`App\Rules\ValidEmail`,
		`App\Contracts\UserRepositoryInterface`,
		`BookStack\Permissions\PermissionApplicator`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "domain" {
			t.Errorf("phpclassify.LayerOf(%q) = %q, want %q", fqn, got, "domain")
		}
	}
}

func TestPHPLayerOf_Infra(t *testing.T) {
	cases := []string{
		`App\Repositories\UserRepository`,
		`BookStack\Entities\Repos\BookRepo`,
		`App\Providers\AppServiceProvider`,
		`App\Facades\Storage`,
		`App\Events\UserRegistered`,
		`App\Listeners\SendWelcomeEmail`,
		`App\Jobs\ProcessPayment`,
		`App\Notifications\InvoicePaid`,
		`App\Mail\OrderShipped`,
		`App\Observers\UserObserver`,
		`Database\Factories\UserFactory`,
		`Database\Seeders\UserSeeder`,
		`App\Queries\ActiveUsersQuery`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "infra" {
			t.Errorf("phpclassify.LayerOf(%q) = %q, want %q", fqn, got, "infra")
		}
	}
}

func TestPHPLayerOf_Tests(t *testing.T) {
	cases := []string{
		`Tests\Unit\UserServiceTest`,
		`Tests\Feature\BookControllerTest`,
		`Test\Api\AuthTest`,
		`App\Services\UserServiceTest`, // class name ends with Test
		`App\Http\Controllers\UserControllerTestCase`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "test" {
			t.Errorf("phpclassify.LayerOf(%q) = %q, want %q", fqn, got, "test")
		}
	}
}

func TestPHPLayerOf_UnknownReturnsEmpty(t *testing.T) {
	cases := []string{
		`App\Theming\ThemeManager`,
		`BookStack\Sorting\SortOptions`,
		`App\Helpers\StringHelper`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "" {
			t.Errorf("phpclassify.LayerOf(%q) = %q, want empty (fallback)", fqn, got)
		}
	}
}

// APPLICATION has higher priority than DOMAIN even when segments overlap.
func TestPHPLayerOf_ApplicationPriorityOverDomain(t *testing.T) {
	// "Services" (domain) is a sibling namespace; "Controllers" wins.
	fqn := `App\Http\Controllers\UserController`
	if got := phpclassify.LayerOf(fqn); got != "application" {
		t.Errorf("phpclassify.LayerOf(%q) = %q, want application", fqn, got)
	}
}

// INFRA has higher priority than DOMAIN.
func TestPHPLayerOf_InfraPriorityOverDomain(t *testing.T) {
	// "Repositories" (infra) outranks any domain segment that might appear.
	fqn := `App\Repositories\UserRepository`
	if got := phpclassify.LayerOf(fqn); got != "infra" {
		t.Errorf("phpclassify.LayerOf(%q) = %q, want infra", fqn, got)
	}
}

// ---- PHPModuleClassifier.Classify integration tests ----

func TestPHPModuleClassifier_Laravel(t *testing.T) {
	edges := []*analysisdomain.DependencyEdge{
		{FromModule: `App\Http\Controllers\UserController`, ToModule: `App\Models\User`},
		{FromModule: `App\Http\Controllers\UserController`, ToModule: `App\Repositories\UserRepository`},
		{FromModule: `App\Repositories\UserRepository`, ToModule: `App\Models\User`},
		{FromModule: `App\Services\UserService`, ToModule: `App\Models\User`},
		{FromModule: `App\Http\Controllers\UserController`, ToModule: `App\Services\UserService`},
		{FromModule: `Tests\Unit\UserServiceTest`, ToModule: `App\Services\UserService`},
	}

	cls := NewPHPModuleClassifier()
	mc, err := cls.Classify(context.Background(), edges)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	assertContains(t, "ApplicationModules", mc.ApplicationModules, `App\Http\Controllers\UserController`)
	assertContains(t, "DomainModules", mc.DomainModules, `App\Models\User`)
	assertContains(t, "DomainModules", mc.DomainModules, `App\Services\UserService`)
	assertContains(t, "InfraModules", mc.InfraModules, `App\Repositories\UserRepository`)
	assertContains(t, "TestModules", mc.TestModules, `Tests\Unit\UserServiceTest`)

	// No module should appear in multiple buckets.
	assertNoDuplicates(t, mc)
}

func TestPHPModuleClassifier_Symfony(t *testing.T) {
	edges := []*analysisdomain.DependencyEdge{
		{FromModule: `App\Controller\ProductController`, ToModule: `App\Entity\Product`},
		{FromModule: `App\Controller\ProductController`, ToModule: `App\Repository\ProductRepository`},
		{FromModule: `App\Repository\ProductRepository`, ToModule: `App\Entity\Product`},
	}

	cls := NewPHPModuleClassifier()
	mc, err := cls.Classify(context.Background(), edges)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	assertContains(t, "ApplicationModules", mc.ApplicationModules, `App\Controller\ProductController`)
	assertContains(t, "DomainModules", mc.DomainModules, `App\Entity\Product`)
	assertContains(t, "InfraModules", mc.InfraModules, `App\Repository\ProductRepository`)
}

func TestPHPModuleClassifier_StructuralFallback(t *testing.T) {
	// Three unrecognized modules; "Hub" has fan-in 2, others have fan-in 1.
	// With 3 unmatched modules threshold = max(2, 3/4) = 2. Hub's fan-in=2, not > 2,
	// so it stays in DOMAIN. This verifies fallback runs without panicking.
	edges := []*analysisdomain.DependencyEdge{
		{FromModule: `App\Theming\Theme`, ToModule: `App\Theming\Hub`},
		{FromModule: `App\Sorting\Sorter`, ToModule: `App\Theming\Hub`},
	}

	cls := NewPHPModuleClassifier()
	mc, err := cls.Classify(context.Background(), edges)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !mc.StructuralFallback {
		t.Error("expected StructuralFallback=true for unrecognized modules")
	}
	// All three end up in domain (fan-in ≤ threshold).
	if len(mc.DomainModules) != 3 {
		t.Errorf("DomainModules: got %v, want 3 modules", mc.DomainModules)
	}
}

// ---- LanguageAwareClassifier tests ----

func TestLanguageAwareClassifier_PHPEdgesRoutedToPHPClassifier(t *testing.T) {
	edges := []*analysisdomain.DependencyEdge{
		{FromModule: `App\Http\Controllers\UserController`, ToModule: `App\Models\User`},
		{FromModule: `App\Repositories\UserRepository`, ToModule: `App\Models\User`},
	}

	cls := NewLanguageAwareClassifier()
	mc, err := cls.Classify(context.Background(), edges)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	assertContains(t, "ApplicationModules", mc.ApplicationModules, `App\Http\Controllers\UserController`)
	assertContains(t, "DomainModules", mc.DomainModules, `App\Models\User`)
	assertContains(t, "InfraModules", mc.InfraModules, `App\Repositories\UserRepository`)
}

func TestLanguageAwareClassifier_PythonEdgesRoutedToGenericClassifier(t *testing.T) {
	// Conduit-style dotted module names — must go through DeterministicModuleClassifier.
	edges := []*analysisdomain.DependencyEdge{
		{FromModule: "conduit.articles.models", ToModule: "conduit.users.models"},
		{FromModule: "conduit.articles.views", ToModule: "conduit.articles.models"},
		{FromModule: "conduit.articles.serializers", ToModule: "conduit.articles.models"},
		{FromModule: "tests.test_articles", ToModule: "conduit.articles.models"},
	}

	cls := NewLanguageAwareClassifier()
	mc, err := cls.Classify(context.Background(), edges)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	// ApplicationModules must be empty — no PHP modules present.
	if len(mc.ApplicationModules) != 0 {
		t.Errorf("ApplicationModules: expected empty for Python graph, got %v", mc.ApplicationModules)
	}
	// Python classifier produces DOMAIN/INFRA/TEST only.
	if len(mc.DomainModules)+len(mc.InfraModules)+len(mc.TestModules) == 0 {
		t.Error("expected non-empty classification for Python graph")
	}
	assertContains(t, "TestModules", mc.TestModules, "tests.test_articles")
}

func TestLanguageAwareClassifier_EmptyEdges(t *testing.T) {
	cls := NewLanguageAwareClassifier()
	mc, err := cls.Classify(context.Background(), nil)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if mc == nil {
		t.Fatal("expected non-nil result for empty edges")
	}
}

// ---- helpers ----

func assertContains(t *testing.T, bucket string, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("%s does not contain %q\n  got: %v", bucket, want, slice)
}

func assertNoDuplicates(t *testing.T, mc *analysisdomain.ModuleClassification) {
	t.Helper()
	all := make(map[string]string)
	check := func(bucket string, modules []string) {
		for _, m := range modules {
			if prev, ok := all[m]; ok {
				t.Errorf("module %q appears in both %s and %s", m, prev, bucket)
			}
			all[m] = bucket
		}
	}
	check("DomainModules", mc.DomainModules)
	check("ApplicationModules", mc.ApplicationModules)
	check("InfraModules", mc.InfraModules)
	check("TestModules", mc.TestModules)
	// Verify sorted.
	if !sort.StringsAreSorted(mc.DomainModules) {
		t.Errorf("DomainModules is not sorted: %v", mc.DomainModules)
	}
	if !sort.StringsAreSorted(mc.ApplicationModules) {
		t.Errorf("ApplicationModules is not sorted: %v", mc.ApplicationModules)
	}
	if !sort.StringsAreSorted(mc.InfraModules) {
		t.Errorf("InfraModules is not sorted: %v", mc.InfraModules)
	}
}
