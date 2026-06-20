// Package ports defines the driven ports for the identity service.
package ports

import (
	"context"

	"milton_prism/core/services/identity/domain"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// UserRepository persists user accounts.
type UserRepository interface {
	Create(ctx context.Context, u *domain.User, passwordHash string) (*domain.User, error)
	GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	// GetCredentialsByEmail returns the user and its stored password hash.
	// The hash MUST NOT be exposed outside the application layer.
	GetCredentialsByEmail(ctx context.Context, email string) (*domain.User, string, error)
	List(ctx context.Context, filter *domain.UsersFilter, params *queryparamsv1.PageQueryParams) ([]*domain.User, *paginationv1.Pagination, error)
	Update(ctx context.Context, u *domain.User) error
	SoftDelete(ctx context.Context, identifier uint64) error
}
