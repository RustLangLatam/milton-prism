package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"milton_prism/core/services/articles/domain"
	"milton_prism/core/services/articles/ports"
	applog "milton_prism/pkg/log"
	articlesv1 "milton_prism/pkg/pb/gen/milton_prism/types/articles/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
	"milton_prism/pkg/pb/impl"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const articlesCollName = "articles"

var _ ports.ArticleRepository = (*MongoArticleRepository)(nil)

type mongoArticleDoc struct {
	ID               primitive.ObjectID  `bson:"_id,omitempty"`
	Identifier       uint64              `bson:"identifier"`
	State            int32               `bson:"state"`
	Slug             string              `bson:"slug"`
	Title            string              `bson:"title"`
	Description      string              `bson:"description"`
	Body             string              `bson:"body"`
	AuthorIdentifier uint64              `bson:"author_identifier"`
	CreateTime       primitive.DateTime  `bson:"create_time"`
	UpdateTime       *primitive.DateTime `bson:"update_time,omitempty"`
	DeleteTime       *primitive.DateTime `bson:"delete_time,omitempty"`
	PurgeTime        *primitive.DateTime `bson:"purge_time,omitempty"`
}

// MongoArticleRepository persists Article records in MongoDB.
type MongoArticleRepository struct {
	db   *mongo.Database
	coll *mongo.Collection
}

func NewMongoArticleRepository(db *mongo.Database) *MongoArticleRepository {
	r := &MongoArticleRepository{db: db, coll: db.Collection(articlesCollName)}
	if _, err := r.coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{Keys: bson.D{{Key: "identifier", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "author_identifier", Value: 1}}},
		{Keys: bson.D{{Key: "slug", Value: 1}, {Key: "author_identifier", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
	}); err != nil {
		applog.Warningf("mongo: create indexes on %s: error=%v", articlesCollName, err)
	}
	return r
}

func (r *MongoArticleRepository) Create(ctx context.Context, a *domain.Article) (*domain.Article, error) {
	id, err := generateIdentifier(ctx, r.db, articlesCollName)
	if err != nil {
		return nil, fmt.Errorf("article: identifier: %w", err)
	}
	doc := articleToDoc(a)
	doc.Identifier = id
	doc.State = int32(articlesv1.ArticleState_ARTICLE_STATE_ACTIVE)
	doc.CreateTime = primitive.NewDateTimeFromTime(time.Now().UTC())
	if _, err := r.coll.InsertOne(ctx, doc); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, domain.ErrArticleAlreadyExists
		}
		return nil, fmt.Errorf("article: insert failed: %w", err)
	}
	return articleDocToDomain(doc), nil
}

func (r *MongoArticleRepository) GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.Article, error) {
	q := bson.M{"identifier": identifier}
	if !includeDeleted {
		q["delete_time"] = nil
	}
	var doc mongoArticleDoc
	if err := r.coll.FindOne(ctx, q).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, domain.ErrArticleNotFound
		}
		return nil, fmt.Errorf("article: find one failed: %w", err)
	}
	return articleDocToDomain(&doc), nil
}

func (r *MongoArticleRepository) List(ctx context.Context, filter *domain.ArticlesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Article, *paginationv1.Pagination, error) {
	q := bson.M{"delete_time": nil}
	if filter != nil {
		if filter.AuthorIdentifier != nil && filter.GetAuthorIdentifier() != 0 {
			q["author_identifier"] = filter.GetAuthorIdentifier()
		}
		if filter.Slug != nil && filter.GetSlug() != "" {
			q["slug"] = filter.GetSlug()
		}
		if filter.State != nil && filter.GetState() != articlesv1.ArticleState_ARTICLE_STATE_UNSPECIFIED {
			q["state"] = int32(filter.GetState())
		}
	}
	skip := int64((params.GetPageNumber() - 1) * params.GetPageSize())
	opts := options.Find().SetSkip(skip).SetLimit(int64(params.GetPageSize()))
	cur, err := r.coll.Find(ctx, q, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("article: list failed: %w", err)
	}
	defer cur.Close(ctx)
	var docs []mongoArticleDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("article: decode failed: %w", err)
	}
	total, _ := r.coll.CountDocuments(ctx, q)
	out := make([]*domain.Article, 0, len(docs))
	for i := range docs {
		out = append(out, articleDocToDomain(&docs[i]))
	}
	return out, impl.NewPagination(params.GetPageNumber(), params.GetPageSize(), uint64(total)), nil
}

func (r *MongoArticleRepository) Update(ctx context.Context, a *domain.Article) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	set := bson.M{
		"slug":        a.GetSlug(),
		"title":       a.GetTitle(),
		"description": a.GetDescription(),
		"body":        a.GetBody(),
		"state":       int32(a.GetState()),
		"update_time": now,
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"identifier": a.GetIdentifier(), "delete_time": nil}, bson.M{"$set": set})
	if err != nil {
		return fmt.Errorf("article: update failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrArticleNotFound
	}
	return nil
}

func (r *MongoArticleRepository) SoftDelete(ctx context.Context, identifier uint64) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{
			"delete_time": now,
			"update_time": now,
			"state":       int32(articlesv1.ArticleState_ARTICLE_STATE_DELETED),
		}},
	)
	if err != nil {
		return fmt.Errorf("article: soft delete failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrArticleNotFound
	}
	return nil
}

func articleToDoc(a *domain.Article) *mongoArticleDoc {
	return &mongoArticleDoc{
		State:            int32(a.GetState()),
		Slug:             a.GetSlug(),
		Title:            a.GetTitle(),
		Description:      a.GetDescription(),
		Body:             a.GetBody(),
		AuthorIdentifier: a.GetAuthorIdentifier(),
	}
}

func articleDocToDomain(d *mongoArticleDoc) *domain.Article {
	if d == nil {
		return nil
	}
	out := &domain.Article{
		Identifier:       d.Identifier,
		State:            articlesv1.ArticleState(d.State),
		Slug:             d.Slug,
		Title:            d.Title,
		Description:      d.Description,
		Body:             d.Body,
		AuthorIdentifier: d.AuthorIdentifier,
	}
	if d.CreateTime != 0 {
		out.CreateTime = timestamppb.New(d.CreateTime.Time())
	}
	if d.UpdateTime != nil {
		out.UpdateTime = timestamppb.New(d.UpdateTime.Time())
	}
	if d.DeleteTime != nil {
		out.DeleteTime = timestamppb.New(d.DeleteTime.Time())
	}
	if d.PurgeTime != nil {
		out.PurgeTime = timestamppb.New(d.PurgeTime.Time())
	}
	return out
}
