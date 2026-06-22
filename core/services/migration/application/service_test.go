package application_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"milton_prism/core/services/migration/application"
	billingdomain "milton_prism/core/services/billing/domain"
	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/mocks"
	"milton_prism/core/services/migration/ports"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
	"milton_prism/pkg/utils/pointers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newSvc(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockTransactionManager, *mocks.MockIdentityClient, *mocks.MockRepositoryClient, *mocks.MockAnalysisClient) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	identity := &mocks.MockIdentityClient{}
	repoClient := &mocks.MockRepositoryClient{}
	analysis := &mocks.MockAnalysisClient{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	svc := application.NewService(repo, tx, identity, repoClient, analysis, nil, nil, nil, nil, nil, nil, nil, nil, nil, "")
	return svc, repo, tx, identity, repoClient, analysis
}

func newSvcWithArtifacts(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockArtifactReader) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	artifacts := &mocks.MockArtifactReader{}
	svc := application.NewService(repo, tx, nil, nil, nil, artifacts, nil, nil, nil, nil, nil, nil, nil, nil, "")
	return svc, repo, artifacts
}

func validMigration() *domain.Migration {
	return &domain.Migration{
		RepositoryId: 42,
		OwnerUserId:  1,
		SourceBranch: "main",
		Target: &migrationv1.TargetConfig{
			Language: domain.TargetLanguageGo,
			Database: domain.TargetDatabaseMongoDB,
		},
	}
}

// ── CreateMigration ───────────────────────────────────────────────────────────

func TestCreateMigration_Success(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	stored := &domain.Migration{Identifier: 10001, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10001), out.GetIdentifier())
	assert.Equal(t, domain.MigrationStatePending, out.GetState())
}

func TestCreateMigration_NilPayload(t *testing.T) {
	svc, _, _, _, _, _ := newSvc(t)
	_, err := svc.CreateMigration(context.Background(), nil)
	assertDomainError(t, err, domain.ErrCodeMissingPayload)
}

func TestCreateMigration_MissingOwnerUserID(t *testing.T) {
	svc, _, _, _, _, _ := newSvc(t)
	m := validMigration()
	m.OwnerUserId = 0
	_, err := svc.CreateMigration(context.Background(), m)
	assertDomainError(t, err, domain.ErrCodeMissingOwnerUserID)
}

func TestCreateMigration_MissingRepositoryID(t *testing.T) {
	svc, _, _, _, _, _ := newSvc(t)
	m := validMigration()
	m.RepositoryId = 0
	_, err := svc.CreateMigration(context.Background(), m)
	assertDomainError(t, err, domain.ErrCodeMissingRepositoryID)
}

func TestCreateMigration_InvalidTargetConfig_NilTarget(t *testing.T) {
	svc, _, _, _, _, _ := newSvc(t)
	m := validMigration()
	m.Target = nil
	_, err := svc.CreateMigration(context.Background(), m)
	assertDomainError(t, err, domain.ErrCodeInvalidTargetConfig)
}

func TestCreateMigration_InvalidTargetConfig_UnspecifiedLanguage(t *testing.T) {
	svc, _, _, _, _, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguageUnspecified
	_, err := svc.CreateMigration(context.Background(), m)
	assertDomainError(t, err, domain.ErrCodeInvalidTargetConfig)
}

func TestCreateMigration_UnsupportedTargetLanguage_Rust(t *testing.T) {
	svc, _, _, _, _, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguageRust
	_, err := svc.CreateMigration(context.Background(), m)
	assertDomainError(t, err, domain.ErrCodeUnsupportedTargetLanguage)
}

func TestCreateMigration_UnsupportedTargetLanguage_Node(t *testing.T) {
	svc, _, _, _, _, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguageNode
	_, err := svc.CreateMigration(context.Background(), m)
	assertDomainError(t, err, domain.ErrCodeUnsupportedTargetLanguage)
}

func TestCreateMigration_Python_Generable_Accepted(t *testing.T) {
	svc, repo, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	m.Target.Language = domain.TargetLanguagePython
	stored := &domain.Migration{Identifier: 10002, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(10002), out.GetIdentifier())
}

func TestCreateMigration_OwnerNotFound(t *testing.T) {
	svc, _, _, identity, _, _ := newSvc(t)
	m := validMigration()
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(domain.ErrOwnerNotFound)
	_, err := svc.CreateMigration(context.Background(), m)
	assertDomainError(t, err, domain.ErrCodeOwnerNotFound)
}

// ── CreateMigration billing plan quota ──────────────────────────────────────────

// newSvcWithBilling wires a BillingClient so migration quota enforcement is active.
func newSvcWithBilling(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockIdentityClient, *mocks.MockRepositoryClient, *mocks.MockBillingClient) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	identity := &mocks.MockIdentityClient{}
	repoClient := &mocks.MockRepositoryClient{}
	billing := &mocks.MockBillingClient{}
	svc := application.NewService(repo, tx, identity, repoClient, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "").
		WithBillingClient(billing)
	return svc, repo, identity, repoClient, billing
}

func freePlan() *billingv1.Plan {
	return &billingv1.Plan{Code: billingdomain.PlanCodeFree, MaxAnalysesPerMonth: 5, MaxMigrationsPerMonth: 1}
}

func enterprisePlan() *billingv1.Plan {
	return &billingv1.Plan{Code: billingdomain.PlanCodeEnterprise, MaxAnalysesPerMonth: billingdomain.Unlimited, MaxMigrationsPerMonth: billingdomain.Unlimited}
}

func TestCreateMigration_PlanLimit_FreeAtLimit_Rejected(t *testing.T) {
	svc, repo, identity, repoClient, billing := newSvcWithBilling(t)
	m := validMigration()
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	billing.On("GetUserPlan", mock.Anything, uint64(1)).Return(freePlan(), nil)
	// Free cap is 1 migration/mo; already at 1 → reject.
	repo.On("CountByOwnerSince", mock.Anything, uint64(1), mock.Anything).Return(int64(1), nil)

	_, err := svc.CreateMigration(context.Background(), m)
	assertDomainError(t, err, domain.ErrCodePlanLimitExceeded)
	// Nothing persisted when over quota.
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestCreateMigration_PlanLimit_FreeUnderLimit_Allowed(t *testing.T) {
	svc, repo, identity, repoClient, billing := newSvcWithBilling(t)
	m := validMigration()
	stored := &domain.Migration{Identifier: 30001, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	billing.On("GetUserPlan", mock.Anything, uint64(1)).Return(freePlan(), nil)
	repo.On("CountByOwnerSince", mock.Anything, uint64(1), mock.Anything).Return(int64(0), nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(30001), out.GetIdentifier())
}

func TestCreateMigration_PlanLimit_Enterprise_NeverBlocked(t *testing.T) {
	svc, repo, identity, repoClient, billing := newSvcWithBilling(t)
	m := validMigration()
	stored := &domain.Migration{Identifier: 30002, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	billing.On("GetUserPlan", mock.Anything, uint64(1)).Return(enterprisePlan(), nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(30002), out.GetIdentifier())
	// Unlimited plan: count is never consulted.
	repo.AssertNotCalled(t, "CountByOwnerSince", mock.Anything, mock.Anything, mock.Anything)
}

func TestCreateMigration_PlanLimit_MonthBoundary_LastMonthDoesNotCount(t *testing.T) {
	svc, repo, identity, repoClient, billing := newSvcWithBilling(t)
	m := validMigration()
	stored := &domain.Migration{Identifier: 30003, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	billing.On("GetUserPlan", mock.Anything, uint64(1)).Return(freePlan(), nil)
	// since must be start-of-current-month UTC; a migration created last month is
	// outside this window and is not counted → count 0 → allowed.
	repo.On("CountByOwnerSince", mock.Anything, uint64(1), mock.MatchedBy(func(since time.Time) bool {
		now := time.Now().UTC()
		want := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return since.Equal(want)
	})).Return(int64(0), nil)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(30003), out.GetIdentifier())
	repo.AssertExpectations(t)
}

func TestCreateMigration_PlanLimit_BillingLookupFails_DegradesOpen(t *testing.T) {
	svc, repo, identity, repoClient, billing := newSvcWithBilling(t)
	m := validMigration()
	stored := &domain.Migration{Identifier: 30004, RepositoryId: 42, OwnerUserId: 1, State: domain.MigrationStatePending}
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("https://github.com/org/repo", nil)
	// Transient billing error must not block the user (degrade open).
	billing.On("GetUserPlan", mock.Anything, uint64(1)).Return(nil, domain.ErrInternal)
	repo.On("Create", mock.Anything, mock.Anything).Return(stored, nil)

	out, err := svc.CreateMigration(context.Background(), m)
	require.NoError(t, err)
	assert.Equal(t, uint64(30004), out.GetIdentifier())
	repo.AssertNotCalled(t, "CountByOwnerSince", mock.Anything, mock.Anything, mock.Anything)
}

func TestCreateMigration_RepositoryNotFound(t *testing.T) {
	svc, _, _, identity, repoClient, _ := newSvc(t)
	m := validMigration()
	identity.On("ValidateUserExists", mock.Anything, uint64(1)).Return(nil)
	repoClient.On("FetchRepositoryURL", mock.Anything, uint64(42)).Return("", domain.ErrRepositoryNotFound)
	_, err := svc.CreateMigration(context.Background(), m)
	assertDomainError(t, err, domain.ErrCodeRepositoryNotFound)
}

// ── GetMigration ──────────────────────────────────────────────────────────────

func TestGetMigration_Success(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	stored := &domain.Migration{Identifier: 7, State: domain.MigrationStatePending}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(stored, nil)
	out, err := svc.GetMigration(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, uint64(7), out.GetIdentifier())
}

func TestGetMigration_ZeroID(t *testing.T) {
	svc, _, _, _, _, _ := newSvc(t)
	_, err := svc.GetMigration(context.Background(), 0)
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}

func TestGetMigration_NotFound(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(99), false).Return(nil, domain.ErrMigrationNotFound)
	_, err := svc.GetMigration(context.Background(), 99)
	assertDomainError(t, err, domain.ErrCodeMigrationNotFound)
}

// ── ListMigrations ────────────────────────────────────────────────────────────

func TestListMigrations_Success(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	items := []*domain.Migration{{Identifier: 1}, {Identifier: 2}}
	repo.On("List", mock.Anything, mock.Anything, mock.Anything).Return(items, nil, nil)
	out, _, err := svc.ListMigrations(context.Background(), nil, &queryparamsv1.PageQueryParams{PageNumber: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Len(t, out, 2)
}

func TestListMigrations_MultiStateFilter(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	items := []*domain.Migration{{Identifier: 1, State: domain.MigrationStateAnalyzing}, {Identifier: 2, State: domain.MigrationStateDesigning}}
	filter := &migrationv1.MigrationsFilter{
		States: []migrationv1.MigrationState{
			migrationv1.MigrationState_MIGRATION_STATE_ANALYZING,
			migrationv1.MigrationState_MIGRATION_STATE_DESIGNING,
		},
	}
	repo.On("List", mock.Anything, mock.MatchedBy(func(f *migrationv1.MigrationsFilter) bool {
		return len(f.GetStates()) == 2 &&
			f.GetStates()[0] == migrationv1.MigrationState_MIGRATION_STATE_ANALYZING &&
			f.GetStates()[1] == migrationv1.MigrationState_MIGRATION_STATE_DESIGNING
	}), mock.Anything).Return(items, nil, nil)
	out, _, err := svc.ListMigrations(context.Background(), filter, &queryparamsv1.PageQueryParams{PageNumber: 1, PageSize: 10})
	require.NoError(t, err)
	assert.Len(t, out, 2)
	repo.AssertExpectations(t)
}

// ── DeleteMigration ───────────────────────────────────────────────────────────

func TestDeleteMigration_TerminalState_Success(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	for _, state := range []domain.MigrationState{domain.MigrationStatePushed, domain.MigrationStateFailed, domain.MigrationStateCancelled} {
		repo.ExpectedCalls = nil
		repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: state}, nil)
		repo.On("SoftDelete", mock.Anything, uint64(7)).Return(nil)
		err := svc.DeleteMigration(context.Background(), 7)
		require.NoError(t, err, "state=%v", state)
	}
}

func TestDeleteMigration_NonTerminalState_Rejected(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	nonTerminal := []domain.MigrationState{
		domain.MigrationStatePending,
		domain.MigrationStateAnalyzing,
		domain.MigrationStateDesigning,
		domain.MigrationStateAwaitingApproval,
		domain.MigrationStateGenerating,
		domain.MigrationStateTesting,
		domain.MigrationStateReady,
	}
	for _, state := range nonTerminal {
		repo.ExpectedCalls = nil
		repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: state}, nil)
		err := svc.DeleteMigration(context.Background(), 7)
		assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
	}
}

// ── StartMigration ────────────────────────────────────────────────────────────

func TestStartMigration_FromPending_Success(t *testing.T) {
	svc, repo, _, _, repoClient, analysis := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, RepositoryId: 42, State: domain.MigrationStatePending}, nil)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateAnalyzing).Return(nil)
	// source_branch is empty when not set on the migration — worker falls back to repo default.
	analysis.On("RunAnalysis", mock.Anything, uint64(42), uint64(7), uint64(0), "", "").Return(nil)
	out, err := svc.StartMigration(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateAnalyzing, out.GetState())
}

func TestStartMigration_SourceBranch_Forwarded(t *testing.T) {
	svc, repo, _, _, repoClient, analysis := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, RepositoryId: 42, State: domain.MigrationStatePending, SourceBranch: "develop"}, nil)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateAnalyzing).Return(nil)
	// source_branch must be forwarded to the analysis dispatch.
	analysis.On("RunAnalysis", mock.Anything, uint64(42), uint64(7), uint64(0), "develop", "").Return(nil)
	out, err := svc.StartMigration(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateAnalyzing, out.GetState())
}

func TestStartMigration_DispatchFailure_RollsBackToPending(t *testing.T) {
	svc, repo, _, _, repoClient, analysis := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, RepositoryId: 42, State: domain.MigrationStatePending}, nil)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateAnalyzing).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStatePending).Return(nil)
	analysis.On("RunAnalysis", mock.Anything, uint64(42), uint64(7), uint64(0), "", "").Return(domain.ErrInternal)
	// dispatch failure must roll back to PENDING and surface the error.
	_, err := svc.StartMigration(context.Background(), 7)
	require.Error(t, err)
	repo.AssertCalled(t, "UpdateState", mock.Anything, uint64(7), domain.MigrationStatePending)
}

func TestStartMigration_RepoAuthFailed_Rejected(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, repoClient, _ := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, RepositoryId: 42, State: domain.MigrationStatePending}, nil)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(domain.ErrRepoAuthFailed)
	// Migration must stay PENDING — no UpdateState call.
	_, err := svc.StartMigration(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeRepoAuthFailed)
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestStartMigration_RepoUnreachable_Rejected(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, repoClient, _ := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, RepositoryId: 42, State: domain.MigrationStatePending}, nil)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(domain.ErrRepoUnreachable)
	_, err := svc.StartMigration(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeRepoUnreachable)
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestStartMigration_NotPending_Rejected(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	notPending := []domain.MigrationState{
		domain.MigrationStateAnalyzing,
		domain.MigrationStateDesigning,
		domain.MigrationStateAwaitingApproval,
		domain.MigrationStateGenerating,
		domain.MigrationStateTesting,
		domain.MigrationStateReady,
		domain.MigrationStatePushed,
		domain.MigrationStateFailed,
		domain.MigrationStateCancelled,
	}
	for _, state := range notPending {
		repo.ExpectedCalls = nil
		repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: state}, nil)
		_, err := svc.StartMigration(context.Background(), 7)
		assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
	}
}

// ── ApproveDesign ─────────────────────────────────────────────────────────────

func TestApproveDesign_Approved_AdvancesToGenerating(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval}, nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateGenerating).Return(nil)
	out, err := svc.ApproveDesign(context.Background(), 7, true, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateGenerating, out.GetState())
}

func TestApproveDesign_Rejected_TransitionsToCancelled(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval}, nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateCancelled).Return(nil)
	out, err := svc.ApproveDesign(context.Background(), 7, false, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateCancelled, out.GetState())
}

func TestApproveDesign_NoServiceBoundaries_Blocked(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	plan := &migrationv1.RestructurePlan{
		NoServiceBoundaries:   true,
		BoundariesExplanation: "No domain layer found.",
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval, Plan: plan}, nil)

	_, err := svc.ApproveDesign(context.Background(), 7, true, nil)
	assertDomainError(t, err, domain.ErrCodeNoServiceBoundaries)
}

func TestApproveDesign_NoServiceBoundaries_CancelStillAllowed(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	plan := &migrationv1.RestructurePlan{
		NoServiceBoundaries: true,
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval, Plan: plan}, nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateCancelled).Return(nil)

	out, err := svc.ApproveDesign(context.Background(), 7, false, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateCancelled, out.GetState())
}

// ── Migrability gate (A.5/A.8) ───────────────────────────────────────────────

func TestApproveDesign_Gate_MIGRABLE_Proceeds(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	assessment := &commonv1.MigrabilityAssessment{Verdict: domain.MigrabilityVerdictMigrable}
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval, MigrabilityAssessment: assessment}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateGenerating).Return(nil)

	_, err := svc.ApproveDesign(context.Background(), 7, true, nil)
	require.NoError(t, err)
}

func TestApproveDesign_Gate_PARTIAL_Proceeds(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	assessment := &commonv1.MigrabilityAssessment{Verdict: domain.MigrabilityVerdictPartial, Blockers: []string{"some structural issue"}}
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval, MigrabilityAssessment: assessment}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateGenerating).Return(nil)

	_, err := svc.ApproveDesign(context.Background(), 7, true, nil)
	require.NoError(t, err) // PARTIAL warns but does not block
}

func TestApproveDesign_Gate_NOT_MIGRABLE_Blocked(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	assessment := &commonv1.MigrabilityAssessment{
		Verdict:  domain.MigrabilityVerdictNotMigrable,
		Blockers: []string{"no domain layer", "god-module with codebase-wide fan-in"},
	}
	m := &domain.Migration{
		Identifier:            7,
		State:                 domain.MigrationStateAwaitingApproval,
		MigrabilityAssessment: assessment,
		MigrabilityOverride:   false,
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)

	_, err := svc.ApproveDesign(context.Background(), 7, true, nil)
	assertDomainError(t, err, domain.ErrCodeNotMigrableBlocked)
}

func TestApproveDesign_Gate_NOT_MIGRABLE_WithOverride_Proceeds(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	assessment := &commonv1.MigrabilityAssessment{
		Verdict:  domain.MigrabilityVerdictNotMigrable,
		Blockers: []string{"no domain layer"},
	}
	m := &domain.Migration{
		Identifier:            7,
		State:                 domain.MigrationStateAwaitingApproval,
		MigrabilityAssessment: assessment,
		MigrabilityOverride:   true, // user explicitly overrode
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateGenerating).Return(nil)

	_, err := svc.ApproveDesign(context.Background(), 7, true, nil)
	require.NoError(t, err)
}

func TestApproveDesign_Gate_AbsentVerdict_Proceeds(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	m := &domain.Migration{
		Identifier:            7,
		State:                 domain.MigrationStateAwaitingApproval,
		MigrabilityAssessment: nil, // never ran assessment
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateGenerating).Return(nil)

	_, err := svc.ApproveDesign(context.Background(), 7, true, nil)
	require.NoError(t, err) // absent verdict does not block
}

func TestApproveDesign_NotAwaitingApproval_Rejected(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	invalid := []domain.MigrationState{
		domain.MigrationStatePending,
		domain.MigrationStateAnalyzing,
		domain.MigrationStateGenerating,
		domain.MigrationStatePushed,
		domain.MigrationStateCancelled,
	}
	for _, state := range invalid {
		repo.ExpectedCalls = nil
		repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: state}, nil)
		_, err := svc.ApproveDesign(context.Background(), 7, true, nil)
		assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
	}
}

func TestApproveDesign_Approved_DispatchesGenerationJob(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	enqueuer := &mocks.MockGenerationJobEnqueuer{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval}, nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateGenerating).Return(nil)
	enqueuer.On("EnqueueGeneration", mock.Anything, uint64(7), mock.Anything).Return(nil)

	svc := application.NewService(repo, tx, nil, nil, nil, nil, enqueuer, nil, nil, nil, nil, nil, nil, nil, "")
	_, err := svc.ApproveDesign(context.Background(), 7, true, nil)
	require.NoError(t, err)
	enqueuer.AssertCalled(t, "EnqueueGeneration", mock.Anything, uint64(7), mock.Anything)
}

func TestApproveDesign_Rejected_DoesNotDispatchGenerationJob(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	enqueuer := &mocks.MockGenerationJobEnqueuer{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval}, nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateCancelled).Return(nil)

	svc := application.NewService(repo, tx, nil, nil, nil, nil, enqueuer, nil, nil, nil, nil, nil, nil, nil, "")
	_, err := svc.ApproveDesign(context.Background(), 7, false, nil)
	require.NoError(t, err)
	enqueuer.AssertNotCalled(t, "EnqueueGeneration", mock.Anything, mock.Anything)
}

// ── CancelMigration ───────────────────────────────────────────────────────────

func TestCancelMigration_FromAnyNonTerminal_Success(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	nonTerminal := []domain.MigrationState{
		domain.MigrationStatePending,
		domain.MigrationStateAnalyzing,
		domain.MigrationStateDesigning,
		domain.MigrationStateAwaitingApproval,
		domain.MigrationStateGenerating,
		domain.MigrationStateTesting,
		domain.MigrationStateReady,
	}
	for _, state := range nonTerminal {
		repo.ExpectedCalls = nil
		repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: state}, nil)
		repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateCancelled).Return(nil)
		out, err := svc.CancelMigration(context.Background(), 7)
		require.NoError(t, err, "state=%v", state)
		assert.Equal(t, domain.MigrationStateCancelled, out.GetState())
	}
}

func TestCancelMigration_AlreadyTerminal_Rejected(t *testing.T) {
	svc, repo, _, _, _, _ := newSvc(t)
	terminal := []domain.MigrationState{
		domain.MigrationStatePushed,
		domain.MigrationStateFailed,
		domain.MigrationStateCancelled,
	}
	for _, state := range terminal {
		repo.ExpectedCalls = nil
		repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: state}, nil)
		_, err := svc.CancelMigration(context.Background(), 7)
		assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
	}
}

// ── GetGenerationPackage ──────────────────────────────────────────────────────

func TestGetGenerationPackage_OK(t *testing.T) {
	t.Parallel()
	svc, repo, artifacts := newSvcWithArtifacts(t)
	plan := &migrationv1.RestructurePlan{
		Services: []*migrationv1.ProposedService{
			{Name: "articles", ErrorPrefix: "ART"},
			{Name: "profile", ErrorPrefix: "PRO"},
		},
	}
	m := &domain.Migration{
		Identifier: 7,
		State:      domain.MigrationStateGenerating,
		Plan:       plan,
		Target:     &migrationv1.TargetConfig{Language: domain.TargetLanguageGo},
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	artifacts.On("ReadArtifacts", mock.Anything, uint64(7)).Return([]domain.ServiceArtifact{
		{ServiceName: "articles", ProtoContent: "proto...", BoundarySpec: "spec...", Incomplete: false},
		{ServiceName: "profile", ProtoContent: "proto2...", BoundarySpec: "spec2...", Incomplete: false},
	}, nil)

	pkg, err := svc.GetGenerationPackage(context.Background(), 7)
	require.NoError(t, err)
	require.NotNil(t, pkg)
	assert.Equal(t, uint64(7), pkg.GetMigrationId())
	assert.Equal(t, "go", pkg.GetOutputProfile())
	require.Len(t, pkg.GetServices(), 2)
	assert.Equal(t, "articles", pkg.GetServices()[0].GetName())
	assert.Equal(t, "ART", pkg.GetServices()[0].GetErrorPrefix())
	assert.Equal(t, "proto...", pkg.GetServices()[0].GetProtoContent())
	assert.Contains(t, pkg.GetServices()[0].GetGeneratorPromptRef(), "service-generator-prompt")
}

// TestGetGenerationPackage_PythonProfile asserts a PYTHON-target migration routes
// to the python output profile and the python generator prompt, and that the
// referenced prompt file actually exists on disk. This guards the dangling-path
// regression where the code referenced a non-existent
// milton-prism-python-service-generator-prompt.md.
func TestGetGenerationPackage_PythonProfile(t *testing.T) {
	t.Parallel()
	svc, repo, artifacts := newSvcWithArtifacts(t)
	plan := &migrationv1.RestructurePlan{
		Services: []*migrationv1.ProposedService{{Name: "articles", ErrorPrefix: "ART"}},
	}
	m := &domain.Migration{
		Identifier: 8,
		State:      domain.MigrationStateGenerating,
		Plan:       plan,
		Target:     &migrationv1.TargetConfig{Language: domain.TargetLanguagePython},
	}
	repo.On("GetByID", mock.Anything, uint64(8), false).Return(m, nil)
	artifacts.On("ReadArtifacts", mock.Anything, uint64(8)).Return([]domain.ServiceArtifact{
		{ServiceName: "articles", ProtoContent: "proto...", BoundarySpec: "spec..."},
	}, nil)

	pkg, err := svc.GetGenerationPackage(context.Background(), 8)
	require.NoError(t, err)
	require.NotNil(t, pkg)
	assert.Equal(t, "python", pkg.GetOutputProfile())
	require.Len(t, pkg.GetServices(), 1)
	promptRef := pkg.GetServices()[0].GetGeneratorPromptRef()
	assert.Equal(t, "docs/prism/milton-prism-service-generator-prompt-python.md", promptRef)

	// The prompt ref is repo-root-relative; tests run from the package dir, so
	// resolve up to the repo root (4 levels: application → migration → services → core).
	abs := filepath.Join("..", "..", "..", "..", promptRef)
	_, statErr := os.Stat(abs)
	assert.NoError(t, statErr, "python generator prompt file must exist: %s", promptRef)
}

func TestGetGenerationPackage_WrongState(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithArtifacts(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval}, nil)
	_, err := svc.GetGenerationPackage(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
}

func TestGetGenerationPackage_MissingID(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithArtifacts(t)
	_, err := svc.GetGenerationPackage(context.Background(), 0)
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}

// ── PublishMigration ──────────────────────────────────────────────────────────

func newSvcWithPublish(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockRepositoryClient, *mocks.MockGenerationFileArtifactReader) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	repoSvc := &mocks.MockRepositoryClient{}
	fileReader := &mocks.MockGenerationFileArtifactReader{}
	svc := application.NewService(repo, tx, nil, repoSvc, nil, nil, nil, nil, nil, fileReader, nil, nil, nil, nil, "")
	return svc, repo, repoSvc, fileReader
}

func TestPublishMigration_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvcWithPublish(t)
	_, _, err := svc.PublishMigration(context.Background(), 0, "https://example.com/r", "", "")
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}

func TestPublishMigration_MissingTargetURL(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newSvcWithPublish(t)
	_, _, err := svc.PublishMigration(context.Background(), 7, "", "", "")
	assertDomainError(t, err, domain.ErrCodeMissingPayload)
}

func TestPublishMigration_WrongState_PENDING(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newSvcWithPublish(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStatePending}, nil)
	_, _, err := svc.PublishMigration(context.Background(), 7, "https://example.com/r", "", "")
	assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
}

func TestPublishMigration_NoArtifacts(t *testing.T) {
	t.Parallel()
	svc, repo, _, fileReader := newSvcWithPublish(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateReady}, nil)
	fileReader.On("ListArtifacts", mock.Anything, uint64(7), "").Return([]ports.GeneratedFile{}, nil)
	_, _, err := svc.PublishMigration(context.Background(), 7, "https://example.com/r", "", "")
	assertDomainError(t, err, domain.ErrCodeNoArtifacts)
}

func TestPublishMigration_Success_READY_to_PUSHED(t *testing.T) {
	t.Parallel()
	svc, repo, repoSvc, fileReader := newSvcWithPublish(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateReady}, nil)
	fileReader.On("ListArtifacts", mock.Anything, uint64(7), "").Return([]ports.GeneratedFile{
		{ServiceName: "user", Path: "core/services/user/domain/domain.go", Content: "package domain\n"},
		{ServiceName: "user", Path: "core/services/user/wire.go", Content: "package user\n"},
	}, nil)
	repoSvc.On("PushFiles", mock.Anything, "https://example.com/r", "tok", mock.Anything, "").Return("main", nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStatePushed).Return(nil)

	m, branch, err := svc.PublishMigration(context.Background(), 7, "https://example.com/r", "tok", "")
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStatePushed, m.GetState())
	assert.Equal(t, "main", branch)
}

func TestPublishMigration_Success_PUSHED_idempotent(t *testing.T) {
	t.Parallel()
	svc, repo, repoSvc, fileReader := newSvcWithPublish(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStatePushed}, nil)
	fileReader.On("ListArtifacts", mock.Anything, uint64(7), "").Return([]ports.GeneratedFile{
		{ServiceName: "user", Path: "core/services/user/domain/domain.go", Content: "package domain\n"},
	}, nil)
	repoSvc.On("PushFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("main", nil)
	// UpdateState should NOT be called when already PUSHED.

	m, branch, err := svc.PublishMigration(context.Background(), 7, "https://example.com/r", "tok", "")
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStatePushed, m.GetState())
	assert.Equal(t, "main", branch)
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishMigration_PushAuthFailed_StaysREADY(t *testing.T) {
	t.Parallel()
	svc, repo, repoSvc, fileReader := newSvcWithPublish(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateReady}, nil)
	fileReader.On("ListArtifacts", mock.Anything, uint64(7), "").Return([]ports.GeneratedFile{
		{ServiceName: "user", Path: "core/services/user/domain/domain.go", Content: "package domain\n"},
	}, nil)
	repoSvc.On("PushFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", domain.ErrPushAuthFailed)

	_, _, err := svc.PublishMigration(context.Background(), 7, "https://example.com/r", "bad-tok", "")
	assertDomainError(t, err, domain.ErrCodePushAuthFailed)
	// State must not have changed.
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishMigration_PushConflict_StaysREADY(t *testing.T) {
	t.Parallel()
	svc, repo, repoSvc, fileReader := newSvcWithPublish(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateReady}, nil)
	fileReader.On("ListArtifacts", mock.Anything, uint64(7), "").Return([]ports.GeneratedFile{
		{ServiceName: "user", Path: "core/services/user/domain/domain.go", Content: "package domain\n"},
	}, nil)
	repoSvc.On("PushFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", domain.ErrPushConflict)

	_, _, err := svc.PublishMigration(context.Background(), 7, "https://example.com/r", "tok", "")
	assertDomainError(t, err, domain.ErrCodePushConflict)
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishMigration_ArtifactConflict_StaysREADY(t *testing.T) {
	t.Parallel()
	svc, repo, repoSvc, fileReader := newSvcWithPublish(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateReady}, nil)
	// Same path, different content across two services → conflict.
	fileReader.On("ListArtifacts", mock.Anything, uint64(7), "").Return([]ports.GeneratedFile{
		{ServiceName: "articles", Path: "pkg/gateway/common/error/message_error.go", Content: "v1\n"},
		{ServiceName: "profile", Path: "pkg/gateway/common/error/message_error.go", Content: "v2\n"},
	}, nil)

	_, _, err := svc.PublishMigration(context.Background(), 7, "https://example.com/r", "tok", "")
	assertDomainError(t, err, domain.ErrCodeArtifactConflict)
	// Push must not have been called and state must not have changed.
	repoSvc.AssertNotCalled(t, "PushFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPublishMigration_ArtifactConflict_ErrorListsPathsAndServices(t *testing.T) {
	t.Parallel()
	svc, repo, _, fileReader := newSvcWithPublish(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateReady}, nil)
	fileReader.On("ListArtifacts", mock.Anything, uint64(7), "").Return([]ports.GeneratedFile{
		{ServiceName: "articles", Path: "pkg/gateway/common/error/message_error.go", Content: "v1\n"},
		{ServiceName: "profile", Path: "pkg/gateway/common/error/message_error.go", Content: "v2\n"},
		{ServiceName: "user", Path: "pkg/gateway/common/error/message_error.go", Content: "v3\n"},
	}, nil)

	_, _, err := svc.PublishMigration(context.Background(), 7, "https://example.com/r", "tok", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pkg/gateway/common/error/message_error.go")
	assert.Contains(t, err.Error(), "articles")
	assert.Contains(t, err.Error(), "profile")
	assert.Contains(t, err.Error(), "user")
}

func TestPublishMigration_SamePathSameContent_NotAConflict(t *testing.T) {
	t.Parallel()
	// Same path and same content across services is benign (truly shared file).
	svc, repo, repoSvc, fileReader := newSvcWithPublish(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateReady}, nil)
	fileReader.On("ListArtifacts", mock.Anything, uint64(7), "").Return([]ports.GeneratedFile{
		{ServiceName: "articles", Path: "pkg/shared/util.go", Content: "package shared\n"},
		{ServiceName: "profile", Path: "pkg/shared/util.go", Content: "package shared\n"},
	}, nil)
	repoSvc.On("PushFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("main", nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStatePushed).Return(nil)

	_, _, err := svc.PublishMigration(context.Background(), 7, "https://example.com/r", "tok", "")
	require.NoError(t, err)
}

// ── AssessMigrability ─────────────────────────────────────────────────────────

func newSvcWithAssessor(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockMigrabilityAssessor) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	assessor := &mocks.MockMigrabilityAssessor{}
	svc := application.NewService(repo, tx, nil, nil, nil, nil, nil, nil, nil, nil, assessor, nil, nil, nil, "")
	return svc, repo, assessor
}

func TestAssessMigrability_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithAssessor(t)
	_, err := svc.AssessMigrability(context.Background(), 0, "en")
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}

func TestAssessMigrability_NoAnalysisSummary_Blocked(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithAssessor(t)
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval, AnalysisSummaryId: 0}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)

	_, err := svc.AssessMigrability(context.Background(), 7, "en")
	assertDomainError(t, err, domain.ErrCodeNoAnalysisSummary)
}

func TestAssessMigrability_Success_PersistsVerdict(t *testing.T) {
	t.Parallel()
	svc, repo, assessor := newSvcWithAssessor(t)
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval, AnalysisSummaryId: 10047}
	verdict := &commonv1.MigrabilityAssessment{
		Verdict:    domain.MigrabilityVerdictMigrable,
		Summary:    "Flask API with 3 clear service boundaries.",
		Confidence: "HIGH",
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	assessor.On("Assess", mock.Anything, uint64(10047), mock.AnythingOfType("string")).Return(verdict, nil)
	repo.On("SetMigrabilityAssessment", mock.Anything, uint64(7), verdict).Return(nil)

	out, err := svc.AssessMigrability(context.Background(), 7, "en")
	require.NoError(t, err)
	require.NotNil(t, out.GetMigrabilityAssessment())
	assert.Equal(t, domain.MigrabilityVerdictMigrable, out.GetMigrabilityAssessment().GetVerdict())
	repo.AssertCalled(t, "SetMigrabilityAssessment", mock.Anything, uint64(7), verdict)
}

func TestAssessMigrability_Idempotent_UpdatesExistingVerdict(t *testing.T) {
	t.Parallel()
	svc, repo, assessor := newSvcWithAssessor(t)
	// Migration already has a previous verdict; re-running should replace it.
	prev := &commonv1.MigrabilityAssessment{Verdict: domain.MigrabilityVerdictPartial}
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval, AnalysisSummaryId: 10047, MigrabilityAssessment: prev}
	newVerdict := &commonv1.MigrabilityAssessment{Verdict: domain.MigrabilityVerdictMigrable, Confidence: "HIGH"}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	assessor.On("Assess", mock.Anything, uint64(10047), mock.AnythingOfType("string")).Return(newVerdict, nil)
	repo.On("SetMigrabilityAssessment", mock.Anything, uint64(7), newVerdict).Return(nil)

	out, err := svc.AssessMigrability(context.Background(), 7, "en")
	require.NoError(t, err)
	assert.Equal(t, domain.MigrabilityVerdictMigrable, out.GetMigrabilityAssessment().GetVerdict())
}

// TestAssessMigrability_UsesPathAScore verifies that when AnalysisSummary has a
// stored migrability_score (path A), AssessMigrability overwrites the score the
// assessor computed (path B) with path A's value. This prevents Louvain
// non-determinism from causing two different scores for the same repo.
func TestAssessMigrability_UsesPathAScore(t *testing.T) {
	t.Parallel()

	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	assessor := &mocks.MockMigrabilityAssessor{}
	analysisClient := &mocks.MockAnalysisClient{}
	svc := application.NewService(repo, tx, nil, nil, analysisClient, nil, nil, nil, nil, nil, assessor, nil, nil, nil, "")

	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval, AnalysisSummaryId: 10047}
	// Path B computes a different score than path A due to a separate Louvain run.
	pathBVerdict := &commonv1.MigrabilityAssessment{
		Verdict:          domain.MigrabilityVerdictMigrable,
		Summary:          "LLM verdict summary.",
		Confidence:       "HIGH",
		MigrabilityScore: pointers.Int32Ptr(75),
		ScoreSignals:     []*commonv1.ScoreSignal{{Signal: "cluster_count", Penalty: 25}},
	}
	pathAScore := &commonv1.MigrabilityScore{
		Value:   82,
		Signals: []*commonv1.ScoreSignal{{Signal: "cluster_count", Penalty: 18}},
	}
	storedSummary := &analysisv1.AnalysisSummary{MigrabilityScore: pathAScore}

	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	assessor.On("Assess", mock.Anything, uint64(10047), mock.AnythingOfType("string")).Return(pathBVerdict, nil)
	analysisClient.On("GetAnalysisSummary", mock.Anything, uint64(10047)).Return(storedSummary, nil)
	repo.On("SetMigrabilityAssessment", mock.Anything, uint64(7), mock.MatchedBy(func(a *commonv1.MigrabilityAssessment) bool {
		return a.GetMigrabilityScore() == 82
	})).Return(nil)

	out, err := svc.AssessMigrability(context.Background(), 7, "en")
	require.NoError(t, err)
	// LLM-produced fields must be preserved.
	assert.Equal(t, domain.MigrabilityVerdictMigrable, out.GetMigrabilityAssessment().GetVerdict())
	assert.Equal(t, "LLM verdict summary.", out.GetMigrabilityAssessment().GetSummary())
	// Score must come from path A, not from the assessor's re-run.
	assert.EqualValues(t, 82, out.GetMigrabilityAssessment().GetMigrabilityScore())
	require.Len(t, out.GetMigrabilityAssessment().GetScoreSignals(), 1)
	assert.Equal(t, int32(18), out.GetMigrabilityAssessment().GetScoreSignals()[0].GetPenalty())
}

// ── SetMigrabilityOverride ────────────────────────────────────────────────────

func TestSetMigrabilityOverride_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithAssessor(t)
	_, err := svc.SetMigrabilityOverride(context.Background(), 0, true)
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}

func TestSetMigrabilityOverride_SetsTrue(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithAssessor(t)
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval, MigrabilityOverride: false}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	repo.On("SetMigrabilityOverride", mock.Anything, uint64(7), true).Return(nil)

	out, err := svc.SetMigrabilityOverride(context.Background(), 7, true)
	require.NoError(t, err)
	assert.True(t, out.GetMigrabilityOverride())
	repo.AssertCalled(t, "SetMigrabilityOverride", mock.Anything, uint64(7), true)
}

func TestSetMigrabilityOverride_ClearsFalse(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithAssessor(t)
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval, MigrabilityOverride: true}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	repo.On("SetMigrabilityOverride", mock.Anything, uint64(7), false).Return(nil)

	out, err := svc.SetMigrabilityOverride(context.Background(), 7, false)
	require.NoError(t, err)
	assert.False(t, out.GetMigrabilityOverride())
}

func TestSetMigrabilityOverride_Idempotent(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithAssessor(t)
	m := &domain.Migration{Identifier: 7, MigrabilityOverride: true}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	repo.On("SetMigrabilityOverride", mock.Anything, uint64(7), true).Return(nil)

	// Setting true when already true is a no-op (no error).
	_, err := svc.SetMigrabilityOverride(context.Background(), 7, true)
	require.NoError(t, err)
}

// ── GenerateRestructuringRoadmap invariants ───────────────────────────────────

// INV-1: MIGRABLE path unchanged — ApproveDesign still works for a MIGRABLE verdict.
// RESTRUCTURING_READY must NOT be reachable via ApproveDesign.
func TestInv1_MigrablePath_Unchanged(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	m := &domain.Migration{
		Identifier:            10,
		State:                 domain.MigrationStateAwaitingApproval,
		MigrabilityAssessment: &commonv1.MigrabilityAssessment{Verdict: domain.MigrabilityVerdictMigrable},
		Plan:                  &migrationv1.RestructurePlan{Services: []*migrationv1.ProposedService{{Name: "user"}}},
	}
	repo.On("GetByID", mock.Anything, uint64(10), false).Return(m, nil)
	repo.On("UpdateState", mock.Anything, uint64(10), domain.MigrationStateGenerating).Return(nil)

	out, err := svc.ApproveDesign(context.Background(), 10, true, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateGenerating, out.GetState())
}

// INV-2: RESTRUCTURING_READY is terminal — CancelMigration and further
// ApproveDesign must both be rejected.
func TestInv2_RestructuringReady_IsTerminal(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	m := &domain.Migration{Identifier: 11, State: domain.MigrationStateRestructuringReady}
	repo.On("GetByID", mock.Anything, uint64(11), false).Return(m, nil)

	_, err := svc.CancelMigration(context.Background(), 11)
	assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
}

// INV-3: Roadmap generation rejected for MIGRABLE verdict — must return
// ErrRoadmapUnavailable so the caller knows this path is wrong.
func TestInv3_GenerateRoadmap_RejectedForMigrable(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	m := &domain.Migration{
		Identifier:            12,
		State:                 domain.MigrationStateAwaitingApproval,
		MigrabilityAssessment: &commonv1.MigrabilityAssessment{Verdict: domain.MigrabilityVerdictMigrable},
		Plan:                  &migrationv1.RestructurePlan{NoServiceBoundaries: false},
	}
	repo.On("GetByID", mock.Anything, uint64(12), false).Return(m, nil)

	_, err := svc.GenerateRestructuringRoadmap(context.Background(), 12)
	assertDomainError(t, err, domain.ErrCodeRoadmapUnavailable)
}

// INV-4: Roadmap generation requires AWAITING_APPROVAL — wrong state returns
// ErrInvalidStateTransition.
func TestInv4_GenerateRoadmap_RequiresAwaitingApproval(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	nonApproval := []domain.MigrationState{
		domain.MigrationStatePending,
		domain.MigrationStateAnalyzing,
		domain.MigrationStateDesigning,
		domain.MigrationStateGenerating,
	}
	for _, state := range nonApproval {
		repo.ExpectedCalls = nil
		repo.On("GetByID", mock.Anything, uint64(13), false).Return(
			&domain.Migration{Identifier: 13, State: state}, nil,
		)
		_, err := svc.GenerateRestructuringRoadmap(context.Background(), 13)
		assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
	}
}

// INV-5: NoServiceBoundaries with no analysis summary — roadmap transitions to
// RESTRUCTURING_READY with the UNAVAILABLE marker in the diagnosis (no signals,
// no score) so the caller always gets a non-empty diagnosis.
func TestInv5_GenerateRoadmap_NoServiceBoundaries(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	m := &domain.Migration{
		Identifier: 14,
		State:      domain.MigrationStateAwaitingApproval,
		Plan: &migrationv1.RestructurePlan{
			NoServiceBoundaries:   true,
			BoundariesExplanation: "No identifiable domain layer found.",
		},
	}
	repo.On("GetByID", mock.Anything, uint64(14), false).Return(m, nil)
	repo.On("SetRestructuringRoadmap", mock.Anything, uint64(14), mock.AnythingOfType("*migrationv1.RestructuringRoadmap")).Return(nil)

	out, err := svc.GenerateRestructuringRoadmap(context.Background(), 14)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateRestructuringReady, out.GetState())
	require.NotNil(t, out.GetRestructuringRoadmap())
	assert.Equal(t, "No identifiable domain layer found.", out.GetRestructuringRoadmap().GetBoundariesExplanation())
	// No LLM assessment AND no analysis summary → diagnosis marker set, not nil.
	require.NotNil(t, out.GetRestructuringRoadmap().GetDiagnosis())
	assert.Equal(t, "UNAVAILABLE", out.GetRestructuringRoadmap().GetDiagnosis().GetVerdict())
}

// INV-8: NoServiceBoundaries without LLM assessment but WITH deterministic score
// on the AnalysisSummary — the exact path taken by migration 10003.
// Must produce 5 structural problems and 5 action steps identical to what the
// LLM-assessment path would produce.
func TestInv8_GenerateRoadmap_NoAssessment_FallsBackToDeterministicScore(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, analysis := newSvc(t)
	m := &domain.Migration{
		Identifier:        17,
		State:             domain.MigrationStateAwaitingApproval,
		AnalysisSummaryId: 66,
		Plan: &migrationv1.RestructurePlan{
			NoServiceBoundaries:   true,
			BoundariesExplanation: "No identifiable domain layer — all modules were classified as infrastructure.",
		},
		// MigrabilityAssessment intentionally absent (LLM never ran).
	}
	summary := &analysisv1.AnalysisSummary{
		Identifier: 66,
		MigrabilityScore: &commonv1.MigrabilityScore{
			Value: 0,
			Signals: []*commonv1.ScoreSignal{
				{Signal: "domain_presence", Penalty: 40, Detail: "no domain modules detected — automatic decomposition is structurally blocked"},
				{Signal: "cluster_count", Penalty: 25, Detail: "no service boundaries detected — monolith cannot be decomposed as-is"},
				{Signal: "hub_severity", Penalty: 20, Detail: "backend.var fan-in=19 — severe shared-state hub (concentrates 56% of incoming coupling)"},
				{Signal: "god_modules", Penalty: 10, Detail: "2 god-module(s): [backend.funcs backend.ingeteam_backend] (>=20 functions + shared state)"},
				{Signal: "routing_layout", Penalty: 5, Detail: "all 54 routes in a single blueprint — no per-domain routing separation"},
			},
		},
		ModuleClassification: &analysisv1.ModuleClassification{
			InfraModules: []string{"backend.funcs", "backend.var", "backend.ingeteam_backend"},
		},
	}
	repo.On("GetByID", mock.Anything, uint64(17), false).Return(m, nil)
	repo.On("SetRestructuringRoadmap", mock.Anything, uint64(17), mock.AnythingOfType("*migrationv1.RestructuringRoadmap")).Return(nil)
	analysis.On("GetAnalysisSummary", mock.Anything, uint64(66)).Return(summary, nil)

	out, err := svc.GenerateRestructuringRoadmap(context.Background(), 17)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateRestructuringReady, out.GetState())

	roadmap := out.GetRestructuringRoadmap()
	require.NotNil(t, roadmap)

	// Diagnosis populated from deterministic score (not LLM).
	require.NotNil(t, roadmap.GetDiagnosis())
	assert.Equal(t, "NO_SERVICE_BOUNDARIES", roadmap.GetDiagnosis().GetVerdict())
	assert.Equal(t, int32(0), roadmap.GetDiagnosis().GetMigrabilityScore())

	// 5 structural problems ordered by penalty desc.
	require.Len(t, roadmap.GetStructuralProblems(), 5)
	assert.Equal(t, "domain_presence", roadmap.GetStructuralProblems()[0].GetSignal())
	assert.Equal(t, int32(40), roadmap.GetStructuralProblems()[0].GetPenalty())

	// 5 action steps ordered by impact desc.
	require.Len(t, roadmap.GetActionPlan(), 5)
	assert.Equal(t, "EXTRACT_DOMAIN", roadmap.GetActionPlan()[0].GetKind())
	assert.Equal(t, int32(40), roadmap.GetActionPlan()[0].GetImpact())
	assert.Equal(t, int32(1), roadmap.GetActionPlan()[0].GetOrder())
	assert.Contains(t, roadmap.GetActionPlan()[0].GetSubject(), "backend.funcs")
	assert.Equal(t, "DEFINE_BOUNDARIES", roadmap.GetActionPlan()[1].GetKind())
	assert.Equal(t, int32(1), roadmap.GetActionPlan()[1].GetDependsOnStep()) // depends on step 1
	assert.Equal(t, "DECOUPLE_STATE", roadmap.GetActionPlan()[2].GetKind())
	assert.Equal(t, "backend.var", roadmap.GetActionPlan()[2].GetSubject())
	assert.Equal(t, "SPLIT_GOD_MODULE", roadmap.GetActionPlan()[3].GetKind())
	assert.Contains(t, roadmap.GetActionPlan()[3].GetSubject(), "backend.funcs")
	assert.Equal(t, "ADD_ROUTING", roadmap.GetActionPlan()[4].GetKind())
	assert.Equal(t, int32(2), roadmap.GetActionPlan()[4].GetDependsOnStep()) // depends on step 2
}

// INV-6: NOT_MIGRABLE with services in plan — action plan is derived from
// score_signals (not from plan recommendations), ordered by penalty desc.
func TestInv6_GenerateRoadmap_NotMigrable_WithServices(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, _ := newSvc(t)
	assessment := &commonv1.MigrabilityAssessment{
		Verdict:          domain.MigrabilityVerdictNotMigrable,
		Summary:          "Deeply coupled monolith with shared god-module.",
		MigrabilityScore: pointers.Int32Ptr(22),
		Confidence:       "HIGH",
		Blockers:         []string{"notiplan.data fan-in=8"},
		ScoreSignals: []*commonv1.ScoreSignal{
			{Signal: "hub_severity", Penalty: 30, Detail: "notiplan.data fan-in=8 — severe shared-state hub"},
			{Signal: "god_modules", Penalty: 20, Detail: "1 god module with >80% codebase reach"},
		},
	}
	plan := &migrationv1.RestructurePlan{
		Services: []*migrationv1.ProposedService{{Name: "user"}, {Name: "notes"}},
		RestructureRecommendations: []*migrationv1.RestructureRecommendation{
			{Kind: "DECOUPLE_STATE", Subject: "notiplan.data", Action: "Extract shared DB layer", Blocking: true},
		},
	}
	m := &domain.Migration{
		Identifier:            15,
		State:                 domain.MigrationStateAwaitingApproval,
		MigrabilityAssessment: assessment,
		Plan:                  plan,
	}
	repo.On("GetByID", mock.Anything, uint64(15), false).Return(m, nil)
	repo.On("SetRestructuringRoadmap", mock.Anything, uint64(15), mock.AnythingOfType("*migrationv1.RestructuringRoadmap")).Return(nil)

	out, err := svc.GenerateRestructuringRoadmap(context.Background(), 15)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateRestructuringReady, out.GetState())
	roadmap := out.GetRestructuringRoadmap()
	require.NotNil(t, roadmap)
	require.NotNil(t, roadmap.GetDiagnosis())
	assert.Equal(t, domain.MigrabilityVerdictNotMigrable, roadmap.GetDiagnosis().GetVerdict())
	assert.Equal(t, int32(22), roadmap.GetDiagnosis().GetMigrabilityScore())
	// Structural problems ordered by penalty desc: hub_severity(30) before god_modules(20).
	require.Len(t, roadmap.GetStructuralProblems(), 2)
	assert.Equal(t, "hub_severity", roadmap.GetStructuralProblems()[0].GetSignal())
	assert.Equal(t, int32(30), roadmap.GetStructuralProblems()[0].GetPenalty())
	// Action plan is derived from score_signals, ordered by penalty desc.
	// hub_severity(30) → step 1, god_modules(20) → step 2.
	require.Len(t, roadmap.GetActionPlan(), 2)
	step1 := roadmap.GetActionPlan()[0]
	assert.Equal(t, "DECOUPLE_STATE", step1.GetKind())
	assert.True(t, step1.GetBlocking())
	assert.Equal(t, int32(30), step1.GetImpact())
	assert.Equal(t, int32(1), step1.GetOrder())
	assert.Equal(t, int32(0), step1.GetDependsOnStep())
	step2 := roadmap.GetActionPlan()[1]
	assert.Equal(t, "SPLIT_GOD_MODULE", step2.GetKind())
	assert.False(t, step2.GetBlocking())
	assert.Equal(t, int32(20), step2.GetImpact())
	assert.Equal(t, int32(2), step2.GetOrder())
}

// INV-7: Full five-signal scenario — verifies ordering, dependency links, and
// concrete subjects derived from module names. Represents a notiplan-like case
// where domain_presence is the root blocker.
func TestInv7_GenerateRoadmap_FiveSignals_DependencyOrder(t *testing.T) {
	t.Parallel()
	svc, repo, _, _, _, analysis := newSvc(t)
	assessment := &commonv1.MigrabilityAssessment{
		Verdict:          domain.MigrabilityVerdictNotMigrable,
		MigrabilityScore: pointers.Int32Ptr(0),
		ScoreSignals: []*commonv1.ScoreSignal{
			{Signal: "domain_presence", Penalty: 40, Detail: "domain ratio 5% — very few domain modules relative to infrastructure"},
			{Signal: "cluster_count", Penalty: 25, Detail: "no service boundaries detected — monolith cannot be decomposed as-is"},
			{Signal: "hub_severity", Penalty: 20, Detail: "backend.var fan-in=19 — severe shared-state hub (concentrates 66% of incoming coupling)"},
			{Signal: "god_modules", Penalty: 10, Detail: "2 god-module(s): [backend.funcs backend.ingeteam_backend] (≥20 functions + shared state)"},
			{Signal: "routing_layout", Penalty: 5, Detail: "all 47 routes in a single blueprint — no per-domain routing separation"},
		},
	}
	m := &domain.Migration{
		Identifier:            16,
		State:                 domain.MigrationStateAwaitingApproval,
		AnalysisSummaryId:     55,
		MigrabilityAssessment: assessment,
	}
	infraSummary := &analysisv1.AnalysisSummary{
		Identifier: 55,
		ModuleClassification: &analysisv1.ModuleClassification{
			InfraModules: []string{"backend.funcs", "backend.var", "backend.ingeteam_backend"},
		},
	}
	repo.On("GetByID", mock.Anything, uint64(16), false).Return(m, nil)
	repo.On("SetRestructuringRoadmap", mock.Anything, uint64(16), mock.AnythingOfType("*migrationv1.RestructuringRoadmap")).Return(nil)
	analysis.On("GetAnalysisSummary", mock.Anything, uint64(55)).Return(infraSummary, nil)

	out, err := svc.GenerateRestructuringRoadmap(context.Background(), 16)
	require.NoError(t, err)
	roadmap := out.GetRestructuringRoadmap()
	require.NotNil(t, roadmap)

	plan := roadmap.GetActionPlan()
	require.Len(t, plan, 5)

	// Step 1: domain_presence (+40, root, no dependency).
	assert.Equal(t, "EXTRACT_DOMAIN", plan[0].GetKind())
	assert.Equal(t, int32(40), plan[0].GetImpact())
	assert.Equal(t, int32(1), plan[0].GetOrder())
	assert.Equal(t, int32(0), plan[0].GetDependsOnStep())
	assert.True(t, plan[0].GetBlocking())
	assert.Contains(t, plan[0].GetSubject(), "backend.funcs")

	// Step 2: cluster_count (+25, depends on step 1).
	assert.Equal(t, "DEFINE_BOUNDARIES", plan[1].GetKind())
	assert.Equal(t, int32(25), plan[1].GetImpact())
	assert.Equal(t, int32(2), plan[1].GetOrder())
	assert.Equal(t, int32(1), plan[1].GetDependsOnStep())

	// Step 3: hub_severity (+20, independent of cluster_count).
	assert.Equal(t, "DECOUPLE_STATE", plan[2].GetKind())
	assert.Equal(t, int32(20), plan[2].GetImpact())
	assert.Equal(t, int32(3), plan[2].GetOrder())
	assert.Equal(t, int32(0), plan[2].GetDependsOnStep())
	assert.Equal(t, "backend.var", plan[2].GetSubject())

	// Step 4: god_modules (+10, names extracted from detail).
	assert.Equal(t, "SPLIT_GOD_MODULE", plan[3].GetKind())
	assert.Equal(t, int32(10), plan[3].GetImpact())
	assert.Equal(t, int32(4), plan[3].GetOrder())
	assert.Contains(t, plan[3].GetSubject(), "backend.funcs")
	assert.Contains(t, plan[3].GetSubject(), "backend.ingeteam_backend")

	// Step 5: routing_layout (+5, depends on cluster_count = step 2).
	assert.Equal(t, "ADD_ROUTING", plan[4].GetKind())
	assert.Equal(t, int32(5), plan[4].GetImpact())
	assert.Equal(t, int32(5), plan[4].GetOrder())
	assert.Equal(t, int32(2), plan[4].GetDependsOnStep())
}

// ── EnrichRoadmap ─────────────────────────────────────────────────────────────

func newSvcWithEnricher(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockRoadmapEnricher) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	enricher := &mocks.MockRoadmapEnricher{}
	svc := application.NewService(repo, tx, nil, nil, nil, nil, nil, nil, nil, nil, nil, enricher, nil, nil, "")
	return svc, repo, enricher
}

func TestEnrichRoadmap_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithEnricher(t)
	_, err := svc.EnrichRoadmap(context.Background(), 0)
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}

func TestEnrichRoadmap_WrongState(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithEnricher(t)
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)

	_, err := svc.EnrichRoadmap(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
}

func TestEnrichRoadmap_NoRoadmap(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithEnricher(t)
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateRestructuringReady}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)

	_, err := svc.EnrichRoadmap(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeNoRoadmap)
}

func TestEnrichRoadmap_Success_PersistsEnrichment(t *testing.T) {
	t.Parallel()
	svc, repo, enricher := newSvcWithEnricher(t)

	roadmap := &domain.RestructuringRoadmap{
		ActionPlan: []*domain.ActionItem{
			{Order: 1, Kind: "EXTRACT_DOMAIN", Subject: "backend.funcs", Impact: 40, Blocking: true},
			{Order: 2, Kind: "DEFINE_BOUNDARIES", Subject: "no service boundaries detected", Impact: 25, Blocking: true, DependsOnStep: 1},
		},
	}
	m := &domain.Migration{
		Identifier:           7,
		State:                domain.MigrationStateRestructuringReady,
		RestructuringRoadmap: roadmap,
	}
	enrichment := &domain.RoadmapEnrichment{
		Steps: []*domain.EnrichedStep{
			{StepOrder: 1, Narrative: "backend.funcs mixes 47 route handlers with session management and ORM setup across 900 lines — extract the domain entities into a dedicated package separating business rules from infrastructure wiring."},
			{StepOrder: 2, Narrative: "Once a domain layer exists, group the extracted modules into functional clusters (e.g. auth, bookings, catalog) using community detection — currently all 47 routes live in a single blueprint with no boundary separation."},
		},
		CostUsd: 0.0012,
	}

	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	enricher.On("Enrich", mock.Anything, roadmap).Return(enrichment, nil)
	repo.On("SetRoadmapEnrichment", mock.Anything, uint64(7), enrichment).Return(nil)

	out, err := svc.EnrichRoadmap(context.Background(), 7)
	require.NoError(t, err)
	require.NotNil(t, out.GetRestructuringRoadmap().GetEnrichment())
	assert.Len(t, out.GetRestructuringRoadmap().GetEnrichment().GetSteps(), 2)
	assert.Equal(t, int32(1), out.GetRestructuringRoadmap().GetEnrichment().GetSteps()[0].GetStepOrder())
	assert.Contains(t, out.GetRestructuringRoadmap().GetEnrichment().GetSteps()[0].GetNarrative(), "backend.funcs")
	assert.InDelta(t, 0.0012, out.GetRestructuringRoadmap().GetEnrichment().GetCostUsd(), 1e-9)
	repo.AssertCalled(t, "SetRoadmapEnrichment", mock.Anything, uint64(7), enrichment)
	// Deterministic action_plan is still present (not replaced).
	assert.Len(t, out.GetRestructuringRoadmap().GetActionPlan(), 2)
}

func TestEnrichRoadmap_Idempotent_ReplacesExistingEnrichment(t *testing.T) {
	t.Parallel()
	svc, repo, enricher := newSvcWithEnricher(t)

	prevEnrichment := &domain.RoadmapEnrichment{
		Steps: []*domain.EnrichedStep{{StepOrder: 1, Narrative: "old narrative"}},
	}
	roadmap := &domain.RestructuringRoadmap{
		ActionPlan: []*domain.ActionItem{
			{Order: 1, Kind: "EXTRACT_DOMAIN", Subject: "backend.funcs", Impact: 40, Blocking: true},
		},
		Enrichment: prevEnrichment,
	}
	m := &domain.Migration{
		Identifier:           7,
		State:                domain.MigrationStateRestructuringReady,
		RestructuringRoadmap: roadmap,
	}
	newEnrichment := &domain.RoadmapEnrichment{
		Steps: []*domain.EnrichedStep{{StepOrder: 1, Narrative: "updated narrative for backend.funcs"}},
	}

	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	enricher.On("Enrich", mock.Anything, roadmap).Return(newEnrichment, nil)
	repo.On("SetRoadmapEnrichment", mock.Anything, uint64(7), newEnrichment).Return(nil)

	out, err := svc.EnrichRoadmap(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, "updated narrative for backend.funcs", out.GetRestructuringRoadmap().GetEnrichment().GetSteps()[0].GetNarrative())
}

// ── GenerateBlueprint ─────────────────────────────────────────────────────────

func newSvcWithBlueprintGenerator(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockBlueprintGenerator) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	gen := &mocks.MockBlueprintGenerator{}
	svc := application.NewService(repo, tx, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, gen, nil, "")
	return svc, repo, gen
}

func TestGenerateBlueprint_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc, _, _ := newSvcWithBlueprintGenerator(t)
	_, err := svc.GenerateBlueprint(context.Background(), 0)
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}

func TestGenerateBlueprint_WrongState(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithBlueprintGenerator(t)
	m := &domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	_, err := svc.GenerateBlueprint(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
}

func TestGenerateBlueprint_NoAnalysis(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithBlueprintGenerator(t)
	m := &domain.Migration{
		Identifier:        7,
		State:             domain.MigrationStateRestructuringReady,
		AnalysisSummaryId: 0,
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	_, err := svc.GenerateBlueprint(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeNoBlueprintAnalysis)
}

func TestGenerateBlueprint_NoRoadmap(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newSvcWithBlueprintGenerator(t)
	m := &domain.Migration{
		Identifier:        7,
		State:             domain.MigrationStateRestructuringReady,
		AnalysisSummaryId: 10003,
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	_, err := svc.GenerateBlueprint(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeNoRoadmap)
}

func TestGenerateBlueprint_Success_PersistsBlueprint(t *testing.T) {
	t.Parallel()
	svc, repo, gen := newSvcWithBlueprintGenerator(t)

	roadmap := &domain.RestructuringRoadmap{
		ActionPlan: []*domain.ActionItem{
			{Order: 1, Kind: "EXTRACT_DOMAIN", Subject: "backend.funcs", Impact: 40, Blocking: true},
			{Order: 2, Kind: "DEFINE_BOUNDARIES", Subject: "no boundaries", Impact: 25, Blocking: true, DependsOnStep: 1},
		},
	}
	m := &domain.Migration{
		Identifier:           7,
		State:                domain.MigrationStateRestructuringReady,
		AnalysisSummaryId:    10003,
		RestructuringRoadmap: roadmap,
	}
	blueprint := &domain.ServiceBlueprint{
		Services:         []*domain.BlueprintService{},
		IsHypothetical:   true,
		PreconditionNote: "Blueprint only valid after completing steps 1-2: extract domain layer from backend.funcs.",
		RequiredSteps:    []int32{1, 2},
		CostUsd:          0.0085,
		ConfidenceNote:   "No domain modules detected — blueprint cannot be proposed before restructuring.",
	}

	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	gen.On("Generate", mock.Anything, uint64(10003), roadmap).Return(blueprint, nil)
	repo.On("SetServiceBlueprint", mock.Anything, uint64(7), blueprint).Return(nil)

	out, err := svc.GenerateBlueprint(context.Background(), 7)
	require.NoError(t, err)
	require.NotNil(t, out.GetRestructuringRoadmap().GetBlueprint())
	assert.True(t, out.GetRestructuringRoadmap().GetBlueprint().GetIsHypothetical())
	assert.Empty(t, out.GetRestructuringRoadmap().GetBlueprint().GetServices())
	assert.NotEmpty(t, out.GetRestructuringRoadmap().GetBlueprint().GetPreconditionNote())
	assert.Equal(t, []int32{1, 2}, out.GetRestructuringRoadmap().GetBlueprint().GetRequiredSteps())
	assert.InDelta(t, 0.0085, out.GetRestructuringRoadmap().GetBlueprint().GetCostUsd(), 1e-9)
	// Deterministic action_plan must be unchanged.
	assert.Len(t, out.GetRestructuringRoadmap().GetActionPlan(), 2)
	repo.AssertCalled(t, "SetServiceBlueprint", mock.Anything, uint64(7), blueprint)
}

func TestGenerateBlueprint_Idempotent_ReplacesExistingBlueprint(t *testing.T) {
	t.Parallel()
	svc, repo, gen := newSvcWithBlueprintGenerator(t)

	oldBlueprint := &domain.ServiceBlueprint{
		Services: []*domain.BlueprintService{{Name: "old-service", Modules: []string{"a"}, Rationale: "old"}},
	}
	roadmap := &domain.RestructuringRoadmap{
		ActionPlan: []*domain.ActionItem{{Order: 1, Kind: "EXTRACT_DOMAIN", Subject: "a", Impact: 40, Blocking: true}},
		Blueprint:  oldBlueprint,
	}
	m := &domain.Migration{
		Identifier:           7,
		State:                domain.MigrationStateRestructuringReady,
		AnalysisSummaryId:    10003,
		RestructuringRoadmap: roadmap,
	}
	newBlueprint := &domain.ServiceBlueprint{
		Services: []*domain.BlueprintService{{Name: "new-service", Modules: []string{"a", "b"}, Rationale: "updated rationale referencing edge weight 12"}},
		CostUsd:  0.009,
	}

	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	gen.On("Generate", mock.Anything, uint64(10003), roadmap).Return(newBlueprint, nil)
	repo.On("SetServiceBlueprint", mock.Anything, uint64(7), newBlueprint).Return(nil)

	out, err := svc.GenerateBlueprint(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, out.GetRestructuringRoadmap().GetBlueprint().GetServices(), 1)
	assert.Equal(t, "new-service", out.GetRestructuringRoadmap().GetBlueprint().GetServices()[0].GetName())
}

// ── StartMigration — reuse path ───────────────────────────────────────────────

func TestStartMigration_Reuse_Success(t *testing.T) {
	svc, repo, _, _, repoClient, analysis := newSvc(t)
	// Migration references an existing analysis; source_branch is empty (inherits from analysis).
	mig := &domain.Migration{
		Identifier:              7,
		RepositoryId:            42,
		OwnerUserId:             1,
		State:                   domain.MigrationStatePending,
		SourceAnalysisSummaryId: 99,
	}
	completedAnalysis := &analysisv1.AnalysisSummary{
		Identifier:   99,
		RepositoryId: 42,
		OwnerUserId:  1,
		State:        analysisv1.AnalysisState_ANALYSIS_STATE_COMPLETED,
		SourceBranch: "develop",
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(mig, nil)
	analysis.On("GetAnalysisSummary", mock.Anything, uint64(99)).Return(completedAnalysis, nil)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateAnalyzing).Return(nil)
	repo.On("AdoptAnalysis", mock.Anything, uint64(7), uint64(99), "develop").Return(nil)

	out, err := svc.StartMigration(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateDesigning, out.GetState())
	assert.Equal(t, uint64(99), out.GetAnalysisSummaryId())
	assert.True(t, out.GetAnalysisReused())
	assert.Equal(t, "develop", out.GetSourceBranch())
	// RunAnalysis must NOT be called.
	analysis.AssertNotCalled(t, "RunAnalysis", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestStartMigration_Reuse_InheritsExplicitBranch(t *testing.T) {
	// When migration already has a source_branch, AdoptAnalysis is called with it
	// (not the analysis's branch).
	svc, repo, _, _, repoClient, analysis := newSvc(t)
	mig := &domain.Migration{
		Identifier:              7,
		RepositoryId:            42,
		State:                   domain.MigrationStatePending,
		SourceBranch:            "feature/x",
		SourceAnalysisSummaryId: 99,
	}
	completedAnalysis := &analysisv1.AnalysisSummary{
		Identifier:   99,
		RepositoryId: 42,
		State:        analysisv1.AnalysisState_ANALYSIS_STATE_COMPLETED,
		SourceBranch: "main",
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(mig, nil)
	analysis.On("GetAnalysisSummary", mock.Anything, uint64(99)).Return(completedAnalysis, nil)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateAnalyzing).Return(nil)
	repo.On("AdoptAnalysis", mock.Anything, uint64(7), uint64(99), "feature/x").Return(nil)

	out, err := svc.StartMigration(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateDesigning, out.GetState())
	assert.Equal(t, "feature/x", out.GetSourceBranch())
}

func TestStartMigration_Reuse_AnalysisNotFound(t *testing.T) {
	svc, repo, _, _, _, analysis := newSvc(t)
	mig := &domain.Migration{
		Identifier:              7,
		RepositoryId:            42,
		State:                   domain.MigrationStatePending,
		SourceAnalysisSummaryId: 99,
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(mig, nil)
	analysis.On("GetAnalysisSummary", mock.Anything, uint64(99)).Return((*analysisv1.AnalysisSummary)(nil), domain.ErrInternal)

	_, err := svc.StartMigration(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeSourceAnalysisNotFound)
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestStartMigration_Reuse_WrongRepository(t *testing.T) {
	svc, repo, _, _, _, analysis := newSvc(t)
	mig := &domain.Migration{
		Identifier:              7,
		RepositoryId:            42,
		State:                   domain.MigrationStatePending,
		SourceAnalysisSummaryId: 99,
	}
	wrongRepoAnalysis := &analysisv1.AnalysisSummary{
		Identifier:   99,
		RepositoryId: 999, // different repo
		State:        analysisv1.AnalysisState_ANALYSIS_STATE_COMPLETED,
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(mig, nil)
	analysis.On("GetAnalysisSummary", mock.Anything, uint64(99)).Return(wrongRepoAnalysis, nil)

	_, err := svc.StartMigration(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeSourceAnalysisInvalid)
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestStartMigration_Reuse_NotCompleted(t *testing.T) {
	svc, repo, _, _, _, analysis := newSvc(t)
	mig := &domain.Migration{
		Identifier:              7,
		RepositoryId:            42,
		State:                   domain.MigrationStatePending,
		SourceAnalysisSummaryId: 99,
	}
	runningAnalysis := &analysisv1.AnalysisSummary{
		Identifier:   99,
		RepositoryId: 42,
		State:        analysisv1.AnalysisState_ANALYSIS_STATE_RUNNING, // not completed yet
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(mig, nil)
	analysis.On("GetAnalysisSummary", mock.Anything, uint64(99)).Return(runningAnalysis, nil)

	_, err := svc.StartMigration(context.Background(), 7)
	assertDomainError(t, err, domain.ErrCodeSourceAnalysisInvalid)
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestStartMigration_Reuse_AdoptFails_RollsBackToPending(t *testing.T) {
	svc, repo, _, _, repoClient, analysis := newSvc(t)
	mig := &domain.Migration{
		Identifier:              7,
		RepositoryId:            42,
		State:                   domain.MigrationStatePending,
		SourceAnalysisSummaryId: 99,
	}
	completedAnalysis := &analysisv1.AnalysisSummary{
		Identifier:   99,
		RepositoryId: 42,
		State:        analysisv1.AnalysisState_ANALYSIS_STATE_COMPLETED,
		SourceBranch: "main",
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(mig, nil)
	analysis.On("GetAnalysisSummary", mock.Anything, uint64(99)).Return(completedAnalysis, nil)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateAnalyzing).Return(nil)
	repo.On("AdoptAnalysis", mock.Anything, uint64(7), uint64(99), "main").Return(domain.ErrInternal)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStatePending).Return(nil)

	_, err := svc.StartMigration(context.Background(), 7)
	require.Error(t, err)
	repo.AssertCalled(t, "UpdateState", mock.Anything, uint64(7), domain.MigrationStatePending)
}

func TestStartMigration_Reuse_EnqueuesDecompose(t *testing.T) {
	// Invariant: after startWithReuse adopts an analysis, a decompose:run job
	// must be enqueued so the migration progresses to AWAITING_APPROVAL.
	t.Parallel()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	repoClient := &mocks.MockRepositoryClient{}
	analysis := &mocks.MockAnalysisClient{}
	decompose := &mocks.MockDecomposeJobEnqueuer{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)

	mig := &domain.Migration{
		Identifier:              7,
		RepositoryId:            42,
		RepositoryUrl:           "https://github.com/example/repo",
		State:                   domain.MigrationStatePending,
		SourceAnalysisSummaryId: 99,
	}
	completedAnalysis := &analysisv1.AnalysisSummary{
		Identifier:   99,
		RepositoryId: 42,
		State:        analysisv1.AnalysisState_ANALYSIS_STATE_COMPLETED,
		SourceBranch: "main",
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(mig, nil)
	analysis.On("GetAnalysisSummary", mock.Anything, uint64(99)).Return(completedAnalysis, nil)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateAnalyzing).Return(nil)
	repo.On("AdoptAnalysis", mock.Anything, uint64(7), uint64(99), "main").Return(nil)
	decompose.On("EnqueueDecompose", mock.Anything, uint64(7), uint64(99), "https://github.com/example/repo", "main").Return(nil)

	svc := application.NewService(repo, tx, nil, repoClient, analysis, nil, nil, decompose, nil, nil, nil, nil, nil, nil, "")
	out, err := svc.StartMigration(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateDesigning, out.GetState())
	// The decompose job must be enqueued with the correct migration, summary, URL, and branch.
	decompose.AssertCalled(t, "EnqueueDecompose", mock.Anything, uint64(7), uint64(99), "https://github.com/example/repo", "main")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertDomainError(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	var de *domain.Error
	require.ErrorAs(t, err, &de, "expected domain.Error, got %T: %v", err, err)
	assert.Equal(t, code, de.Code)
}

// ── RunMigration (single-shot roadmap orchestration) ───────────────────────────

// runSvc builds a service with the repo, repository-client, analysis-client and
// generation enqueuer wired — the ports RunMigration drives across states.
func runSvc(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockRepositoryClient, *mocks.MockAnalysisClient, *mocks.MockGenerationJobEnqueuer) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	repoClient := &mocks.MockRepositoryClient{}
	analysis := &mocks.MockAnalysisClient{}
	enqueuer := &mocks.MockGenerationJobEnqueuer{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	svc := application.NewService(repo, tx, nil, repoClient, analysis, nil, enqueuer, nil, nil, nil, nil, nil, nil, nil, "")
	return svc, repo, repoClient, analysis, enqueuer
}

func TestRunMigration_FromPending_SetsAutoApproveAndStarts(t *testing.T) {
	svc, repo, repoClient, analysis, _ := runSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, RepositoryId: 42, State: domain.MigrationStatePending}, nil)
	repo.On("SetAutoApprove", mock.Anything, uint64(7), true).Return(nil)
	repoClient.On("ProbeConnection", mock.Anything, uint64(42)).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateAnalyzing).Return(nil)
	analysis.On("RunAnalysis", mock.Anything, uint64(42), uint64(7), uint64(0), "", "").Return(nil)

	out, err := svc.RunMigration(context.Background(), 7, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateAnalyzing, out.GetState())
	assert.True(t, out.GetAutoApprove())
	repo.AssertCalled(t, "SetAutoApprove", mock.Anything, uint64(7), true)
}

func TestRunMigration_FromDesigning_ArmsAutoApproveOnly(t *testing.T) {
	svc, repo, _, _, _ := runSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateDesigning}, nil)
	repo.On("SetAutoApprove", mock.Anything, uint64(7), true).Return(nil)

	out, err := svc.RunMigration(context.Background(), 7, nil)
	require.NoError(t, err)
	// Still DESIGNING — the worker auto-approves when AWAITING_APPROVAL is reached.
	assert.Equal(t, domain.MigrationStateDesigning, out.GetState())
	assert.True(t, out.GetAutoApprove())
	// No StartMigration side effects.
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestRunMigration_FromAwaitingApproval_ApprovesImmediately(t *testing.T) {
	svc, repo, _, _, enqueuer := runSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval}, nil)
	repo.On("SetAutoApprove", mock.Anything, uint64(7), true).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateGenerating).Return(nil)
	enqueuer.On("EnqueueGeneration", mock.Anything, uint64(7), mock.Anything).Return(nil)

	out, err := svc.RunMigration(context.Background(), 7, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateGenerating, out.GetState())
	enqueuer.AssertCalled(t, "EnqueueGeneration", mock.Anything, uint64(7), mock.Anything)
}

func TestRunMigration_FromAwaitingApproval_ServiceFilterForwarded(t *testing.T) {
	svc, repo, _, _, enqueuer := runSvc(t)
	filter := []string{"articles", "users"}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateAwaitingApproval}, nil)
	repo.On("SetAutoApprove", mock.Anything, uint64(7), true).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateGenerating).Return(nil)
	enqueuer.On("EnqueueGeneration", mock.Anything, uint64(7), filter).Return(nil)

	_, err := svc.RunMigration(context.Background(), 7, filter)
	require.NoError(t, err)
	enqueuer.AssertCalled(t, "EnqueueGeneration", mock.Anything, uint64(7), filter)
}

func TestRunMigration_Gate_NOT_MIGRABLE_Blocked(t *testing.T) {
	svc, repo, _, _, _ := runSvc(t)
	m := &domain.Migration{
		Identifier:            7,
		State:                 domain.MigrationStateAwaitingApproval,
		MigrabilityAssessment: &commonv1.MigrabilityAssessment{Verdict: domain.MigrabilityVerdictNotMigrable},
		MigrabilityOverride:   false,
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	repo.On("SetAutoApprove", mock.Anything, uint64(7), true).Return(nil)

	// The one-shot run must NOT bypass the migrability gate: it still blocks.
	_, err := svc.RunMigration(context.Background(), 7, nil)
	assertDomainError(t, err, domain.ErrCodeNotMigrableBlocked)
	// No state advance.
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestRunMigration_NoServiceBoundaries_Blocked(t *testing.T) {
	svc, repo, _, _, _ := runSvc(t)
	m := &domain.Migration{
		Identifier: 7,
		State:      domain.MigrationStateAwaitingApproval,
		Plan:       &migrationv1.RestructurePlan{NoServiceBoundaries: true},
	}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(m, nil)
	repo.On("SetAutoApprove", mock.Anything, uint64(7), true).Return(nil)

	_, err := svc.RunMigration(context.Background(), 7, nil)
	assertDomainError(t, err, domain.ErrCodeNoServiceBoundaries)
}

func TestRunMigration_FromGenerating_NoOpReassert(t *testing.T) {
	svc, repo, _, _, _ := runSvc(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(
		&domain.Migration{Identifier: 7, State: domain.MigrationStateGenerating}, nil)
	repo.On("SetAutoApprove", mock.Anything, uint64(7), true).Return(nil)

	out, err := svc.RunMigration(context.Background(), 7, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.MigrationStateGenerating, out.GetState())
	// Already past the approval gate — no re-approval, no re-enqueue.
	repo.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
}

func TestRunMigration_TerminalStates_Rejected(t *testing.T) {
	svc, repo, _, _, _ := runSvc(t)
	terminal := []domain.MigrationState{
		domain.MigrationStatePushed,
		domain.MigrationStateFailed,
		domain.MigrationStateCancelled,
		domain.MigrationStateRestructuringReady,
	}
	for _, state := range terminal {
		repo.ExpectedCalls = nil
		repo.On("GetByID", mock.Anything, uint64(7), false).Return(&domain.Migration{Identifier: 7, State: state}, nil)
		_, err := svc.RunMigration(context.Background(), 7, nil)
		assertDomainError(t, err, domain.ErrCodeInvalidStateTransition)
	}
}

func TestRunMigration_MissingIdentifier_Rejected(t *testing.T) {
	svc, _, _, _, _ := runSvc(t)
	_, err := svc.RunMigration(context.Background(), 0, nil)
	assertDomainError(t, err, domain.ErrCodeMissingIdentifier)
}
