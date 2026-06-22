package application_test

import (
	"context"
	"testing"
	"time"

	"milton_prism/core/services/analysis/application"
	"milton_prism/core/services/analysis/domain"
	"milton_prism/core/services/analysis/mocks"
	billingdomain "milton_prism/core/services/billing/domain"
	analysissvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newSvc(t *testing.T) (*application.Service, *mocks.MockAnalysisSummaryRepository, *mocks.MockRepositoryClient, *mocks.MockJobEnqueuer) {
	t.Helper()
	repo := &mocks.MockAnalysisSummaryRepository{}
	repoClient := &mocks.MockRepositoryClient{}
	enqueuer := &mocks.MockJobEnqueuer{}
	svc := application.NewService(repo, repoClient, enqueuer)
	return svc, repo, repoClient, enqueuer
}

// ── GetAnalysisSummary ────────────────────────────────────────────────────────

func TestGetAnalysisSummary_Success(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newSvc(t)
	stored := &domain.AnalysisSummary{Identifier: 1, RepositoryId: 42, State: domain.AnalysisStateCompleted}
	repo.On("GetByID", mock.Anything, uint64(1), false).Return(stored, nil)
	out, err := svc.GetAnalysisSummary(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), out.GetIdentifier())
	assert.Equal(t, domain.AnalysisStateCompleted, out.GetState())
}

func TestGetAnalysisSummary_ZeroID(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvc(t)
	_, err := svc.GetAnalysisSummary(context.Background(), 0)
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}

func TestGetAnalysisSummary_NotFound(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(99), false).Return(nil, domain.ErrAnalysisSummaryNotFound)
	_, err := svc.GetAnalysisSummary(context.Background(), 99)
	assertDomainError(t, err, domain.ErrCodeAnalysisSummaryNotFound)
}

// ── ListAnalysisSummaries ─────────────────────────────────────────────────────

func TestListAnalysisSummaries_Success(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newSvc(t)
	items := []*domain.AnalysisSummary{{Identifier: 1}, {Identifier: 2}}
	repo.On("List", mock.Anything, mock.Anything, mock.Anything).Return(items, nil, nil)
	out, _, err := svc.ListAnalysisSummaries(context.Background(), nil, &queryparamsv1.PageQueryParams{PageNumber: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Len(t, out, 2)
}

func TestListAnalysisSummaries_StandaloneFilter(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newSvc(t)
	// Only standalone summaries (migration_id=0) should be returned.
	standalone := []*domain.AnalysisSummary{
		{Identifier: 10, RepositoryId: 1, MigrationId: 0},
		{Identifier: 11, RepositoryId: 2, MigrationId: 0},
	}
	filter := &analysissvcv1.AnalysisSummariesFilter{Standalone: true}
	repo.On("List", mock.Anything, mock.MatchedBy(func(f *analysissvcv1.AnalysisSummariesFilter) bool {
		return f.GetStandalone()
	}), mock.Anything).Return(standalone, nil, nil)

	out, _, err := svc.ListAnalysisSummaries(context.Background(), filter, &queryparamsv1.PageQueryParams{PageNumber: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Len(t, out, 2)
	for _, s := range out {
		assert.Equal(t, uint64(0), s.GetMigrationId(), "standalone filter must exclude migration-linked summaries")
	}
}

func TestListAnalysisSummaries_MultiStateFilter(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newSvc(t)
	items := []*domain.AnalysisSummary{{Identifier: 1, State: domain.AnalysisStateRunning}, {Identifier: 2, State: domain.AnalysisStateCompleted}}
	filter := &analysissvcv1.AnalysisSummariesFilter{
		States: []analysisv1.AnalysisState{
			analysisv1.AnalysisState_ANALYSIS_STATE_RUNNING,
			analysisv1.AnalysisState_ANALYSIS_STATE_COMPLETED,
		},
	}
	repo.On("List", mock.Anything, mock.MatchedBy(func(f *analysissvcv1.AnalysisSummariesFilter) bool {
		return len(f.GetStates()) == 2 &&
			f.GetStates()[0] == analysisv1.AnalysisState_ANALYSIS_STATE_RUNNING &&
			f.GetStates()[1] == analysisv1.AnalysisState_ANALYSIS_STATE_COMPLETED
	}), mock.Anything).Return(items, nil, nil)
	out, _, err := svc.ListAnalysisSummaries(context.Background(), filter, &queryparamsv1.PageQueryParams{PageNumber: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Len(t, out, 2)
	repo.AssertExpectations(t)
}

// ── RunAnalysis ───────────────────────────────────────────────────────────────

func TestRunAnalysis_Success(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	created := &domain.AnalysisSummary{Identifier: 10001, RepositoryId: 42, State: domain.AnalysisStateRunning}
	// Standalone run (migrationID=0): ProbeConnection is called first.
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	// Dedup: return empty SHA → dedup skipped, normal analysis proceeds.
	repoClient.On("GetBranchSHA", mock.Anything, uint64(42), "main").Return("", nil)
	repo.On("Create", mock.Anything, mock.MatchedBy(func(s *domain.AnalysisSummary) bool {
		return s.GetState() == domain.AnalysisStateRunning && s.GetRepositoryId() == 42
	})).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, uint64(10001), uint64(42), uint64(0), "https://github.com/org/repo.git", "main", "").Return(nil)

	out, err := svc.RunAnalysis(context.Background(), 42, 0, 0, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Nil(t, out.Duplicate)
	assert.Equal(t, domain.AnalysisStateRunning, out.Summary.GetState())
	assert.Equal(t, uint64(10001), out.Summary.GetIdentifier())
}

func TestRunAnalysis_Standalone_RepoAuthFailed_Rejected(t *testing.T) {
	t.Parallel()
	svc, _, repoClient, _ := newSvc(t)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(domain.ErrRepoAuthFailed)
	_, err := svc.RunAnalysis(context.Background(), 42, 0, 0, "", "", false)
	assertDomainError(t, err, domain.ErrCodeRepoAuthFailed)
}

func TestRunAnalysis_Standalone_RepoUnreachable_Rejected(t *testing.T) {
	t.Parallel()
	svc, _, repoClient, _ := newSvc(t)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(domain.ErrRepoUnreachable)
	_, err := svc.RunAnalysis(context.Background(), 42, 0, 0, "", "", false)
	assertDomainError(t, err, domain.ErrCodeRepoUnreachable)
}

func TestRunAnalysis_MigrationTriggered_SkipsProbe(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	created := &domain.AnalysisSummary{Identifier: 10001, RepositoryId: 42, MigrationId: 7, State: domain.AnalysisStateRunning}
	// migrationID != 0 → ProbeConnection must NOT be called (already validated by StartMigration).
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	_, err := svc.RunAnalysis(context.Background(), 42, 7, 0, "", "", false)
	require.NoError(t, err)
	repoClient.AssertNotCalled(t, "ProbeConnection", mock.Anything, mock.Anything)
}

func TestRunAnalysis_SetsRepositoryURL(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	const repoURL = "https://github.com/org/conduit.git"
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return(repoURL, "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, uint64(42), "main").Return("", nil)
	repo.On("Create", mock.Anything, mock.MatchedBy(func(s *domain.AnalysisSummary) bool {
		return s.GetRepositoryUrl() == repoURL
	})).Return(&domain.AnalysisSummary{Identifier: 10010, RepositoryId: 42, RepositoryUrl: repoURL, State: domain.AnalysisStateRunning}, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	out, err := svc.RunAnalysis(context.Background(), 42, 0, 0, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Equal(t, repoURL, out.Summary.GetRepositoryUrl())
}

func TestRunAnalysis_WithMigrationID(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	created := &domain.AnalysisSummary{Identifier: 10002, RepositoryId: 42, MigrationId: 7, State: domain.AnalysisStateRunning}
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repo.On("Create", mock.Anything, mock.MatchedBy(func(s *domain.AnalysisSummary) bool {
		return s.GetRepositoryId() == 42 && s.GetMigrationId() == 7
	})).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, uint64(10002), uint64(42), uint64(7), "https://github.com/org/repo.git", "main", "").Return(nil)

	out, err := svc.RunAnalysis(context.Background(), 42, 7, 0, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Equal(t, uint64(7), out.Summary.GetMigrationId())
}

func TestRunAnalysis_SourceBranch_OverridesDefault(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	created := &domain.AnalysisSummary{Identifier: 10005, RepositoryId: 42, State: domain.AnalysisStateRunning}
	// Repo default is "main" but caller explicitly requests "develop".
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, uint64(42), "develop").Return("", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	// Enqueuer must receive "develop", not "main".
	enqueuer.On("EnqueueAnalysis", mock.Anything, uint64(10005), uint64(42), uint64(0), "https://github.com/org/repo.git", "develop", "").Return(nil)

	out, err := svc.RunAnalysis(context.Background(), 42, 0, 0, "develop", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Equal(t, uint64(10005), out.Summary.GetIdentifier())
	enqueuer.AssertExpectations(t)
}

// TestRunAnalysis_PersistsDefaultBranch verifies that when no source_branch is
// provided, the repository's default branch is captured in the AnalysisSummary.
func TestRunAnalysis_PersistsDefaultBranch(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	created := &domain.AnalysisSummary{Identifier: 10006, RepositoryId: 42, State: domain.AnalysisStateRunning, SourceBranch: "main"}
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, uint64(42), "main").Return("", nil)
	repo.On("Create", mock.Anything, mock.MatchedBy(func(s *domain.AnalysisSummary) bool {
		return s.GetSourceBranch() == "main"
	})).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, "main", "").Return(nil)

	out, err := svc.RunAnalysis(context.Background(), 42, 0, 0, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Equal(t, "main", out.Summary.GetSourceBranch())
}

// TestRunAnalysis_PersistsExplicitSourceBranch verifies that when source_branch
// is provided, the override is captured in the AnalysisSummary (not the default).
func TestRunAnalysis_PersistsExplicitSourceBranch(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	created := &domain.AnalysisSummary{Identifier: 10007, RepositoryId: 42, State: domain.AnalysisStateRunning, SourceBranch: "develop"}
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, uint64(42), "develop").Return("", nil)
	repo.On("Create", mock.Anything, mock.MatchedBy(func(s *domain.AnalysisSummary) bool {
		return s.GetSourceBranch() == "develop"
	})).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, "develop", "").Return(nil)

	out, err := svc.RunAnalysis(context.Background(), 42, 0, 0, "develop", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Equal(t, "develop", out.Summary.GetSourceBranch())
}

func TestRunAnalysis_MissingRepositoryID(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvc(t)
	_, err := svc.RunAnalysis(context.Background(), 0, 0, 0, "", "", false)
	assertDomainError(t, err, domain.ErrCodeMissingRepositoryID)
}

func TestRunAnalysis_RepositoryNotFound(t *testing.T) {
	t.Parallel()
	svc, _, repoClient, _ := newSvc(t)
	// Probe fires before GetRemoteURL; repo not found surfaces at probe time.
	repoClient.On("ProbeConnection", mock.Anything, uint64(99)).Return(domain.ErrRepositoryNotFound)
	_, err := svc.RunAnalysis(context.Background(), 99, 0, 0, "", "", false)
	assertDomainError(t, err, domain.ErrCodeRepositoryNotFound)
}

func TestRunAnalysis_EnqueueFailureIsIgnored(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	created := &domain.AnalysisSummary{Identifier: 10003, RepositoryId: 42, State: domain.AnalysisStateRunning}
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, uint64(42), "main").Return("", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, uint64(10003), uint64(42), uint64(0), "https://github.com/org/repo.git", "main", "").Return(domain.ErrInternal)

	// enqueue failure must NOT propagate — RunAnalysis still returns the summary.
	out, err := svc.RunAnalysis(context.Background(), 42, 0, 0, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Equal(t, uint64(10003), out.Summary.GetIdentifier())
}

// ── RunAnalysis dedup ─────────────────────────────────────────────────────────

func TestRunAnalysis_Dedup_SameCommit_ReturnsDuplicate(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, _ := newSvc(t)
	const sha = "abc123def456abc123def456abc123def456abc1"
	repID := uint64(42)
	branch := "main"
	existing := &domain.AnalysisSummary{
		Identifier:   9999,
		RepositoryId: repID,
		SourceBranch: branch,
		CommitSha:    sha,
		State:        domain.AnalysisStateCompleted,
	}
	repoClient.On("ProbeConnection", mock.Anything, repID).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, repID).Return("https://github.com/org/repo.git", branch, nil)
	repoClient.On("GetBranchSHA", mock.Anything, repID, branch).Return(sha, nil)
	repo.On("List", mock.Anything, mock.MatchedBy(func(f *analysissvcv1.AnalysisSummariesFilter) bool {
		return f.GetRepositoryId() == repID && f.GetSourceBranch() == branch
	}), mock.Anything).Return([]*domain.AnalysisSummary{existing}, nil, nil)

	out, err := svc.RunAnalysis(context.Background(), repID, 0, 0, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Duplicate)
	assert.Nil(t, out.Summary)
	assert.Equal(t, uint64(9999), out.Duplicate.GetIdentifier())
	assert.Equal(t, sha, out.Duplicate.GetCommitSha())
	// Create must NOT be called — no new summary created.
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestRunAnalysis_Dedup_DifferentCommit_RunsNormally(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	const headSHA = "newsha000"
	const existingSHA = "oldsha999"
	repID := uint64(42)
	branch := "main"
	created := &domain.AnalysisSummary{Identifier: 10008, RepositoryId: repID, State: domain.AnalysisStateRunning}
	existingOld := &domain.AnalysisSummary{
		Identifier: 9998, RepositoryId: repID, SourceBranch: branch,
		CommitSha: existingSHA, State: domain.AnalysisStateCompleted,
	}
	repoClient.On("ProbeConnection", mock.Anything, repID).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, repID).Return("https://github.com/org/repo.git", branch, nil)
	repoClient.On("GetBranchSHA", mock.Anything, repID, branch).Return(headSHA, nil)
	repo.On("List", mock.Anything, mock.Anything, mock.Anything).Return([]*domain.AnalysisSummary{existingOld}, nil, nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	out, err := svc.RunAnalysis(context.Background(), repID, 0, 0, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Nil(t, out.Duplicate)
}

func TestRunAnalysis_Dedup_NoExisting_RunsNormally(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	repID := uint64(42)
	created := &domain.AnalysisSummary{Identifier: 10009, RepositoryId: repID, State: domain.AnalysisStateRunning}
	repoClient.On("ProbeConnection", mock.Anything, repID).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, repID).Return("https://github.com/org/repo.git", "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, repID, "main").Return("abc123", nil)
	repo.On("List", mock.Anything, mock.Anything, mock.Anything).Return([]*domain.AnalysisSummary{}, nil, nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	out, err := svc.RunAnalysis(context.Background(), repID, 0, 0, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Nil(t, out.Duplicate)
}

func TestRunAnalysis_Dedup_Force_BypassesDuplicateCheck(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	repID := uint64(42)
	created := &domain.AnalysisSummary{Identifier: 10011, RepositoryId: repID, State: domain.AnalysisStateRunning}
	repoClient.On("ProbeConnection", mock.Anything, repID).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, repID).Return("https://github.com/org/repo.git", "main", nil)
	// GetBranchSHA must NOT be called when force=true.
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	out, err := svc.RunAnalysis(context.Background(), repID, 0, 0, "", "", true)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Nil(t, out.Duplicate)
	repoClient.AssertNotCalled(t, "GetBranchSHA", mock.Anything, mock.Anything, mock.Anything)
}

func TestRunAnalysis_Dedup_MigrationTriggered_SkipsDedup(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer := newSvc(t)
	repID := uint64(42)
	created := &domain.AnalysisSummary{Identifier: 10012, RepositoryId: repID, MigrationId: 5, State: domain.AnalysisStateRunning}
	// migrationID != 0 → dedup check skipped entirely in the service layer.
	repoClient.On("GetRemoteURL", mock.Anything, repID).Return("https://github.com/org/repo.git", "main", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	out, err := svc.RunAnalysis(context.Background(), repID, 5, 0, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	repoClient.AssertNotCalled(t, "GetBranchSHA", mock.Anything, mock.Anything, mock.Anything)
}

// ── SelectRoot ─────────────────────────────────────────────────────────────────

func awaitingSummary() *domain.AnalysisSummary {
	return &domain.AnalysisSummary{
		Identifier:     7,
		RepositoryId:   42,
		MigrationId:    9,
		OwnerUserId:    1,
		RepositoryUrl:  "https://github.com/org/repo.git",
		SourceBranch:   "main",
		State:          domain.AnalysisStateAwaitingRootSelection,
		RootCandidates: []string{"services/api", "services/web"},
	}
}

func TestSelectRoot_Success_PersistsAndReEnqueues(t *testing.T) {
	t.Parallel()
	svc, repo, _, enqueuer := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(awaitingSummary(), nil)
	updated := &domain.AnalysisSummary{
		Identifier: 7, RepositoryId: 42, MigrationId: 9, OwnerUserId: 1,
		RepositoryUrl: "https://github.com/org/repo.git", SourceBranch: "main",
		State: domain.AnalysisStateRunning, RootSubdirectory: "services/api",
	}
	repo.On("MarkRootSelected", mock.Anything, uint64(7), "services/api").Return(updated, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, uint64(7), uint64(42), uint64(9),
		"https://github.com/org/repo.git", "main", "services/api").Return(nil)

	out, err := svc.SelectRoot(context.Background(), 7, "services/api")
	require.NoError(t, err)
	assert.Equal(t, domain.AnalysisStateRunning, out.GetState())
	assert.Equal(t, "services/api", out.GetRootSubdirectory())
	enqueuer.AssertExpectations(t)
	repo.AssertExpectations(t)
}

func TestSelectRoot_NotACandidate_FailsClosed(t *testing.T) {
	t.Parallel()
	svc, repo, _, enqueuer := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(awaitingSummary(), nil)

	_, err := svc.SelectRoot(context.Background(), 7, "services/other")
	assertDomainError(t, err, domain.ErrCodeInvalidRootSelection)
	repo.AssertNotCalled(t, "MarkRootSelected", mock.Anything, mock.Anything, mock.Anything)
	enqueuer.AssertNotCalled(t, "EnqueueAnalysis",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestSelectRoot_EmptyChoice_FailsClosed(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newSvc(t)
	// Empty is rejected before any repo lookup is strictly needed, but the
	// service normalises first; GetByID is not reached for empty input.
	_, err := svc.SelectRoot(context.Background(), 7, "")
	assertDomainError(t, err, domain.ErrCodeInvalidRootSelection)
	repo.AssertNotCalled(t, "MarkRootSelected", mock.Anything, mock.Anything, mock.Anything)
}

func TestSelectRoot_TraversalChoice_Rejected(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvc(t)
	_, err := svc.SelectRoot(context.Background(), 7, "../escape")
	assertDomainError(t, err, domain.ErrCodeInvalidRootSubdirectory)
}

func TestSelectRoot_NotAwaitingState_FailsClosed(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newSvc(t)
	running := &domain.AnalysisSummary{Identifier: 7, OwnerUserId: 1, State: domain.AnalysisStateRunning}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(running, nil)

	_, err := svc.SelectRoot(context.Background(), 7, "services/api")
	assertDomainError(t, err, domain.ErrCodeInvalidRootSelection)
	repo.AssertNotCalled(t, "MarkRootSelected", mock.Anything, mock.Anything, mock.Anything)
}

func TestSelectRoot_ZeroID_Rejected(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvc(t)
	_, err := svc.SelectRoot(context.Background(), 0, "services/api")
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}

// ── RunAnalysis billing plan quota ─────────────────────────────────────────────

// newSvcWithPlans wires a PlanProvider so quota enforcement is active.
func newSvcWithPlans(t *testing.T) (*application.Service, *mocks.MockAnalysisSummaryRepository, *mocks.MockRepositoryClient, *mocks.MockJobEnqueuer, *mocks.MockPlanProvider) {
	t.Helper()
	repo := &mocks.MockAnalysisSummaryRepository{}
	repoClient := &mocks.MockRepositoryClient{}
	enqueuer := &mocks.MockJobEnqueuer{}
	plans := &mocks.MockPlanProvider{}
	svc := application.NewService(repo, repoClient, enqueuer).WithPlanProvider(plans)
	return svc, repo, repoClient, enqueuer, plans
}

func freePlan() *billingv1.Plan {
	return &billingv1.Plan{Code: billingdomain.PlanCodeFree, MaxAnalysesPerMonth: 5, MaxMigrationsPerMonth: 1}
}

func enterprisePlan() *billingv1.Plan {
	return &billingv1.Plan{Code: billingdomain.PlanCodeEnterprise, MaxAnalysesPerMonth: billingdomain.Unlimited, MaxMigrationsPerMonth: billingdomain.Unlimited}
}

func TestRunAnalysis_PlanLimit_FreeAtLimit_Rejected(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer, plans := newSvcWithPlans(t)
	const owner = uint64(7)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, uint64(42), "main").Return("", nil)
	plans.On("GetUserPlan", mock.Anything, owner).Return(freePlan(), nil)
	// Free cap is 5; already at 5 this month → reject.
	repo.On("CountByOwnerSince", mock.Anything, owner, mock.Anything).Return(int64(5), nil)

	_, err := svc.RunAnalysis(context.Background(), 42, 0, owner, "", "", false)
	assertDomainError(t, err, domain.ErrCodePlanLimitExceeded)
	// No summary created when over quota.
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	enqueuer.AssertNotCalled(t, "EnqueueAnalysis",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRunAnalysis_PlanLimit_FreeUnderLimit_Allowed(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer, plans := newSvcWithPlans(t)
	const owner = uint64(7)
	created := &domain.AnalysisSummary{Identifier: 20001, RepositoryId: 42, OwnerUserId: owner, State: domain.AnalysisStateRunning}
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, uint64(42), "main").Return("", nil)
	plans.On("GetUserPlan", mock.Anything, owner).Return(freePlan(), nil)
	repo.On("CountByOwnerSince", mock.Anything, owner, mock.Anything).Return(int64(4), nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	out, err := svc.RunAnalysis(context.Background(), 42, 0, owner, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	assert.Equal(t, uint64(20001), out.Summary.GetIdentifier())
}

func TestRunAnalysis_PlanLimit_Enterprise_NeverBlocked(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer, plans := newSvcWithPlans(t)
	const owner = uint64(7)
	created := &domain.AnalysisSummary{Identifier: 20002, RepositoryId: 42, OwnerUserId: owner, State: domain.AnalysisStateRunning}
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, uint64(42), "main").Return("", nil)
	plans.On("GetUserPlan", mock.Anything, owner).Return(enterprisePlan(), nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	out, err := svc.RunAnalysis(context.Background(), 42, 0, owner, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	// Unlimited plan: count is never consulted.
	repo.AssertNotCalled(t, "CountByOwnerSince", mock.Anything, mock.Anything, mock.Anything)
}

func TestRunAnalysis_PlanLimit_MonthBoundary_LastMonthDoesNotCount(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer, plans := newSvcWithPlans(t)
	const owner = uint64(7)
	created := &domain.AnalysisSummary{Identifier: 20003, RepositoryId: 42, OwnerUserId: owner, State: domain.AnalysisStateRunning}
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, uint64(42), "main").Return("", nil)
	plans.On("GetUserPlan", mock.Anything, owner).Return(freePlan(), nil)
	// The count is scoped to since = start-of-current-month UTC. Assert the boundary
	// is the first of the current month at 00:00 UTC, and that the count (analyses
	// from last month excluded) is 0 → allowed.
	repo.On("CountByOwnerSince", mock.Anything, owner, mock.MatchedBy(func(since time.Time) bool {
		now := time.Now().UTC()
		want := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return since.Equal(want)
	})).Return(int64(0), nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	out, err := svc.RunAnalysis(context.Background(), 42, 0, owner, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	repo.AssertExpectations(t)
}

func TestRunAnalysis_PlanLimit_ForceReRun_NotCounted(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer, plans := newSvcWithPlans(t)
	const owner = uint64(7)
	created := &domain.AnalysisSummary{Identifier: 20004, RepositoryId: 42, OwnerUserId: owner, State: domain.AnalysisStateRunning}
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// force=true → quota check skipped entirely (re-run does not consume a slot).
	out, err := svc.RunAnalysis(context.Background(), 42, 0, owner, "", "", true)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	plans.AssertNotCalled(t, "GetUserPlan", mock.Anything, mock.Anything)
	repo.AssertNotCalled(t, "CountByOwnerSince", mock.Anything, mock.Anything, mock.Anything)
}

func TestRunAnalysis_PlanLimit_MigrationTriggered_NotCounted(t *testing.T) {
	t.Parallel()
	svc, repo, repoClient, enqueuer, plans := newSvcWithPlans(t)
	const owner = uint64(7)
	created := &domain.AnalysisSummary{Identifier: 20005, RepositoryId: 42, MigrationId: 9, OwnerUserId: owner, State: domain.AnalysisStateRunning}
	repoClient.On("GetRemoteURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo.git", "main", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// migrationID != 0 → analysis quota not enforced here (migration quota gates it).
	out, err := svc.RunAnalysis(context.Background(), 42, 9, owner, "", "", false)
	require.NoError(t, err)
	require.NotNil(t, out.Summary)
	plans.AssertNotCalled(t, "GetUserPlan", mock.Anything, mock.Anything)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertDomainError(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	var de *domain.Error
	require.ErrorAs(t, err, &de, "expected domain.Error, got %T: %v", err, err)
	assert.Equal(t, code, de.Code)
}
