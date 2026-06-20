package application_test

import (
	"context"
	"testing"

	"milton_prism/core/services/articles/application"
	"milton_prism/core/services/articles/domain"
	"milton_prism/core/services/articles/mocks"
	articlesv1 "milton_prism/pkg/pb/gen/milton_prism/types/articles/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func validArticle(id, authorID uint64) *domain.Article {
	return &articlesv1.Article{
		Identifier:       id,
		Slug:             "test-slug",
		Title:            "Test Title",
		Description:      "desc",
		Body:             "body",
		AuthorIdentifier: authorID,
		State:            articlesv1.ArticleState_ARTICLE_STATE_ACTIVE,
	}
}

func validTag(id uint64) *domain.Tag {
	return &articlesv1.Tag{
		Identifier: id,
		Tagname:    "golang",
		State:      articlesv1.TagState_TAG_STATE_ACTIVE,
	}
}

func newSvc(articles *mocks.MockArticleRepository, tags *mocks.MockTagRepository, profile *mocks.MockProfileClient) *application.Service {
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	return application.NewService(articles, tags, tx, profile)
}

// ─── CreateArticle ───────────────────────────────────────────────────────────

func TestCreateArticle_MissingPayload_Nil(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{}, nil)
	_, err := svc.CreateArticle(context.Background(), nil)
	assert.ErrorIs(t, err, domain.ErrMissingPayload)
}

func TestCreateArticle_MissingPayload_EmptySlug(t *testing.T) {
	t.Parallel()
	a := validArticle(0, 7)
	a.Slug = ""
	svc := newSvc(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{}, nil)
	_, err := svc.CreateArticle(context.Background(), a)
	assert.ErrorIs(t, err, domain.ErrMissingPayload)
}

func TestCreateArticle_MissingAuthorIdentifier(t *testing.T) {
	t.Parallel()
	a := validArticle(0, 0)
	svc := newSvc(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{}, nil)
	_, err := svc.CreateArticle(context.Background(), a)
	assert.ErrorIs(t, err, domain.ErrMissingAuthorIdentifier)
}

func TestCreateArticle_AuthorNotFound(t *testing.T) {
	t.Parallel()
	profileClient := &mocks.MockProfileClient{}
	profileClient.On("ValidateProfileExists", mock.Anything, uint64(7)).Return(domain.ErrAuthorNotFound)
	svc := newSvc(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{}, profileClient)
	_, err := svc.CreateArticle(context.Background(), validArticle(0, 7))
	assert.ErrorIs(t, err, domain.ErrAuthorNotFound)
}

func TestCreateArticle_AlreadyExists(t *testing.T) {
	t.Parallel()
	profileClient := &mocks.MockProfileClient{}
	profileClient.On("ValidateProfileExists", mock.Anything, uint64(7)).Return(nil)
	repo := &mocks.MockArticleRepository{}
	repo.On("Create", mock.Anything, mock.Anything).Return((*domain.Article)(nil), domain.ErrArticleAlreadyExists)
	svc := newSvc(repo, &mocks.MockTagRepository{}, profileClient)
	_, err := svc.CreateArticle(context.Background(), validArticle(0, 7))
	assert.ErrorIs(t, err, domain.ErrArticleAlreadyExists)
}

func TestCreateArticle_OK(t *testing.T) {
	t.Parallel()
	profileClient := &mocks.MockProfileClient{}
	profileClient.On("ValidateProfileExists", mock.Anything, uint64(7)).Return(nil)
	created := validArticle(42, 7)
	repo := &mocks.MockArticleRepository{}
	repo.On("Create", mock.Anything, mock.Anything).Return(created, nil)
	svc := newSvc(repo, &mocks.MockTagRepository{}, profileClient)
	out, err := svc.CreateArticle(context.Background(), validArticle(0, 7))
	assert.NoError(t, err)
	assert.Equal(t, created, out)
}

// ─── GetArticle ──────────────────────────────────────────────────────────────

func TestGetArticle_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{}, nil)
	_, err := svc.GetArticle(context.Background(), 0)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestGetArticle_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockArticleRepository{}
	repo.On("GetByID", mock.Anything, uint64(99), false).Return((*domain.Article)(nil), domain.ErrArticleNotFound)
	svc := newSvc(repo, &mocks.MockTagRepository{}, nil)
	_, err := svc.GetArticle(context.Background(), 99)
	assert.ErrorIs(t, err, domain.ErrArticleNotFound)
}

func TestGetArticle_OK(t *testing.T) {
	t.Parallel()
	a := validArticle(42, 7)
	repo := &mocks.MockArticleRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(a, nil)
	svc := newSvc(repo, &mocks.MockTagRepository{}, nil)
	out, err := svc.GetArticle(context.Background(), 42)
	assert.NoError(t, err)
	assert.Equal(t, a, out)
}

// ─── DeleteArticle ───────────────────────────────────────────────────────────

func TestDeleteArticle_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{}, nil)
	err := svc.DeleteArticle(context.Background(), 0)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestDeleteArticle_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockArticleRepository{}
	repo.On("SoftDelete", mock.Anything, uint64(99)).Return(domain.ErrArticleNotFound)
	svc := newSvc(repo, &mocks.MockTagRepository{}, nil)
	err := svc.DeleteArticle(context.Background(), 99)
	assert.ErrorIs(t, err, domain.ErrArticleNotFound)
}

func TestDeleteArticle_OK(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockArticleRepository{}
	repo.On("SoftDelete", mock.Anything, uint64(42)).Return(nil)
	svc := newSvc(repo, &mocks.MockTagRepository{}, nil)
	assert.NoError(t, svc.DeleteArticle(context.Background(), 42))
}

// ─── UpdateArticle ───────────────────────────────────────────────────────────

func TestUpdateArticle_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{}, nil)
	_, err := svc.UpdateArticle(context.Background(), validArticle(0, 7), nil)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestUpdateArticle_NotFound(t *testing.T) {
	t.Parallel()
	repo := &mocks.MockArticleRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return((*domain.Article)(nil), domain.ErrArticleNotFound)
	svc := newSvc(repo, &mocks.MockTagRepository{}, nil)
	_, err := svc.UpdateArticle(context.Background(), validArticle(42, 7), nil)
	assert.ErrorIs(t, err, domain.ErrArticleNotFound)
}

func TestUpdateArticle_OK(t *testing.T) {
	t.Parallel()
	existing := validArticle(42, 7)
	repo := &mocks.MockArticleRepository{}
	repo.On("GetByID", mock.Anything, uint64(42), false).Return(existing, nil)
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)
	svc := newSvc(repo, &mocks.MockTagRepository{}, nil)
	out, err := svc.UpdateArticle(context.Background(), validArticle(42, 7), nil)
	assert.NoError(t, err)
	assert.Equal(t, existing, out)
}

// ─── GetTag ──────────────────────────────────────────────────────────────────

func TestGetTag_MissingIdentifier(t *testing.T) {
	t.Parallel()
	svc := newSvc(&mocks.MockArticleRepository{}, &mocks.MockTagRepository{}, nil)
	_, err := svc.GetTag(context.Background(), 0)
	assert.ErrorIs(t, err, domain.ErrMissingIdentifier)
}

func TestGetTag_NotFound(t *testing.T) {
	t.Parallel()
	tags := &mocks.MockTagRepository{}
	tags.On("GetByID", mock.Anything, uint64(99)).Return((*domain.Tag)(nil), domain.ErrTagNotFound)
	svc := newSvc(&mocks.MockArticleRepository{}, tags, nil)
	_, err := svc.GetTag(context.Background(), 99)
	assert.ErrorIs(t, err, domain.ErrTagNotFound)
}

func TestGetTag_OK(t *testing.T) {
	t.Parallel()
	tag := validTag(5)
	tags := &mocks.MockTagRepository{}
	tags.On("GetByID", mock.Anything, uint64(5)).Return(tag, nil)
	svc := newSvc(&mocks.MockArticleRepository{}, tags, nil)
	out, err := svc.GetTag(context.Background(), 5)
	assert.NoError(t, err)
	assert.Equal(t, tag, out)
}
