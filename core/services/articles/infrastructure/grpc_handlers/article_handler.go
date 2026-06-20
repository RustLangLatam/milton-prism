// Package grpc_handlers exposes the articles application service as an
// ArticleServiceServer. It is the driving adapter on top of the hexagonal core.
package grpc_handlers

import (
	"context"
	"errors"

	"milton_prism/core/services/articles/application"
	"milton_prism/core/services/articles/domain"
	coreerror "milton_prism/core/shared/error"
	applog "milton_prism/pkg/log"
	articlessvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/articles/v1"
	articlesv1 "milton_prism/pkg/pb/gen/milton_prism/types/articles/v1"

	"google.golang.org/protobuf/types/known/emptypb"
)

// AuthExtractor validates the access token in ctx and returns the authenticated
// user's identifier and whether the caller is a system user.
type AuthExtractor func(ctx context.Context) (userID uint64, isSystem bool, err error)

// ArticleHandler implements articlessvcv1.ArticleServiceServer.
type ArticleHandler struct {
	articlessvcv1.UnimplementedArticleServiceServer
	svc         *application.Service
	authExtract AuthExtractor
}

// NewArticleHandler builds a handler bound to the application service.
func NewArticleHandler(svc *application.Service, authExtract AuthExtractor) *ArticleHandler {
	return &ArticleHandler{svc: svc, authExtract: authExtract}
}

func (h *ArticleHandler) CreateArticle(ctx context.Context, req *articlessvcv1.CreateArticleRequest) (*articlesv1.Article, error) {
	callerID, _, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("articles: CreateArticle authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetArticle() == nil {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingPayload, domain.ErrMissingPayload.Message)
	}
	a := req.GetArticle()
	if a.GetAuthorIdentifier() == 0 {
		a.AuthorIdentifier = callerID
	}
	out, err := h.svc.CreateArticle(ctx, a)
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *ArticleHandler) GetArticle(ctx context.Context, req *articlessvcv1.GetArticleRequest) (*articlesv1.Article, error) {
	if _, _, err := h.authExtract(ctx); err != nil {
		applog.Warningf("articles: GetArticle authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	out, err := h.svc.GetArticle(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *ArticleHandler) ListArticles(ctx context.Context, req *articlessvcv1.ListArticlesRequest) (*articlessvcv1.ListArticlesResponse, error) {
	if _, _, err := h.authExtract(ctx); err != nil {
		applog.Warningf("articles: ListArticles authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	filter := req.GetFilter()
	if filter == nil {
		filter = &articlesv1.ArticlesFilter{}
	}
	items, pag, err := h.svc.ListArticles(ctx, filter, req.GetPageParams())
	if err != nil {
		return nil, h.mapError(err)
	}
	return &articlessvcv1.ListArticlesResponse{Articles: items, Pagination: pag}, nil
}

func (h *ArticleHandler) UpdateArticle(ctx context.Context, req *articlessvcv1.UpdateArticleRequest) (*articlesv1.Article, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("articles: UpdateArticle authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetArticle() == nil || req.GetArticle().GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	existing, err := h.svc.GetArticle(ctx, req.GetArticle().GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && existing.GetAuthorIdentifier() != callerID {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	out, err := h.svc.UpdateArticle(ctx, req.GetArticle(), req.GetUpdateMask())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *ArticleHandler) DeleteArticle(ctx context.Context, req *articlessvcv1.DeleteArticleRequest) (*emptypb.Empty, error) {
	callerID, isSystem, err := h.authExtract(ctx)
	if err != nil {
		applog.Warningf("articles: DeleteArticle authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	existing, err := h.svc.GetArticle(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	if !isSystem && existing.GetAuthorIdentifier() != callerID {
		return nil, coreerror.NewPermissionDeniedError(domain.ErrCodeForbiddenAccess, domain.ErrForbiddenAccess.Message)
	}
	if err := h.svc.DeleteArticle(ctx, req.GetIdentifier()); err != nil {
		return nil, h.mapError(err)
	}
	return &emptypb.Empty{}, nil
}

func (h *ArticleHandler) GetTag(ctx context.Context, req *articlessvcv1.GetTagRequest) (*articlesv1.Tag, error) {
	if _, _, err := h.authExtract(ctx); err != nil {
		applog.Warningf("articles: GetTag authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	if req.GetIdentifier() == 0 {
		return nil, coreerror.NewInvalidArgumentError(domain.ErrCodeMissingIdentifier, domain.ErrMissingIdentifier.Message)
	}
	out, err := h.svc.GetTag(ctx, req.GetIdentifier())
	if err != nil {
		return nil, h.mapError(err)
	}
	return out, nil
}

func (h *ArticleHandler) ListTags(ctx context.Context, req *articlessvcv1.ListTagsRequest) (*articlessvcv1.ListTagsResponse, error) {
	if _, _, err := h.authExtract(ctx); err != nil {
		applog.Warningf("articles: ListTags authentication failed: error=%v", err)
		return nil, coreerror.TokenValidationErrorInvalid
	}
	items, pag, err := h.svc.ListTags(ctx, req.GetPageParams())
	if err != nil {
		return nil, h.mapError(err)
	}
	return &articlessvcv1.ListTagsResponse{Tags: items, Pagination: pag}, nil
}

func (h *ArticleHandler) mapError(err error) error {
	if err == nil {
		return nil
	}
	var dErr *domain.Error
	if errors.As(err, &dErr) {
		switch dErr.Code {
		case domain.ErrCodeArticleNotFound, domain.ErrCodeTagNotFound, domain.ErrCodeAuthorNotFound:
			return coreerror.NewNotFoundError(dErr.Code, dErr.Message)
		case domain.ErrCodeArticleAlreadyExists:
			return coreerror.NewAlreadyExistsError(dErr.Code, dErr.Message)
		case domain.ErrCodeForbiddenAccess:
			return coreerror.NewPermissionDeniedError(dErr.Code, dErr.Message)
		case domain.ErrCodeMissingIdentifier, domain.ErrCodeMissingPayload, domain.ErrCodeMissingAuthorIdentifier:
			return coreerror.NewInvalidArgumentError(dErr.Code, dErr.Message)
		case domain.ErrCodeInternal:
			applog.Warningf("internal articles error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(dErr.Code, dErr.Message)
		default:
			applog.Warningf("unhandled articles error: code=%s error=%v", dErr.Code, err)
			return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
		}
	}
	applog.Warningf("unhandled articles error: error=%v", err)
	return coreerror.NewInternalError(domain.ErrCodeInternal, domain.ErrInternal.Message)
}
