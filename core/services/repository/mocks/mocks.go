// Package mocks contains testify-based stand-ins for the repository service
// driven ports. They live next to the real implementations so they can be used
// from any test in this module without a circular import.
package mocks

import (
	"context"

	"milton_prism/core/services/repository/domain"
	"milton_prism/core/services/repository/ports"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"github.com/stretchr/testify/mock"
)

// Compile-time interface checks.
var (
	_ ports.RepositoryRepository = (*MockRepositoryRepository)(nil)
	_ ports.TransactionManager   = (*MockTransactionManager)(nil)
	_ ports.IdentityClient       = (*MockIdentityClient)(nil)
	_ ports.GitClient            = (*MockGitClient)(nil)
)

// MockRepositoryRepository is a testify mock for ports.RepositoryRepository.
type MockRepositoryRepository struct {
	mock.Mock
}

func (m *MockRepositoryRepository) Create(ctx context.Context, r *domain.Repository) (*domain.Repository, error) {
	args := m.Called(ctx, r)
	v, _ := args.Get(0).(*domain.Repository)
	return v, args.Error(1)
}

func (m *MockRepositoryRepository) GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.Repository, error) {
	args := m.Called(ctx, identifier, includeDeleted)
	v, _ := args.Get(0).(*domain.Repository)
	return v, args.Error(1)
}

func (m *MockRepositoryRepository) List(ctx context.Context, filter *domain.RepositoriesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Repository, *paginationv1.Pagination, error) {
	args := m.Called(ctx, filter, params)
	items, _ := args.Get(0).([]*domain.Repository)
	pag, _ := args.Get(1).(*paginationv1.Pagination)
	return items, pag, args.Error(2)
}

func (m *MockRepositoryRepository) Update(ctx context.Context, r *domain.Repository) error {
	return m.Called(ctx, r).Error(0)
}

func (m *MockRepositoryRepository) SoftDelete(ctx context.Context, identifier uint64) error {
	return m.Called(ctx, identifier).Error(0)
}

func (m *MockRepositoryRepository) UpdateConnectionStatus(ctx context.Context, identifier uint64, status domain.ConnectionStatus) error {
	return m.Called(ctx, identifier, status).Error(0)
}

// MockTransactionManager is a pass-through implementation of ports.TransactionManager.
type MockTransactionManager struct {
	mock.Mock
}

func (m *MockTransactionManager) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	args := m.Called(ctx, fn)
	if args.Get(0) == nil {
		return fn(ctx)
	}
	return args.Error(0)
}

// MockIdentityClient is a testify mock for ports.IdentityClient.
type MockIdentityClient struct {
	mock.Mock
}

func (m *MockIdentityClient) ValidateUserExists(ctx context.Context, userID uint64) error {
	return m.Called(ctx, userID).Error(0)
}

// MockGitClient is a testify mock for ports.GitClient.
type MockGitClient struct {
	mock.Mock
}

func (m *MockGitClient) ProbeSource(ctx context.Context, remoteURL, token string) (*domain.SourceProbeResult, error) {
	args := m.Called(ctx, remoteURL, token)
	v, _ := args.Get(0).(*domain.SourceProbeResult)
	return v, args.Error(1)
}

func (m *MockGitClient) TestConnection(ctx context.Context, remoteURL, credentialRef string) (domain.ConnectionStatus, error) {
	args := m.Called(ctx, remoteURL, credentialRef)
	return args.Get(0).(domain.ConnectionStatus), args.Error(1)
}

func (m *MockGitClient) ListBranches(ctx context.Context, remoteURL, credentialRef string) ([]*domain.Branch, error) {
	args := m.Called(ctx, remoteURL, credentialRef)
	branches, _ := args.Get(0).([]*domain.Branch)
	return branches, args.Error(1)
}

func (m *MockGitClient) PushResult(ctx context.Context, targetURL, writeToken string, files []*domain.PushFile, commitMessage string) (string, error) {
	args := m.Called(ctx, targetURL, writeToken, files, commitMessage)
	return args.String(0), args.Error(1)
}
