package repositories

import (
	"context"

	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/ports"
	"milton_prism/core/shared/grpc_client_sdk"
	identitysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/identity/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var _ ports.IdentityClient = (*IdentityClientAdapter)(nil)

// IdentityClientAdapter wraps the identity gRPC client and exposes the single
// driven-port operation needed by the migration service.
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
func (a *IdentityClientAdapter) ValidateUserExists(ctx context.Context, userID uint64) error {
	// Forward the caller's Bearer token so the identity service can authorise the
	// lookup. gRPC does not propagate incoming metadata automatically.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	_, err := a.client.GetUser(ctx, &identitysvcv1.GetUserRequest{Identifier: userID})
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok && s.Code() == codes.NotFound {
		return domain.ErrOwnerNotFound
	}
	return domain.ErrInternal
}
