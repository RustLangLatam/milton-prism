package application

import (
	"fmt"
	"sort"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	workerdomain "milton_prism/core/worker/analysis/domain"
)

// intakeInput bundles the already-computed pipeline signals the intake guards
// reason over. Like the architectural-pattern classifier, the guards are a pure
// function of in-memory data — no I/O, no LLM — so they are cheap, deterministic,
// and unit-testable in isolation.
type intakeInput struct {
	// detectedLangs is the go-enry inventory, sorted by file count descending.
	detectedLangs []workerdomain.DetectedLanguage
	// technologies carries the detected frameworks/libraries (category="framework"
	// is the strongest backend signal).
	technologies []*analysisdomain.Technology
	// manifestDeps carries the parsed dependency ecosystems (Composer/PyPI/… ⇒ a
	// backend package manager; npm-only ⇒ frontend-leaning).
	manifestDeps []workerdomain.Dependency
	// cards carry route registrations; HTTP routes are a backend signal.
	cards []*analysisdomain.ModuleCard
	// blueprints are Flask blueprint registrations — a backend (web) signal.
	blueprints []*analysisdomain.BlueprintInfo
	// supportedLanguages is the set of languages with a registered Tier-2 analyzer
	// at run time (today: PHP, Python). Used by the language-support guard so the
	// supported set is never hardcoded — it always reflects the wired registry.
	supportedLanguages map[string]struct{}
}

// backendFrameworkSlugs is the set of framework slugs that unambiguously indicate
// a server-side (backend) application. Matched against Technology.Slug (stable,
// lowercase) so it is independent of how a package manager names the package.
var backendFrameworkSlugs = map[string]struct{}{
	"laravel": {}, "symfony": {}, "codeigniter": {}, "slim": {}, "lumen": {},
	"flask": {}, "django": {}, "fastapi": {}, "tornado": {}, "bottle": {}, "pyramid": {},
	"express": {}, "nestjs": {}, "koa": {}, "hapi": {}, "fastify": {},
	"spring": {}, "springboot": {}, "quarkus": {}, "micronaut": {},
	"rails": {}, "sinatra": {},
	"aspnet": {}, "aspnetcore": {},
	"gin": {}, "echo": {}, "fiber": {}, "chi": {},
}

// frontendFrameworkSlugs is the set of framework slugs that indicate a frontend
// (browser-side) application. A repo whose only framework is one of these — and
// which has no backend framework, no backend ecosystem, and no routes — is a
// frontend-only SPA.
var frontendFrameworkSlugs = map[string]struct{}{
	"react": {}, "vue": {}, "angular": {}, "svelte": {}, "nextjs": {},
	"nuxt": {}, "gatsby": {}, "ember": {}, "preact": {}, "solid": {},
}

// mobileFrameworkSlugs / mobile language markers indicate a mobile application.
var mobileFrameworkSlugs = map[string]struct{}{
	"flutter": {}, "reactnative": {}, "ionic": {}, "xamarin": {},
}

// backendEcosystems maps a parsed manifest ecosystem to whether it is a
// server-side (backend) package manager. npm is intentionally excluded: JS deps
// co-exist with any stack and, on their own, are a frontend-leaning signal.
func isBackendEcosystem(e workerdomain.Ecosystem) bool {
	switch e {
	case workerdomain.EcosystemComposer, workerdomain.EcosystemPyPI,
		workerdomain.EcosystemMaven, workerdomain.EcosystemNuGet,
		workerdomain.EcosystemRubyGems:
		return true
	default:
		return false
	}
}

// assessIntake runs both deterministic intake guards and returns a single
// IntakeAssessment. It is the canonical "can the platform migrate this?" gate:
//
//	migratable == (codebase_kind == BACKEND) && language_supported
//
// HONEST DEGRADATION (Canon): the guards never block and never silently degrade.
// When a check fails, the analysis still completes with all Tier-1 facts, but the
// assessment carries migratable=false plus a specific, human-readable warning so
// the report says exactly why the repo is not migrable instead of emitting a
// migrability verdict as though it were.
//
// Limits (Lesson 11): backend/non-backend cannot be proven from static structure
// alone (a backend can lack a recognised framework; a fullstack repo mixes both).
// The classifier therefore reports a confidence and falls back to UNSPECIFIED
// rather than guessing when the signal mix is genuinely ambiguous. UNSPECIFIED is
// treated as non-migratable but with a softer, "could not confirm" warning.
func assessIntake(in intakeInput) *analysisdomain.IntakeAssessment {
	kind, kindConf, kindEvidence := classifyCodebaseKind(in)

	primaryLang := primaryBackendLanguage(in)
	_, langSupported := in.supportedLanguages[primaryLang]
	supported := sortedKeys(in.supportedLanguages)

	a := &analysisdomain.IntakeAssessment{
		CodebaseKind:       kind,
		KindConfidence:     float32(kindConf),
		PrimaryLanguage:    primaryLang,
		LanguageSupported:  langSupported,
		SupportedLanguages: supported,
		Evidence:           kindEvidence,
	}

	var warnings []string

	// Guard (5) — codebase kind. The platform migrates BACKEND today.
	switch kind {
	case analysisdomain.CodebaseKindBackend:
		// migratable on this axis.
	case analysisdomain.CodebaseKindUnspecified:
		warnings = append(warnings,
			"Could not confirm this repository is a backend service (no web framework, "+
				"backend package manager, or HTTP routes detected). The platform migrates "+
				"backends today; this analysis may be incomplete for a non-backend repository.")
	default:
		warnings = append(warnings, fmt.Sprintf(
			"This repository looks like a %s, not a backend service. The platform only "+
				"migrates backends today, so no migrability verdict is produced for it.",
			codebaseKindPhrase(kind)))
	}

	// Guard (7) — primary-language support. Only meaningful to call out when the
	// repo is (or might be) a backend; for a frontend/library/CLI/mobile repo the
	// kind warning already explains why nothing migrable was produced, and naming
	// an "unsupported language" on top would be noise.
	backendish := kind == analysisdomain.CodebaseKindBackend || kind == analysisdomain.CodebaseKindUnspecified
	if backendish && primaryLang != "" && !langSupported {
		warnings = append(warnings, fmt.Sprintf(
			"Primary language %s is not supported yet (supported: %s). No dependency graph "+
				"was produced, so the deep migrability analysis is unavailable for this repository.",
			primaryLang, strings.Join(supported, ", ")))
	}

	a.Migratable = kind == analysisdomain.CodebaseKindBackend && langSupported
	a.Warnings = warnings
	return a
}

// classifyCodebaseKind deterministically maps the pipeline signals to a CodebaseKind
// with a confidence in [0,1] and the evidence used. Decision order (first decisive
// signal wins); a backend signal always dominates a frontend one (fullstack repos
// are migrated for their backend).
func classifyCodebaseKind(in intakeInput) (analysisdomain.CodebaseKind, float64, []string) {
	var evidence []string

	// Collect framework/ecosystem signals.
	var backendFW, frontendFW, mobileFW []string
	for _, t := range in.technologies {
		if t.GetCategory() != "framework" {
			continue
		}
		slug := strings.ToLower(t.GetSlug())
		name := t.GetName()
		switch {
		case isInSet(backendFrameworkSlugs, slug):
			backendFW = append(backendFW, name)
		case isInSet(frontendFrameworkSlugs, slug):
			frontendFW = append(frontendFW, name)
		case isInSet(mobileFrameworkSlugs, slug):
			mobileFW = append(mobileFW, name)
		}
	}

	hasBackendEcosystem := false
	for _, d := range in.manifestDeps {
		if isBackendEcosystem(d.Ecosystem) {
			hasBackendEcosystem = true
			break
		}
	}

	routeCount := 0
	for _, c := range in.cards {
		routeCount += len(c.GetRoutes())
	}
	hasRoutes := routeCount > 0 || len(in.blueprints) > 0

	// --- BACKEND (strongest; wins over a co-present frontend) ---
	if len(backendFW) > 0 {
		evidence = append(evidence, "framework: "+strings.Join(dedup(backendFW), ", ")+" (web/backend)")
		if hasRoutes {
			evidence = append(evidence, routesEvidence(routeCount, in.blueprints))
		}
		if hasBackendEcosystem {
			evidence = append(evidence, "backend package manager present")
		}
		return analysisdomain.CodebaseKindBackend, 0.95, evidence
	}
	if hasRoutes {
		// HTTP routes/blueprints without a catalogued framework still mean a server.
		evidence = append(evidence, routesEvidence(routeCount, in.blueprints))
		if hasBackendEcosystem {
			evidence = append(evidence, "backend package manager present")
			return analysisdomain.CodebaseKindBackend, 0.9, evidence
		}
		return analysisdomain.CodebaseKindBackend, 0.8, evidence
	}
	if hasBackendEcosystem {
		// A backend package manager (Composer/PyPI/Maven/…) with no recognised
		// framework: most likely a backend whose framework we don't catalogue, or a
		// backend library. Backend is the safer call for a migration platform, but at
		// lower confidence and only if there's no overriding frontend/mobile signal.
		if len(mobileFW) == 0 && len(frontendFW) == 0 {
			evidence = append(evidence, "backend package manager present; no catalogued web framework")
			return analysisdomain.CodebaseKindBackend, 0.6, evidence
		}
	}

	// --- MOBILE ---
	if len(mobileFW) > 0 {
		evidence = append(evidence, "framework: "+strings.Join(dedup(mobileFW), ", ")+" (mobile)")
		return analysisdomain.CodebaseKindMobile, 0.85, evidence
	}

	// --- FRONTEND (only when there is NO backend signal at all) ---
	if len(frontendFW) > 0 {
		evidence = append(evidence, "framework: "+strings.Join(dedup(frontendFW), ", ")+" (frontend SPA)")
		evidence = append(evidence, "no backend framework, package manager, or HTTP routes")
		return analysisdomain.CodebaseKindFrontend, 0.9, evidence
	}

	// JS/TS dominated, npm-only, no server framework ⇒ frontend-leaning.
	primary := primaryDetectedLanguage(in.detectedLangs)
	if isFrontendLanguage(primary) && !hasBackendEcosystem {
		evidence = append(evidence, fmt.Sprintf("primary language %s, npm-only, no server framework", primary))
		return analysisdomain.CodebaseKindFrontend, 0.6, evidence
	}

	// Nothing decisive.
	if primary != "" {
		evidence = append(evidence, fmt.Sprintf("primary language %s; no decisive backend/frontend signal", primary))
	} else {
		evidence = append(evidence, "no language, framework, or ecosystem signal")
	}
	return analysisdomain.CodebaseKindUnspecified, 0.3, evidence
}

// primaryDetectedLanguage returns the highest-ranked detected language name, or "".
// detectedLangs is already sorted by file count descending by the inventory stage.
func primaryDetectedLanguage(langs []workerdomain.DetectedLanguage) string {
	if len(langs) == 0 {
		return ""
	}
	return langs[0].Name
}

// primaryBackendLanguage returns the language the backend is actually written in,
// preferring the first detected language that has a Tier-2 analyzer when one is
// present in the inventory. This mirrors the manifest-language boost: vendored
// frontend assets can outrank the real backend language by raw file count, so when
// a supported backend language appears anywhere in the inventory it is the honest
// "primary language" for the migrability question. Falls back to the top-ranked
// language otherwise.
func primaryBackendLanguage(in intakeInput) string {
	for _, l := range in.detectedLangs {
		if _, ok := in.supportedLanguages[l.Name]; ok {
			return l.Name
		}
	}
	return primaryDetectedLanguage(in.detectedLangs)
}

// isFrontendLanguage reports whether name is a browser-side language.
func isFrontendLanguage(name string) bool {
	switch name {
	case "JavaScript", "TypeScript", "JSX", "TSX", "Vue", "HTML", "CSS", "SCSS":
		return true
	default:
		return false
	}
}

// codebaseKindPhrase renders a CodebaseKind as a natural-language noun phrase for
// the warning message.
func codebaseKindPhrase(k analysisdomain.CodebaseKind) string {
	switch k {
	case analysisdomain.CodebaseKindFrontend:
		return "frontend-only application (SPA / static site)"
	case analysisdomain.CodebaseKindLibrary:
		return "reusable library / package"
	case analysisdomain.CodebaseKindCLI:
		return "command-line tool"
	case analysisdomain.CodebaseKindMobile:
		return "mobile application"
	default:
		return "non-backend project"
	}
}

// routesEvidence renders the route/blueprint signal into an evidence string.
func routesEvidence(routeCount int, blueprints []*analysisdomain.BlueprintInfo) string {
	if routeCount > 0 {
		return fmt.Sprintf("HTTP routes: %d", routeCount)
	}
	return fmt.Sprintf("Flask blueprints: %d", len(blueprints))
}

// --- small helpers ---

func isInSet(set map[string]struct{}, key string) bool {
	if key == "" {
		return false
	}
	_, ok := set[key]
	return ok
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedup(xs []string) []string {
	seen := make(map[string]struct{}, len(xs))
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}
