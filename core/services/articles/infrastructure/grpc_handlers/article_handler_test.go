package grpc_handlers_test

import (
	"context"
	"errors"
	"testing"

	"milton_prism/core/services/articles/application"
	"milton_prism/core/services/articles/domain"
	"milton_prism/core/services/articles/infrastructure/grpc_handlers"
	"milton_prism/core/services/articles/mocks"
	articlessvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/articles/v1"
	articlesv1 "milton_prism/pkg/pb/gen/milton_prism/types/articles/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func authedExtractor(id uint64) grpc_handlers.AuthExtractor {
	return func(_ context.Context) (uint64, bool, error) { return id, false, nil }
}

func failedExtractor() grpc_handlers.AuthExtractor {
	return func(_ context.Context) (uint64, bool, error) { return 0, false, errors.New("no token") }
}

func newHandler(articleRepo *mocks.MockArticleRepository, tagRepo *mocks.MockTagRepository) *grpc_handlers.ArticleHandler {
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	svc := application.NewService(articleRepo, tagRepo, tx, nil)
	return grpc_handlers.NewArticleHandler(svc, authedExtractor(7))
}

// ─── CreateArticle ───────────────────────────────────────────────────────────

func TestCreateArticle_Handler_Unauthenticated(t *testing.T) {
	t.Parallel()
	tx := &mocks.MockTransactionManager{}
	svc := application.NewService(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{}, tx, nil)
	h := grpc_handlers.NewArticleHandler(svc, failedExtractor())
	_, err := h.CreateArticle(context.Background(), &articlessvcv1.CreateArticleRequest{
		Article: &articlesv1.Article{Slug: "s", Title: "t", AuthorIdentifier: 1},
	})
	assert.Error(t, err)
}

func TestCreateArticle_Handler_MissingPayload(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{})
	_, err := h.CreateArticle(context.Background(), &articlessvcv1.CreateArticleRequest{})
	assert.Error(t, err)
}

func TestCreateArticle_Handler_OK(t *testing.T) {
	t.Parallel()
	created := &articlesv1.Article{Identifier: 42, Slug: "s", Title: "t", AuthorIdentifier: 7}
	repo := &mocks.MockArticleRepository{}
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	h := newHandler(repo, &mocks.MockTagRepository{})
	out, err := h.CreateArticle(context.Background(), &articlessvcv1.CreateArticleRequest{
		Article: &articlesv1.Article{Slug: "s", Title: "t", AuthorIdentifier: 7},
	})
	assert.NoError(t, err)
	assert.Equal(t, uint64(42), out.GetIdentifier())
}

// ─── GetArticle ──────────────────────────────────────────────────────────────

func TestGetArticle_Handler_MissingIdentifier(t *testing.T) {
	t.Parallel()
	h := newHandler(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{})
	_, err := h.GetArticle(context.Background(), &articlessvcv1.GetArticleRequest{Identifier: 0})
	assert.Error(t, err)
}

func TestGetArticle_Handler_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockArticleRepository{}
	repo.On("GetByID", mock.Anything, uint64(99), false).Return((*domain.Article)(nil), domain.ErrArticleNotFound)
	h := newHandler(repo, &mocks.MockTagRepository{})
	_, err := h.GetArticle(context.Background(), &articlessvcv1.GetArticleRequest{Identifier: 99})
	assert.Error(t, err)
}

func TestGetArticle_Handler_OK(t *testing.T) {
	t.Parallel()
	a := &articlesv1.Article{Identifier: 42, Slug: "s", Title: "t", AuthorIdentifier: 7}
	repo := &mocks.MockArticleRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(a, nil)
	h := newHandler(repo, &mocks.MockTagRepository{})
	out, err := h.GetArticle(context.Background(), &articlessvcv1.GetArticleRequest{Identifier: 42})
	assert.NoError(t, err)
	assert.Equal(t, uint64(42), out.GetIdentifier())
}

// ─── DeleteArticle ───────────────────────────────────────────────────────────

func TestDeleteArticle_Handler_Forbidden(t *testing.T) {
	t.Parallel()
	// caller is 7, article is owned by 99 → forbidden
	a := &articlesv1.Article{Identifier: 42, AuthorIdentifier: 99}
	repo := &mocks.MockArticleRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(a, nil)
	h := newHandler(repo, &mocks.MockTagRepository{})
	_, err := h.DeleteArticle(context.Background(), &articlessvcv1.DeleteArticleRequest{Identifier: 42})
	assert.Error(t, err)
}

func TestDeleteArticle_Handler_OK(t *testing.T) {
	t.Parallel()
	a := &articlesv1.Article{Identifier: 42, AuthorIdentifier: 7}
	repo := &mocks.MockArticleRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(a, nil)
	repo.On("SoftDelete", mock.Anything, uint64(42)).Return(nil)
	h := newHandler(repo, &mocks.MockTagRepository{})
	_, err := h.DeleteArticle(context.Background(), &articlessvcv1.DeleteArticleRequest{Identifier: 42})
	assert.NoError(t, err)
}

// ─── GetTag ──────────────────────────────────────────────────────────────────

func TestGetTag_Handler_NotFound(t *testing.T) {
	t.Parallel()
	tags := &mocks.MockTagRepository{}
	tags.On("GetByID", mock.Anything, uint64(99)).Return((*domain.Tag)(nil), domain.ErrTagNotFound)
	h := newHandler(&mocks.MockArticleRepository{}, tags)
	_, err := h.GetTag(context.Background(), &articlessvcv1.GetTagRequest{Identifier: 99})
	assert.Error(t, err)
}

func TestGetTag_Handler_OK(t *testing.T) {
	t.Parallel()
	tag := &articlesv1.Tag{Identifier: 5, Tagname: "golang"}
	tags := &mocks.MockTagRepository{}
	tags.On("GetByID", mock.Anything, uint64(5)).Return(tag, nil)
	h := newHandler(&mocks.MockArticleRepository{}, tags)
	out, err := h.GetTag(context.Background(), &articlessvcv1.GetTagRequest{Identifier: 5})
	assert.NoError(t, err)
	assert.Equal(t, "golang", out.GetTagname())
}
