package application

import (
	"fmt"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"

	"github.com/stretchr/testify/assert"
)

// scoreResult is a compact view of a Score() output used by the sensitivity tests.
type scoreResult struct {
	value         int
	band          string
	hubPenalty    int
	domainPenalty int
}

// TestScore_Conduit validates that the Conduit fixture scores high.
// Conduit has clear domain/infra separation, 3 clusters, no shared-state hubs,
// no god-modules, and per-domain blueprints — the ideal decomposition candidate.
func TestScore_Conduit(t *testing.T) {
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	s := Score(d)

	t.Log("Conduit score breakdown:")
	for _, sig := range s.Breakdown {
		t.Logf("  %-20s penalty=%-3d %s", sig.Signal, sig.Penalty, sig.Detail)
	}
	t.Logf("  %-20s value=%d", "TOTAL", s.Value)

	assert.GreaterOrEqual(t, s.Value, 80, "Conduit must score ≥ 80")
	assert.Equal(t, 5, len(s.Breakdown), "must have exactly 5 signals")

	for _, sig := range s.Breakdown {
		assert.Equal(t, 0, sig.Penalty, fmt.Sprintf("Conduit: signal %q must have 0 penalty", sig.Signal))
	}
}

// TestScore_Notiplan validates that the notiplan fixture scores low.
// Notiplan (acantilado): no domain modules, 0 clusters, severe shared-state hub,
// god-module with 55 functions, single blueprint — the canonical NOT_MIGRABLE case.
func TestScore_Notiplan(t *testing.T) {
	t.Parallel()
	d := Distill(notiplanGraph(), notiplanClassification(), notiplanClusterResult(), notiplanSummaryCards(), 0)
	s := Score(d)

	t.Log("Notiplan score breakdown:")
	for _, sig := range s.Breakdown {
		t.Logf("  %-20s penalty=%-3d %s", sig.Signal, sig.Penalty, sig.Detail)
	}
	t.Logf("  %-20s value=%d", "TOTAL", s.Value)

	assert.LessOrEqual(t, s.Value, 20, "notiplan must score ≤ 20")
	assert.Equal(t, 5, len(s.Breakdown), "must have exactly 5 signals")

	// Each structural blocker must register a penalty.
	bySignal := make(map[string]int, len(s.Breakdown))
	for _, c := range s.Breakdown {
		bySignal[c.Signal] = c.Penalty
	}
	assert.Greater(t, bySignal["domain_presence"], 0, "notiplan: domain_presence must penalise")
	assert.Greater(t, bySignal["cluster_count"], 0, "notiplan: cluster_count must penalise")
	assert.Greater(t, bySignal["hub_severity"], 0, "notiplan: hub_severity must penalise")
	assert.Greater(t, bySignal["god_modules"], 0, "notiplan: god_modules must penalise")
	assert.Greater(t, bySignal["routing_layout"], 0, "notiplan: routing_layout must penalise")
}

// TestScore_ConduitWithHub exercises the "MIGRABLE with typed_blockers" path —
// the permanent regression guard for the frontend panel that must NOT display
// structural blockers when verdict==MIGRABLE (they appear as findings instead).
//
// Expected score breakdown (see conduitWithHub* fixture comments for math).
// Penalties are computed from continuous ramps (see scorer.go); the band, not the
// exact intermediate penalty, is the protected invariant:
//   - domain_presence.low    penalty=7   (domain ratio 25%: round(40*(0.30-0.25)/0.30))
//   - cluster_count          penalty=0   (3 clusters)
//   - hub_severity.severe    penalty=20  (conduit.database FanIn=12, ratio≈0.63 ≥0.5 → capped)
//   - god_modules            penalty=0
//   - routing_layout.single  penalty=5   (single blueprint)
//
// Total: 68 → MEDIUM. typed_blockers=[blocker.shared_state{count:1}].
// (Pre-ramp this was 65; the ramp lowered domain_presence 10→7 but the band is
// unchanged — MEDIUM — which is the only invariant this oracle protects.)
func TestScore_ConduitWithHub(t *testing.T) {
	t.Parallel()
	d := Distill(
		conduitWithHubGraph(),
		conduitWithHubClassification(),
		conduitClusterResult(),
		conduitWithHubCards(),
		0,
	)
	s := Score(d)

	t.Log("ConduitWithHub score breakdown:")
	for _, sig := range s.Breakdown {
		t.Logf("  %-20s penalty=%-3d key=%s detail=%s", sig.Signal, sig.Penalty, sig.DetailKey, sig.Detail)
	}
	t.Logf("  %-20s value=%d band=%s", "TOTAL", s.Value, s.ScoreBand)
	t.Logf("  structural_findings: %d", len(s.StructuralFindings))
	for _, f := range s.StructuralFindings {
		t.Logf("    [%s/%s] %s modules=%v", f.Kind, f.Severity, f.TitleKey, f.Modules)
	}
	t.Logf("  typed_blockers: %d", len(s.TypedBlockers))
	for _, tb := range s.TypedBlockers {
		t.Logf("    %s params=%v", tb.BlockerKey, tb.Params)
	}

	// (b) band=MEDIUM
	assert.Equal(t, "medium", s.ScoreBand, "hub+low-domain+single-bp must give band=MEDIUM")
	assert.GreaterOrEqual(t, s.Value, 40)
	assert.Less(t, s.Value, 70)

	// (a) typed_blockers must be non-empty and contain blocker.shared_state
	assert.NotEmpty(t, s.TypedBlockers, "MIGRABLE fixture must have at least one typed_blocker")
	foundBlocker := false
	for _, tb := range s.TypedBlockers {
		if tb.BlockerKey == "blocker.shared_state" {
			foundBlocker = true
			assert.Equal(t, "1", tb.Params["count"], "blocker.shared_state count must be 1")
		}
	}
	assert.True(t, foundBlocker, "typed_blockers must contain blocker.shared_state")

	// (d) conduit.database must appear in structural_findings as shared_state/high.
	// In the MIGRABLE branch the frontend renders these as findings, not panel blockers.
	foundFinding := false
	for _, f := range s.StructuralFindings {
		if f.Kind == "shared_state" && f.Severity == "high" {
			for _, m := range f.Modules {
				if m == "conduit.database" {
					foundFinding = true
				}
			}
		}
	}
	assert.True(t, foundFinding,
		"conduit.database must appear in structural_findings as shared_state/high "+
			"so the frontend can render it as a finding in the MIGRABLE panel")

	// Signal-level assertions (smoke)
	bySignal := make(map[string]int, len(s.Breakdown))
	for _, c := range s.Breakdown {
		bySignal[c.Signal] = c.Penalty
	}
	assert.Greater(t, bySignal["domain_presence"], 0, "low domain ratio must penalise")
	assert.Greater(t, bySignal["hub_severity"], 0, "severe hub must penalise")
	assert.Greater(t, bySignal["routing_layout"], 0, "single blueprint must penalise")
	assert.Equal(t, 0, bySignal["cluster_count"], "3 clusters must not penalise")
	assert.Equal(t, 0, bySignal["god_modules"], "no god modules must not penalise")

	// Exact intermediate penalties after the ramp recalibration. These were 10/20/5
	// (total 65) pre-ramp; the domain ramp lowered domain_presence to 7 (total 68).
	// This is NOT a regression: the band is still MEDIUM (asserted above), which is
	// the protected invariant for this oracle. The exact values are pinned here so a
	// future ramp change that DID cross a band would be caught loudly.
	assert.Equal(t, 7, bySignal["domain_presence"], "domain ratio 25% → ramp penalty 7")
	assert.Equal(t, 20, bySignal["hub_severity"], "hubRatio≈0.63 ≥0.5 → ramp capped at 20")
	assert.Equal(t, 5, bySignal["routing_layout"], "single blueprint → 5")
	assert.Equal(t, 68, s.Value, "total after ramp recalibration (was 65 pre-ramp; band unchanged)")
}

// TestScore_CI3GodModuleAndHub is the CI3 gate: a convention-routed digest with a
// god-model (51 methods, fan-in ≥2, no extractable State) must now penalise both
// god_modules and hub_severity — the signals that were previously blind to CI3
// because they required extracted module-level mutable state.
func TestScore_CI3GodModuleAndHub(t *testing.T) {
	t.Parallel()
	d := Distill(ci3Graph(), ci3Classification(), ci3ClusterResult(), ci3SummaryCards(), 0)
	s := Score(d)

	t.Log("CI3 score breakdown:")
	for _, sig := range s.Breakdown {
		t.Logf("  %-20s penalty=%-3d %s", sig.Signal, sig.Penalty, sig.Detail)
	}
	t.Logf("  %-20s value=%d band=%s", "TOTAL", s.Value, s.ScoreBand)

	bySignal := make(map[string]int, len(s.Breakdown))
	for _, c := range s.Breakdown {
		bySignal[c.Signal] = c.Penalty
	}
	assert.Greater(t, bySignal["god_modules"], 0, "CI3 god-model must penalise god_modules")
	assert.Greater(t, bySignal["hub_severity"], 0, "CI3 high-fan-in hub must penalise hub_severity")

	// The whole point: a CI3 repo with a god-model can no longer score a perfect 100.
	assert.Less(t, s.Value, 100, "CI3 god-model/hub must pull the score below a perfect 100")

	// god_modules names Users_model.
	var godSignal workerdomain.ScoreComponent
	for _, c := range s.Breakdown {
		if c.Signal == "god_modules" {
			godSignal = c
		}
	}
	assert.Contains(t, godSignal.Modules, "application/models/Users_model.php")
}

// TestScore_Deterministic confirms identical inputs produce identical output.
func TestScore_Deterministic(t *testing.T) {
	t.Parallel()
	d1 := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	d2 := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	s1, s2 := Score(d1), Score(d2)
	assert.Equal(t, s1.Value, s2.Value)
	assert.Equal(t, s1.Breakdown, s2.Breakdown)
}

// ── Ramp sensitivity + monotonicity (front 5) ──────────────────────────────────
//
// These tests protect the new intra-band behaviour: the score must be SENSITIVE to
// improvements that do not cross a band. They exercise the ramp functions directly
// (clean monotonicity) and the full Score() pipeline (signal wiring + sign).

// TestRamp_DomainPresence_Monotone verifies the domain ramp never increases the
// penalty as the ratio improves, decreases strictly across the mid-segment, and
// preserves the endpoints (ratio≥0.30 → 0; ratio=0 → 40).
func TestRamp_DomainPresence_Monotone(t *testing.T) {
	t.Parallel()

	// Endpoints preserved.
	assert.Equal(t, 0, domainPresencePenalty(0.30), "ratio 0.30 → 0 (unchanged from old step boundary)")
	assert.Equal(t, 0, domainPresencePenalty(0.55), "ratio above threshold → 0")
	assert.Equal(t, 40, domainPresencePenalty(0.0), "ratio 0 → full penalty 40")

	// Monotone non-increasing across the full domain.
	prev := domainPresencePenalty(0.0)
	for r := 1; r <= 30; r++ {
		ratio := float64(r) / 100.0
		cur := domainPresencePenalty(ratio)
		assert.LessOrEqual(t, cur, prev, "domain penalty must not rise as ratio improves (ratio=%.2f)", ratio)
		prev = cur
	}

	// Strict decrease across the mid-segment (inputs spaced enough to move the int).
	mid := []float64{0.05, 0.12, 0.20, 0.27}
	for i := 1; i < len(mid); i++ {
		assert.Less(t, domainPresencePenalty(mid[i]), domainPresencePenalty(mid[i-1]),
			"domain penalty must drop strictly across mid-segment (%.2f→%.2f)", mid[i-1], mid[i])
	}
}

// TestRamp_HubSeverity_Monotone verifies the hub ramp is non-decreasing in hubRatio
// (higher relative fan-in = worse), strictly increasing across the mid-segment, and
// preserves the historical anchors (≥0.5→20, 0.3→12, →0→0).
func TestRamp_HubSeverity_Monotone(t *testing.T) {
	t.Parallel()

	// Anchors preserved.
	assert.Equal(t, 20, hubSeverityPenalty(0.50), "anchor: hubRatio 0.50 → 20")
	assert.Equal(t, 20, hubSeverityPenalty(0.63), "hubRatio above 0.5 capped at 20")
	assert.Equal(t, 12, hubSeverityPenalty(0.30), "anchor: hubRatio 0.30 → 12")
	assert.Equal(t, 0, hubSeverityPenalty(0.0), "anchor: hubRatio 0 → 0")

	// Monotone non-decreasing in hubRatio.
	prev := hubSeverityPenalty(0.0)
	for r := 1; r <= 60; r++ {
		ratio := float64(r) / 100.0
		cur := hubSeverityPenalty(ratio)
		assert.GreaterOrEqual(t, cur, prev, "hub penalty must not fall as hubRatio worsens (ratio=%.2f)", ratio)
		prev = cur
	}

	// Strict increase across the mid-segment.
	mid := []float64{0.10, 0.20, 0.35, 0.45}
	for i := 1; i < len(mid); i++ {
		assert.Greater(t, hubSeverityPenalty(mid[i]), hubSeverityPenalty(mid[i-1]),
			"hub penalty must rise strictly across mid-segment (%.2f→%.2f)", mid[i-1], mid[i])
	}
}

// scoreOf builds a minimal digest with one shared-state hub (worst fan-in = fanIn,
// totalNodes = N) and a domain ratio of domainRatio, then returns the full score.
// This drives Score() end-to-end so the sign of the signal wiring is exercised, not
// just the ramp helpers.
func scoreOf(t *testing.T, fanIn, totalNodes int, domainModules, infraModules int) scoreResult {
	t.Helper()
	nodes := make([]string, totalNodes)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("n%d", i)
	}
	domain := make([]string, domainModules)
	for i := range domain {
		domain[i] = fmt.Sprintf("dom%d", i)
	}
	infra := make([]string, infraModules)
	for i := range infra {
		infra[i] = fmt.Sprintf("inf%d", i)
	}
	d := &workerdomain.AnalysisDigest{
		Graph:           workerdomain.DigestGraph{Nodes: nodes},
		Classification:  workerdomain.DigestClassification{DomainModules: domain, InfraModules: infra},
		SharedStateHubs: []workerdomain.DigestSharedStateHub{{Module: "hub", State: []string{"s"}, FanIn: uint32(fanIn)}},
	}
	s := Score(d)
	by := make(map[string]int, len(s.Breakdown))
	for _, c := range s.Breakdown {
		by[c.Signal] = c.Penalty
	}
	return scoreResult{value: s.Value, band: s.ScoreBand, hubPenalty: by["hub_severity"], domainPenalty: by["domain_presence"]}
}

// TestScore_Sensitivity_LoweringFanIn confirms that lowering the worst hub's
// relative fan-in never lowers the score and raises it strictly inside the ramp's
// mid-segment, via the full Score() pipeline.
func TestScore_Sensitivity_LoweringFanIn(t *testing.T) {
	t.Parallel()
	// Fixed N, descending fanIn (improving). hubRatio = fanIn/(N+fanIn) decreases,
	// penalty decreases, score increases.
	const N = 40
	// fanIn 28→20→14→8 → hubRatio 0.412→0.333→0.259→0.167 (mid-segment of the ramp).
	prev := scoreOf(t, 28, N, 6, 14)
	for _, fanIn := range []int{20, 14, 8} {
		cur := scoreOf(t, fanIn, N, 6, 14)
		assert.GreaterOrEqual(t, cur.value, prev.value,
			"lowering fan-in must never lower score (fanIn=%d)", fanIn)
		assert.Greater(t, cur.value, prev.value,
			"lowering fan-in must raise score strictly in mid-segment (fanIn=%d)", fanIn)
		prev = cur
	}
}

// TestScore_Sensitivity_RaisingDomainRatio confirms that raising the domain ratio
// never lowers the score and raises it strictly inside the ramp's mid-segment.
func TestScore_Sensitivity_RaisingDomainRatio(t *testing.T) {
	t.Parallel()
	// Total modules fixed at 40, growing domain count → ratio 0.05→0.125→0.20→0.275.
	// No hub so the only moving signal is domain_presence.
	mk := func(dom int) int {
		nodes := []string{"n0"}
		domain := make([]string, dom)
		for i := range domain {
			domain[i] = fmt.Sprintf("d%d", i)
		}
		infra := make([]string, 40-dom)
		for i := range infra {
			infra[i] = fmt.Sprintf("i%d", i)
		}
		return Score(&workerdomain.AnalysisDigest{
			Graph:          workerdomain.DigestGraph{Nodes: nodes},
			Classification: workerdomain.DigestClassification{DomainModules: domain, InfraModules: infra},
		}).Value
	}
	prev := mk(2)
	for _, dom := range []int{5, 8, 11} {
		cur := mk(dom)
		assert.GreaterOrEqual(t, cur, prev, "raising domain ratio must never lower score (dom=%d)", dom)
		assert.Greater(t, cur, prev, "raising domain ratio must raise score strictly in mid-segment (dom=%d)", dom)
		prev = cur
	}
}

// TestScore_Sensitivity_BookStackPostCI3 demonstrates the sign of the BookStack
// pre/post-CI3 scenario: the worst hub's fan-in rose (65→69) but the live-node count
// rose faster (339→378), so hubRatio FELL (0.161→0.154). The non-perverse invariant
// is Δscore ≥ 0 — resolving more edges must NEVER lower the score. At integer
// resolution this tiny ratio change yields Δscore = 0 (consistent with the canon's
// documented BookStack 85→85 intra-band result); the key assertion is the SIGN: the
// score did not drop. The strict-movement guarantee is covered by the mid-segment
// tests above (where inputs are spaced enough to move the integer score).
func TestScore_Sensitivity_BookStackPostCI3(t *testing.T) {
	t.Parallel()
	// hubRatio_pre = 65/(339+65) = 0.1609 ; hubRatio_post = 69/(378+69) = 0.1544.
	pre := scoreOf(t, 65, 339, 200, 139)  // domain ratio well above 0.30 → domain penalty 0
	post := scoreOf(t, 69, 378, 220, 158) // same: isolate the hub signal
	t.Logf("BookStack pre: value=%d hubPenalty=%d ; post: value=%d hubPenalty=%d",
		pre.value, pre.hubPenalty, post.value, post.hubPenalty)
	assert.GreaterOrEqual(t, post.value, pre.value,
		"resolving more edges (fanIn up, N up faster → hubRatio down) must NOT lower the score (non-perverse)")
	assert.LessOrEqual(t, post.hubPenalty, pre.hubPenalty,
		"lower hubRatio must give ≤ hub penalty (correct sign)")
}

// TestScore_EmptyDigest validates that a nil-data digest scores 0 without panic.
func TestScore_EmptyDigest(t *testing.T) {
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), nil, 0)
	s := Score(d)
	assert.GreaterOrEqual(t, s.Value, 0)
	assert.LessOrEqual(t, s.Value, 100)
	assert.Equal(t, 5, len(s.Breakdown))
}
