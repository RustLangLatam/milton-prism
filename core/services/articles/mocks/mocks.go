// Package mocks contains testify-based stand-ins for the articles service
// driven ports.
package mocks

import (
	"context"

	"milton_prism/core/services/articles/domain"
	"milton_prism/core/services/articles/ports"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"github.com/stretchr/testify/mock"
)

// Compile-time interface checks.
var (
	_ ports.ArticleRepository = (*MockArticleRepository)(nil)
	_ ports.TagRepository     = (*MockTagRepository)(nil)
	_ ports.TransactionManager = (*MockTransactionManager)(nil)
	_ ports.ProfileClient     = (*MockProfileClient)(nil)
)

// MockArticleRepository is a testify mock for ports.ArticleRepository.
type MockArticleRepository struct {
	mock.Mock
}

func (m *MockArticleRepository) Create(ctx context.Context, a *domain.Article) (*domain.Article, error) {
	args := m.Called(ctx, a)
	v, _ := args.Get(0).(*domain.Article)
	return v, args.Error(1)
}

func (m *MockArticleRepository) GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.Article, error) {
	args := m.Called(ctx, identifier, includeDeleted)
	v, _ := args.Get(0).(*domain.Article)
	return v, args.Error(1)
}

func (m *MockArticleRepository) List(ctx context.Context, filter *domain.ArticlesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Article, *paginationv1.Pagination, error) {
	args := m.Called(ctx, filter, params)
	items, _ := args.Get(0).([]*domain.Article)
	pag, _ := args.Get(1).(*paginationv1.Pagination)
	return items, pag, args.Error(2)
}

func (m *MockArticleRepository) Update(ctx context.Context, a *domain.Article) error {
	return m.Called(ctx, a).Error(0)
}

func (m *MockArticleRepository) SoftDelete(ctx context.Context, identifier uint64) error {
	return m.Called(ctx, identifier).Error(0)
}

// MockTagRepository is a testify mock for ports.TagRepository.
type MockTagRepository struct {
	mock.Mock
}

func (m *MockTagRepository) GetByID(ctx context.Context, identifier uint64) (*domain.Tag, error) {
	args := m.Called(ctx, identifier)
	v, _ := args.Get(0).(*domain.Tag)
	return v, args.Error(1)
}

func (m *MockTagRepository) List(ctx context.Context, params *queryparamsv1.PageQueryParams) ([]*domain.Tag, *paginationv1.Pagination, error) {
	args := m.Called(ctx, params)
	items, _ := args.Get(0).([]*domain.Tag)
	pag, _ := args.Get(1).(*paginationv1.Pagination)
	return items, pag, args.Error(2)
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

// MockProfileClient is a testify mock for ports.ProfileClient.
type MockProfileClient struct {
	mock.Mock
}

func (m *MockProfileClient) ValidateProfileExists(ctx context.Context, profileID uint64) error {
	return m.Called(ctx, profileID).Error(0)
}
