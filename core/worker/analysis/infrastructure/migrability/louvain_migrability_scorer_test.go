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
