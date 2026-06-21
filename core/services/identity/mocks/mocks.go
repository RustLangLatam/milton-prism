// Package mocks contains testify-based stand-ins for the identity service driven
// ports. They live next to the real implementations so they can be used from
// any test in this module without a circular import.
package mocks

import (
	"context"

	"milton_prism/core/services/identity/domain"
	"milton_prism/core/services/identity/ports"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"github.com/stretchr/testify/mock"
)

// Compile-time interface checks.
var (
	_ ports.UserRepository     = (*MockUserRepository)(nil)
	_ ports.TransactionManager = (*MockTransactionManager)(nil)
	_ ports.PasswordHasher     = (*MockPasswordHasher)(nil)
	_ ports.TokenManager       = (*MockTokenManager)(nil)
	_ ports.SessionStore       = (*MockSessionStore)(nil)
)

// MockUserRepository is a testify mock for ports.UserRepository.
type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) Create(ctx context.Context, u *domain.User, passwordHash string) (*domain.User, error) {
	args := m.Called(ctx, u, passwordHash)
	v, _ := args.Get(0).(*domain.User)
	return v, args.Error(1)
}

func (m *MockUserRepository) GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.User, error) {
	args := m.Called(ctx, identifier, includeDeleted)
	v, _ := args.Get(0).(*domain.User)
	return v, args.Error(1)
}

func (m *MockUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	v, _ := args.Get(0).(*domain.User)
	return v, args.Error(1)
}

func (m *MockUserRepository) GetCredentialsByEmail(ctx context.Context, email string) (*domain.User, string, error) {
	args := m.Called(ctx, email)
	v, _ := args.Get(0).(*domain.User)
	return v, args.String(1), args.Error(2)
}

func (m *MockUserRepository) List(ctx context.Context, filter *domain.UsersFilter, params *queryparamsv1.PageQueryParams) ([]*domain.User, *paginationv1.Pagination, error) {
	args := m.Called(ctx, filter, params)
	items, _ := args.Get(0).([]*domain.User)
	pag, _ := args.Get(1).(*paginationv1.Pagination)
	return items, pag, args.Error(2)
}

func (m *MockUserRepository) Update(ctx context.Context, u *domain.User) error {
	return m.Called(ctx, u).Error(0)
}

func (m *MockUserRepository) SoftDelete(ctx context.Context, identifier uint64) error {
	return m.Called(ctx, identifier).Error(0)
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

// MockPasswordHasher is a testify mock for ports.PasswordHasher.
type MockPasswordHasher struct {
	mock.Mock
}

func (m *MockPasswordHasher) Hash(plain string) (string, error) {
	args := m.Called(plain)
	return args.String(0), args.Error(1)
}

func (m *MockPasswordHasher) Verify(hash, plain string) error {
	return m.Called(hash, plain).Error(0)
}

// MockTokenManager is a testify mock for ports.TokenManager.
type MockTokenManager struct {
	mock.Mock
}

func (m *MockTokenManager) NewTokens(ctx context.Context, userID uint64, systemUser bool, sessionID string) (*domain.AuthorizationTokens, error) {
	args := m.Called(ctx, userID, systemUser, sessionID)
	v, _ := args.Get(0).(*domain.AuthorizationTokens)
	return v, args.Error(1)
}

func (m *MockTokenManager) ExtractSessionID(refreshToken string) (string, error) {
	args := m.Called(refreshToken)
	return args.String(0), args.Error(1)
}

func (m *MockTokenManager) Revoke(ctx context.Context, token string) error {
	return m.Called(ctx, token).Error(0)
}

// MockSessionStore is a testify mock for ports.SessionStore.
type MockSessionStore struct {
	mock.Mock
}

func (m *MockSessionStore) Save(ctx context.Context, sessionID string, userID uint64, systemUser bool) error {
	return m.Called(ctx, sessionID, userID, systemUser).Error(0)
}

func (m *MockSessionStore) Get(ctx context.Context, sessionID string) (uint64, bool, bool, error) {
	args := m.Called(ctx, sessionID)
	return args.Get(0).(uint64), args.Bool(1), args.Bool(2), args.Error(3)
}

func (m *MockSessionStore) Delete(ctx context.Context, sessionID string) error {
	return m.Called(ctx, sessionID).Error(0)
}
