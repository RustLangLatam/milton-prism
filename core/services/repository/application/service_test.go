package application_test

import (
	"context"
	"errors"
	"testing"

	"milton_prism/core/services/repository/application"
	"milton_prism/core/services/repository/domain"
	"milton_prism/core/services/repository/mocks"
	"milton_prism/core/services/repository/ports"
	repositoryv1 "milton_prism/pkg/pb/gen/milton_prism/types/repository/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func validRepo(id uint64, ownerID uint64) *domain.Repository {
	return &repositoryv1.Repository{
		Identifier:  id,
		OwnerUserId: ownerID,
		Provider:    repositoryv1.GitProvider_GIT_PROVIDER_GITHUB,
		RemoteUrl:   "https://github.com/org/repo",
	}
}

func newSvc(repo ports.RepositoryRepository, identity ports.IdentityClient, git ports.GitClient) *application.Service {
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	return application.NewService(repo, tx, identity, git)
}

// ─── CreateRepository ────────────────────────────────────────────────────────

func TestCreateRepository_MissingPayload_NilRepo(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, nil)
	_, err := svc.CreateRepository(context.Background(), nil)
	assert.ErrorIs(t, err, domain.ErrMissingPayload)
}

func TestCreateRepository_MissingPayload_EmptyURL(t *testing.T) {
	t.Parallel()
	r := validRepo(0, 1)
	r.RemoteUrl = ""
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, nil)
	_, err := svc.CreateRepository(context.Background(), r)
	assert.ErrorIs(t, err, domain.ErrMissingPayload)
}

func TestCreateRepository_MissingOwnerUserID(t *testing.T) {
	t.Parallel()
	r := validRepo(0, 0)
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, nil)
	_, err := svc.CreateRepository(context.Background(), r)
	assert.ErrorIs(t, err, domain.ErrMissingOwnerUserID)
}

func TestCreateRepository_OwnerNotFound(t *testing.T) {
	t.Parallel()
	idClient := &mocks.MockIdentityClient{}
	idClient.On("ValidateUserExists", mock.Anything, uint64(7)).Return(domain.ErrOwnerNotFound)
	svc := newSvc(&mocks.MockRepositoryRepository{}, idClient, nil)
	_, err := svc.CreateRepository(context.Background(), validRepo(0, 7))
	assert.ErrorIs(t, err, domain.ErrOwnerNotFound)
}

func TestCreateRepository_AlreadyExists(t *testing.T) {
	t.Parallel()
	idClient := &mocks.MockIdentityClient{}
	idClient.On("ValidateUserExists", mock.Anything, uint64(7)).Return(nil)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("Create", mock.Anything, mock.Anything).Return((*domain.Repository)(nil), domain.ErrRepositoryAlreadyExists)
	svc := newSvc(repo, idClient, nil)
	_, err := svc.CreateRepository(context.Background(), validRepo(0, 7))
	assert.ErrorIs(t, err, domain.ErrRepositoryAlreadyExists)
}

func TestCreateRepository_OK(t *testing.T) {
	t.Parallel()
	idClient := &mocks.MockIdentityClient{}
	idClient.On("ValidateUserExists", mock.Anything, uint64(7)).Return(nil)
	created := validRepo(42, 7)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	svc := newSvc(repo, idClient, nil)
	out, err := svc.CreateRepository(context.Background(), validRepo(0, 7))
	assert.NoError(t, err)
	assert.Equal(t, created, out)
}

// ─── GetRepository ───────────────────────────────────────────────────────────

func TestGetRepository_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, nil)
	_, err := svc.GetRepository(context.Background(), 0)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestGetRepository_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(99), false).Return((*domain.Repository)(nil), domain.ErrRepositoryNotFound)
	svc := newSvc(repo, nil, nil)
	_, err := svc.GetRepository(context.Background(), 99)
	assert.ErrorIs(t, err, domain.ErrRepositoryNotFound)
}

func TestGetRepository_OK(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	svc := newSvc(repo, nil, nil)
	out, err := svc.GetRepository(context.Background(), 42)
	assert.NoError(t, err)
	assert.Equal(t, r, out)
}

// ─── DeleteRepository ────────────────────────────────────────────────────────

func TestDeleteRepository_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, nil)
	err := svc.DeleteRepository(context.Background(), 0)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestDeleteRepository_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("SoftDelete", mock.Anything, uint64(99)).Return(domain.ErrRepositoryNotFound)
	svc := newSvc(repo, nil, nil)
	err := svc.DeleteRepository(context.Background(), 99)
	assert.ErrorIs(t, err, domain.ErrRepositoryNotFound)
}

func TestDeleteRepository_OK(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("SoftDelete", mock.Anything, uint64(42)).Return(nil)
	svc := newSvc(repo, nil, nil)
	assert.NoError(t, svc.DeleteRepository(context.Background(), 42))
}

// ─── UpdateRepository ────────────────────────────────────────────────────────

func TestUpdateRepository_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, nil)
	_, err := svc.UpdateRepository(context.Background(), validRepo(0, 7), nil)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestUpdateRepository_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return((*domain.Repository)(nil), domain.ErrRepositoryNotFound)
	svc := newSvc(repo, nil, nil)
	_, err := svc.UpdateRepository(context.Background(), validRepo(42, 7), nil)
	assert.ErrorIs(t, err, domain.ErrRepositoryNotFound)
}

func TestUpdateRepository_OK(t *testing.T) {
	t.Parallel()
	existing := validRepo(42, 7)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(existing, nil)
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	svc := newSvc(repo, nil, nil)
	out, err := svc.UpdateRepository(context.Background(), validRepo(42, 7), nil)
	assert.NoError(t, err)
	assert.Equal(t, existing, out)
}

// ─── ProbeSourceRepository ───────────────────────────────────────────────────

func TestProbeSourceRepository_MissingURL(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, &mocks.MockGitClient{})
	_, err := svc.ProbeSourceRepository(context.Background(), "", "")
	assert.ErrorIs(t, err, domain.ErrInvalidRemoteURL)
}

func TestProbeSourceRepository_PublicRepo(t *testing.T) {
	t.Parallel()
	git := &mocks.MockGitClient{}
	git.On("ProbeSource", mock.Anything, "https://example.com/public/repo", "").Return(
		&domain.SourceProbeResult{Reachable: true, Visibility: domain.RepositoryVisibilityPublic, AuthValid: true}, nil,
	)
	svc := newSvc(nil, nil, git)
	result, err := svc.ProbeSourceRepository(context.Background(), "https://example.com/public/repo", "")
	require.NoError(t, err)
	assert.True(t, result.Reachable)
	assert.Equal(t, domain.RepositoryVisibilityPublic, result.Visibility)
	assert.True(t, result.AuthValid)
}

func TestProbeSourceRepository_PrivateWithValidToken(t *testing.T) {
	t.Parallel()
	git := &mocks.MockGitClient{}
	git.On("ProbeSource", mock.Anything, "https://example.com/private/repo", "mytoken").Return(
		&domain.SourceProbeResult{Reachable: true, Visibility: domain.RepositoryVisibilityPrivate, AuthValid: true}, nil,
	)
	svc := newSvc(nil, nil, git)
	result, err := svc.ProbeSourceRepository(context.Background(), "https://example.com/private/repo", "mytoken")
	require.NoError(t, err)
	assert.True(t, result.Reachable)
	assert.Equal(t, domain.RepositoryVisibilityPrivate, result.Visibility)
	assert.True(t, result.AuthValid)
}

func TestProbeSourceRepository_Unreachable(t *testing.T) {
	t.Parallel()
	git := &mocks.MockGitClient{}
	git.On("ProbeSource", mock.Anything, "https://bad.host/repo", "").Return(
		&domain.SourceProbeResult{Reachable: false, ErrorMessage: "Could not resolve the host."}, nil,
	)
	svc := newSvc(nil, nil, git)
	result, err := svc.ProbeSourceRepository(context.Background(), "https://bad.host/repo", "")
	require.NoError(t, err)
	assert.False(t, result.Reachable)
	assert.NotEmpty(t, result.ErrorMessage)
}

// ─── TestConnection ──────────────────────────────────────────────────────────

func TestTestConnection_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, &mocks.MockGitClient{})
	_, err := svc.TestConnection(context.Background(), 0)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestTestConnection_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return((*domain.Repository)(nil), domain.ErrRepositoryNotFound)
	svc := newSvc(repo, nil, &mocks.MockGitClient{})
	_, err := svc.TestConnection(context.Background(), 42)
	assert.ErrorIs(t, err, domain.ErrRepositoryNotFound)
}

func TestTestConnection_OK(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	// Update persists both state and connection_status.
	repo.On("Update", mock.Anything, mock.MatchedBy(func(upd *domain.Repository) bool {
		return upd.GetState() == domain.RepositoryStateConnected &&
			upd.GetConnectionStatus() == domain.ConnectionStatusOK
	})).Return(nil)
	gitClient := &mocks.MockGitClient{}
	gitClient.On("TestConnection", mock.Anything, r.RemoteUrl, r.CredentialRef).Return(domain.ConnectionStatusOK, nil)
	svc := newSvc(repo, nil, gitClient)
	status, err := svc.TestConnection(context.Background(), 42)
	assert.NoError(t, err)
	assert.Equal(t, domain.ConnectionStatusOK, status)
}

func TestTestConnection_GitFails_ReturnsUnreachable(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	repo.On("Update", mock.Anything, mock.MatchedBy(func(upd *domain.Repository) bool {
		return upd.GetState() == domain.RepositoryStateDisconnected &&
			upd.GetConnectionStatus() == domain.ConnectionStatusUnreachable
	})).Return(nil)
	gitClient := &mocks.MockGitClient{}
	gitClient.On("TestConnection", mock.Anything, r.RemoteUrl, r.CredentialRef).Return(domain.ConnectionStatusUnreachable, errors.New("timeout"))
	svc := newSvc(repo, nil, gitClient)
	status, err := svc.TestConnection(context.Background(), 42)
	assert.NoError(t, err)
	assert.Equal(t, domain.ConnectionStatusUnreachable, status)
}

func TestTestConnection_AuthFailed_PersistsError(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	repo.On("Update", mock.Anything, mock.MatchedBy(func(upd *domain.Repository) bool {
		return upd.GetState() == domain.RepositoryStateError &&
			upd.GetConnectionStatus() == domain.ConnectionStatusAuthFailed
	})).Return(nil)
	gitClient := &mocks.MockGitClient{}
	gitClient.On("TestConnection", mock.Anything, r.RemoteUrl, r.CredentialRef).Return(domain.ConnectionStatusAuthFailed, nil)
	svc := newSvc(repo, nil, gitClient)
	status, err := svc.TestConnection(context.Background(), 42)
	assert.NoError(t, err)
	assert.Equal(t, domain.ConnectionStatusAuthFailed, status)
}

// ─── ListBranches ────────────────────────────────────────────────────────────

func TestListBranches_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, &mocks.MockGitClient{})
	_, err := svc.ListBranches(context.Background(), 0)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestListBranches_OK(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	branches := []*domain.Branch{{Name: "main", IsDefault: true}}
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	gitClient := &mocks.MockGitClient{}
	gitClient.On("ListBranches", mock.Anything, r.RemoteUrl, r.CredentialRef).Return(branches, nil)
	svc := newSvc(repo, nil, gitClient)
	out, err := svc.ListBranches(context.Background(), 42)
	assert.NoError(t, err)
	assert.Equal(t, branches, out)
}

func TestListBranches_SetsConnectionOKAndDefaultBranchOnSuccess(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	branches := []*domain.Branch{
		{Name: "develop", IsDefault: false},
		{Name: "main", IsDefault: true},
	}
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	repo.On("Update", mock.Anything, mock.MatchedBy(func(upd *domain.Repository) bool {
		return upd.GetConnectionStatus() == domain.ConnectionStatusOK &&
			upd.GetState() == domain.RepositoryStateConnected &&
			upd.GetDefaultBranch() == "main"
	})).Return(nil)
	gitClient := &mocks.MockGitClient{}
	gitClient.On("ListBranches", mock.Anything, r.RemoteUrl, r.CredentialRef).Return(branches, nil)
	svc := newSvc(repo, nil, gitClient)
	out, err := svc.ListBranches(context.Background(), 42)
	assert.NoError(t, err)
	assert.Len(t, out, 2)
	repo.AssertExpectations(t)
}

func TestListBranches_DegradesToAuthFailedOnForbidden(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	repo.On("Update", mock.Anything, mock.MatchedBy(func(upd *domain.Repository) bool {
		return upd.GetConnectionStatus() == domain.ConnectionStatusAuthFailed &&
			upd.GetState() == domain.RepositoryStateError
	})).Return(nil)
	gitClient := &mocks.MockGitClient{}
	gitClient.On("ListBranches", mock.Anything, r.RemoteUrl, r.CredentialRef).Return(([]*domain.Branch)(nil), domain.ErrForbiddenAccess)
	svc := newSvc(repo, nil, gitClient)
	_, err := svc.ListBranches(context.Background(), 42)
	assert.ErrorIs(t, err, domain.ErrForbiddenAccess)
	repo.AssertExpectations(t)
}

func TestListBranches_DegradesToUnreachableOnConnectionFail(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	repo.On("Update", mock.Anything, mock.MatchedBy(func(upd *domain.Repository) bool {
		return upd.GetConnectionStatus() == domain.ConnectionStatusUnreachable &&
			upd.GetState() == domain.RepositoryStateDisconnected
	})).Return(nil)
	gitClient := &mocks.MockGitClient{}
	gitClient.On("ListBranches", mock.Anything, r.RemoteUrl, r.CredentialRef).Return(([]*domain.Branch)(nil), domain.ErrConnectionFailed)
	svc := newSvc(repo, nil, gitClient)
	_, err := svc.ListBranches(context.Background(), 42)
	assert.ErrorIs(t, err, domain.ErrConnectionFailed)
	repo.AssertExpectations(t)
}

func TestTestConnection_PassesStoredCredentialToGit(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	r.CredentialRef = "ghp_stored_token"
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	gitClient := &mocks.MockGitClient{}
	gitClient.On("TestConnection", mock.Anything, r.RemoteUrl, "ghp_stored_token").Return(domain.ConnectionStatusOK, nil)
	svc := newSvc(repo, nil, gitClient)
	_, err := svc.TestConnection(context.Background(), 42)
	assert.NoError(t, err)
	gitClient.AssertExpectations(t)
}

func TestListBranches_PassesStoredCredentialToGit(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	r.CredentialRef = "ghp_stored_token"
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	gitClient := &mocks.MockGitClient{}
	gitClient.On("ListBranches", mock.Anything, r.RemoteUrl, "ghp_stored_token").Return([]*domain.Branch{{Name: "main"}}, nil)
	svc := newSvc(repo, nil, gitClient)
	branches, err := svc.ListBranches(context.Background(), 42)
	assert.NoError(t, err)
	assert.Len(t, branches, 1)
	gitClient.AssertExpectations(t)
}

func TestListBranches_GitError_Propagated(t *testing.T) {
	t.Parallel()
	r := validRepo(42, 7)
	repo := &mocks.MockRepositoryRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(r, nil)
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	gitClient := &mocks.MockGitClient{}
	gitClient.On("ListBranches", mock.Anything, r.RemoteUrl, r.CredentialRef).Return(([]*domain.Branch)(nil), domain.ErrForbiddenAccess)
	svc := newSvc(repo, nil, gitClient)
	_, err := svc.ListBranches(context.Background(), 42)
	assert.ErrorIs(t, err, domain.ErrForbiddenAccess)
}

// ─── PushResult ──────────────────────────────────────────────────────────────

func TestPushResult_MissingTargetURL(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, &mocks.MockGitClient{})
	_, err := svc.PushResult(context.Background(), "", "", nil, "")
	assert.ErrorIs(t, err, domain.ErrInvalidRemoteURL)
}

func TestPushResult_MissingFiles(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockRepositoryRepository{}, nil, &mocks.MockGitClient{})
	_, err := svc.PushResult(context.Background(), "https://example.com/org/repo", "", []*domain.PushFile{}, "")
	assert.ErrorIs(t, err, domain.ErrMissingPayload)
}

func TestPushResult_OK(t *testing.T) {
	t.Parallel()
	files := []*domain.PushFile{{Path: "main.go", Content: "package main\n"}}
	gitClient := &mocks.MockGitClient{}
	gitClient.On("PushResult", mock.Anything, "https://example.com/org/repo", "tok", files, "chore: test").Return("main", nil)
	svc := newSvc(nil, nil, gitClient)
	branch, err := svc.PushResult(context.Background(), "https://example.com/org/repo", "tok", files, "chore: test")
	require.NoError(t, err)
	assert.Equal(t, "main", branch)
}

func TestPushResult_AuthFailed(t *testing.T) {
	t.Parallel()
	files := []*domain.PushFile{{Path: "main.go", Content: "package main\n"}}
	gitClient := &mocks.MockGitClient{}
	gitClient.On("PushResult", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", domain.ErrPushAuthFailed)
	svc := newSvc(nil, nil, gitClient)
	_, err := svc.PushResult(context.Background(), "https://example.com/org/repo", "bad", files, "")
	assert.ErrorIs(t, err, domain.ErrPushAuthFailed)
}
