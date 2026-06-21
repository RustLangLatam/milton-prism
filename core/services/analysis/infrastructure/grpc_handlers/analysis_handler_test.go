package grpc_handlers_test

import (
	"context"
	"testing"

	"milton_prism/core/services/analysis/application"
	"milton_prism/core/services/analysis/domain"
	"milton_prism/core/services/analysis/infrastructure/grpc_handlers"
	"milton_prism/core/services/analysis/mocks"
	anlsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	summaryID  uint64 = 1
	repoID     uint64 = 42
	migrationID uint64 = 7
)

func newHandler(t *testing.T) (*grpc_handlers.AnalysisHandler, *mocks.MockAnalysisSummaryRepository, *mocks.MockRepositoryClient, *mocks.MockJobEnqueuer) {
	t.Helper()
	repo := &mocks.MockAnalysisSummaryRepository{}
	repoClient := &mocks.MockRepositoryClient{}
	enqueuer := &mocks.MockJobEnqueuer{}
	svc := application.NewService(repo, repoClient, enqueuer)
	h := grpc_handlers.NewAnalysisHandler(svc, okAuth)
	return h, repo, repoClient, enqueuer
}

func okAuth(_ context.Context) (uint64, bool, error) {
	return 1, false, nil
}

func failAuth(_ context.Context) (uint64, bool, error) {
	return 0, false, domain.ErrInternal
}

func runningSummary() *domain.AnalysisSummary {
	return &domain.AnalysisSummary{Identifier: summaryID, RepositoryId: repoID, OwnerUserId: 1, State: domain.AnalysisStateRunning}
}

// ── GetAnalysisSummary ────────────────────────────────────────────────────────

func TestHandlerGetAnalysisSummary_Success(t *testing.T) {
	t.Parallel()
	h, repo, _, _ := newHandler(t)
	repo.On("GetByID", mock.Anything, summaryID, false).Return(runningSummary(), nil)
	out, err := h.GetAnalysisSummary(context.Background(), &anlsvcv1.GetAnalysisSummaryRequest{Identifier: summaryID})
	require.NoError(t, err)
	assert.Equal(t, summaryID, out.GetIdentifier())
}

func TestHandlerGetAnalysisSummary_AuthFailure(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockAnalysisSummaryRepository{}
	repoClient := &mocks.MockRepositoryClient{}
	enqueuer := &mocks.MockJobEnqueuer{}
	svc := application.NewService(repo, repoClient, enqueuer)
	h := grpc_handlers.NewAnalysisHandler(svc, failAuth)
	_, err := h.GetAnalysisSummary(context.Background(), &anlsvcv1.GetAnalysisSummaryRequest{Identifier: summaryID})
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestHandlerGetAnalysisSummary_ZeroID(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newHandler(t)
	_, err := h.GetAnalysisSummary(context.Background(), &anlsvcv1.GetAnalysisSummaryRequest{Identifier: 0})
	assertGRPCCode(t, err, codes.InvalidArgument)
}

func TestHandlerGetAnalysisSummary_NotFound(t *testing.T) {
	t.Parallel()
	h, repo, _, _ := newHandler(t)
	repo.On("GetByID", mock.Anything, summaryID, false).Return(nil, domain.ErrAnalysisSummaryNotFound)
	_, err := h.GetAnalysisSummary(context.Background(), &anlsvcv1.GetAnalysisSummaryRequest{Identifier: summaryID})
	assertGRPCCode(t, err, codes.NotFound)
}

// ── ListAnalysisSummaries ─────────────────────────────────────────────────────

func TestHandlerListAnalysisSummaries_Success(t *testing.T) {
	t.Parallel()
	h, repo, _, _ := newHandler(t)
	repo.On("List", mock.Anything, mock.Anything, mock.Anything).Return([]*domain.AnalysisSummary{{Identifier: 1}}, nil, nil)
	out, err := h.ListAnalysisSummaries(context.Background(), &anlsvcv1.ListAnalysisSummariesRequest{})
	require.NoError(t, err)
	assert.Len(t, out.GetAnalysisSummaries(), 1)
}

func TestHandlerListAnalysisSummaries_AuthFailure(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockAnalysisSummaryRepository{}
	repoClient := &mocks.MockRepositoryClient{}
	enqueuer := &mocks.MockJobEnqueuer{}
	svc := application.NewService(repo, repoClient, enqueuer)
	h := grpc_handlers.NewAnalysisHandler(svc, failAuth)
	_, err := h.ListAnalysisSummaries(context.Background(), &anlsvcv1.ListAnalysisSummariesRequest{})
	assertGRPCCode(t, err, codes.Unauthenticated)
}

// ── RunAnalysis ───────────────────────────────────────────────────────────────

func TestHandlerRunAnalysis_Success(t *testing.T) {
	t.Parallel()
	h, repo, repoClient, enqueuer := newHandler(t)
	created := &domain.AnalysisSummary{Identifier: summaryID, RepositoryId: repoID, State: domain.AnalysisStateRunning}
	repoClient.On("ProbeConnection", mock.Anything, repoID).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, repoID).Return("https://github.com/org/repo.git", "main", nil)
	// Dedup: return empty SHA → dedup skipped, normal analysis proceeds.
	repoClient.On("GetBranchSHA", mock.Anything, repoID, "main").Return("", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, summaryID, repoID, uint64(0), mock.Anything, mock.Anything).Return(nil)

	out, err := h.RunAnalysis(context.Background(), &anlsvcv1.RunAnalysisRequest{RepositoryId: repoID})
	require.NoError(t, err)
	assert.Equal(t, domain.AnalysisStateRunning, out.GetAnalysisSummary().GetState())
	assert.Equal(t, summaryID, out.GetAnalysisSummary().GetIdentifier())
}

func TestHandlerRunAnalysis_MissingRepositoryID(t *testing.T) {
	t.Parallel()
	h, _, _, _ := newHandler(t)
	_, err := h.RunAnalysis(context.Background(), &anlsvcv1.RunAnalysisRequest{RepositoryId: 0})
	assertGRPCCode(t, err, codes.InvalidArgument)
}

func TestHandlerRunAnalysis_RepositoryNotFound(t *testing.T) {
	t.Parallel()
	h, _, repoClient, _ := newHandler(t)
	// Probe fires before GetRemoteURL; not-found surfaces at probe time.
	repoClient.On("ProbeConnection", mock.Anything, repoID).Return(domain.ErrRepositoryNotFound)
	_, err := h.RunAnalysis(context.Background(), &anlsvcv1.RunAnalysisRequest{RepositoryId: repoID})
	assertGRPCCode(t, err, codes.NotFound)
}

func TestHandlerRunAnalysis_RepoAuthFailed(t *testing.T) {
	t.Parallel()
	h, _, repoClient, _ := newHandler(t)
	repoClient.On("ProbeConnection", mock.Anything, repoID).Return(domain.ErrRepoAuthFailed)
	_, err := h.RunAnalysis(context.Background(), &anlsvcv1.RunAnalysisRequest{RepositoryId: repoID})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

func TestHandlerRunAnalysis_RepoUnreachable(t *testing.T) {
	t.Parallel()
	h, _, repoClient, _ := newHandler(t)
	repoClient.On("ProbeConnection", mock.Anything, repoID).Return(domain.ErrRepoUnreachable)
	_, err := h.RunAnalysis(context.Background(), &anlsvcv1.RunAnalysisRequest{RepositoryId: repoID})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

func TestHandlerRunAnalysis_AuthFailure(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockAnalysisSummaryRepository{}
	repoClient := &mocks.MockRepositoryClient{}
	enqueuer := &mocks.MockJobEnqueuer{}
	svc := application.NewService(repo, repoClient, enqueuer)
	h := grpc_handlers.NewAnalysisHandler(svc, failAuth)
	_, err := h.RunAnalysis(context.Background(), &anlsvcv1.RunAnalysisRequest{RepositoryId: repoID})
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestHandlerRunAnalysis_DuplicateFound(t *testing.T) {
	t.Parallel()
	h, repo, repoClient, _ := newHandler(t)
	const sha = "abc123abc123abc123abc123abc123abc123abc1"
	existing := &domain.AnalysisSummary{Identifier: 9999, RepositoryId: repoID, CommitSha: sha, State: domain.AnalysisStateCompleted}
	repoClient.On("ProbeConnection", mock.Anything, repoID).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, repoID).Return("https://github.com/org/repo.git", "main", nil)
	repoClient.On("GetBranchSHA", mock.Anything, repoID, "main").Return(sha, nil)
	repo.On("List", mock.Anything, mock.Anything, mock.Anything).Return([]*domain.AnalysisSummary{existing}, nil, nil)

	out, err := h.RunAnalysis(context.Background(), &anlsvcv1.RunAnalysisRequest{RepositoryId: repoID})
	require.NoError(t, err)
	assert.True(t, out.GetDuplicateFound())
	assert.NotNil(t, out.GetExistingAnalysis())
	assert.Equal(t, uint64(9999), out.GetExistingAnalysis().GetIdentifier())
}

func TestHandlerRunAnalysis_Force_BypassesDedup(t *testing.T) {
	t.Parallel()
	h, repo, repoClient, enqueuer := newHandler(t)
	created := &domain.AnalysisSummary{Identifier: summaryID, RepositoryId: repoID, State: domain.AnalysisStateRunning}
	repoClient.On("ProbeConnection", mock.Anything, repoID).Return(nil)
	repoClient.On("GetRemoteURL", mock.Anything, repoID).Return("https://github.com/org/repo.git", "main", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	enqueuer.On("EnqueueAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	out, err := h.RunAnalysis(context.Background(), &anlsvcv1.RunAnalysisRequest{RepositoryId: repoID, Force: true})
	require.NoError(t, err)
	assert.False(t, out.GetDuplicateFound())
	assert.NotNil(t, out.GetAnalysisSummary())
	// GetBranchSHA must NOT be called when force=true.
	repoClient.AssertNotCalled(t, "GetBranchSHA", mock.Anything, mock.Anything, mock.Anything)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertGRPCCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, code, st.Code())
}
