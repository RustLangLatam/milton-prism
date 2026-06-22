package application

import (
	"fmt"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
)

// patternInput bundles the deterministic structural signals the pipeline has
// already computed, so the classifier is a pure function of in-memory data with
// no I/O. Every field is something an earlier stage produced.
type patternInput struct {
	classification *analysisdomain.ModuleClassification
	score          *commonv1.MigrabilityScore
	cards          []*analysisdomain.ModuleCard
	blueprints     []*analysisdomain.BlueprintInfo
	technologies   []*analysisdomain.Technology
	edges          []*analysisdomain.DependencyEdge
	deepAvailable  bool
}

// classifyArchitecturalPattern maps the already-computed structural signals to one
// canonical architectural pattern with a confidence in [0,1] and the evidence used.
//
// HEURISTICS (deterministic; documented in milton-prism-analysis-engine-spec.md §A.10):
//
// The classifier reasons over six signals, in this order:
//
//	S1 deepAvailable      — was deep (Tier-2) structural analysis produced at all?
//	S2 domainRatio        — domain / (domain+infra), from ModuleClassification.
//	S3 layers             — which layers are present (domain, infra, application).
//	S4 clusters           — number of cohesive clusters (from migrability cluster_count).
//	S5 framework          — the detected web framework (MVC frameworks are a strong prior).
//	S6 routing            — HTTP routing topology (blueprints / route decorators).
//
// Decision order (first match wins; confidence reflects how unambiguous the match is):
//
//  1. NO STRUCTURE: deepAvailable=false OR no domain layer (domainRatio≈0 / DomainEmpty)
//     ⇒ Spaghetti / Big ball of mud / Acantilado. A framework, if any, lowers
//     confidence (the code is framework-shaped but has no separable domain).
//
//  2. MVC: an MVC framework is detected (Laravel, CodeIgniter, Rails, Django,
//     Symfony, Express+routes…) AND HTTP routes/controllers exist. This is the
//     dominant prior for framework monoliths and is checked before the generic
//     layering rules because frameworks impose MVC regardless of folder ratios.
//
//  3. MODULAR MONOLITH: ≥3 cohesive clusters AND a healthy domain ratio (≥0.30)
//     AND no single dominant god/hub — several independent modules in one deployable.
//
//  4. CLEAN / HEXAGONAL: an explicit application layer is present (application_modules
//     non-empty) AND a strong domain ratio (≥0.40). Hexagonal when the application
//     layer is the only "extra" layer over domain+infra (ports/adapters shape);
//     Clean when clustering is also strong (≥3 clusters ⇒ inward-pointing concentric
//     layers). v1 cannot prove dependency-rule direction statically, so these two are
//     reported at reduced confidence and only when the layer signal is explicit.
//
//  5. LAYERED / N-TIER: a domain layer and an infra layer both exist with a
//     moderate ratio, but neither an explicit application layer nor multi-cluster
//     modularity — the classic stacked presentation/business/data shape.
//
//  6. FALLBACK: domain present but signals are weak ⇒ Layered at low confidence.
func classifyArchitecturalPattern(in patternInput) *analysisdomain.ArchitecturalPattern {
	domainN := len(in.classification.GetDomainModules())
	infraN := len(in.classification.GetInfraModules())
	appN := len(in.classification.GetApplicationModules())
	denom := domainN + infraN
	var domainRatio float64
	if denom > 0 {
		domainRatio = float64(domainN) / float64(denom)
	}
	clusters := clusterCountFromScore(in.score)
	framework, fwSlug := primaryFramework(in.technologies)
	noDomain := domainN == 0 || in.classification.GetStructuralFallback() && domainN == 0

	ev := func(items ...string) []string { return items }
	ratioStr := fmt.Sprintf("domain/infra ratio %.0f%%", domainRatio*100)

	// ── Rule 1: no separable structure ⇒ Spaghetti / Acantilado ──────────────
	if !in.deepAvailable || noDomain {
		evidence := []string{}
		conf := float32(0.85)
		if !in.deepAvailable {
			evidence = append(evidence, "no deep structural analysis available (analyzer blind to layout)")
			conf = 0.6
		} else {
			evidence = append(evidence, "no domain layer detected", ratioStr)
		}
		if framework != "" {
			evidence = append(evidence, "framework: "+framework+" (present but no separable domain)")
			conf -= 0.1
		}
		return pattern(analysisdomain.APKindSpaghetti, "Spaghetti / Big ball of mud", conf, evidence)
	}

	// ── Rule 2: MVC framework ⇒ MVC ──────────────────────────────────────────
	// A detected MVC framework (Laravel, CodeIgniter, Rails, Django, Symfony…) is
	// the dominant prior: these frameworks impose an MVC layout regardless of folder
	// ratios. We do NOT require extracted HTTP routes — the PHP/convention analyzers
	// frequently cannot emit RouteInfo (controllers are wired by the framework
	// router, not by decorators the static pass sees), so requiring routes would
	// mis-bucket every Laravel/CodeIgniter monolith. The framework + a controller
	// layer (or any structural data) is enough. Confidence rises with corroborating
	// signals (explicit application layer, extracted routes).
	if isMVCFramework(fwSlug) {
		evidence := ev("framework: "+framework+" (MVC)", ratioStr)
		conf := float32(0.7)
		if appN > 0 {
			evidence = append(evidence, fmt.Sprintf("%d application-layer (controller/middleware) module(s)", appN))
			conf = 0.85
		}
		if r := routeCount(in.cards); r > 0 {
			evidence = append(evidence, fmt.Sprintf("%d HTTP route(s) on controllers", r))
			conf += 0.05
		}
		if len(in.blueprints) > 0 {
			evidence = append(evidence, fmt.Sprintf("%d blueprint group(s)", len(in.blueprints)))
		}
		return pattern(analysisdomain.APKindMVC, "MVC", conf, evidence)
	}

	// ── Rule 3: modular monolith vs. layered ─────────────────────────────────
	// Many cohesive clusters with a healthy domain ratio indicate several
	// independent modules in one deployable — a modular monolith. BUT when a
	// dominant shared-state hub couples those clusters together, the modules are not
	// truly independent: the shape is better described as layered (a shared core all
	// features depend on). We therefore split:
	//   - dominant hub present ⇒ Layered/N-tier (the hub is the shared layer)
	//   - no dominant hub      ⇒ Modular monolith (genuinely independent modules)
	if clusters >= 3 && domainRatio >= 0.30 {
		if hasDominantHub(in.score) {
			evidence := ev(
				ratioStr,
				fmt.Sprintf("%d clusters over a shared infrastructure core", clusters),
				"a dominant shared-state hub couples the feature modules (shared layer)",
			)
			return pattern(analysisdomain.APKindLayered, "Layered / N-tier", 0.6, evidence)
		}
		return pattern(analysisdomain.APKindModularMonolith, "Modular monolith", 0.7, ev(
			fmt.Sprintf("%d cohesive clusters with no dominant coupling hub", clusters),
			ratioStr,
		))
	}

	// ── Rule 4: explicit application layer ⇒ Clean / Hexagonal ───────────────
	if appN > 0 && domainRatio >= 0.40 {
		evidence := ev(
			fmt.Sprintf("explicit application layer (%d module(s))", appN),
			ratioStr,
			fmt.Sprintf("domain layer (%d module(s)) separated from infrastructure (%d)", domainN, infraN),
		)
		if clusters >= 3 {
			evidence = append(evidence, fmt.Sprintf("%d clusters (concentric/inward layering)", clusters))
			return pattern(analysisdomain.APKindClean, "Clean Architecture", 0.6, evidence)
		}
		return pattern(analysisdomain.APKindHexagonal, "Hexagonal (Ports & Adapters)", 0.55, evidence)
	}

	// ── Rule 5: layered / N-tier ─────────────────────────────────────────────
	if domainN > 0 && infraN > 0 {
		evidence := ev(
			ratioStr,
			fmt.Sprintf("domain (%d) and infrastructure (%d) layers present", domainN, infraN),
		)
		conf := float32(0.6)
		if framework != "" {
			evidence = append(evidence, "framework: "+framework)
		}
		if clusters <= 2 {
			evidence = append(evidence, fmt.Sprintf("%d cluster(s) — limited modular separation", clusters))
		}
		if in.classification.GetStructuralFallback() {
			evidence = append(evidence, "classification via structural fallback (heuristic boundaries)")
			conf = 0.45
		}
		return pattern(analysisdomain.APKindLayered, "Layered / N-tier", conf, evidence)
	}

	// ── Rule 6: weak fallback ────────────────────────────────────────────────
	return pattern(
		analysisdomain.APKindLayered,
		"Layered / N-tier",
		0.4,
		ev(ratioStr, "weak structural signal — classified as layered by default"),
	)
}

// mvcFrameworkSlugs are frameworks whose conventional architecture is MVC (or an
// MVC variant). A detected framework in this set is a strong prior for the MVC
// pattern, overriding generic ratio-based layering rules.
var mvcFrameworkSlugs = map[string]bool{
	"laravel":     true,
	"codeigniter": true,
	"symfony":     true,
	"cakephp":     true,
	"yii":         true,
	"rails":       true,
	"django":      true,
	"drf":         true,
	"express":     true,
	"adonis":      true,
	"sails":       true,
	"spring":      true, // Spring MVC
}

func isMVCFramework(slug string) bool { return mvcFrameworkSlugs[strings.ToLower(slug)] }

// primaryFramework returns the display name and slug of the first framework-category
// technology, or empty strings when none is present.
func primaryFramework(techs []*analysisdomain.Technology) (name, slug string) {
	for _, t := range techs {
		if t.GetCategory() == "framework" {
			return t.GetName(), t.GetSlug()
		}
	}
	return "", ""
}

// clusterCountFromScore reads the cluster_count signal's implied cluster total from
// the migrability score. The scorer stores the count in the detail_params of the
// cluster_count signal ("count"); when the penalty maps to a known step we recover
// the count without re-running clustering. Returns 0 when unavailable.
func clusterCountFromScore(score *commonv1.MigrabilityScore) int {
	if score == nil {
		return 0
	}
	for _, s := range score.GetSignals() {
		if s.GetSignal() != "cluster_count" {
			continue
		}
		if c, ok := s.GetDetailParams()["count"]; ok {
			return atoiSafe(c)
		}
		// No explicit count param ⇒ the penalty encodes the band: 0→25, 1→15, 2→5, ≥3→0.
		switch s.GetPenalty() {
		case 25:
			return 0
		case 15:
			return 1
		case 5:
			return 2
		default:
			return 3
		}
	}
	return 0
}

// hasDominantHub reports whether the migrability score recorded a severe/moderate
// shared-state hub (hub_severity penalty > 0).
func hasDominantHub(score *commonv1.MigrabilityScore) bool {
	if score == nil {
		return false
	}
	for _, s := range score.GetSignals() {
		if s.GetSignal() == "hub_severity" && s.GetPenalty() > 0 {
			return true
		}
	}
	return false
}

// routeCount sums HTTP routes across all module cards.
func routeCount(cards []*analysisdomain.ModuleCard) int {
	n := 0
	for _, c := range cards {
		n += len(c.GetRoutes())
	}
	return n
}

// pattern builds an ArchitecturalPattern, clamping confidence to [0,1] and sorting
// nothing (evidence order is intentional). Confidence below 0 is clamped to 0.
func pattern(kind analysisdomain.APKind, name string, conf float32, evidence []string) *analysisdomain.ArchitecturalPattern {
	if conf < 0 {
		conf = 0
	}
	if conf > 1 {
		conf = 1
	}
	// Stable evidence: keep insertion order but drop empties.
	cleaned := evidence[:0]
	for _, e := range evidence {
		if strings.TrimSpace(e) != "" {
			cleaned = append(cleaned, e)
		}
	}
	return &analysisdomain.ArchitecturalPattern{
		Kind:       kind,
		Name:       name,
		Confidence: conf,
		Evidence:   cleaned,
	}
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
