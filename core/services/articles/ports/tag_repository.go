package ports

import (
	"context"

	"milton_prism/core/services/articles/domain"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// TagRepository is the driven port for reading tag records.
type TagRepository interface {
	GetByID(ctx context.Context, identifier uint64) (*domain.Tag, error)
	List(ctx context.Context, params *queryparamsv1.PageQueryParams) ([]*domain.Tag, *paginationv1.Pagination, error)
}
