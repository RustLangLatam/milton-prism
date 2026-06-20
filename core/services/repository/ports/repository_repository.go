// Package ports defines the driven port interfaces for the repository service.
package ports

import (
	"context"

	"milton_prism/core/services/repository/domain"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// RepositoryRepository is the driven port for persisting repository records.
type RepositoryRepository interface {
	// Create persists a new repository and returns the stored record.
	Create(ctx context.Context, r *domain.Repository) (*domain.Repository, error)
	// GetByID fetches a repository by its numeric identifier.
	// If includeDeleted is false, soft-deleted records are excluded.
	GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.Repository, error)
	// List returns a paginated, filtered set of repositories.
	List(ctx context.Context, filter *domain.RepositoriesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Repository, *paginationv1.Pagination, error)
	// Update persists mutable field changes for an existing repository.
	Update(ctx context.Context, r *domain.Repository) error
	// SoftDelete marks the repository as deleted without removing it.
	SoftDelete(ctx context.Context, identifier uint64) error
	// UpdateConnectionStatus updates only the connection_status field.
	UpdateConnectionStatus(ctx context.Context, identifier uint64, status domain.ConnectionStatus) error
}
