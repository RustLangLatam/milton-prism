// Package application contains the articles service's use-case logic.
// It depends only on domain types and driven port interfaces — never on
// infrastructure packages.
package application

import (
	"context"
	"fmt"

	"milton_prism/core/services/articles/domain"
	"milton_prism/core/services/articles/ports"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Service orchestrates articles use cases.
type Service struct {
	articles ports.ArticleRepository
	tags     ports.TagRepository
	tx       ports.TransactionManager
	profile  ports.ProfileClient
}

// NewService wires port implementations into the application service.
func NewService(
	articles ports.ArticleRepository,
	tags ports.TagRepository,
	tx ports.TransactionManager,
	profile ports.ProfileClient,
) *Service {
	return &Service{articles: articles, tags: tags, tx: tx, profile: profile}
}

// CreateArticle validates the payload, confirms the author profile exists, and
// persists the new article inside a transaction.
func (s *Service) CreateArticle(ctx context.Context, a *domain.Article) (*domain.Article, error) {
	if a == nil || a.GetSlug() == "" || a.GetTitle() == "" {
		return nil, domain.ErrMissingPayload
	}
	if a.GetAuthorIdentifier() == 0 {
		return nil, domain.ErrMissingAuthorIdentifier
	}
	if s.profile != nil {
		if err := s.profile.ValidateProfileExists(ctx, a.GetAuthorIdentifier()); err != nil {
			return nil, err
		}
	}
	var out *domain.Article
	err := s.tx.WithTransaction(ctx, func(txCtx context.Context) error {
		var createErr error
		out, createErr = s.articles.Create(txCtx, a)
		return createErr
	})
	if err != nil {
		return nil, fmt.Errorf("create article: %w", err)
	}
	return out, nil
}

// GetArticle fetches an article by identifier.
func (s *Service) GetArticle(ctx context.Context, identifier uint64) (*domain.Article, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	return s.articles.GetByID(ctx, identifier, false)
}

// ListArticles returns a paginated, filtered list of articles.
func (s *Service) ListArticles(ctx context.Context, filter *domain.ArticlesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Article, *paginationv1.Pagination, error) {
	return s.articles.List(ctx, filter, params)
}

// UpdateArticle applies a FieldMask-bounded partial update and returns the
// updated article.
func (s *Service) UpdateArticle(ctx context.Context, a *domain.Article, mask *fieldmaskpb.FieldMask) (*domain.Article, error) {
	if a == nil || a.GetIdentifier() == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	existing, err := s.articles.GetByID(ctx, a.GetIdentifier(), false)
	if err != nil {
		return nil, err
	}
	applyArticleMask(existing, a, mask)
	if err := s.articles.Update(ctx, existing); err != nil {
		return nil, err
	}
	return existing, nil
}

// DeleteArticle soft-deletes an article by identifier.
func (s *Service) DeleteArticle(ctx context.Context, identifier uint64) error {
	if identifier == 0 {
		return domain.ErrMissingIdentifier
	}
	return s.articles.SoftDelete(ctx, identifier)
}

// GetTag fetches a tag by identifier.
func (s *Service) GetTag(ctx context.Context, identifier uint64) (*domain.Tag, error) {
	if identifier == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	return s.tags.GetByID(ctx, identifier)
}

// ListTags returns a paginated list of all active tags.
func (s *Service) ListTags(ctx context.Context, params *queryparamsv1.PageQueryParams) ([]*domain.Tag, *paginationv1.Pagination, error) {
	return s.tags.List(ctx, params)
}

// applyArticleMask updates existing with values from update for each path in mask.
// A nil or empty mask updates all mutable fields ("*" semantics per AIP-134).
func applyArticleMask(existing, update *domain.Article, mask *fieldmaskpb.FieldMask) {
	if mask == nil || len(mask.GetPaths()) == 0 {
		existing.Slug = update.GetSlug()
		existing.Title = update.GetTitle()
		existing.Description = update.GetDescription()
		existing.Body = update.GetBody()
		existing.State = update.GetState()
		return
	}
	for _, path := range mask.GetPaths() {
		switch path {
		case "slug":
			existing.Slug = update.GetSlug()
		case "title":
			existing.Title = update.GetTitle()
		case "description":
			existing.Description = update.GetDescription()
		case "body":
			existing.Body = update.GetBody()
		case "state":
			existing.State = update.GetState()
		}
	}
}
