// Package mocks contains testify-based stand-ins for the analysis service
// driven ports. They live next to the real implementations so they can be used
// from any test in this module without a circular import.
package mocks

import (
	"context"
	"time"

	"milton_prism/core/services/analysis/domain"
	"milton_prism/core/services/analysis/ports"
	analysissvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"github.com/stretchr/testify/mock"
)

// Compile-time interface checks.
var (
	_ ports.AnalysisSummaryRepository = (*MockAnalysisSummaryRepository)(nil)
	_ ports.RepositoryClient          = (*MockRepositoryClient)(nil)
	_ ports.JobEnqueuer               = (*MockJobEnqueuer)(nil)
	_ ports.PlanProvider              = (*MockPlanProvider)(nil)
	_ ports.MigrationClient           = (*MockMigrationClient)(nil)
)

// MockMigrationClient is a testify mock for ports.MigrationClient.
type MockMigrationClient struct {
	mock.Mock
}

func (m *MockMigrationClient) CountLiveMigrationsByAnalysis(ctx context.Context, analysisSummaryID uint64) (int64, error) {
	args := m.Called(ctx, analysisSummaryID)
	return args.Get(0).(int64), args.Error(1)
}

// MockAnalysisSummaryRepository is a testify mock for ports.AnalysisSummaryRepository.
type MockAnalysisSummaryRepository struct {
	mock.Mock
}

func (m *MockAnalysisSummaryRepository) Create(ctx context.Context, s *domain.AnalysisSummary) (*domain.AnalysisSummary, error) {
	args := m.Called(ctx, s)
	v, _ := args.Get(0).(*domain.AnalysisSummary)
	return v, args.Error(1)
}

func (m *MockAnalysisSummaryRepository) GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.AnalysisSummary, error) {
	args := m.Called(ctx, identifier, includeDeleted)
	v, _ := args.Get(0).(*domain.AnalysisSummary)
	return v, args.Error(1)
}

func (m *MockAnalysisSummaryRepository) List(ctx context.Context, filter *analysissvcv1.AnalysisSummariesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.AnalysisSummary, *paginationv1.Pagination, error) {
	args := m.Called(ctx, filter, params)
	items, _ := args.Get(0).([]*domain.AnalysisSummary)
	pag, _ := args.Get(1).(*paginationv1.Pagination)
	return items, pag, args.Error(2)
}

func (m *MockAnalysisSummaryRepository) SoftDelete(ctx context.Context, identifier uint64) error {
	return m.Called(ctx, identifier).Error(0)
}

func (m *MockAnalysisSummaryRepository) UpdateState(ctx context.Context, identifier uint64, state domain.AnalysisState) error {
	return m.Called(ctx, identifier, state).Error(0)
}

func (m *MockAnalysisSummaryRepository) UpdateMigrabilityAssessment(ctx context.Context, identifier uint64, assessment *domain.MigrabilityAssessment) error {
	return m.Called(ctx, identifier, assessment).Error(0)
}

func (m *MockAnalysisSummaryRepository) MarkRootSelected(ctx context.Context, identifier uint64, rootSubdirectory string) (*domain.AnalysisSummary, error) {
	args := m.Called(ctx, identifier, rootSubdirectory)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.AnalysisSummary), args.Error(1)
}

func (m *MockAnalysisSummaryRepository) CountByOwnerSince(ctx context.Context, ownerID uint64, since time.Time) (int64, error) {
	args := m.Called(ctx, ownerID, since)
	return args.Get(0).(int64), args.Error(1)
}

// MockPlanProvider is a testify mock for ports.PlanProvider.
type MockPlanProvider struct {
	mock.Mock
}

func (m *MockPlanProvider) GetUserPlan(ctx context.Context, userID uint64) (*billingv1.Plan, error) {
	args := m.Called(ctx, userID)
	v, _ := args.Get(0).(*billingv1.Plan)
	return v, args.Error(1)
}

// MockRepositoryClient is a testify mock for ports.RepositoryClient.
type MockRepositoryClient struct {
	mock.Mock
}

func (m *MockRepositoryClient) ValidateRepositoryExists(ctx context.Context, repositoryID uint64) error {
	return m.Called(ctx, repositoryID).Error(0)
}

func (m *MockRepositoryClient) GetRemoteURL(ctx context.Context, repositoryID uint64) (string, string, error) {
	args := m.Called(ctx, repositoryID)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockRepositoryClient) ProbeConnection(ctx context.Context, repositoryID uint64) error {
	return m.Called(ctx, repositoryID).Error(0)
}

func (m *MockRepositoryClient) GetBranchSHA(ctx context.Context, repositoryID uint64, branch string) (string, error) {
	args := m.Called(ctx, repositoryID, branch)
	return args.String(0), args.Error(1)
}

// MockJobEnqueuer is a testify mock for ports.JobEnqueuer.
type MockJobEnqueuer struct {
	mock.Mock
}

func (m *MockJobEnqueuer) EnqueueAnalysis(ctx context.Context, summaryID, repositoryID, migrationID uint64, remoteURL, defaultBranch, rootSubdirectory string) error {
	return m.Called(ctx, summaryID, repositoryID, migrationID, remoteURL, defaultBranch, rootSubdirectory).Error(0)
}
