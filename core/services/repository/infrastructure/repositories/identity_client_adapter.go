package repositories

import (
	"context"

	"milton_prism/core/services/repository/domain"
	"milton_prism/core/services/repository/ports"
	"milton_prism/core/shared/grpc_client_sdk"
	identitysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/identity/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ ports.IdentityClient = (*IdentityClientAdapter)(nil)

// IdentityClientAdapter wraps the identity gRPC client and exposes the single
// driven-port operation needed by the repository service.
type IdentityClientAdapter struct {
	client *grpc_client_sdk.IdentityGrpcClient
}

// NewIdentityClientAdapter wraps an IdentityGrpcClient behind the driven port.
func NewIdentityClientAdapter(c *grpc_client_sdk.IdentityGrpcClient) *IdentityClientAdapter {
	return &IdentityClientAdapter{client: c}
}

// ValidateUserExists returns nil when the user with userID exists in the
// identity service. It maps NotFound to domain.ErrOwnerNotFound and any other
// error to domain.ErrInternal.
//
// The context is forwarded as-is; callers must ensure it carries the appropriate
// authentication metadata for the identity service endpoint.
func (a *IdentityClientAdapter) ValidateUserExists(ctx context.Context, userID uint64) error {
	_, err := a.client.GetUser(ctx, &identitysvcv1.GetUserRequest{Identifier: userID})
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
		return domain.ErrOwnerNotFound
	}
	return domain.ErrInternal
}
