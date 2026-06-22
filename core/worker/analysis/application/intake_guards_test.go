package application

import (
	"strings"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	workerdomain "milton_prism/core/worker/analysis/domain"
)

// supported is the run-time analyzer set used across the intake tests: PHP + Python,
// matching the production registry wiring.
func supported() map[string]struct{} {
	return map[string]struct{}{"PHP": {}, "Python": {}}
}

func tech(name, category, slug string) *analysisdomain.Technology {
	return &analysisdomain.Technology{Name: name, Category: category, Slug: slug}
}

func lang(name string, files uint64) workerdomain.DetectedLanguage {
	return workerdomain.DetectedLanguage{Name: name, Files: files}
}

func cardWithRoutes(n int) *analysisdomain.ModuleCard {
	routes := make([]*analysisdomain.RouteInfo, n)
	for i := range routes {
		routes[i] = &analysisdomain.RouteInfo{Method: "GET", Path: "/x"}
	}
	return &analysisdomain.ModuleCard{Module: "m", Routes: routes}
}

func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// --- Backend, supported language: no warning, migratable. (no false positive) ---

func TestIntake_BackendSupported_PHP_Laravel(t *testing.T) {
	t.Parallel()
	// BookStack-like: Laravel framework, Composer ecosystem, PHP primary.
	got := assessIntake(intakeInput{
		detectedLangs:      []workerdomain.DetectedLanguage{lang("PHP", 400), lang("Blade", 200)},
		technologies:       []*analysisdomain.Technology{tech("Laravel", "framework", "laravel")},
		manifestDeps:       []workerdomain.Dependency{{Ecosystem: workerdomain.EcosystemComposer, Package: "laravel/framework"}},
		supportedLanguages: supported(),
	})
	if got.GetCodebaseKind() != analysisdomain.CodebaseKindBackend {
		t.Fatalf("kind = %v, want BACKEND", got.GetCodebaseKind())
	}
	if !got.GetLanguageSupported() {
		t.Fatalf("languageSupported = false, want true")
	}
	if !got.GetMigratable() {
		t.Fatalf("migratable = false, want true")
	}
	if len(got.GetWarnings()) != 0 {
		t.Fatalf("warnings = %v, want none", got.GetWarnings())
	}
	if got.GetPrimaryLanguage() != "PHP" {
		t.Fatalf("primaryLanguage = %q, want PHP", got.GetPrimaryLanguage())
	}
}

func TestIntake_BackendSupported_Python_Flask(t *testing.T) {
	t.Parallel()
	// flask-realworld-like: Flask blueprints, PyPI ecosystem, Python primary.
	got := assessIntake(intakeInput{
		detectedLangs:      []workerdomain.DetectedLanguage{lang("Python", 120)},
		technologies:       []*analysisdomain.Technology{tech("Flask", "framework", "flask")},
		manifestDeps:       []workerdomain.Dependency{{Ecosystem: workerdomain.EcosystemPyPI, Package: "flask"}},
		blueprints:         []*analysisdomain.BlueprintInfo{{Name: "users"}},
		supportedLanguages: supported(),
	})
	if !got.GetMigratable() || len(got.GetWarnings()) != 0 {
		t.Fatalf("want migratable & no warnings, got migratable=%v warnings=%v", got.GetMigratable(), got.GetWarnings())
	}
}

// Backend with routes but no catalogued framework still classifies BACKEND.
func TestIntake_BackendByRoutesNoFramework(t *testing.T) {
	t.Parallel()
	got := assessIntake(intakeInput{
		detectedLangs:      []workerdomain.DetectedLanguage{lang("Python", 60)},
		manifestDeps:       []workerdomain.Dependency{{Ecosystem: workerdomain.EcosystemPyPI, Package: "somelib"}},
		cards:              []*analysisdomain.ModuleCard{cardWithRoutes(5)},
		supportedLanguages: supported(),
	})
	if got.GetCodebaseKind() != analysisdomain.CodebaseKindBackend {
		t.Fatalf("kind = %v, want BACKEND", got.GetCodebaseKind())
	}
	if !got.GetMigratable() {
		t.Fatalf("migratable = false, want true")
	}
}

// --- Guard (5): non-backend (frontend-only SPA) → warning, not migratable. ---

func TestIntake_FrontendOnlySPA_NotMigratable(t *testing.T) {
	t.Parallel()
	// React SPA: only npm, React framework, JS primary, no routes/backend ecosystem.
	got := assessIntake(intakeInput{
		detectedLangs:      []workerdomain.DetectedLanguage{lang("JavaScript", 200), lang("CSS", 40)},
		technologies:       []*analysisdomain.Technology{tech("React", "framework", "react")},
		manifestDeps:       []workerdomain.Dependency{{Ecosystem: workerdomain.EcosystemNpm, Package: "react"}},
		supportedLanguages: supported(),
	})
	if got.GetCodebaseKind() != analysisdomain.CodebaseKindFrontend {
		t.Fatalf("kind = %v, want FRONTEND", got.GetCodebaseKind())
	}
	if got.GetMigratable() {
		t.Fatalf("migratable = true, want false for a frontend SPA")
	}
	if !hasWarningContaining(got.GetWarnings(), "frontend") {
		t.Fatalf("expected a frontend warning, got %v", got.GetWarnings())
	}
	// Guard (7) must NOT also fire: the kind warning already explains it.
	if hasWarningContaining(got.GetWarnings(), "not supported yet") {
		t.Fatalf("language warning should be suppressed for a frontend repo, got %v", got.GetWarnings())
	}
}

// npm-only JS with no framework at all is still frontend-leaning (lower confidence).
func TestIntake_NpmOnlyJS_Frontend(t *testing.T) {
	t.Parallel()
	got := assessIntake(intakeInput{
		detectedLangs:      []workerdomain.DetectedLanguage{lang("TypeScript", 90)},
		manifestDeps:       []workerdomain.Dependency{{Ecosystem: workerdomain.EcosystemNpm, Package: "lodash"}},
		supportedLanguages: supported(),
	})
	if got.GetCodebaseKind() != analysisdomain.CodebaseKindFrontend {
		t.Fatalf("kind = %v, want FRONTEND", got.GetCodebaseKind())
	}
	if got.GetMigratable() {
		t.Fatalf("migratable = true, want false")
	}
}

// Mobile app → not migratable.
func TestIntake_Mobile_NotMigratable(t *testing.T) {
	t.Parallel()
	got := assessIntake(intakeInput{
		detectedLangs:      []workerdomain.DetectedLanguage{lang("Dart", 150)},
		technologies:       []*analysisdomain.Technology{tech("Flutter", "framework", "flutter")},
		supportedLanguages: supported(),
	})
	if got.GetCodebaseKind() != analysisdomain.CodebaseKindMobile {
		t.Fatalf("kind = %v, want MOBILE", got.GetCodebaseKind())
	}
	if got.GetMigratable() {
		t.Fatalf("migratable = true, want false")
	}
	if !hasWarningContaining(got.GetWarnings(), "mobile") {
		t.Fatalf("expected a mobile warning, got %v", got.GetWarnings())
	}
}

// --- Guard (7): backend in an UNSUPPORTED language → warning, not migratable. ---

func TestIntake_UnsupportedLanguage_JavaSpring(t *testing.T) {
	t.Parallel()
	// A real Spring backend, but Java has no Tier-2 analyzer today.
	got := assessIntake(intakeInput{
		detectedLangs:      []workerdomain.DetectedLanguage{lang("Java", 300)},
		technologies:       []*analysisdomain.Technology{tech("Spring", "framework", "spring")},
		manifestDeps:       []workerdomain.Dependency{{Ecosystem: workerdomain.EcosystemMaven, Package: "org.springframework"}},
		supportedLanguages: supported(),
	})
	if got.GetCodebaseKind() != analysisdomain.CodebaseKindBackend {
		t.Fatalf("kind = %v, want BACKEND (Spring is a backend)", got.GetCodebaseKind())
	}
	if got.GetLanguageSupported() {
		t.Fatalf("languageSupported = true, want false for Java")
	}
	if got.GetMigratable() {
		t.Fatalf("migratable = true, want false for an unsupported-language backend")
	}
	if !hasWarningContaining(got.GetWarnings(), "Java is not supported yet") {
		t.Fatalf("expected an unsupported-language warning naming Java, got %v", got.GetWarnings())
	}
	// The warning must list the supported set honestly.
	if !hasWarningContaining(got.GetWarnings(), "PHP, Python") {
		t.Fatalf("expected supported list in warning, got %v", got.GetWarnings())
	}
}

func TestIntake_UnsupportedLanguage_Ruby(t *testing.T) {
	t.Parallel()
	got := assessIntake(intakeInput{
		detectedLangs:      []workerdomain.DetectedLanguage{lang("Ruby", 180)},
		technologies:       []*analysisdomain.Technology{tech("Rails", "framework", "rails")},
		manifestDeps:       []workerdomain.Dependency{{Ecosystem: workerdomain.EcosystemRubyGems, Package: "rails"}},
		supportedLanguages: supported(),
	})
	if got.GetCodebaseKind() != analysisdomain.CodebaseKindBackend {
		t.Fatalf("kind = %v, want BACKEND", got.GetCodebaseKind())
	}
	if got.GetMigratable() {
		t.Fatalf("migratable = true, want false for Ruby (unsupported)")
	}
	if !hasWarningContaining(got.GetWarnings(), "Ruby is not supported yet") {
		t.Fatalf("expected Ruby warning, got %v", got.GetWarnings())
	}
}

// --- Edge: primary-language boost. Vendored JS outranks PHP by file count, but a
// supported backend language present anywhere is the honest primary language. ---

func TestIntake_PrimaryLanguageBoost_VendoredJSDoesNotMaskPHP(t *testing.T) {
	t.Parallel()
	got := assessIntake(intakeInput{
		// JS has more files (vendored assets) than PHP, but PHP is the backend.
		detectedLangs:      []workerdomain.DetectedLanguage{lang("JavaScript", 500), lang("PHP", 300)},
		technologies:       []*analysisdomain.Technology{tech("Laravel", "framework", "laravel")},
		manifestDeps:       []workerdomain.Dependency{{Ecosystem: workerdomain.EcosystemComposer, Package: "laravel/framework"}},
		supportedLanguages: supported(),
	})
	if got.GetPrimaryLanguage() != "PHP" {
		t.Fatalf("primaryLanguage = %q, want PHP (boosted over vendored JS)", got.GetPrimaryLanguage())
	}
	if !got.GetMigratable() {
		t.Fatalf("migratable = false, want true")
	}
}

// --- Edge: empty repo / no signal → UNSPECIFIED, not migratable, soft warning. ---

func TestIntake_NoSignal_Unspecified(t *testing.T) {
	t.Parallel()
	got := assessIntake(intakeInput{supportedLanguages: supported()})
	if got.GetCodebaseKind() != analysisdomain.CodebaseKindUnspecified {
		t.Fatalf("kind = %v, want UNSPECIFIED", got.GetCodebaseKind())
	}
	if got.GetMigratable() {
		t.Fatalf("migratable = true, want false")
	}
	if !hasWarningContaining(got.GetWarnings(), "Could not confirm") {
		t.Fatalf("expected a soft 'could not confirm' warning, got %v", got.GetWarnings())
	}
}

// --- Supported set is reported honestly and sorted. ---

func TestIntake_SupportedLanguagesReportedSorted(t *testing.T) {
	t.Parallel()
	got := assessIntake(intakeInput{
		detectedLangs:      []workerdomain.DetectedLanguage{lang("PHP", 10)},
		technologies:       []*analysisdomain.Technology{tech("Laravel", "framework", "laravel")},
		supportedLanguages: map[string]struct{}{"Python": {}, "PHP": {}},
	})
	want := []string{"PHP", "Python"}
	if got := got.GetSupportedLanguages(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("supportedLanguages = %v, want %v (sorted)", got, want)
	}
}
