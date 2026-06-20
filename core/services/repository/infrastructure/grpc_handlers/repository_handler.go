// Package grpc_handlers exposes the repository application service as a
// RepositoryServiceServer. It is the driving adapter on top of the hexagonal core.
package grpc_handlers

import (
	"context"
	"errors"

	"milton_prism/core/services/repository/application"
	"milton_prism/core/services/repository/domain"
	coreerror "milton_prism/core/shared/error"
	applog "milton_prism/pkg/log"
	repositorysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/repository/v1"
	repositoryv1 "milton_prism/pkg/pb/gen/milton_prism/types/repository/v1"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AuthExtractor validates the access token in ctx and returns the authenticated
// user's identifier and whether the caller is a system user.
type AuthExtractor func(ctx context.Context) (userID uint64, isSystem bool, err error)

// RepositoryHandler implements repositorysvcv1.RepositoryServiceServer.
type RepositoryHandler struct {
	repositorysvcv1.UnimplementedRepositoryServiceServer
	svc         *application.Service
	authExtract AuthExtractor
}

// NewRepositoryHandler builds a handler bound to the application service.
func NewRepositoryHandler(svc *application.Service, authExtract AuthExtractor) *RepositoryHandler {
	return &RepositoryHandler{svc: svc, authExtract: authExtract}
}

// CreateRepository registers a new git repository.
func (h *RepositoryHandler) CreateRepository(ctx context.Context, req *repositorysvcv1.CreateRepositoryRequest) (*repositoryv1.Repository, error) {
	callerID, _, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("repository: CreateRepository authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetRepository() == nil {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingPayload, domain.ErrMissingPayload.Message)
	}
	r := req.GetRepository()
	if r.GetOwnerUserId() == 0 {
		r.OwnerUserId = callerID
	}
	out, err := h.svc.CreateRepository(ctx, r)
	if err != nil {
		return nil, h.mapError(err)
	}
	out.CredentialRef = ""
	return out, nil
}

// GetRepository returns a repository by identifier.
func (h *RepositoryHandler) GetRepository(ctx context.Context, req *repositorysvcv1.GetRepositoryRequest) (*repositoryv1.Repository, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("repository: GetRepository authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	r, err := h.svc.GetRepository(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && r.GetOwnerUserId() != callerID {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	r.CredentialRef = ""
	return r, nil
}

// ListRepositories returns a paginated list of repositories.
func (h *RepositoryHandler) ListRepositories(ctx context.Context, req *repositorysvcv1.ListRepositoriesRequest) (*repositorysvcv1.ListRepositoriesResponse, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("repository: ListRepositories authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	filter := req.GetFilter()
	if filter == nil {
		filter = &repositoryv1.RepositoriesFilter{}
	}
	// Non-system callers see only their own repositories.
	if !isSystem {
		filter.OwnerUserId = &callerID
	}
	items, p, err := h.svc.ListRepositories(ctx, filter, req.GetPageParams())
	if err != nil {
		return nil, h.mapError(err)
	}
	for _, item := range items {
		item.CredentialRef = ""
	}
	return &repositorysvcv1.ListRepositoriesResponse{Repositories: items, Pagination: p}, nil
}

// UpdateRepository applies a FieldMask-bounded partial update.
func (h *RepositoryHandler) UpdateRepository(ctx context.Context, req *repositorysvcv1.UpdateRepositoryRequest) (*repositoryv1.Repository, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("repository: UpdateRepository authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetRepository() == nil || req.GetRepository().GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	existing, err := h.svc.GetRepository(ctx, req.GetRepository().GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && existing.GetOwnerUserId() != callerID {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.UpdateRepository(ctx, req.GetRepository(), req.GetUpdateMask())
	if err != nil {
		return nil, h.mapError(err)
	}
	out.CredentialRef = ""
	return out, nil
}

// DeleteRepository soft-deletes a repository.
func (h *RepositoryHandler) DeleteRepository(ctx context.Context, req *repositorysvcv1.DeleteRepositoryRequest) (*emptypb.Empty, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("repository: DeleteRepository authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	existing, err := h.svc.GetRepository(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && existing.GetOwnerUserId() != callerID {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	if err := h.svc.DeleteRepository(ctx, req.GetIdentifier()); err != nil {
		return nil, h.mapError(err)
	}
	return &emptypb.Empty{}, nil
}

// ProbeSourceRepository checks a remote URL without cloning.
func (h *RepositoryHandler) ProbeSourceRepository(ctx context.Context, req *repositorysvcv1.ProbeSourceRepositoryRequest) (*repositorysvcv1.ProbeSourceRepositoryResponse, error) {
	if _, _, err := h.authExtract(ctx); err != nil {
		applog.Warningf("repository: ProbeSourceRepository authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	result, err := h.svc.ProbeSourceRepository(ctx, req.GetRemoteUrl(), req.GetToken())
	if err != nil {
		return nil, h.mapError(err)
	}
	return &repositorysvcv1.ProbeSourceRepositoryResponse{
		Reachable:    result.Reachable,
		Visibility:   repositoryv1.RepositoryVisibility(result.Visibility),
		AuthValid:    result.AuthValid,
		ErrorMessage: result.ErrorMessage,
	}, nil
}

// TestConnection probes the remote git URL and persists the result.
func (h *RepositoryHandler) TestConnection(ctx context.Context, req *repositorysvcv1.TestConnectionRequest) (*repositorysvcv1.TestConnectionResponse, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("repository: TestConnection authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	existing, err := h.svc.GetRepository(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && existing.GetOwnerUserId() != callerID {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	status, err := h.svc.TestConnection(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return &repositorysvcv1.TestConnectionResponse{Status: status}, nil
}

// ListBranches returns the branches available on the remote.
func (h *RepositoryHandler) ListBranches(ctx context.Context, req *repositorysvcv1.ListBranchesRequest) (*repositorysvcv1.ListBranchesResponse, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("repository: ListBranches authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	existing, err := h.svc.GetRepository(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && existing.GetOwnerUserId() != callerID {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	branches, err := h.svc.ListBranches(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return &repositorysvcv1.ListBranchesResponse{Branches: branches}, nil
}

// PushResult commits migration artifacts and pushes to the caller-supplied target.
func (h *RepositoryHandler) PushResult(ctx context.Context, req *repositorysvcv1.PushResultRequest) (*repositorysvcv1.PushResultResponse, error) {
	if _, _, err := h.authExtract(ctx); err != nil {
		applog.Warningf("repository: PushResult authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	files := make([]*domain.PushFile, 0, len(req.GetFiles()))
	for _, f := range req.GetFiles() {
		files = append(files, &domain.PushFile{Path: f.GetPath(), Content: f.GetContent()})
	}
	pushedBranch, err := h.svc.PushResult(ctx, req.GetTargetUrl(), req.GetWriteToken(), files, req.GetCommitMessage())
	if err != nil {
		return nil, h.mapError(err)
	}
	return &repositorysvcv1.PushResultResponse{PushedBranch: pushedBranch}, nil
}

func (h *RepositoryHandler) mapError(err error) error {
	if err == nil {
		return nil
	}
	var dErr *domain.Error
	if errors.As(err, &dErr) {
		switch dErr.Code {
		case domain.ErrCodeRepositoryNotFound:
			return coreerror.NewNotFoundError(dErr.Code, dErr.Message)
		case domain.ErrCodeRepositoryAlreadyExists:
			return coreerror.NewAlreadyExistsError(dErr.Code, dErr.Message)
		case domain.ErrCodeOwnerNotFound:
			return coreerror.NewNotFoundError(dErr.Code, dErr.Message)
		case domain.ErrCodeConnectionFailed:
			return coreerror.NewInternalError(dErr.Code, dErr.Message)
		case domain.ErrCodeForbiddenAccess:
			return coreerror.NewPermissionDeniedError(dErr.Code, dErr.Message)
		case domain.ErrCodeMissingIdentifier, domain.ErrCodeMissingPayload,
			domain.ErrCodeMissingOwnerUserID, domain.ErrCodeInvalidRemoteURL:
			return coreerror.NewInvalidArgumentError(dErr.Code, dErr.Message)
		case domain.ErrCodeInternal:
			applog.Warningf("internal repository error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(dErr.Code, dErr.Message)
		default:
			applog.Warningf("unhandled repository error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
		}
	}
	applog.Warningf("unhandled repository error: error=%v", err)
	return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
}
