package grpc_handlers_test

import (
	"context"
	"errors"
	"testing"

	"milton_prism/core/services/repository/application"
	"milton_prism/core/services/repository/domain"
	"milton_prism/core/services/repository/infrastructure/grpc_handlers"
	"milton_prism/core/services/repository/mocks"
	"milton_prism/core/services/repository/ports"
	repositorysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/repository/v1"
	repositoryv1 "milton_prism/pkg/pb/gen/milton_prism/types/repository/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ─── Auth fakes ──────────────────────────────────────────────────────────────

func authOK(_ context.Context) (uint64, bool, error)   { return 1, false, nil }
func authSys(_ context.Context) (uint64, bool, error)  { return 1, true, nil }
func authFail(_ context.Context) (uint64, bool, error) { return 0, false, errors.New("no token") }
func authUser(id uint64) grpc_handlers.AuthExtractor {
	return func(_ context.Context) (uint64, bool, error) { return id, false, nil }
}

// ─── Constructor helpers ──────────────────────────────────────────────────────

func newHandler(
	repo ports.RepositoryRepository,
	identity ports.IdentityClient,
	git ports.GitClient,
	auth grpc_handlers.AuthExtractor,
) *grpc_handlers.RepositoryHandler {
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	svc := application.NewService(repo, tx, identity, git)
	return grpc_handlers.NewRepositoryHandler(svc, auth)
}

func validRepoProto(id, ownerID uint64) *repositoryv1.Repository {
	return &repositoryv1.Repository{
		Identifier:  id,
		OwnerUserId: ownerID,
		Provider:    repositoryv1.GitProvider_GIT_PROVIDER_GITHUB,
		RemoteUrl:   "https://github.com/org/repo",
	}
}

// ─── CreateRepository ────────────────────────────────────────────────────────

func TestHandler_CreateRepository_AuthFail(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authFail)
	_, err := h.CreateRepository(context.Background(), &repositorysvcv1.CreateRepositoryRequest{
		Repository: validRepoProto(0, 1),
	})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_CreateRepository_NilRepository(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authOK)
	_, err := h.CreateRepository(context.Background(), &repositorysvcv1.CreateRepositoryRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_CreateRepository_OK(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("Create", mock.Anything, mock.Anything).Return(validRepoProto(42, 1), nil)
	h := newHandler(repo, nil, nil, authOK)
	out, err := h.CreateRepository(context.Background(), &repositorysvcv1.CreateRepositoryRequest{
		Repository: validRepoProto(0, 1),
	})
	assert.NoError(t, err)
	assert.Equal(t, uint64(42), out.GetIdentifier())
	assert.Empty(t, out.GetCredentialRef())
}

// ─── GetRepository ───────────────────────────────────────────────────────────

func TestHandler_GetRepository_AuthFail(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authFail)
	_, err := h.GetRepository(context.Background(), &repositorysvcv1.GetRepositoryRequest{Identifier: 1})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_GetRepository_ZeroIdentifier(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authOK)
	_, err := h.GetRepository(context.Background(), &repositorysvcv1.GetRepositoryRequest{Identifier: 0})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_GetRepository_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(99), false).Return((*domain.Repository)(nil), domain.ErrRepositoryNotFound)
	h := newHandler(repo, nil, nil, authOK)
	_, err := h.GetRepository(context.Background(), &repositorysvcv1.GetRepositoryRequest{Identifier: 99})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_GetRepository_PermissionDenied_WrongOwner(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 99)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	h := newHandler(repo, nil, nil, authUser(1))
	_, err := h.GetRepository(context.Background(), &repositorysvcv1.GetRepositoryRequest{Identifier: 42})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestHandler_GetRepository_OK_Owner(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 1)
	r.CredentialRef = "vault:secret"
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	h := newHandler(repo, nil, nil, authUser(1))
	out, err := h.GetRepository(context.Background(), &repositorysvcv1.GetRepositoryRequest{Identifier: 42})
	assert.NoError(t, err)
	assert.Equal(t, uint64(42), out.GetIdentifier())
	assert.Empty(t, out.GetCredentialRef())
}

func TestHandler_GetRepository_OK_SystemUser(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 99)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	h := newHandler(repo, nil, nil, authSys)
	out, err := h.GetRepository(context.Background(), &repositorysvcv1.GetRepositoryRequest{Identifier: 42})
	assert.NoError(t, err)
	assert.Equal(t, uint64(42), out.GetIdentifier())
}

// ─── ListRepositories ────────────────────────────────────────────────────────

func TestHandler_ListRepositories_AuthFail(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authFail)
	_, err := h.ListRepositories(context.Background(), &repositorysvcv1.ListRepositoriesRequest{})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_ListRepositories_ForcesOwnerFilter(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("List", mock.Anything, mock.MatchedBy(func(f *domain.RepositoriesFilter) bool {
		return f.GetOwnerUserId() == 1
	}), mock.Anything).Return([]*domain.Repository{}, nil, nil)
	h := newHandler(repo, nil, nil, authUser(1))
	_, err := h.ListRepositories(context.Background(), &repositorysvcv1.ListRepositoriesRequest{})
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestHandler_ListRepositories_SystemUser_NoOwnerFilter(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("List", mock.Anything, mock.Anything, mock.Anything).Return([]*domain.Repository{}, nil, nil)
	h := newHandler(repo, nil, nil, authSys)
	_, err := h.ListRepositories(context.Background(), &repositorysvcv1.ListRepositoriesRequest{})
	assert.NoError(t, err)
}

// ─── UpdateRepository ────────────────────────────────────────────────────────

func TestHandler_UpdateRepository_AuthFail(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authFail)
	_, err := h.UpdateRepository(context.Background(), &repositorysvcv1.UpdateRepositoryRequest{
		Repository: validRepoProto(42, 1),
	})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_UpdateRepository_NilRepo(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authOK)
	_, err := h.UpdateRepository(context.Background(), &repositorysvcv1.UpdateRepositoryRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_UpdateRepository_PermissionDenied(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 99)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	h := newHandler(repo, nil, nil, authUser(1))
	_, err := h.UpdateRepository(context.Background(), &repositorysvcv1.UpdateRepositoryRequest{
		Repository: validRepoProto(42, 1),
	})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestHandler_UpdateRepository_OK(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 1)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil).Twice()
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	h := newHandler(repo, nil, nil, authUser(1))
	out, err := h.UpdateRepository(context.Background(), &repositorysvcv1.UpdateRepositoryRequest{
		Repository: validRepoProto(42, 1),
	})
	assert.NoError(t, err)
	assert.Equal(t, uint64(42), out.GetIdentifier())
}

// ─── DeleteRepository ────────────────────────────────────────────────────────

func TestHandler_DeleteRepository_AuthFail(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authFail)
	_, err := h.DeleteRepository(context.Background(), &repositorysvcv1.DeleteRepositoryRequest{Identifier: 42})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_DeleteRepository_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(99), false).Return((*domain.Repository)(nil), domain.ErrRepositoryNotFound)
	h := newHandler(repo, nil, nil, authOK)
	_, err := h.DeleteRepository(context.Background(), &repositorysvcv1.DeleteRepositoryRequest{Identifier: 99})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_DeleteRepository_PermissionDenied(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 99)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	h := newHandler(repo, nil, nil, authUser(1))
	_, err := h.DeleteRepository(context.Background(), &repositorysvcv1.DeleteRepositoryRequest{Identifier: 42})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestHandler_DeleteRepository_OK(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 1)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	repo.On("SoftDelete", mock.Anything, uint64(42)).Return(nil)
	h := newHandler(repo, nil, nil, authUser(1))
	_, err := h.DeleteRepository(context.Background(), &repositorysvcv1.DeleteRepositoryRequest{Identifier: 42})
	assert.NoError(t, err)
}

// ─── Security: credential_ref never leaks in API responses ──────────────────

func TestHandler_CreateRepository_StripsCredentialRef(t *testing.T) {
	t.Parallel()
	created := validRepoProto(42, 1)
	created.CredentialRef = "ghp_secret"
	repo := &mocks.MockRepositoryRepository{}
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	h := newHandler(repo, nil, nil, authOK)
	out, err := h.CreateRepository(context.Background(), &repositorysvcv1.CreateRepositoryRequest{
		Repository: validRepoProto(0, 1),
	})
	assert.NoError(t, err)
	assert.Empty(t, out.GetCredentialRef())
}

func TestHandler_ListRepositories_StripsCredentialRef(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 1)
	r.CredentialRef = "ghp_secret"
	repo := &mocks.MockRepositoryRepository{}
	repo.On("List", mock.Anything, mock.Anything, mock.Anything).Return([]*domain.Repository{r}, nil, nil)
	h := newHandler(repo, nil, nil, authSys)
	out, err := h.ListRepositories(context.Background(), &repositorysvcv1.ListRepositoriesRequest{})
	assert.NoError(t, err)
	require.Len(t, out.GetRepositories(), 1)
	assert.Empty(t, out.GetRepositories()[0].GetCredentialRef())
}

// UpdateRepository returns existing after applyRepositoryMask; if GetByID now
// returns the stored credential, the handler must still strip it from the response.
func TestHandler_UpdateRepository_StripsCredentialRef(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 1)
	r.CredentialRef = "stored-secret"
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil).Twice()
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	h := newHandler(repo, nil, nil, authUser(1))
	out, err := h.UpdateRepository(context.Background(), &repositorysvcv1.UpdateRepositoryRequest{
		Repository: validRepoProto(42, 1),
	})
	assert.NoError(t, err)
	assert.Empty(t, out.GetCredentialRef())
}

// ─── TestConnection ──────────────────────────────────────────────────────────

func TestHandler_TestConnection_AuthFail(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authFail)
	_, err := h.TestConnection(context.Background(), &repositorysvcv1.TestConnectionRequest{Identifier: 1})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_TestConnection_OK(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 1)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil).Times(2)
	// TestConnection now persists both state and connection_status via Update.
	repo.On("Update", mock.Anything, mock.MatchedBy(func(upd *domain.Repository) bool {
		return upd.GetState() == domain.RepositoryStateConnected &&
			upd.GetConnectionStatus() == domain.ConnectionStatusOK
	})).Return(nil)
	git := &mocks.MockGitClient{}
	git.On("TestConnection", mock.Anything, r.RemoteUrl, r.CredentialRef).Return(domain.ConnectionStatusOK, nil)
	h := newHandler(repo, nil, git, authUser(1))
	out, err := h.TestConnection(context.Background(), &repositorysvcv1.TestConnectionRequest{Identifier: 42})
	assert.NoError(t, err)
	assert.Equal(t, repositoryv1.ConnectionStatus_CONNECTION_STATUS_OK, out.GetStatus())
}

func TestHandler_TestConnection_UsesStoredCredential(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 1)
	r.CredentialRef = "ghp_stored_token"
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil).Times(2)
	repo.On("Update", mock.Anything, mock.MatchedBy(func(upd *domain.Repository) bool {
		return upd.GetConnectionStatus() == domain.ConnectionStatusOK
	})).Return(nil)
	git := &mocks.MockGitClient{}
	git.On("TestConnection", mock.Anything, r.RemoteUrl, "ghp_stored_token").Return(domain.ConnectionStatusOK, nil)
	h := newHandler(repo, nil, git, authUser(1))
	out, err := h.TestConnection(context.Background(), &repositorysvcv1.TestConnectionRequest{Identifier: 42})
	assert.NoError(t, err)
	assert.Equal(t, repositoryv1.ConnectionStatus_CONNECTION_STATUS_OK, out.GetStatus())
	git.AssertExpectations(t)
}

// ─── ListBranches ────────────────────────────────────────────────────────────

func TestHandler_ListBranches_AuthFail(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authFail)
	_, err := h.ListBranches(context.Background(), &repositorysvcv1.ListBranchesRequest{Identifier: 1})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_ListBranches_OK(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 1)
	branches := []*domain.Branch{{Name: "main", IsDefault: true}}
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil).Times(2)
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	git := &mocks.MockGitClient{}
	git.On("ListBranches", mock.Anything, r.RemoteUrl, r.CredentialRef).Return(branches, nil)
	h := newHandler(repo, nil, git, authUser(1))
	out, err := h.ListBranches(context.Background(), &repositorysvcv1.ListBranchesRequest{Identifier: 42})
	assert.NoError(t, err)
	assert.Len(t, out.GetBranches(), 1)
	assert.Equal(t, "main", out.GetBranches()[0].GetName())
}

func TestHandler_ListBranches_UsesStoredCredential(t *testing.T) {
	t.Parallel()
	r := validRepoProto(42, 1)
	r.CredentialRef = "ghp_stored_token"
	branches := []*domain.Branch{{Name: "main", IsDefault: true}}
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil).Times(2)
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	git := &mocks.MockGitClient{}
	git.On("ListBranches", mock.Anything, r.RemoteUrl, "ghp_stored_token").Return(branches, nil)
	h := newHandler(repo, nil, git, authUser(1))
	out, err := h.ListBranches(context.Background(), &repositorysvcv1.ListBranchesRequest{Identifier: 42})
	assert.NoError(t, err)
	assert.Len(t, out.GetBranches(), 1)
	git.AssertExpectations(t)
}

// ─── PushResult ──────────────────────────────────────────────────────────────

func TestHandler_PushResult_AuthFail(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockRepositoryRepository{}, nil, nil, authFail)
	_, err := h.PushResult(context.Background(), &repositorysvcv1.PushResultRequest{
		TargetUrl: "https://example.com/org/repo",
		Files:     []*repositorysvcv1.FileEntry{{Path: "a.go", Content: "package a\n"}},
	})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_PushResult_OK(t *testing.T) {
	t.Parallel()
	gitClient := &mocks.MockGitClient{}
	gitClient.On("PushResult", mock.Anything, "https://example.com/org/repo", "tok", mock.Anything, "chore: test").Return("main", nil)
	h := newHandler(nil, nil, gitClient, authUser(1))
	out, err := h.PushResult(context.Background(), &repositorysvcv1.PushResultRequest{
		TargetUrl:     "https://example.com/org/repo",
		WriteToken:    "tok",
		Files:         []*repositorysvcv1.FileEntry{{Path: "a.go", Content: "package a\n"}},
		CommitMessage: "chore: test",
	})
	assert.NoError(t, err)
	assert.Equal(t, "main", out.GetPushedBranch())
}

// ─── mapError ────────────────────────────────────────────────────────────────

func TestHandler_mapError_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(1), false).Return((*domain.Repository)(nil), domain.ErrRepositoryNotFound)
	h := newHandler(repo, nil, nil, authOK)
	_, err := h.GetRepository(context.Background(), &repositorysvcv1.GetRepositoryRequest{Identifier: 1})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_mapError_AlreadyExists(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("Create", mock.Anything, mock.Anything).Return((*domain.Repository)(nil), domain.ErrRepositoryAlreadyExists)
	h := newHandler(repo, nil, nil, authOK)
	_, err := h.CreateRepository(context.Background(), &repositorysvcv1.CreateRepositoryRequest{
		Repository: validRepoProto(0, 1),
	})
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestHandler_mapError_OwnerNotFound(t *testing.T) {
	t.Parallel()
	idClient := &mocks.MockIdentityClient{}
	idClient.On("ValidateUserExists", mock.Anything, uint64(1)).Return(domain.ErrOwnerNotFound)
	h := newHandler(&mocks.MockRepositoryRepository{}, idClient, nil, authOK)
	_, err := h.CreateRepository(context.Background(), &repositorysvcv1.CreateRepositoryRequest{
		Repository: validRepoProto(0, 1),
	})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_mapError_Internal(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("Create", mock.Anything, mock.Anything).Return((*domain.Repository)(nil), domain.ErrInternal)
	h := newHandler(repo, nil, nil, authOK)
	_, err := h.CreateRepository(context.Background(), &repositorysvcv1.CreateRepositoryRequest{
		Repository: validRepoProto(0, 1),
	})
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestHandler_mapError_UnknownError(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("Create", mock.Anything, mock.Anything).Return((*domain.Repository)(nil), errors.New("unexpected"))
	h := newHandler(repo, nil, nil, authOK)
	_, err := h.CreateRepository(context.Background(), &repositorysvcv1.CreateRepositoryRequest{
		Repository: validRepoProto(0, 1),
	})
	assert.Equal(t, codes.Internal, status.Code(err))
}
