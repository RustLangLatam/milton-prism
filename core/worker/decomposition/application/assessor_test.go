package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	analysisports "milton_prism/core/worker/analysis/ports"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/mocks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// conduitDigest produces a digest representative of Conduit (3 clusters, clear domain).
func conduitDigest() *workerdomain.AnalysisDigest {
	d := Distill(conduitGraph(), conduitClassification(), conduitClusterResult(), conduitSummaryCards(), 0)
	return d
}

// notiplanDigest produces a digest representative of notiplan (0 clusters, all infra).
func notiplanDigest() *workerdomain.AnalysisDigest {
	d := Distill(notiplanGraph(), notiplanClassification(), notiplanClusterResult(), notiplanSummaryCards(), 0)
	return d
}

func mockVerdict(v workerdomain.MigrabilityVerdict) analysisports.ModelResponse {
	b, _ := json.Marshal(v)
	return analysisports.ModelResponse{Content: string(b), InputTokens: 100, OutputTokens: 50, CostUSD: 0.00004}
}

// ── prompt content tests ──────────────────────────────────────────────────────

func TestAssessor_Prompt_Conduit_MentionsClusters(t *testing.T) {
	t.Parallel()
	d := conduitDigest()
	prompt := buildAssessmentPrompt(d, Score(d), "en")
	assert.Contains(t, prompt, "3 service boundaries")
	assert.Contains(t, prompt, "conduit.articles")
	assert.Contains(t, prompt, "conduit.profile")
}

func TestAssessor_Prompt_Notiplan_MentionsNoBoundaries(t *testing.T) {
	t.Parallel()
	d := notiplanDigest()
	prompt := buildAssessmentPrompt(d, Score(d), "en")
	assert.Contains(t, prompt, "No service boundaries")
}

func TestAssessor_Prompt_Notiplan_MentionsDomainEmpty(t *testing.T) {
	t.Parallel()
	d := notiplanDigest()
	prompt := buildAssessmentPrompt(d, Score(d), "en")
	assert.Contains(t, prompt, "Zero domain modules")
}

func TestAssessor_Prompt_Notiplan_MentionsSharedStateHub(t *testing.T) {
	t.Parallel()
	d := notiplanDigest()
	prompt := buildAssessmentPrompt(d, Score(d), "en")
	// backend.funcs is the god module with mutable state and high fan-in.
	assert.Contains(t, prompt, "backend.funcs")
	assert.True(t, strings.Contains(prompt, "SHARED-STATE HUB") ||
		strings.Contains(prompt, "manager_id_mesa_masters"),
		"prompt must mention the shared-state hub or its state vars")
}

func TestAssessor_Prompt_ContainsSchema(t *testing.T) {
	t.Parallel()
	d := conduitDigest()
	prompt := buildAssessmentPrompt(d, Score(d), "en")
	assert.Contains(t, prompt, `"verdict"`)
	assert.Contains(t, prompt, "MIGRABLE")
	assert.Contains(t, prompt, "NOT_MIGRABLE")
	assert.Contains(t, prompt, `"blockers"`)
}

// ── MIGRABLE-with-blocker guard regression ────────────────────────────────────

// TestAssessor_Assess_ConduitWithHub_MIGRABLE_NonEmptyBlockers is the permanent
// regression test for the case "MIGRABLE verdict + non-empty typed_blockers".
// It proves that:
//
//	(a) typed_blockers contains blocker.shared_state (conduit.database hub FanIn=12)
//	(b) score_band == MEDIUM (score=65, see conduitWithHub* fixture comments)
//	(c) verdict == MIGRABLE — the LLM verdict overrides the structural signal
//	(d) conduit.database appears in structural_findings as shared_state/high
//
// The frontend guard must suppress typed_blockers from the MIGRABLE panel — they
// are shown as structural_findings instead. This test is the regression that ensures
// the suppression guard is NOT trivially tested on a score-100, blocker-free fixture.
//
// The LLM verdict is provided by a mock that returns MIGRABLE explicitly. The mock
// is here ONLY to fix the verdict — all structural data (score, band, findings,
// blockers) come from the real scorer running over the conduitWithHub digest.
func TestAssessor_Assess_ConduitWithHub_MIGRABLE_NonEmptyBlockers(t *testing.T) {
	t.Parallel()

	d := Distill(
		conduitWithHubGraph(),
		conduitWithHubClassification(),
		conduitClusterResult(),
		conduitWithHubCards(),
		0,
	)
	score := Score(d)

	// Sanity: the scorer must have produced a non-empty typed_blockers before we
	// even call the assessor. If this fails the fixture is misconfigured.
	if len(score.TypedBlockers) == 0 {
		t.Fatal("fixture misconfigured: conduitWithHub scorer produced zero typed_blockers; " +
			"check conduitWithHubGraph/Classification/Cards so conduit.database hub has FanIn≥10")
	}

	mc := &mocks.MockModelClient{}
	// Mock verdict: MIGRABLE — only the verdict is mocked; structural data is real.
	mc.On("Complete", mock.Anything, mock.MatchedBy(func(req analysisports.ModelRequest) bool {
		return req.Purpose == "migrability-assessment"
	})).Return(mockVerdict(workerdomain.MigrabilityVerdict{
		Verdict:    workerdomain.VerdictMigrable,
		Summary:    "Flask Conduit with 3 service boundaries and a shared DB hub — decomposable under Prism v1.",
		Reasons:    []string{"3 domain clusters detected", "clear domain/infra separation despite shared database hub"},
		Blockers:   []string{},
		Confidence: workerdomain.ConfidenceHigh,
	}), nil)

	assessor := NewAssessor(mc)
	result, err := assessor.Assess(context.Background(), d, score, "en")
	require.NoError(t, err)

	// (c) verdict == MIGRABLE
	assert.Equal(t, workerdomain.VerdictMigrable, result.Verdict.Verdict)

	// (a) typed_blockers non-empty — contains blocker.shared_state
	require.NotEmpty(t, score.TypedBlockers)
	foundBlocker := false
	for _, tb := range score.TypedBlockers {
		if tb.BlockerKey == "blocker.shared_state" {
			foundBlocker = true
		}
	}
	assert.True(t, foundBlocker, "typed_blockers must contain blocker.shared_state")

	// (b) score_band == MEDIUM
	assert.Equal(t, "medium", score.ScoreBand)

	// (d) conduit.database in structural_findings as shared_state/high
	foundFinding := false
	for _, f := range score.StructuralFindings {
		if f.Kind == "shared_state" && f.Severity == "high" {
			for _, m := range f.Modules {
				if m == "conduit.database" {
					foundFinding = true
				}
			}
		}
	}
	assert.True(t, foundFinding,
		"structural_findings must contain conduit.database as shared_state/high; "+
			"frontend renders it as a finding (not a blocker) in the MIGRABLE panel")

	mc.AssertExpectations(t)
}

// ── verdict parsing tests ─────────────────────────────────────────────────────

func TestAssessor_Assess_Conduit_MIGRABLE(t *testing.T) {
	t.Parallel()
	mc := &mocks.MockModelClient{}
	expected := workerdomain.MigrabilityVerdict{
		Verdict:    workerdomain.VerdictMigrable,
		Summary:    "Flask REST API for a blogging platform, blueprint-organized with 3 clear service boundaries.",
		Reasons:    []string{"3 service clusters detected", "clear domain/infra separation"},
		Blockers:   []string{},
		Confidence: workerdomain.ConfidenceHigh,
	}
	mc.On("Complete", mock.Anything, mock.MatchedBy(func(req analysisports.ModelRequest) bool {
		return req.Purpose == "migrability-assessment"
	})).Return(mockVerdict(expected), nil)

	assessor := NewAssessor(mc)
	d := conduitDigest()
	result, err := assessor.Assess(context.Background(), d, Score(d), "en")

	require.NoError(t, err)
	require.NotNil(t, result.Verdict)
	assert.Equal(t, workerdomain.VerdictMigrable, result.Verdict.Verdict)
	assert.Equal(t, workerdomain.ConfidenceHigh, result.Verdict.Confidence)
	assert.Empty(t, result.Verdict.Blockers)
	assert.Greater(t, result.CostUSD, 0.0)
	mc.AssertExpectations(t)
}

func TestAssessor_Assess_Notiplan_NOT_MIGRABLE(t *testing.T) {
	t.Parallel()
	mc := &mocks.MockModelClient{}
	expected := workerdomain.MigrabilityVerdict{
		Verdict: workerdomain.VerdictNotMigrable,
		Summary: "Flask application with no domain layer; all logic in a single god module.",
		Reasons: []string{"zero domain modules", "no service boundaries"},
		Blockers: []string{
			"backend.funcs is a shared-state hub (fan-in=12) — must be split before decomposition",
			"no domain/infra separation — codebase requires structural refactoring first",
		},
		Confidence: workerdomain.ConfidenceHigh,
	}
	mc.On("Complete", mock.Anything, mock.Anything).Return(mockVerdict(expected), nil)

	assessor := NewAssessor(mc)
	d := notiplanDigest()
	result, err := assessor.Assess(context.Background(), d, Score(d), "en")

	require.NoError(t, err)
	require.NotNil(t, result.Verdict)
	assert.Equal(t, workerdomain.VerdictNotMigrable, result.Verdict.Verdict)
	assert.NotEmpty(t, result.Verdict.Blockers)
	mc.AssertExpectations(t)
}

func TestAssessor_Assess_RetriesOnInvalidJSON(t *testing.T) {
	// First response is not JSON; second is valid.
	t.Parallel()
	mc := &mocks.MockModelClient{}
	valid := workerdomain.MigrabilityVerdict{
		Verdict: workerdomain.VerdictPartial, Summary: "s", Reasons: []string{"r"},
		Blockers: []string{"b"}, Confidence: workerdomain.ConfidenceMedium,
	}
	b, _ := json.Marshal(valid)

	first := analysisports.ModelResponse{Content: "not json at all", InputTokens: 80, OutputTokens: 20}
	second := analysisports.ModelResponse{Content: string(b), InputTokens: 90, OutputTokens: 60}

	mc.On("Complete", mock.Anything, mock.Anything).Return(first, nil).Once()
	mc.On("Complete", mock.Anything, mock.Anything).Return(second, nil).Once()

	assessor := NewAssessor(mc)
	d := conduitDigest()
	result, err := assessor.Assess(context.Background(), d, Score(d), "en")

	require.NoError(t, err)
	assert.Equal(t, workerdomain.VerdictPartial, result.Verdict.Verdict)
	// Cost must be summed across both calls.
	assert.Equal(t, first.InputTokens+second.InputTokens, result.InputTokens)
	mc.AssertExpectations(t)
}

func TestAssessor_Assess_PropagatesModelError(t *testing.T) {
	t.Parallel()
	mc := &mocks.MockModelClient{}
	mc.On("Complete", mock.Anything, mock.Anything).
		Return(analysisports.ModelResponse{}, assert.AnError)

	assessor := NewAssessor(mc)
	d := conduitDigest()
	_, err := assessor.Assess(context.Background(), d, Score(d), "en")
	assert.Error(t, err)
}

func TestAssessor_Assess_JSONInMarkdownFence(t *testing.T) {
	// Model wraps JSON in ```json ... ``` fences — should be stripped.
	t.Parallel()
	mc := &mocks.MockModelClient{}
	v := workerdomain.MigrabilityVerdict{
		Verdict: workerdomain.VerdictMigrable, Summary: "s",
		Reasons: []string{"r"}, Blockers: []string{}, Confidence: workerdomain.ConfidenceHigh,
	}
	b, _ := json.Marshal(v)
	fenced := "```json\n" + string(b) + "\n```"

	mc.On("Complete", mock.Anything, mock.Anything).
		Return(analysisports.ModelResponse{Content: fenced}, nil)

	assessor := NewAssessor(mc)
	d := conduitDigest()
	result, err := assessor.Assess(context.Background(), d, Score(d), "en")
	require.NoError(t, err)
	assert.Equal(t, workerdomain.VerdictMigrable, result.Verdict.Verdict)
}
