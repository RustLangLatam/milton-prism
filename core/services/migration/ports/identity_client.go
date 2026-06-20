package ports

import "context"

// IdentityClient is the driven port for validating user existence via the
// identity service. The migration service calls this before creating a migration
// to confirm the owner_user_id refers to an active user.
type IdentityClient interface {
	// ValidateUserExists returns nil when the user exists and is active,
	// domain.ErrOwnerNotFound when the user is not found, or domain.ErrInternal
	// for unexpected transport errors.
	ValidateUserExists(ctx context.Context, userID uint64) error
}
