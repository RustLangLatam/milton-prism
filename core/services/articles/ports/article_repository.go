// Package ports defines the driven port interfaces for the articles service.
package ports

import (
	"context"

	"milton_prism/core/services/articles/domain"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
)

// ArticleRepository is the driven port for persisting article records.
type ArticleRepository interface {
	Create(ctx context.Context, a *domain.Article) (*domain.Article, error)
	GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.Article, error)
	List(ctx context.Context, filter *domain.ArticlesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Article, *paginationv1.Pagination, error)
	Update(ctx context.Context, a *domain.Article) error
	SoftDelete(ctx context.Context, identifier uint64) error
}
