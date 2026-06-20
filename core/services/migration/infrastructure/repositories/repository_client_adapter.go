package repositories

import (
	"context"
	"strings"

	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/ports"
	"milton_prism/core/shared/grpc_client_sdk"
	repositorysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/repository/v1"
	repositoryv1 "milton_prism/pkg/pb/gen/milton_prism/types/repository/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var _ ports.RepositoryClient = (*RepositoryClientAdapter)(nil)

// RepositoryClientAdapter wraps the repository gRPC client and exposes the
// driven-port operations needed by the migration service.
type RepositoryClientAdapter struct {
	client *grpc_client_sdk.RepositoryGrpcClient
}

// NewRepositoryClientAdapter wraps a RepositoryGrpcClient behind the driven port.
func NewRepositoryClientAdapter(c *grpc_client_sdk.RepositoryGrpcClient) *RepositoryClientAdapter {
	return &RepositoryClientAdapter{client: c}
}

// FetchRepositoryURL validates that the repository exists and returns its remote URL.
// It maps NotFound to domain.ErrRepositoryNotFound and any other error to domain.ErrInternal.
func (a *RepositoryClientAdapter) FetchRepositoryURL(ctx context.Context, repositoryID uint64) (string, error) {
	ctx = forwardMetadata(ctx)
	resp, err := a.client.GetRepository(ctx, &repositorysvcv1.GetRepositoryRequest{Identifier: repositoryID})
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
			return "", domain.ErrRepositoryNotFound
		}
		return "", domain.ErrInternal
	}
	return resp.GetRemoteUrl(), nil
}

// PushFiles commits files to a temporary workspace and pushes to targetURL via
// the repository service's PushResult RPC. writeToken is forwarded as-is and
// is never logged or stored by this adapter.
func (a *RepositoryClientAdapter) PushFiles(ctx context.Context, targetURL, writeToken string, files []ports.PushFile, commitMessage string) (string, error) {
	ctx = forwardMetadata(ctx)

	entries := make([]*repositorysvcv1.FileEntry, len(files))
	for i, f := range files {
		entries[i] = &repositorysvcv1.FileEntry{Path: f.Path, Content: f.Content}
	}

	resp, err := a.client.PushResult(ctx, &repositorysvcv1.PushResultRequest{
		TargetUrl:     targetURL,
		WriteToken:    writeToken,
		Files:         entries,
		CommitMessage: commitMessage,
	})
	if err != nil {
		return "", mapPushGRPCError(err)
	}
	return resp.GetPushedBranch(), nil
}

// ProbeConnection calls TestConnection on the repository service (live git
// probe + credential check) and maps the result to a migration domain error.
// Also persists the updated connection_status on the repository record (side
// effect of TestConnection on the repository service side).
func (a *RepositoryClientAdapter) ProbeConnection(ctx context.Context, repositoryID uint64) error {
	ctx = forwardMetadata(ctx)
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

// forwardMetadata copies the incoming gRPC metadata to the outgoing context
// so the downstream service receives the caller's Bearer token.
func forwardMetadata(ctx context.Context) context.Context {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		return metadata.NewOutgoingContext(ctx, md)
	}
	return ctx
}

// mapPushGRPCError translates a repository-service gRPC error into a typed
// migration domain error. The raw gRPC message (which may contain infrastructure
// detail) is never surfaced directly — only the domain code propagates.
func mapPushGRPCError(err error) error {
	s, ok := status.FromError(err)
	if !ok {
		return domain.ErrInternal
	}
	msg := strings.ToLower(s.Message())
	switch {
	case s.Code() == codes.Unauthenticated,
		strings.Contains(msg, "repo206"),
		strings.Contains(msg, "push_auth"):
		return domain.ErrPushAuthFailed
	case s.Code() == codes.FailedPrecondition && strings.Contains(msg, "repo207"),
		strings.Contains(msg, "push_rejected"):
		return domain.ErrPushConflict
	case strings.Contains(msg, "repo208"),
		strings.Contains(msg, "push_network"):
		return domain.ErrPushNetworkError
	case s.Code() == codes.FailedPrecondition:
		// Catch-all for other precondition failures from repository service.
		return domain.ErrPushConflict
	default:
		return domain.ErrInternal
	}
}
