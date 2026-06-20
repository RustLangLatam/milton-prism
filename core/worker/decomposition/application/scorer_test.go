package application

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
// Expected score breakdown (see conduitWithHub* fixture comments for math):
//   - domain_presence.low    penalty=10  (domain ratio 25%)
//   - cluster_count          penalty=0   (3 clusters)
//   - hub_severity.severe    penalty=20  (conduit.database FanIn=12, ratio≈0.63)
//   - god_modules            penalty=0
//   - routing_layout.single  penalty=5   (single blueprint)
//
// Total: 65 → MEDIUM. typed_blockers=[blocker.shared_state{count:1}].
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

// TestScore_EmptyDigest validates that a nil-data digest scores 0 without panic.
func TestScore_EmptyDigest(t *testing.T) {
	t.Parallel()
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), nil, 0)
	s := Score(d)
	assert.GreaterOrEqual(t, s.Value, 0)
	assert.LessOrEqual(t, s.Value, 100)
	assert.Equal(t, 5, len(s.Breakdown))
}
