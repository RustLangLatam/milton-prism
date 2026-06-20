package repositories

import (
	"context"
	"errors"
	"fmt"

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

const tagsCollName = "tags"

var _ ports.TagRepository = (*MongoTagRepository)(nil)

type mongoTagDoc struct {
	ID         primitive.ObjectID  `bson:"_id,omitempty"`
	Identifier uint64              `bson:"identifier"`
	State      int32               `bson:"state"`
	Tagname    string              `bson:"tagname"`
	DeleteTime *primitive.DateTime `bson:"delete_time,omitempty"`
	PurgeTime  *primitive.DateTime `bson:"purge_time,omitempty"`
}

// MongoTagRepository reads Tag records from MongoDB.
type MongoTagRepository struct {
	db   *mongo.Database
	coll *mongo.Collection
}

func NewMongoTagRepository(db *mongo.Database) *MongoTagRepository {
	r := &MongoTagRepository{db: db, coll: db.Collection(tagsCollName)}
	if _, err := r.coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{Keys: bson.D{{Key: "identifier", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "tagname", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
	}); err != nil {
		applog.Warningf("mongo: create indexes on %s: error=%v", tagsCollName, err)
	}
	return r
}

func (r *MongoTagRepository) GetByID(ctx context.Context, identifier uint64) (*domain.Tag, error) {
	var doc mongoTagDoc
	if err := r.coll.FindOne(ctx, bson.M{"identifier": identifier, "delete_time": nil}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, domain.ErrTagNotFound
		}
		return nil, fmt.Errorf("tag: find one failed: %w", err)
	}
	return tagDocToDomain(&doc), nil
}

func (r *MongoTagRepository) List(ctx context.Context, params *queryparamsv1.PageQueryParams) ([]*domain.Tag, *paginationv1.Pagination, error) {
	q := bson.M{"delete_time": nil}
	skip := int64((params.GetPageNumber() - 1) * params.GetPageSize())
	opts := options.Find().SetSkip(skip).SetLimit(int64(params.GetPageSize()))
	cur, err := r.coll.Find(ctx, q, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("tag: list failed: %w", err)
	}
	defer cur.Close(ctx)
	var docs []mongoTagDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("tag: decode failed: %w", err)
	}
	total, _ := r.coll.CountDocuments(ctx, q)
	out := make([]*domain.Tag, 0, len(docs))
	for i := range docs {
		out = append(out, tagDocToDomain(&docs[i]))
	}
	return out, impl.NewPagination(params.GetPageNumber(), params.GetPageSize(), uint64(total)), nil
}

func tagDocToDomain(d *mongoTagDoc) *domain.Tag {
	if d == nil {
		return nil
	}
	out := &domain.Tag{
		Identifier: d.Identifier,
		State:      articlesv1.TagState(d.State),
		Tagname:    d.Tagname,
	}
	if d.DeleteTime != nil {
		out.DeleteTime = timestamppb.New(d.DeleteTime.Time())
	}
	if d.PurgeTime != nil {
		out.PurgeTime = timestamppb.New(d.PurgeTime.Time())
	}
	return out
}
