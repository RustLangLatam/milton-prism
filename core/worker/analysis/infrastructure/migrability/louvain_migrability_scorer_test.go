package migrability

import (
	"context"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScore_GuardrailUpdatesClassification verifies that when the coherence
// guardrail fires (hub-and-spoke graph with no real domain boundaries), Score
// updates the caller's ModuleClassification so that DomainModules is empty and
// the modules move to InfraModules. This keeps module_classification_bytes
// consistent with the score (DomainEmpty=true → score ~0).
//
// The fixture mirrors notiplan 10003: all non-hub modules import only hub nodes
// (backend.var, backend.funcs) with no cross-spoke edges, producing singleton
// fallback clusters with zero internal cohesion.
func TestScore_GuardrailUpdatesClassification(t *testing.T) {
	// hub-and-spoke: backend.var and backend.funcs are the hubs; everything
	// else imports only the hubs — no cross-spoke edges.
	edges := []*analysisdomain.DependencyEdge{
		{FromModule: "backend.create_tables", ToModule: "backend.var", Weight: 2},
		{FromModule: "backend.reset_tareas", ToModule: "backend.var", Weight: 1},
		{FromModule: "backend.session", ToModule: "backend.var", Weight: 1},
		{FromModule: "backend.itxi", ToModule: "backend.var", Weight: 1},
		{FromModule: "backend.op_disp", ToModule: "backend.var", Weight: 2},
		{FromModule: "backend.operario_disp", ToModule: "backend.var", Weight: 2},
		{FromModule: "backend.plant_table_report", ToModule: "backend.funcs", Weight: 8},
		{FromModule: "backend.plant_table_report", ToModule: "backend.var", Weight: 2},
		{FromModule: "backend.funcs", ToModule: "backend.var", Weight: 3},
		{FromModule: "backend.ingeteam_backend", ToModule: "backend.funcs", Weight: 1},
		{FromModule: "backend.ingeteam_backend", ToModule: "backend.var", Weight: 1},
	}

	// Stage 6c result: structural fallback detected hubs, spokes labelled domain.
	cls := &analysisdomain.ModuleClassification{
		DomainModules:      []string{"backend.create_tables", "backend.ingeteam_backend", "backend.op_disp", "backend.operario_disp", "backend.plant_table_report", "backend.reset_tareas", "backend.session", "backend.itxi"},
		InfraModules:       []string{"backend.funcs", "backend.var"},
		StructuralFallback: true,
	}

	s := NewLouvainMigrabilityScorer()
	score, err := s.Score(context.Background(), edges, cls, nil, nil)
	require.NoError(t, err)

	// Guardrail must have fired: DomainEmpty=true (−40) + no clusters (−25) = 35.
	// Hub/god/routing penalties require module cards; without them the floor is 35.
	assert.Equal(t, int32(35), score.GetValue(), "hub-and-spoke graph must score 35 after guardrail (DomainEmpty−40, NoClusters−25)")

	// Caller's classification must be corrected in-place.
	assert.Empty(t, cls.DomainModules, "DomainModules must be cleared after guardrail fires")
	assert.Contains(t, cls.InfraModules, "backend.create_tables", "previously-domain modules must move to InfraModules")
	assert.Contains(t, cls.InfraModules, "backend.funcs", "original infra must remain in InfraModules")

	// All original domain modules must be accounted for in infra.
	for _, m := range []string{"backend.create_tables", "backend.ingeteam_backend", "backend.op_disp",
		"backend.operario_disp", "backend.plant_table_report", "backend.reset_tareas", "backend.session", "backend.itxi"} {
		assert.Contains(t, cls.InfraModules, m, "guardrail: %q must be in InfraModules", m)
	}
}

// TestScore_NoGuardrailForRealDomain verifies that Score does NOT mutate the
// classification when the graph has real domain structure (Conduit-like).
func TestScore_NoGuardrailForRealDomain(t *testing.T) {
	edges := []*analysisdomain.DependencyEdge{
		{FromModule: "conduit.articles.views", ToModule: "conduit.articles.models", Weight: 2},
		{FromModule: "conduit.articles.views", ToModule: "conduit.user.models", Weight: 1},
		{FromModule: "conduit.profile.views", ToModule: "conduit.profile.models", Weight: 2},
		{FromModule: "conduit.user.views", ToModule: "conduit.user.models", Weight: 2},
	}
	cls := &analysisdomain.ModuleClassification{
		DomainModules: []string{
			"conduit.articles.models", "conduit.articles.views",
			"conduit.profile.models", "conduit.profile.views",
			"conduit.user.models", "conduit.user.views",
		},
		InfraModules: []string{"conduit.app", "conduit.config"},
	}
	origDomain := make([]string, len(cls.DomainModules))
	copy(origDomain, cls.DomainModules)

	s := NewLouvainMigrabilityScorer()
	score, err := s.Score(context.Background(), edges, cls, nil, nil)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, score.GetValue(), int32(70), "Conduit must score well")
	assert.Equal(t, origDomain, cls.DomainModules, "DomainModules must be unchanged when guardrail does not fire")
}

// ci3MethodNames returns n synthetic method names for a CI3 god-model card.
func ci3MethodNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "method"
	}
	return out
}

// TestScore_CI3_AssessorPathScoresHubsAndGod is the assessor-path gate.
//
// The migrability assessor (AnalysisMigrabilityAssessorAdapter.Assess) loads the
// persisted cards via MongoGraphLoader.LoadCards — a proto round-trip that
// preserves Module and File verbatim — and feeds them through the SAME
// decomposition Distill→Score this scorer drives. CodeIgniter 3 cards carry
// Module == File ending in .php and NO extractable module-level state, so the
// only honest coupling signal is structural fan-in. This test feeds CI3-shaped
// *analysisdomain.ModuleCard (exactly what LoadCards produces) and asserts the
// god-model and base class surface as hubs, pulling the score below a perfect
// 100 — i.e. the assessor cannot re-score a CI3 monolith back up to 100.
//
// Regression guard for: eurofunding evaluateMigrability previously persisted 100
// because the CI3 hub predicate did not fire on the assessor's loaded cards.
func TestScore_CI3_AssessorPathScoresHubsAndGod(t *testing.T) {
	// Six controllers each load the Users_model god-model and extend MY_Controller.
	controllers := []string{
		"application/controllers/Admin.php",
		"application/controllers/Users.php",
		"application/controllers/Memo.php",
		"application/controllers/Workers.php",
		"application/controllers/Reports.php",
		"application/controllers/Auth.php",
	}
	var edges []*analysisdomain.DependencyEdge
	for _, ctl := range controllers {
		edges = append(edges,
			&analysisdomain.DependencyEdge{FromModule: ctl, ToModule: "application/models/Users_model.php", Weight: 1},
			&analysisdomain.DependencyEdge{FromModule: ctl, ToModule: "application/core/MY_Controller.php", Weight: 1},
		)
	}

	cls := &analysisdomain.ModuleClassification{
		// CI3 convention modules fall through to the structural-fallback infra bucket.
		InfraModules: append([]string{
			"application/models/Users_model.php",
			"application/core/MY_Controller.php",
		}, controllers...),
		StructuralFallback: true,
	}

	// CI3 cards: Module == File == workspace-relative .php path, NO ModuleLevelState.
	cards := []*analysisdomain.ModuleCard{
		{
			Module:    "application/models/Users_model.php",
			File:      "application/models/Users_model.php",
			Functions: ci3MethodNames(51), // god-model: >= 20 functions
			Classes:   []string{"Users_model"},
			Loc:       900,
		},
		{
			Module:    "application/core/MY_Controller.php",
			File:      "application/core/MY_Controller.php",
			Functions: ci3MethodNames(8),
			Classes:   []string{"MY_Controller"},
			Loc:       300,
		},
	}
	for _, ctl := range controllers {
		cards = append(cards, &analysisdomain.ModuleCard{
			Module:    ctl,
			File:      ctl,
			Functions: ci3MethodNames(10),
			Classes:   []string{"Controller"},
			Loc:       150,
		})
	}

	s := NewLouvainMigrabilityScorer()
	score, err := s.Score(context.Background(), edges, cls, cards, nil)
	require.NoError(t, err)

	bySignal := make(map[string]int32, len(score.GetSignals()))
	for _, sig := range score.GetSignals() {
		bySignal[sig.GetSignal()] = sig.GetPenalty()
	}
	assert.Greater(t, bySignal["hub_severity"], int32(0),
		"CI3 high-fan-in hub (Module==File .php, no State) must penalise hub_severity on the assessor path")
	assert.Greater(t, bySignal["god_modules"], int32(0),
		"CI3 god-model (51 methods, high fan-in) must penalise god_modules on the assessor path")
	assert.Less(t, score.GetValue(), int32(100),
		"the assessor path must not re-score a CI3 monolith back to a perfect 100")
}
