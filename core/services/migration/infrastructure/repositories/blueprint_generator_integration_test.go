//go:build integration

// Integration test for BlueprintGeneratorAdapter.GenerateFromDigest using the REAL
// Anthropic model client. This file is excluded from the normal gate (go test ./...)
// and must be run explicitly:
//
//	ANTHROPIC_API_KEY=sk-ant-... go test ./core/services/migration/infrastructure/repositories/... -tags integration -run TestBlueprintGeneratorAdapter_RealClusters_Live -v
//
// Purpose: verify that the categorical-labels prompt (#2b) holds when the LLM has
// real service clusters to describe — the unit test in blueprint_generator_mock_test.go
// validates the adapter mechanics with a stub, but cannot confirm that the real LLM
// respects the no-digits honesty contract. This test closes that gap.
package repositories

import (
	"context"
	"strings"
	"testing"

	"milton_prism/core/services/migration/domain"
	analysisadapters "milton_prism/core/worker/analysis/infrastructure/adapters"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBlueprintGeneratorAdapter_RealClusters_Live calls the real Anthropic API with
// the synthetic 3-cluster Conduit digest and verifies the honesty contract in vivo.
//
// Assertions — same three as the unit test, now over real LLM output:
//
//	(a) is_hypothetical=false — digest has 3 real clusters; LLM must not flag as hypothetical.
//	(b) No digits in LLM prose — rationale, confidence_note, precondition_note must use
//	    only categorical labels ("very-high coupling", "moderate", …). This is the assertion
//	    the mock test cannot give: a stub cannot prove the LLM respects the constraint.
//	(c) Services map to digest clusters — LLM must not invent modules outside the graph.
func TestBlueprintGeneratorAdapter_RealClusters_Live(t *testing.T) {
	realClient, err := analysisadapters.NewAnthropicModelClient(nil)
	if err != nil {
		t.Skipf("ANTHROPIC_API_KEY not set — skipping live LLM test: %v", err)
	}

	adapter := &BlueprintGeneratorAdapter{client: realClient}
	digest := conduitMockDigest()
	roadmap := &domain.RestructuringRoadmap{
		ActionPlan: []*migrationv1.ActionItem{
			{Order: 1, Kind: "DEFINE_BOUNDARIES", Subject: "conduit.articles, conduit.profile, conduit.user", Blocking: false, Impact: 10},
		},
	}

	blueprint, err := adapter.GenerateFromDigest(context.Background(), digest, roadmap)
	require.NoError(t, err)
	require.NotNil(t, blueprint)

	services := blueprint.GetServices()

	// ── Live output — always printed so the caller can review prose quality. ──────
	t.Logf("=== LIVE BLUEPRINT (real LLM, real clusters) ===")
	t.Logf("cost_usd:        %.6f", blueprint.GetCostUsd())
	t.Logf("is_hypothetical: %v", blueprint.GetIsHypothetical())
	t.Logf("confidence_note: %s", blueprint.GetConfidenceNote())
	t.Logf("precondition_note: %q", blueprint.GetPreconditionNote())
	for i, svc := range services {
		t.Logf("  service[%d]: name=%q modules=%v", i, svc.GetName(), svc.GetModules())
		t.Logf("             rationale: %s", svc.GetRationale())
	}
	t.Logf("=== END ===")

	// (a) Real clusters → LLM must not return is_hypothetical.
	assert.False(t, blueprint.GetIsHypothetical(),
		"digest has real Louvain clusters — blueprint must not be flagged as hypothetical")

	// (b) CRITICAL: no numeric digits anywhere in LLM-generated prose.
	// The categorical prompt instructs the LLM to use level labels, not raw numbers.
	// This is the assertion the mock test cannot provide.
	proseFields := map[string]string{
		"confidence_note":   blueprint.GetConfidenceNote(),
		"precondition_note": blueprint.GetPreconditionNote(),
	}
	for field, text := range proseFields {
		digits := reAnyDigit.FindAllString(text, -1)
		assert.Empty(t, digits,
			"HONESTY VIOLATION: %s contains digits %v — use categorical labels only.\nFull text: %s",
			field, digits, text)
	}
	for _, svc := range services {
		digits := reAnyDigit.FindAllString(svc.GetRationale(), -1)
		assert.Empty(t, digits,
			"HONESTY VIOLATION: service %q rationale contains digits %v — use categorical labels only.\nFull text: %s",
			svc.GetName(), digits, svc.GetRationale())
	}

	// (c) LLM must not invent modules outside the digest graph.
	require.GreaterOrEqual(t, len(services), 2,
		"expected at least 2 services for 3 Louvain clusters")

	nodeSet := make(map[string]bool, len(digest.Graph.Nodes))
	for _, n := range digest.Graph.Nodes {
		nodeSet[n] = true
	}
	for _, svc := range services {
		for _, m := range svc.GetModules() {
			assert.Truef(t, nodeSet[m],
				"service %q declares module %q absent from digest graph — LLM invented a module",
				svc.GetName(), m)
		}
	}

	// Print a digit-check summary for quick scan.
	var violatingServices []string
	for _, svc := range services {
		if reAnyDigit.MatchString(svc.GetRationale()) {
			violatingServices = append(violatingServices, svc.GetName())
		}
	}
	if len(violatingServices) == 0 {
		t.Logf("DIGIT CHECK: PASS — no digits found in any service rationale, confidence_note, or precondition_note")
	} else {
		t.Logf("DIGIT CHECK: FAIL — digits found in services: %s", strings.Join(violatingServices, ", "))
	}
}
