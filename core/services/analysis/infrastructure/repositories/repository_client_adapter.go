package repositories

import (
	"context"

	"milton_prism/core/services/analysis/domain"
	"milton_prism/core/services/analysis/ports"
	"milton_prism/core/shared/grpc_client_sdk"
	repositorysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/repository/v1"
	repositoryv1 "milton_prism/pkg/pb/gen/milton_prism/types/repository/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var _ ports.RepositoryClient = (*RepositoryClientAdapter)(nil)

// RepositoryClientAdapter wraps the repository gRPC client and exposes the
// driven-port operations needed by the analysis service.
type RepositoryClientAdapter struct {
	client *grpc_client_sdk.RepositoryGrpcClient
}

// NewRepositoryClientAdapter wraps a RepositoryGrpcClient behind the driven port.
func NewRepositoryClientAdapter(c *grpc_client_sdk.RepositoryGrpcClient) *RepositoryClientAdapter {
	return &RepositoryClientAdapter{client: c}
}

// ValidateRepositoryExists returns nil when the repository exists in the
// repository service. It maps NotFound to domain.ErrRepositoryNotFound and any
// other error to domain.ErrInternal.
func (a *RepositoryClientAdapter) ValidateRepositoryExists(ctx context.Context, repositoryID uint64) error {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	_, err := a.client.GetRepository(ctx, &repositorysvcv1.GetRepositoryRequest{Identifier: repositoryID})
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
		return domain.ErrRepositoryNotFound
	}
	return domain.ErrInternal
}

// ProbeConnection calls TestConnection on the repository service (live git
// probe + credential check) and maps the result to an analysis domain error.
func (a *RepositoryClientAdapter) ProbeConnection(ctx context.Context, repositoryID uint64) error {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	resp, err := a.client.TestConnection(ctx, &repositorysvcv1.TestConnectionRequest{Identifier: repositoryID})
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
			return domain.ErrRepositoryNotFound
		}
		return domain.ErrInternal
	}
	switch resp.GetStatus() {
	case repositoryv1.ConnectionStatus_CONNECTION_STATUS_OK:
		return nil
	case repositoryv1.ConnectionStatus_CONNECTION_STATUS_AUTH_FAILED:
		return domain.ErrRepoAuthFailed
	default:
		return domain.ErrRepoUnreachable
	}
}

// GetBranchSHA returns the HEAD commit SHA for the named branch by calling
// ListBranches on the repository service and searching the response. Returns
// domain.ErrRepositoryNotFound when the repository does not exist or the branch
// is absent in the response.
func (a *RepositoryClientAdapter) GetBranchSHA(ctx context.Context, repositoryID uint64, branch string) (string, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	resp, err := a.client.ListBranches(ctx, &repositorysvcv1.ListBranchesRequest{Identifier: repositoryID})
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
			return "", domain.ErrRepositoryNotFound
		}
		return "", domain.ErrInternal
	}
	for _, b := range resp.GetBranches() {
		if b.GetName() == branch {
			return b.GetCommitSha(), nil
		}
	}
	return "", domain.ErrRepositoryNotFound
}

// GetRemoteURL returns the remote URL and default branch of the repository.
func (a *RepositoryClientAdapter) GetRemoteURL(ctx context.Context, repositoryID uint64) (string, string, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	repo, err := a.client.GetRepository(ctx, &repositorysvcv1.GetRepositoryRequest{Identifier: repositoryID})
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
			return "", "", domain.ErrRepositoryNotFound
		}
		return "", "", domain.ErrInternal
	}
	return repo.GetRemoteUrl(), repo.GetDefaultBranch(), nil
}
