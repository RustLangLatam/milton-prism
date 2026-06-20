package application_test

import (
	"context"
	"strings"
	"testing"

	"milton_prism/core/services/migration/application"
	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/mocks"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func flaskRoadmap() *domain.RestructuringRoadmap {
	return &domain.RestructuringRoadmap{
		ActionPlan: []*migrationv1.ActionItem{
			{Order: 1, Kind: "EXTRACT_DOMAIN", Subject: "backend.funcs, backend.var", Action: "Extract domain logic", Blocking: true, Impact: 40},
			{Order: 2, Kind: "DEFINE_BOUNDARIES", Subject: "no boundaries", Action: "Define service clusters", Blocking: true, Impact: 25, DependsOnStep: 1},
			{Order: 3, Kind: "DECOUPLE_STATE", Subject: "backend.var", Action: "Decouple shared state", Blocking: true, Impact: 20},
			{Order: 4, Kind: "SPLIT_GOD_MODULE", Subject: "backend.funcs", Action: "Split god module", Blocking: false, Impact: 10},
			{Order: 5, Kind: "ADD_ROUTING", Subject: "all routes", Action: "Add per-domain routing", Blocking: false, Impact: 5, DependsOnStep: 2},
		},
	}
}

func newSvcWithDetector(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockStackDetector) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	detector := &mocks.MockStackDetector{}
	svc := application.NewService(repo, tx, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, detector, "")
	return svc, repo, detector
}

// ── ExportActionPlanPrompt use-case unit tests ────────────────────────────────

func TestExportActionPlanPrompt_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithDetector(t)
	_, _, err := svc.ExportActionPlanPrompt(context.Background(), 0)
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}

func TestExportActionPlanPrompt_WrongState(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithDetector(t)
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	_, _, err := svc.ExportActionPlanPrompt(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
}

func TestExportActionPlanPrompt_NoActionPlan(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithDetector(t)
	m := &domain.Migration{
		Identifier:           7,
		State:                domain.MigrationStateRestructuringReady,
		RestructuringRoadmap: &domain.RestructuringRoadmap{},
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	_, _, err := svc.ExportActionPlanPrompt(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeNoActionPlan)
}

func TestExportActionPlanPrompt_Flask_FullProfile(t *testing.T) {
	t.Parallel()
	svc, repo, detector := newSvcWithDetector(t)

	m := &domain.Migration{
		Identifier:           7,
		State:                domain.MigrationStateRestructuringReady,
		AnalysisSummaryId:    10003,
		RepositoryUrl:        "https://github.com/org/notiplan_backend",
		RestructuringRoadmap: flaskRoadmap(),
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	detector.On("Detect", mock.Anything, uint64(10003)).Return("Flask", []string{"Python", "Flask"}, nil)

	filename, content, err := svc.ExportActionPlanPrompt(context.Background(), 7)
	require.NoError(t, err)

	md := string(content)

	// Filename
	assert.Equal(t, "restructuring-prompt-notiplan_backend-7.md", filename)

	// Header fields
	assert.Contains(t, md, "Migration #7")
	assert.Contains(t, md, "https://github.com/org/notiplan_backend")
	assert.Contains(t, md, "Python / Flask")

	// Honesty disclaimer present
	assert.Contains(t, md, "hypotheses")
	assert.Contains(t, md, "human-supervised")

	// All 5 steps present with stack-specific content
	assert.Contains(t, md, "Step 1 — EXTRACT_DOMAIN")
	assert.Contains(t, md, "Step 2 — DEFINE_BOUNDARIES")
	assert.Contains(t, md, "Step 3 — DECOUPLE_STATE")
	assert.Contains(t, md, "Step 4 — SPLIT_GOD_MODULE")
	assert.Contains(t, md, "Step 5 — ADD_ROUTING")

	// Stack-specific "how" present for at least 3 kinds
	assert.Contains(t, md, "domain/__init__.py")
	assert.Contains(t, md, "StateManager")
	assert.Contains(t, md, "Blueprint")

	// Dependencies rendered
	assert.Contains(t, md, "Complete Step 1 before starting this step")
	assert.Contains(t, md, "Complete Step 2 before starting this step")

	// BLOCKING marker
	assert.Contains(t, md, "BLOCKING")

	// Agent usage section
	assert.Contains(t, md, "How to use this with your code agent")

	// Must NOT contain any "profile not available" hole text
	assert.NotContains(t, md, "not available yet")
}

func TestExportActionPlanPrompt_UnknownStack_HoleExport(t *testing.T) {
	t.Parallel()
	svc, repo, detector := newSvcWithDetector(t)

	m := &domain.Migration{
		Identifier:           8,
		State:                domain.MigrationStateRestructuringReady,
		AnalysisSummaryId:    10099,
		RepositoryUrl:        "https://github.com/org/php-app",
		RestructuringRoadmap: flaskRoadmap(),
	}
	repo.On("GetByID", mock.Anything, uint64(8), false).Return(m, nil)
	// Simulate a PHP repo where no profile exists
	detector.On("Detect", mock.Anything, uint64(10099)).Return("Laravel", []string{"PHP", "Laravel"}, nil)

	_, content, err := svc.ExportActionPlanPrompt(context.Background(), 8)
	require.NoError(t, err)

	md := string(content)

	// Must produce the honest hole message
	assert.Contains(t, md, "not available yet")
	assert.Contains(t, md, "Laravel")

	// Must still include the agnostic action plan
	assert.Contains(t, md, "Step 1 — EXTRACT_DOMAIN")

	// Must NOT include Python-specific content
	assert.NotContains(t, md, "domain/__init__.py")
	assert.NotContains(t, md, "StateManager")
	assert.NotContains(t, md, "Flask Blueprint")
}

func TestExportActionPlanPrompt_DetectorNil_HoleExport(t *testing.T) {
	t.Parallel()
	// When stackDetector is nil (not wired), export should still work gracefully.
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	svc := application.NewService(repo, tx, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "")

	m := &domain.Migration{
		Identifier:           9,
		State:                domain.MigrationStateRestructuringReady,
		RestructuringRoadmap: flaskRoadmap(),
	}
	repo.On("GetByID", mock.Anything, uint64(9), false).Return(m, nil)

	_, content, err := svc.ExportActionPlanPrompt(context.Background(), 9)
	require.NoError(t, err)

	md := string(content)
	// Stack unknown → hole export
	assert.Contains(t, md, "Action Plan")
	// No Python-specific content
	assert.NotContains(t, md, "domain/__init__.py")
}

// ── BuildActionPlanPrompt unit tests ─────────────────────────────────────────

func TestBuildActionPlanPrompt_DisclaimerAlwaysPresent(t *testing.T) {
	t.Parallel()
	roadmap := flaskRoadmap()
	md := string(application.BuildActionPlanPrompt("https://github.com/org/repo", 42, roadmap, "Flask", []string{"Python", "Flask"}, nil))
	assert.Contains(t, md, "hypotheses")
	assert.Contains(t, md, "human-supervised")
}

func TestBuildActionPlanPrompt_HoleNeverContainsPythonInstructions(t *testing.T) {
	t.Parallel()
	roadmap := flaskRoadmap()
	// nil profile = no stack known
	md := string(application.BuildActionPlanPrompt("", 1, roadmap, "Ruby", []string{"Ruby", "Rails"}, nil))
	assert.NotContains(t, md, "domain/__init__.py")
	assert.NotContains(t, md, "StateManager")
	assert.Contains(t, md, "Ruby")
	assert.Contains(t, md, "not available yet")
}

func TestBuildActionPlanPrompt_DependenciesRendered(t *testing.T) {
	t.Parallel()
	roadmap := flaskRoadmap()
	md := string(application.BuildActionPlanPrompt("", 1, roadmap, "Flask", []string{"Python"}, nil))
	// Even in hole mode, dependencies must appear (agnostic plan).
	// Format is "**Prerequisite:** Step N".
	assert.Contains(t, md, "**Prerequisite:** Step 1") // step 2 depends on step 1
	assert.Contains(t, md, "**Prerequisite:** Step 2") // step 5 depends on step 2
}

func TestBuildActionPlanPrompt_ImpactRendered(t *testing.T) {
	t.Parallel()
	roadmap := flaskRoadmap()
	md := string(application.BuildActionPlanPrompt("", 1, roadmap, "Flask", []string{"Python", "Flask"}, nil))
	// Hole: no profile → agnostic
	assert.Contains(t, md, "+40 pts")
}

// ── repoSlugFromURL (via filename in ExportActionPlanPrompt) ─────────────────

func TestExportActionPlanPrompt_FilenameSlug(t *testing.T) {
	t.Parallel()
	cases := []struct {
		repoURL  string
		wantSlug string
	}{
		{"https://github.com/org/my-service", "my-service"},
		{"https://github.com/org/notiplan_backend", "notiplan_backend"},
		{"https://github.com/org/UPPER.Case", "UPPER-Case"},
		{"", "repo"},
	}
	for _, tc := range cases {
		repo := &mocks.MockMigrationRepository{}
		tx := &mocks.MockTransactionManager{}
		tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
		detector := &mocks.MockStackDetector{}
		svc := application.NewService(repo, tx, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, detector, "")

		m := &domain.Migration{
			Identifier:           7,
			State:                domain.MigrationStateRestructuringReady,
			AnalysisSummaryId:    1,
			RepositoryUrl:        tc.repoURL,
			RestructuringRoadmap: flaskRoadmap(),
		}
		repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
		detector.On("Detect", mock.Anything, uint64(1)).Return("", nil, nil)

		filename, _, err := svc.ExportActionPlanPrompt(context.Background(), 7)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(filename, "restructuring-prompt-"+tc.wantSlug+"-"),
			"filename %q should start with restructuring-prompt-%s-", filename, tc.wantSlug)
	}
}
