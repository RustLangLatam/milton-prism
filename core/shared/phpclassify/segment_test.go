package phpclassify_test

import (
	"testing"

	"milton_prism/core/shared/phpclassify"
)

func TestLayerOf_Application(t *testing.T) {
	cases := []string{
		`App\Http\Controllers\UserController`,
		`BookStack\Http\Controllers\BookController`,
		`App\Console\Commands\SyncUsers`,
		`App\Http\Middleware\Authenticate`,
		`App\Http\Requests\LoginRequest`,
		`App\Actions\CreateBook`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "application" {
			t.Errorf("LayerOf(%q) = %q, want application", fqn, got)
		}
	}
}

func TestLayerOf_Infra(t *testing.T) {
	cases := []string{
		`App\Repositories\UserRepository`,
		`BookStack\Providers\AppServiceProvider`,
		`App\Facades\Auth`,
		`App\Events\UserCreated`,
		`App\Jobs\ProcessUpload`,
		`App\Mail\PasswordReset`,
		`Database\Factories\UserFactory`,
		`Database\Seeders\DatabaseSeeder`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "infra" {
			t.Errorf("LayerOf(%q) = %q, want infra", fqn, got)
		}
	}
}

// Exception classes are error contracts owned by their callers — they must be
// classified as infra in both the analysis and decomposition workers so they
// are excluded from the domain clustering subgraph.
func TestLayerOf_ExceptionsAreInfra(t *testing.T) {
	cases := []string{
		// Top-level exception namespace: no competing domain segment.
		`BookStack\Exceptions\NotFoundException`,
		`BookStack\Exceptions\OidcException`,
		`App\Exceptions\Handler`,
		// Exception nested under a domain namespace: infra wins over domain
		// because infra has higher priority in LayerOf's priority order.
		`BookStack\Entities\Exceptions\UserNotFound`,
		`App\Models\Exceptions\InvalidState`,
		// Single-segment paths.
		`Exceptions\Base`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "infra" {
			t.Errorf("LayerOf(%q) = %q, want infra", fqn, got)
		}
	}
}

// Exception under an infra namespace (e.g. Repositories) was already infra;
// the Exceptions move does not change the result.
func TestLayerOf_ExceptionsUnderInfraStillInfra(t *testing.T) {
	cases := []string{
		`App\Repositories\Exceptions\RecordNotFound`,
		`BookStack\Providers\Exceptions\ContainerException`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "infra" {
			t.Errorf("LayerOf(%q) = %q, want infra", fqn, got)
		}
	}
}

func TestLayerOf_Domain(t *testing.T) {
	cases := []string{
		`BookStack\Entities\Models\Book`,
		`App\Models\User`,
		`App\Services\BookService`,
		`App\Policies\BookPolicy`,
		`App\Rules\UniqueEmail`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "domain" {
			t.Errorf("LayerOf(%q) = %q, want domain", fqn, got)
		}
	}
}

func TestLayerOf_Test(t *testing.T) {
	cases := []string{
		`Tests\Unit\UserTest`,
		`Test\Feature\AuthTest`,
		`App\UserTest`,
		`SomeClass\TestCase`,
	}
	for _, fqn := range cases {
		if got := phpclassify.LayerOf(fqn); got != "test" {
			t.Errorf("LayerOf(%q) = %q, want test", fqn, got)
		}
	}
}

// Infra beats domain when both segments appear (e.g. a Repository in a domain
// namespace prefix). This validates the priority-order in LayerOf.
func TestLayerOf_InfraPriorityOverDomain(t *testing.T) {
	// Repositories is infra, Entities is domain — infra wins.
	fqn := `App\Entities\Repositories\UserRepository`
	if got := phpclassify.LayerOf(fqn); got != "infra" {
		t.Errorf("LayerOf(%q) = %q, want infra", fqn, got)
	}
}

func TestIsPHPModule(t *testing.T) {
	if !phpclassify.IsPHPModule(`App\Http\Controllers\UserController`) {
		t.Error("expected PHP module")
	}
	if phpclassify.IsPHPModule("backend.var") {
		t.Error("expected non-PHP module")
	}
}
