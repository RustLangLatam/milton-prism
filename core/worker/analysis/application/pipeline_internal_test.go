package application

import "testing"

// TestIsTestModuleByName verifies the dot-and-backslash split logic added to
// handle PHP namespace separators. The fix replaced strings.Split(m, ".") with
// FieldsFunc splitting on both '.' and '\' so that PHP test namespaces like
// BookStack\Tests\Feature\AuthTest are correctly identified.
func TestIsTestModuleByName(t *testing.T) {
	t.Parallel()

	hits := []string{
		// Python / dot-separated — pre-existing behaviour
		"app.tests.models",
		"app.test.models",
		"tests.views",
		"test.utils",
		"spec.helpers",
		"app.test_utils",
		"app.models_test",
		// PHP backslash — the fixed behaviour
		`BookStack\Tests\Feature\AuthTest`,
		`BookStack\Tests\Unit\PermissionTest`,
		`App\Tests\Feature\UserTest`,
		`BookStack\test\SomeClass`,        // lowercase "test" component
		`App\Modules\spec\SpecRunner`,
		`App\test_helpers\Bootstrap`,
		`App\Models\models_test`,
	}

	misses := []string{
		// Real domain/infra modules — must NOT be flagged
		"app.models",
		"app.views",
		"app.contestant",     // "test" is a substring inside a word, not a full component
		`App\Contestant`,     // PHP equivalent — backslash, but "Contestant" ≠ "test"
		`App\Entities\User`,
		`App\Exceptions\NotFoundException`,
		`BookStack\Services\BookService`,
		`BookStack\Http\Controllers\BookController`,
		// Edge case: empty string
		"",
	}

	for _, m := range hits {
		if !isTestModuleByName(m) {
			t.Errorf("expected %q to be identified as a test module", m)
		}
	}
	for _, m := range misses {
		if isTestModuleByName(m) {
			t.Errorf("expected %q NOT to be identified as a test module", m)
		}
	}
}
