package grpc_handlers_test

import (
	"context"
	"testing"

	"milton_prism/core/services/migration/application"
	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/infrastructure/grpc_handlers"
	"milton_prism/core/services/migration/mocks"
	"milton_prism/core/services/migration/ports"
	migsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/migration/v1"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	callerID uint64 = 1
	migID    uint64 = 7
	repoID   uint64 = 42
)

func newHandler(t *testing.T) (*grpc_handlers.MigrationHandler, *mocks.MockMigrationRepository, *mocks.MockTransactionManager, *mocks.MockRepositoryClient, *mocks.MockAnalysisClient) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	identity := &mocks.MockIdentityClient{}
	repoClient := &mocks.MockRepositoryClient{}
	analysis := &mocks.MockAnalysisClient{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	svc := application.NewService(repo, tx, identity, repoClient, analysis, nil, nil, nil, nil, nil, nil, nil, nil, nil, "")
	h := grpc_handlers.NewMigrationHandler(svc, okAuth)
	return h, repo, tx, repoClient, analysis
}

func okAuth(_ context.Context) (uint64, bool, error) {
	return callerID, false, nil
}

func systemAuth(_ context.Context) (uint64, bool, error) {
	return callerID, true, nil
}

func failAuth(_ context.Context) (uint64, bool, error) {
	return 0, false, domain.ErrInternal
}

func pendingMigration() *domain.Migration {
	return &domain.Migration{
		Identifier:   migID,
		RepositoryId: repoID,
		OwnerUserId:  callerID,
		State:        domain.MigrationStatePending,
	}
}

// ── GetMigration ──────────────────────────────────────────────────────────────

func TestHandlerGetMigration_Success(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(pendingMigration(), nil)
	out, err := h.GetMigration(context.Background(), &migsvcv1.GetMigrationRequest{Identifier: migID})
	require.NoError(t, err)
	assert.Equal(t, migID, out.GetIdentifier())
}

func TestHandlerGetMigration_AuthFailure(t *testing.T) {
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	identity := &mocks.MockIdentityClient{}
	repoClient := &mocks.MockRepositoryClient{}
	analysis := &mocks.MockAnalysisClient{}
	svc := application.NewService(repo, tx, identity, repoClient, analysis, nil, nil, nil, nil, nil, nil, nil, nil, nil, "")
	h := grpc_handlers.NewMigrationHandler(svc, failAuth)
	_, err := h.GetMigration(context.Background(), &migsvcv1.GetMigrationRequest{Identifier: migID})
	require.Error(t, err)
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestHandlerGetMigration_ZeroID(t *testing.T) {
	h, _, _, _, _ := newHandler(t)
	_, err := h.GetMigration(context.Background(), &migsvcv1.GetMigrationRequest{Identifier: 0})
	require.Error(t, err)
	assertGRPCCode(t, err, codes.InvalidArgument)
}

func TestHandlerGetMigration_NotFound(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(nil, domain.ErrMigrationNotFound)
	_, err := h.GetMigration(context.Background(), &migsvcv1.GetMigrationRequest{Identifier: migID})
	assertGRPCCode(t, err, codes.NotFound)
}

func TestHandlerGetMigration_ForbiddenAccess(t *testing.T) {
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	identity := &mocks.MockIdentityClient{}
	repoClient := &mocks.MockRepositoryClient{}
	analysis := &mocks.MockAnalysisClient{}
	svc := application.NewService(repo, tx, identity, repoClient, analysis, nil, nil, nil, nil, nil, nil, nil, nil, nil, "")
	// caller is 99, not the owner (callerID=1)
	h := grpc_handlers.NewMigrationHandler(svc, func(_ context.Context) (uint64, bool, error) {
		return 99, false, nil
	})
	m := pendingMigration()
	m.OwnerUserId = callerID
	repo.On("GetByID", mock.Anything, migID, false).Return(m, nil)
	_, err := h.GetMigration(context.Background(), &migsvcv1.GetMigrationRequest{Identifier: migID})
	assertGRPCCode(t, err, codes.PermissionDenied)
}

// migrationWithRoadmap returns an owned migration carrying both an assessment of
// the given verdict and a non-nil restructuring roadmap with a stale score.
func migrationWithRoadmap(verdict string) *domain.Migration {
	score := int32(35)
	return &domain.Migration{
		Identifier:  migID,
		OwnerUserId: callerID,
		State:       domain.MigrationStateRestructuringReady,
		MigrabilityAssessment: &commonv1.MigrabilityAssessment{
			Verdict: verdict,
		},
		RestructuringRoadmap: &migrationv1.RestructuringRoadmap{
			Diagnosis: &migrationv1.RoadmapDiagnosis{
				Verdict:          domain.MigrabilityVerdictNotMigrable,
				MigrabilityScore: &score,
			},
		},
	}
}

func TestHandlerGetMigration_IncompleteVerdict_OmitsOrphanRoadmap(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).
		Return(migrationWithRoadmap(domain.MigrabilityVerdictIncompleteNoStructuralData), nil)
	out, err := h.GetMigration(context.Background(), &migsvcv1.GetMigrationRequest{Identifier: migID})
	require.NoError(t, err)
	assert.Nil(t, out.GetRestructuringRoadmap(), "orphan roadmap must be suppressed on INCOMPLETE verdict")
	assert.Equal(t, domain.MigrabilityVerdictIncompleteNoStructuralData, out.GetMigrabilityAssessment().GetVerdict())
}

func TestHandlerGetMigration_NotMigrableVerdict_KeepsRoadmap(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).
		Return(migrationWithRoadmap(domain.MigrabilityVerdictNotMigrable), nil)
	out, err := h.GetMigration(context.Background(), &migsvcv1.GetMigrationRequest{Identifier: migID})
	require.NoError(t, err)
	require.NotNil(t, out.GetRestructuringRoadmap(), "normal path must still serve its roadmap")
	assert.Equal(t, int32(35), out.GetRestructuringRoadmap().GetDiagnosis().GetMigrabilityScore())
}

// ── StartMigration ────────────────────────────────────────────────────────────

func TestHandlerStartMigration_Success(t *testing.T) {
	h, repo, _, repoClient, analysis := newHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(pendingMigration(), nil)
	repoClient.On("ProbeConnection", mock.Anything, repoID).Return(nil)
	repo.On("UpdateState", mock.Anything, migID, domain.MigrationStateAnalyzing).Return(nil)
	analysis.On("RunAnalysis", mock.Anything, repoID, migID, callerID, "", "").Return(nil)
	out, err := h.StartMigration(context.Background(), &migsvcv1.StartMigrationRequest{Identifier: migID})
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateAnalyzing, out.GetState())
}

func TestHandlerStartMigration_IllegalTransition(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	m := pendingMigration()
	m.State = domain.MigrationStateAnalyzing
	// First GetMigration call in handler (ownership check), then StartMigration call
	repo.On("GetByID", mock.Anything, migID, false).Return(m, nil)
	_, err := h.StartMigration(context.Background(), &migsvcv1.StartMigrationRequest{Identifier: migID})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

// ── RunMigration ──────────────────────────────────────────────────────────────

func TestHandlerRunMigration_FromPending_Success(t *testing.T) {
	h, repo, _, repoClient, analysis := newHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(pendingMigration(), nil)
	repo.On("SetAutoApprove", mock.Anything, migID, true).Return(nil)
	repoClient.On("ProbeConnection", mock.Anything, repoID).Return(nil)
	repo.On("UpdateState", mock.Anything, migID, domain.MigrationStateAnalyzing).Return(nil)
	analysis.On("RunAnalysis", mock.Anything, repoID, migID, callerID, "", "").Return(nil)

	out, err := h.RunMigration(context.Background(), &migsvcv1.RunMigrationRequest{Identifier: migID})
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateAnalyzing, out.GetState())
	assert.True(t, out.GetAutoApprove())
}

func TestHandlerRunMigration_AuthFailure(t *testing.T) {
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	svc := application.NewService(repo, tx, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "")
	h := grpc_handlers.NewMigrationHandler(svc, failAuth)
	_, err := h.RunMigration(context.Background(), &migsvcv1.RunMigrationRequest{Identifier: migID})
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestHandlerRunMigration_ZeroID(t *testing.T) {
	h, _, _, _, _ := newHandler(t)
	_, err := h.RunMigration(context.Background(), &migsvcv1.RunMigrationRequest{Identifier: 0})
	assertGRPCCode(t, err, codes.InvalidArgument)
}

func TestHandlerRunMigration_TerminalState_Rejected(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	m := pendingMigration()
	m.State = domain.MigrationStatePushed
	repo.On("GetByID", mock.Anything, migID, false).Return(m, nil)
	_, err := h.RunMigration(context.Background(), &migsvcv1.RunMigrationRequest{Identifier: migID})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

// ── ApproveDesign ─────────────────────────────────────────────────────────────

func TestHandlerApproveDesign_Approved(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	m := pendingMigration()
	m.State = domain.MigrationStateAwaitingApproval
	repo.On("GetByID", mock.Anything, migID, false).Return(m, nil)
	repo.On("UpdateState", mock.Anything, migID, domain.MigrationStateGenerating).Return(nil)
	out, err := h.ApproveDesign(context.Background(), &migsvcv1.ApproveDesignRequest{Identifier: migID, Approved: true})
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateGenerating, out.GetState())
}

func TestHandlerApproveDesign_Rejected(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	m := pendingMigration()
	m.State = domain.MigrationStateAwaitingApproval
	repo.On("GetByID", mock.Anything, migID, false).Return(m, nil)
	repo.On("UpdateState", mock.Anything, migID, domain.MigrationStateCancelled).Return(nil)
	out, err := h.ApproveDesign(context.Background(), &migsvcv1.ApproveDesignRequest{Identifier: migID, Approved: false})
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateCancelled, out.GetState())
}

func TestHandlerApproveDesign_IllegalTransition(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(pendingMigration(), nil)
	_, err := h.ApproveDesign(context.Background(), &migsvcv1.ApproveDesignRequest{Identifier: migID, Approved: true})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

// ── CancelMigration ───────────────────────────────────────────────────────────

func TestHandlerCancelMigration_Success(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(pendingMigration(), nil)
	repo.On("UpdateState", mock.Anything, migID, domain.MigrationStateCancelled).Return(nil)
	out, err := h.CancelMigration(context.Background(), &migsvcv1.CancelMigrationRequest{Identifier: migID})
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateCancelled, out.GetState())
}

func TestHandlerCancelMigration_AlreadyTerminal(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	m := pendingMigration()
	m.State = domain.MigrationStatePushed
	repo.On("GetByID", mock.Anything, migID, false).Return(m, nil)
	_, err := h.CancelMigration(context.Background(), &migsvcv1.CancelMigrationRequest{Identifier: migID})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

// ── DeleteMigration ───────────────────────────────────────────────────────────

func TestHandlerDeleteMigration_Success(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	m := pendingMigration()
	m.State = domain.MigrationStatePushed
	repo.On("GetByID", mock.Anything, migID, false).Return(m, nil)
	repo.On("SoftDelete", mock.Anything, migID).Return(nil)
	_, err := h.DeleteMigration(context.Background(), &migsvcv1.DeleteMigrationRequest{Identifier: migID})
	require.NoError(t, err)
}

func TestHandlerDeleteMigration_NonTerminal_Rejected(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(pendingMigration(), nil)
	_, err := h.DeleteMigration(context.Background(), &migsvcv1.DeleteMigrationRequest{Identifier: migID})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

// ── ListMigrations — non-system forced filter ─────────────────────────────────

func TestHandlerListMigrations_NonSystemForcedFilter(t *testing.T) {
	h, repo, _, _, _ := newHandler(t)
	repo.On("List", mock.Anything, mock.MatchedBy(func(f *migrationv1.MigrationsFilter) bool {
		return f.OwnerUserId != nil && *f.OwnerUserId == callerID
	}), mock.Anything).Return([]*domain.Migration{}, nil, nil)
	out, err := h.ListMigrations(context.Background(), &migsvcv1.ListMigrationsRequest{})
	require.NoError(t, err)
	assert.Empty(t, out.GetMigrations())
}

func TestHandlerListMigrations_SystemBypassesFilter(t *testing.T) {
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	identity := &mocks.MockIdentityClient{}
	repoClient := &mocks.MockRepositoryClient{}
	analysis := &mocks.MockAnalysisClient{}
	svc := application.NewService(repo, tx, identity, repoClient, analysis, nil, nil, nil, nil, nil, nil, nil, nil, nil, "")
	h := grpc_handlers.NewMigrationHandler(svc, systemAuth)
	repo.On("List", mock.Anything, mock.Anything, mock.Anything).Return([]*domain.Migration{{Identifier: 1}, {Identifier: 2}}, nil, nil)
	out, err := h.ListMigrations(context.Background(), &migsvcv1.ListMigrationsRequest{})
	require.NoError(t, err)
	assert.Len(t, out.GetMigrations(), 2)
}

// ── PublishMigration ──────────────────────────────────────────────────────────

func newPublishHandler(t *testing.T) (*grpc_handlers.MigrationHandler, *mocks.MockMigrationRepository, *mocks.MockRepositoryClient, *mocks.MockGenerationFileArtifactReader) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	identity := &mocks.MockIdentityClient{}
	repoClient := &mocks.MockRepositoryClient{}
	fileReader := &mocks.MockGenerationFileArtifactReader{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	svc := application.NewService(repo, tx, identity, repoClient, nil, nil, nil, nil, nil, fileReader, nil, nil, nil, nil, "")
	h := grpc_handlers.NewMigrationHandler(svc, okAuth)
	return h, repo, repoClient, fileReader
}

func readyMigration() *domain.Migration {
	m := pendingMigration()
	m.State = domain.MigrationStateReady
	return m
}

func TestHandlerPublishMigration_ZeroID(t *testing.T) {
	h, _, _, _ := newPublishHandler(t)
	_, err := h.PublishMigration(context.Background(), &migsvcv1.PublishMigrationRequest{MigrationId: 0})
	assertGRPCCode(t, err, codes.InvalidArgument)
}

func TestHandlerPublishMigration_AuthFailure(t *testing.T) {
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	svc := application.NewService(repo, tx, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "")
	h := grpc_handlers.NewMigrationHandler(svc, failAuth)
	_, err := h.PublishMigration(context.Background(), &migsvcv1.PublishMigrationRequest{MigrationId: migID})
	assertGRPCCode(t, err, codes.Unauthenticated)
}

func TestHandlerPublishMigration_Forbidden(t *testing.T) {
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	svc := application.NewService(repo, tx, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "")
	h := grpc_handlers.NewMigrationHandler(svc, func(_ context.Context) (uint64, bool, error) {
		return 99, false, nil // caller is not the owner
	})
	m := readyMigration()
	m.OwnerUserId = callerID
	repo.On("GetByID", mock.Anything, migID, false).Return(m, nil)
	_, err := h.PublishMigration(context.Background(), &migsvcv1.PublishMigrationRequest{MigrationId: migID, TargetUrl: "https://example.com/repo.git"})
	assertGRPCCode(t, err, codes.PermissionDenied)
}

func TestHandlerPublishMigration_WrongState(t *testing.T) {
	h, repo, _, _ := newPublishHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(pendingMigration(), nil)
	_, err := h.PublishMigration(context.Background(), &migsvcv1.PublishMigrationRequest{MigrationId: migID, TargetUrl: "https://example.com/repo.git"})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

func TestHandlerPublishMigration_NoArtifacts(t *testing.T) {
	h, repo, _, fileReader := newPublishHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(readyMigration(), nil)
	fileReader.On("ListArtifacts", mock.Anything, migID, "").Return([]ports.GeneratedFile{}, nil)
	_, err := h.PublishMigration(context.Background(), &migsvcv1.PublishMigrationRequest{MigrationId: migID, TargetUrl: "https://example.com/repo.git"})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

func TestHandlerPublishMigration_PushAuthFailed(t *testing.T) {
	h, repo, repoClient, fileReader := newPublishHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(readyMigration(), nil)
	fileReader.On("ListArtifacts", mock.Anything, migID, "").Return([]ports.GeneratedFile{
		{ServiceName: "user", Path: "core/services/user/domain/domain.go", Content: "package domain"},
	}, nil)
	repoClient.On("PushFiles", mock.Anything, "https://example.com/repo.git", "bad-token", mock.Anything, "").
		Return("", domain.ErrPushAuthFailed)
	_, err := h.PublishMigration(context.Background(), &migsvcv1.PublishMigrationRequest{
		MigrationId: migID,
		TargetUrl:   "https://example.com/repo.git",
		WriteToken:  "bad-token",
	})
	assertGRPCCode(t, err, codes.FailedPrecondition)
}

func TestHandlerPublishMigration_Success(t *testing.T) {
	h, repo, repoClient, fileReader := newPublishHandler(t)
	repo.On("GetByID", mock.Anything, migID, false).Return(readyMigration(), nil)
	fileReader.On("ListArtifacts", mock.Anything, migID, "").Return([]ports.GeneratedFile{
		{ServiceName: "user", Path: "core/services/user/domain/domain.go", Content: "package domain"},
	}, nil)
	repoClient.On("PushFiles", mock.Anything, "https://example.com/repo.git", "tok", mock.Anything, "").
		Return("main", nil)
	repo.On("UpdateState", mock.Anything, migID, domain.MigrationStatePushed).Return(nil)

	out, err := h.PublishMigration(context.Background(), &migsvcv1.PublishMigrationRequest{
		MigrationId: migID,
		TargetUrl:   "https://example.com/repo.git",
		WriteToken:  "tok",
	})
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStatePushed, out.GetMigration().GetState())
	assert.Equal(t, "main", out.GetPushedBranch())
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertGRPCCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, code, st.Code())
}
