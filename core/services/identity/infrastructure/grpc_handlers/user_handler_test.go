package grpc_handlers_test

import (
	"context"
	"errors"
	"testing"

	"milton_prism/core/services/identity/application"
	"milton_prism/core/services/identity/domain"
	"milton_prism/core/services/identity/infrastructure/grpc_handlers"
	"milton_prism/core/services/identity/mocks"
	"milton_prism/core/services/identity/ports"
	identitysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/identity/v1"
	identityv1 "milton_prism/pkg/pb/gen/milton_prism/types/identity/v1"
	tokenv1 "milton_prism/pkg/pb/gen/milton_prism/types/token/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ── Auth extractors used in tests ─────────────────────────────────────────────

// authOK is an AuthExtractor that always succeeds as a system user.
func authOK(_ context.Context) (uint64, bool, error) { return 1, true, nil }

// authFail is an AuthExtractor that always rejects the request.
func authFail(_ context.Context) (uint64, bool, error) { return 0, false, errors.New("no token") }

// authUser returns an AuthExtractor that reports the caller as a regular (non-system) user.
func authUser(id uint64) grpc_handlers.AuthExtractor {
	return func(_ context.Context) (uint64, bool, error) { return id, false, nil }
}

// sessionOK is a SessionExtractor that always succeeds.
func sessionOK(_ context.Context) (uint64, string, string, error) { return 1, "sid1", "acc-tok", nil }

// sessionFail is a SessionExtractor that always fails.
func sessionFail(_ context.Context) (uint64, string, string, error) {
	return 0, "", "", errors.New("no session token")
}

// ── Handler constructors ───────────────────────────────────────────────────────

// newHandler builds a handler wiring the given port mocks. Accept interface
// types to avoid the typed-nil-as-interface footgun when passing nil.
func newHandler(repo ports.UserRepository, tokens ports.TokenManager, sessions ports.SessionStore) *grpc_handlers.IdentityHandler {
	svc := application.NewService(repo, nil, nil, tokens, sessions)
	return grpc_handlers.NewIdentityHandler(svc, authOK, sessionOK)
}

func newHandlerWithAuth(auth grpc_handlers.AuthExtractor, repo ports.UserRepository) *grpc_handlers.IdentityHandler {
	svc := application.NewService(repo, nil, nil, nil, nil)
	return grpc_handlers.NewIdentityHandler(svc, auth, sessionOK)
}

func newHandlerWithSession(sess grpc_handlers.SessionExtractor, tokens ports.TokenManager, sessions ports.SessionStore) *grpc_handlers.IdentityHandler {
	svc := application.NewService(&mocks.MockUserRepository{}, nil, nil, tokens, sessions)
	return grpc_handlers.NewIdentityHandler(svc, authOK, sess)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func validUser(id uint64, email string) *identityv1.User {
	return &identityv1.User{Identifier: id, Email: email}
}

// ── CreateUser (public — no auth required) ────────────────────────────────────

func TestHandler_CreateUser_NilUser(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockUserRepository{}, nil, nil)
	_, err := h.CreateUser(context.Background(), &identitysvcv1.CreateUserRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_CreateUser_EmptyPassword(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockUserRepository{}, nil, nil)
	_, err := h.CreateUser(context.Background(), &identitysvcv1.CreateUserRequest{
		User: validUser(0, "u@x.com"),
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_CreateUser_DuplicateEmail(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("Create", mock.Anything, mock.Anything, mock.Anything).Return((*domain.User)(nil), domain.ErrEmailAlreadyExists)
	h := newHandler(repo, nil, nil)
	_, err := h.CreateUser(context.Background(), &identitysvcv1.CreateUserRequest{
		User:     validUser(0, "dup@x.com"),
		Password: "pass",
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestHandler_CreateUser_OK(t *testing.T) {
	t.Parallel()
	u := validUser(1, "u@x.com")
	repo := &mocks.MockUserRepository{}
	repo.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(u, nil)
	hasher := &mocks.MockPasswordHasher{}
	hasher.On("Hash", "secret").Return("hashed", nil)
	svc := application.NewService(repo, nil, hasher, nil, nil)
	h := grpc_handlers.NewIdentityHandler(svc, authOK, sessionOK)
	resp, err := h.CreateUser(context.Background(), &identitysvcv1.CreateUserRequest{
		User:     validUser(0, "u@x.com"),
		Password: "secret",
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), resp.GetIdentifier())
}

// ── GetUser (auth required) ───────────────────────────────────────────────────

func TestHandler_GetUser_AuthFail(t *testing.T) {
	t.Parallel()
	svc := application.NewService(&mocks.MockUserRepository{}, nil, nil, nil, nil)
	h := grpc_handlers.NewIdentityHandler(svc, authFail, sessionOK)
	_, err := h.GetUser(context.Background(), &identitysvcv1.GetUserRequest{Identifier: 1})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_GetUser_ZeroIdentifier(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockUserRepository{}, nil, nil)
	_, err := h.GetUser(context.Background(), &identitysvcv1.GetUserRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_GetUser_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("GetByID", mock.Anything, uint64(99), false).Return((*domain.User)(nil), domain.ErrUserNotFound)
	h := newHandler(repo, nil, nil)
	_, err := h.GetUser(context.Background(), &identitysvcv1.GetUserRequest{Identifier: 99})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_GetUser_OK(t *testing.T) {
	t.Parallel()
	u := validUser(7, "u@x.com")
	repo := &mocks.MockUserRepository{}
	repo.On("GetByID", mock.Anything, uint64(7), false).Return(u, nil)
	h := newHandler(repo, nil, nil)
	resp, err := h.GetUser(context.Background(), &identitysvcv1.GetUserRequest{Identifier: 7})
	require.NoError(t, err)
	assert.Equal(t, uint64(7), resp.GetIdentifier())
}

// ── GetUser ownership ─────────────────────────────────────────────────────────

func TestHandler_GetUser_NonSystemOwnRecord(t *testing.T) {
	t.Parallel()
	u := validUser(5, "u@x.com")
	repo := &mocks.MockUserRepository{}
	repo.On("GetByID", mock.Anything, uint64(5), false).Return(u, nil)
	h := newHandlerWithAuth(authUser(5), repo)
	resp, err := h.GetUser(context.Background(), &identitysvcv1.GetUserRequest{Identifier: 5})
	require.NoError(t, err)
	assert.Equal(t, uint64(5), resp.GetIdentifier())
}

func TestHandler_GetUser_NonSystemOtherRecord(t *testing.T) {
	t.Parallel()
	h := newHandlerWithAuth(authUser(1), &mocks.MockUserRepository{})
	_, err := h.GetUser(context.Background(), &identitysvcv1.GetUserRequest{Identifier: 5})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// ── ListUsers (auth + system-user required) ───────────────────────────────────

func TestHandler_ListUsers_AuthFail(t *testing.T) {
	t.Parallel()
	svc := application.NewService(&mocks.MockUserRepository{}, nil, nil, nil, nil)
	h := grpc_handlers.NewIdentityHandler(svc, authFail, sessionOK)
	_, err := h.ListUsers(context.Background(), &identitysvcv1.ListUsersRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_ListUsers_NonSystemDenied(t *testing.T) {
	t.Parallel()
	h := newHandlerWithAuth(authUser(1), &mocks.MockUserRepository{})
	_, err := h.ListUsers(context.Background(), &identitysvcv1.ListUsersRequest{})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestHandler_ListUsers_OK(t *testing.T) {
	t.Parallel()
	users := []*domain.User{validUser(1, "a@x.com"), validUser(2, "b@x.com")}
	repo := &mocks.MockUserRepository{}
	repo.On("List", mock.Anything, mock.Anything, mock.Anything).Return(users, nil, nil)
	h := newHandler(repo, nil, nil)
	resp, err := h.ListUsers(context.Background(), &identitysvcv1.ListUsersRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.GetUsers(), 2)
}

// ── UpdateUser (auth required) ────────────────────────────────────────────────

func TestHandler_UpdateUser_AuthFail(t *testing.T) {
	t.Parallel()
	svc := application.NewService(&mocks.MockUserRepository{}, nil, nil, nil, nil)
	h := grpc_handlers.NewIdentityHandler(svc, authFail, sessionOK)
	_, err := h.UpdateUser(context.Background(), &identitysvcv1.UpdateUserRequest{User: validUser(1, "u@x.com")})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_UpdateUser_NilUser(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockUserRepository{}, nil, nil)
	_, err := h.UpdateUser(context.Background(), &identitysvcv1.UpdateUserRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_UpdateUser_NonSystemOwnRecord(t *testing.T) {
	t.Parallel()
	u := validUser(3, "u@x.com")
	repo := &mocks.MockUserRepository{}
	repo.On("GetByID", mock.Anything, uint64(3), false).Return(u, nil)
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	h := newHandlerWithAuth(authUser(3), repo)
	_, err := h.UpdateUser(context.Background(), &identitysvcv1.UpdateUserRequest{User: validUser(3, "u@x.com")})
	assert.NoError(t, err)
}

func TestHandler_UpdateUser_NonSystemOtherRecord(t *testing.T) {
	t.Parallel()
	h := newHandlerWithAuth(authUser(1), &mocks.MockUserRepository{})
	_, err := h.UpdateUser(context.Background(), &identitysvcv1.UpdateUserRequest{User: validUser(5, "u@x.com")})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// ── DeleteUser (auth required) ────────────────────────────────────────────────

func TestHandler_DeleteUser_AuthFail(t *testing.T) {
	t.Parallel()
	svc := application.NewService(&mocks.MockUserRepository{}, nil, nil, nil, nil)
	h := grpc_handlers.NewIdentityHandler(svc, authFail, sessionOK)
	_, err := h.DeleteUser(context.Background(), &identitysvcv1.DeleteUserRequest{Identifier: 1})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_DeleteUser_NonSystemOwnRecord(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("SoftDelete", mock.Anything, uint64(4)).Return(nil)
	h := newHandlerWithAuth(authUser(4), repo)
	_, err := h.DeleteUser(context.Background(), &identitysvcv1.DeleteUserRequest{Identifier: 4})
	assert.NoError(t, err)
}

func TestHandler_DeleteUser_NonSystemOtherRecord(t *testing.T) {
	t.Parallel()
	h := newHandlerWithAuth(authUser(1), &mocks.MockUserRepository{})
	_, err := h.DeleteUser(context.Background(), &identitysvcv1.DeleteUserRequest{Identifier: 9})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestHandler_DeleteUser_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("SoftDelete", mock.Anything, uint64(1)).Return(domain.ErrUserNotFound)
	h := newHandler(repo, nil, nil)
	_, err := h.DeleteUser(context.Background(), &identitysvcv1.DeleteUserRequest{Identifier: 1})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// ── AuthenticateUser (public — no auth required) ──────────────────────────────

func TestHandler_AuthenticateUser_EmptyEmail(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockUserRepository{}, nil, nil)
	_, err := h.AuthenticateUser(context.Background(), &identitysvcv1.AuthenticateUserRequest{Password: "p"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_AuthenticateUser_EmptyPassword(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockUserRepository{}, nil, nil)
	_, err := h.AuthenticateUser(context.Background(), &identitysvcv1.AuthenticateUserRequest{Email: "u@x.com"})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_AuthenticateUser_InvalidCredentials(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("GetCredentialsByEmail", mock.Anything, "u@x.com").Return((*domain.User)(nil), "", domain.ErrUserNotFound)
	h := newHandler(repo, nil, nil)
	_, err := h.AuthenticateUser(context.Background(), &identitysvcv1.AuthenticateUserRequest{Email: "u@x.com", Password: "bad"})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_AuthenticateUser_OK(t *testing.T) {
	t.Parallel()
	u := validUser(1, "u@x.com")
	u.State = identityv1.UserState_USER_STATE_ACTIVE
	authTokens := &tokenv1.AuthorizationTokens{}
	repo := &mocks.MockUserRepository{}
	repo.On("GetCredentialsByEmail", mock.Anything, "u@x.com").Return(u, "hash", nil)
	hasher := &mocks.MockPasswordHasher{}
	hasher.On("Verify", "hash", "pass").Return(nil)
	tokMgr := &mocks.MockTokenManager{}
	tokMgr.On("NewTokens", mock.Anything, uint64(1), false, mock.AnythingOfType("string")).Return(authTokens, nil)
	sessStore := &mocks.MockSessionStore{}
	sessStore.On("Save", mock.Anything, mock.AnythingOfType("string"), uint64(1), false).Return(nil)
	svc := application.NewService(repo, nil, hasher, tokMgr, sessStore)
	h := grpc_handlers.NewIdentityHandler(svc, authOK, sessionOK)
	resp, err := h.AuthenticateUser(context.Background(), &identitysvcv1.AuthenticateUserRequest{Email: "u@x.com", Password: "pass"})
	require.NoError(t, err)
	assert.Equal(t, authTokens, resp)
}

// ── RefreshToken (public — no auth required) ──────────────────────────────────

func TestHandler_RefreshToken_EmptyToken(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockUserRepository{}, nil, nil)
	_, err := h.RefreshToken(context.Background(), &identitysvcv1.RefreshTokenRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_RefreshToken_InvalidToken(t *testing.T) {
	t.Parallel()
	tokMgr := &mocks.MockTokenManager{}
	tokMgr.On("ExtractSessionID", "bad").Return("", errors.New("invalid"))
	h := newHandler(&mocks.MockUserRepository{}, tokMgr, nil)
	_, err := h.RefreshToken(context.Background(), &identitysvcv1.RefreshTokenRequest{RefreshToken: "bad"})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

// ── Logout (session auth required) ───────────────────────────────────────────

func TestHandler_Logout_SessionFail(t *testing.T) {
	t.Parallel()
	h := newHandlerWithSession(sessionFail, nil, nil)
	_, err := h.Logout(context.Background(), &identitysvcv1.LogoutRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_Logout_OK(t *testing.T) {
	t.Parallel()
	tokMgr := &mocks.MockTokenManager{}
	tokMgr.On("Revoke", mock.Anything, "acc-tok").Return(nil)
	sessStore := &mocks.MockSessionStore{}
	sessStore.On("Delete", mock.Anything, "sid1").Return(nil)
	h := newHandlerWithSession(sessionOK, tokMgr, sessStore)
	_, err := h.Logout(context.Background(), &identitysvcv1.LogoutRequest{})
	assert.NoError(t, err)
}

// ── GetCurrentUser (auth required) ────────────────────────────────────────────

func TestHandler_GetCurrentUser_AuthFail(t *testing.T) {
	t.Parallel()
	svc := application.NewService(&mocks.MockUserRepository{}, nil, nil, nil, nil)
	h := grpc_handlers.NewIdentityHandler(svc, authFail, sessionOK)
	_, err := h.GetCurrentUser(context.Background(), &identitysvcv1.GetCurrentUserRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_GetCurrentUser_OK(t *testing.T) {
	t.Parallel()
	u := validUser(1, "u@x.com")
	repo := &mocks.MockUserRepository{}
	repo.On("GetByID", mock.Anything, uint64(1), false).Return(u, nil)
	h := newHandler(repo, nil, nil)
	resp, err := h.GetCurrentUser(context.Background(), &identitysvcv1.GetCurrentUserRequest{})
	require.NoError(t, err)
	assert.Equal(t, uint64(1), resp.GetIdentifier())
}

// ── mapError: domain error → gRPC status code ────────────────────────────────

func TestHandler_mapError_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("GetByID", mock.Anything, uint64(1), false).Return((*domain.User)(nil), domain.ErrUserNotFound)
	h := newHandler(repo, nil, nil)
	_, err := h.GetUser(context.Background(), &identitysvcv1.GetUserRequest{Identifier: 1})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_mapError_AlreadyExists(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("Create", mock.Anything, mock.Anything, mock.Anything).Return((*domain.User)(nil), domain.ErrEmailAlreadyExists)
	h := newHandler(repo, nil, nil)
	_, err := h.CreateUser(context.Background(), &identitysvcv1.CreateUserRequest{
		User:     validUser(0, "dup@x.com"),
		Password: "pass",
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestHandler_mapError_PermissionDenied_Suspended(t *testing.T) {
	t.Parallel()
	u := validUser(1, "u@x.com")
	u.State = identityv1.UserState_USER_STATE_SUSPENDED
	repo := &mocks.MockUserRepository{}
	repo.On("GetCredentialsByEmail", mock.Anything, "u@x.com").Return(u, "hash", nil)
	hasher := &mocks.MockPasswordHasher{}
	hasher.On("Verify", "hash", "pass").Return(nil)
	svc := application.NewService(repo, nil, hasher, nil, nil)
	h := grpc_handlers.NewIdentityHandler(svc, authOK, sessionOK)
	_, err := h.AuthenticateUser(context.Background(), &identitysvcv1.AuthenticateUserRequest{Email: "u@x.com", Password: "pass"})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestHandler_mapError_Unauthenticated_InvalidCredentials(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("GetCredentialsByEmail", mock.Anything, "u@x.com").Return((*domain.User)(nil), "", domain.ErrUserNotFound)
	h := newHandler(repo, nil, nil)
	_, err := h.AuthenticateUser(context.Background(), &identitysvcv1.AuthenticateUserRequest{Email: "u@x.com", Password: "bad"})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_mapError_Internal_RepoError(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("SoftDelete", mock.Anything, uint64(1)).Return(errors.New("db down"))
	h := newHandler(repo, nil, nil)
	_, err := h.DeleteUser(context.Background(), &identitysvcv1.DeleteUserRequest{Identifier: 1})
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestHandler_mapError_Internal_DomainError(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockUserRepository{}
	repo.On("SoftDelete", mock.Anything, uint64(1)).Return(domain.ErrInternal)
	h := newHandler(repo, nil, nil)
	_, err := h.DeleteUser(context.Background(), &identitysvcv1.DeleteUserRequest{Identifier: 1})
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestHandler_mapError_Internal_TokenGeneration(t *testing.T) {
	t.Parallel()
	u := validUser(1, "u@x.com")
	u.State = identityv1.UserState_USER_STATE_ACTIVE
	repo := &mocks.MockUserRepository{}
	repo.On("GetCredentialsByEmail", mock.Anything, "u@x.com").Return(u, "hash", nil)
	hasher := &mocks.MockPasswordHasher{}
	hasher.On("Verify", "hash", "pass").Return(nil)
	// nil token manager → ErrTokenGeneration → Internal
	svc := application.NewService(repo, nil, hasher, nil, nil)
	h := grpc_handlers.NewIdentityHandler(svc, authOK, sessionOK)
	_, err := h.AuthenticateUser(context.Background(), &identitysvcv1.AuthenticateUserRequest{Email: "u@x.com", Password: "pass"})
	assert.Equal(t, codes.Internal, status.Code(err))
}
