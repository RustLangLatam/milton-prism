package application_test

import (
	"context"
	"errors"
	"testing"

	"milton_prism/core/services/identity/application"
	"milton_prism/core/services/identity/domain"
	"milton_prism/core/services/identity/mocks"
	"milton_prism/core/services/identity/ports"
	identityv1 "milton_prism/pkg/pb/gen/milton_prism/types/identity/v1"
	tokenv1 "milton_prism/pkg/pb/gen/milton_prism/types/token/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func validUser(id uint64, email string) *domain.User {
	return &identityv1.User{Identifier: id, Email: email}
}

func newSvc(repo ports.UserRepository, hasher ports.PasswordHasher, tokens ports.TokenManager, sessions ports.SessionStore) *application.Service {
	return application.NewService(repo, nil, hasher, tokens, sessions)
}

// ─── CreateUser ──────────────────────────────────────────────────────────────

func TestCreateUser_MissingPayload(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockUserRepository{}, nil, nil, nil)
	_, err := svc.CreateUser(context.Background(), &domain.User{}, "secret")
	assert.ErrorIs(t, err, domain.ErrMissingPayload)
}

func TestCreateUser_MissingPassword(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockUserRepository{}, nil, nil, nil)
	_, err := svc.CreateUser(context.Background(), validUser(0, "u@x.com"), "")
	assert.ErrorIs(t, err, domain.ErrMissingPassword)
}

func TestCreateUser_DuplicateEmail(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("Create", mock.Anything, mock.Anything, mock.Anything).Return((*domain.User)(nil), domain.ErrEmailAlreadyExists)
	svc := newSvc(repo, nil, nil, nil)
	_, err := svc.CreateUser(context.Background(), validUser(0, "dup@x.com"), "pass")
	assert.ErrorIs(t, err, domain.ErrEmailAlreadyExists)
}

func TestCreateUser_InternalError(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("Create", mock.Anything, mock.Anything, mock.Anything).Return((*domain.User)(nil), errors.New("db down"))
	svc := newSvc(repo, nil, nil, nil)
	_, err := svc.CreateUser(context.Background(), validUser(0, "u@x.com"), "pass")
	assert.ErrorIs(t, err, domain.ErrInternal)
}

func TestCreateUser_OK(t *testing.T) {
	t.Parallel()
	u := validUser(1, "u@x.com")
	repo := &mocks.MockUserRepository{}
	repo.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(u, nil)
	hasher := &mocks.MockPasswordHasher{}
	hasher.On("Hash", "pass").Return("hashed", nil)
	svc := newSvc(repo, hasher, nil, nil)
	out, err := svc.CreateUser(context.Background(), validUser(0, "u@x.com"), "pass")
	assert.NoError(t, err)
	assert.Equal(t, u, out)
}

// ─── GetUser ─────────────────────────────────────────────────────────────────

func TestGetUser_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockUserRepository{}, nil, nil, nil)
	_, err := svc.GetUser(context.Background(), 0)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestGetUser_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return((*domain.User)(nil), domain.ErrUserNotFound)
	svc := newSvc(repo, nil, nil, nil)
	_, err := svc.GetUser(context.Background(), 42)
	assert.ErrorIs(t, err, domain.ErrUserNotFound)
}

func TestGetUser_OK(t *testing.T) {
	t.Parallel()
	u := validUser(42, "u@x.com")
	repo := &mocks.MockUserRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(u, nil)
	svc := newSvc(repo, nil, nil, nil)
	out, err := svc.GetUser(context.Background(), 42)
	assert.NoError(t, err)
	assert.Equal(t, u, out)
}

// ─── DeleteUser ──────────────────────────────────────────────────────────────

func TestDeleteUser_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockUserRepository{}, nil, nil, nil)
	err := svc.DeleteUser(context.Background(), 0)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestDeleteUser_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("SoftDelete", mock.Anything, uint64(99)).Return(domain.ErrUserNotFound)
	svc := newSvc(repo, nil, nil, nil)
	err := svc.DeleteUser(context.Background(), 99)
	assert.ErrorIs(t, err, domain.ErrUserNotFound)
}

func TestDeleteUser_OK(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("SoftDelete", mock.Anything, uint64(5)).Return(nil)
	svc := newSvc(repo, nil, nil, nil)
	assert.NoError(t, svc.DeleteUser(context.Background(), 5))
}

// ─── AuthenticateUser ────────────────────────────────────────────────────────

func TestAuthenticateUser_MissingEmail(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockUserRepository{}, nil, nil, nil)
	_, err := svc.AuthenticateUser(context.Background(), "", "pass")
	assert.ErrorIs(t, err, domain.ErrMissingEmail)
}

func TestAuthenticateUser_MissingPassword(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockUserRepository{}, nil, nil, nil)
	_, err := svc.AuthenticateUser(context.Background(), "u@x.com", "")
	assert.ErrorIs(t, err, domain.ErrMissingPassword)
}

func TestAuthenticateUser_UserNotFound_ReturnsInvalidCredentials(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("GetCredentialsByEmail", mock.Anything, "u@x.com").Return((*domain.User)(nil), "", domain.ErrUserNotFound)
	svc := newSvc(repo, nil, nil, nil)
	_, err := svc.AuthenticateUser(context.Background(), "u@x.com", "pass")
	assert.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestAuthenticateUser_WrongPassword(t *testing.T) {
	t.Parallel()
	u := validUser(1, "u@x.com")
	u.State = identityv1.UserState_USER_STATE_ACTIVE
	repo := &mocks.MockUserRepository{}
	repo.On("GetCredentialsByEmail", mock.Anything, "u@x.com").Return(u, "hash", nil)
	hasher := &mocks.MockPasswordHasher{}
	hasher.On("Verify", "hash", "wrong").Return(errors.New("mismatch"))
	svc := newSvc(repo, hasher, nil, nil)
	_, err := svc.AuthenticateUser(context.Background(), "u@x.com", "wrong")
	assert.ErrorIs(t, err, domain.ErrInvalidCredentials)
}

func TestAuthenticateUser_OK(t *testing.T) {
	t.Parallel()
	u := validUser(1, "u@x.com")
	u.State = identityv1.UserState_USER_STATE_ACTIVE
	tokens := &tokenv1.AuthorizationTokens{}
	repo := &mocks.MockUserRepository{}
	repo.On("GetCredentialsByEmail", mock.Anything, "u@x.com").Return(u, "hash", nil)
	hasher := &mocks.MockPasswordHasher{}
	hasher.On("Verify", "hash", "pass").Return(nil)
	tokMgr := &mocks.MockTokenManager{}
	tokMgr.On("NewTokens", mock.Anything, uint64(1), false, mock.AnythingOfType("string")).Return(tokens, nil)
	sessStore := &mocks.MockSessionStore{}
	sessStore.On("Save", mock.Anything, mock.AnythingOfType("string"), uint64(1), false).Return(nil)
	svc := newSvc(repo, hasher, tokMgr, sessStore)
	out, err := svc.AuthenticateUser(context.Background(), "u@x.com", "pass")
	assert.NoError(t, err)
	assert.Equal(t, tokens, out)
}

// ─── RefreshToken ────────────────────────────────────────────────────────────

func TestRefreshToken_EmptyToken(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockUserRepository{}, nil, &mocks.MockTokenManager{}, nil)
	_, err := svc.RefreshToken(context.Background(), "")
	assert.ErrorIs(t, err, domain.ErrInvalidToken)
}

func TestRefreshToken_InvalidToken(t *testing.T) {
	t.Parallel()
	tokMgr := &mocks.MockTokenManager{}
	tokMgr.On("ExtractSessionID", "bad").Return("", errors.New("invalid"))
	svc := newSvc(&mocks.MockUserRepository{}, nil, tokMgr, nil)
	_, err := svc.RefreshToken(context.Background(), "bad")
	assert.ErrorIs(t, err, domain.ErrInvalidToken)
}

func TestRefreshToken_InvalidSession(t *testing.T) {
	t.Parallel()
	tokMgr := &mocks.MockTokenManager{}
	tokMgr.On("ExtractSessionID", "tok").Return("sess1", nil)
	sessStore := &mocks.MockSessionStore{}
	sessStore.On("Get", mock.Anything, "sess1").Return(uint64(0), false, false, errors.New("not found"))
	svc := newSvc(&mocks.MockUserRepository{}, nil, tokMgr, sessStore)
	_, err := svc.RefreshToken(context.Background(), "tok")
	assert.ErrorIs(t, err, domain.ErrInvalidSession)
}

func TestRefreshToken_OK(t *testing.T) {
	t.Parallel()
	u := validUser(1, "u@x.com")
	newTokens := &tokenv1.AuthorizationTokens{}
	repo := &mocks.MockUserRepository{}
	repo.On("GetByID", mock.Anything, uint64(1), false).Return(u, nil)
	tokMgr := &mocks.MockTokenManager{}
	tokMgr.On("ExtractSessionID", "old-ref").Return("sess1", nil)
	tokMgr.On("Revoke", mock.Anything, "old-ref").Return(nil)
	tokMgr.On("NewTokens", mock.Anything, uint64(1), false, "sess1").Return(newTokens, nil)
	sessStore := &mocks.MockSessionStore{}
	sessStore.On("Get", mock.Anything, "sess1").Return(uint64(1), false, true, nil)
	svc := newSvc(repo, nil, tokMgr, sessStore)
	out, err := svc.RefreshToken(context.Background(), "old-ref")
	assert.NoError(t, err)
	assert.Equal(t, newTokens, out)
}

// ─── Logout ──────────────────────────────────────────────────────────────────

func TestLogout_OK(t *testing.T) {
	t.Parallel()
	tokMgr := &mocks.MockTokenManager{}
	tokMgr.On("Revoke", mock.Anything, "acc").Return(nil)
	sessStore := &mocks.MockSessionStore{}
	sessStore.On("Delete", mock.Anything, "sess1").Return(nil)
	svc := newSvc(&mocks.MockUserRepository{}, nil, tokMgr, sessStore)
	assert.NoError(t, svc.Logout(context.Background(), "sess1", "acc"))
}
