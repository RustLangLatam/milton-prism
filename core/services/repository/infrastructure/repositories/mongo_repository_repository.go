package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"milton_prism/core/services/repository/domain"
	"milton_prism/core/services/repository/ports"
	applog "milton_prism/pkg/log"
	repositoryv1 "milton_prism/pkg/pb/gen/milton_prism/types/repository/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
	"milton_prism/pkg/pb/impl"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const repositoriesCollName = "repositories"

var _ ports.RepositoryRepository = (*MongoRepositoryRepository)(nil)

type mongoRepositoryDoc struct {
	ID               primitive.ObjectID  `bson:"_id,omitempty"`
	Identifier       uint64              `bson:"identifier"`
	OwnerUserID      uint64              `bson:"owner_user_id"`
	Provider         int32               `bson:"provider"`
	RemoteURL        string              `bson:"remote_url"`
	DefaultBranch    string              `bson:"default_branch,omitempty"`
	State            int32               `bson:"state,omitempty"`
	ConnectionStatus int32               `bson:"connection_status,omitempty"`
	CredentialRef    string              `bson:"credential_ref,omitempty"`
	CreateTime       primitive.DateTime  `bson:"create_time"`
	UpdateTime       *primitive.DateTime `bson:"update_time,omitempty"`
	DeleteTime       *primitive.DateTime `bson:"delete_time,omitempty"`
	PurgeTime        *primitive.DateTime `bson:"purge_time,omitempty"`
}

// MongoRepositoryRepository persists Repository records in MongoDB.
type MongoRepositoryRepository struct {
	db   *mongo.Database
	coll *mongo.Collection
}

func NewMongoRepositoryRepository(db *mongo.Database) *MongoRepositoryRepository {
	r := &MongoRepositoryRepository{db: db, coll: db.Collection(repositoriesCollName)}
	if _, err := r.coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{Keys: bson.D{{Key: "identifier", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "owner_user_id", Value: 1}}},
		{Keys: bson.D{{Key: "owner_user_id", Value: 1}, {Key: "remote_url", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
	}); err != nil {
		applog.Warningf("mongo: create indexes on %s: error=%v", repositoriesCollName, err)
	}
	return r
}

func (r *MongoRepositoryRepository) Create(ctx context.Context, repo *domain.Repository) (*domain.Repository, error) {
	id, err := generateIdentifier(ctx, r.db, repositoriesCollName)
	if err != nil {
		return nil, fmt.Errorf("repository: identifier: %w", err)
	}
	doc := repoToDoc(repo)
	doc.Identifier = id
	doc.CreateTime = primitive.NewDateTimeFromTime(time.Now().UTC())
	if _, err := r.coll.InsertOne(ctx, doc); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, domain.ErrRepositoryAlreadyExists
		}
		return nil, fmt.Errorf("repository: insert failed: %w", err)
	}
	return repoDocToDomain(doc), nil
}

func (r *MongoRepositoryRepository) GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.Repository, error) {
	q := bson.M{"identifier": identifier}
	if !includeDeleted {
		q["delete_time"] = nil
	}
	var doc mongoRepositoryDoc
	if err := r.coll.FindOne(ctx, q).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, domain.ErrRepositoryNotFound
		}
		return nil, fmt.Errorf("repository: find one failed: %w", err)
	}
	return repoDocToDomain(&doc), nil
}

func (r *MongoRepositoryRepository) List(ctx context.Context, filter *domain.RepositoriesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Repository, *paginationv1.Pagination, error) {
	q := bson.M{"delete_time": nil}
	if filter != nil {
		if filter.OwnerUserId != nil && filter.GetOwnerUserId() != 0 {
			q["owner_user_id"] = filter.GetOwnerUserId()
		}
		if filter.State != nil && filter.GetState() != repositoryv1.RepositoryState_REPOSITORY_STATE_UNSPECIFIED {
			q["state"] = int32(filter.GetState())
		}
		if filter.Provider != nil && filter.GetProvider() != repositoryv1.GitProvider_GIT_PROVIDER_UNSPECIFIED {
			q["provider"] = int32(filter.GetProvider())
		}
	}
	skip := int64((params.GetPageNumber() - 1) * params.GetPageSize())
	opts := options.Find().SetSkip(skip).SetLimit(int64(params.GetPageSize()))
	cur, err := r.coll.Find(ctx, q, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("repository: list failed: %w", err)
	}
	defer cur.Close(ctx)
	var docs []mongoRepositoryDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("repository: decode failed: %w", err)
	}
	total, _ := r.coll.CountDocuments(ctx, q)
	out := make([]*domain.Repository, 0, len(docs))
	for i := range docs {
		out = append(out, repoDocToDomain(&docs[i]))
	}
	return out, impl.NewPagination(params.GetPageNumber(), params.GetPageSize(), uint64(total)), nil
}

func (r *MongoRepositoryRepository) Update(ctx context.Context, repo *domain.Repository) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	set := bson.M{
		"remote_url":        repo.GetRemoteUrl(),
		"default_branch":    repo.GetDefaultBranch(),
		"state":             int32(repo.GetState()),
		"connection_status": int32(repo.GetConnectionStatus()),
		"update_time":       now,
	}
	if repo.GetCredentialRef() != "" {
		set["credential_ref"] = repo.GetCredentialRef()
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"identifier": repo.GetIdentifier(), "delete_time": nil}, bson.M{"$set": set})
	if err != nil {
		return fmt.Errorf("repository: update failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrRepositoryNotFound
	}
	return nil
}

func (r *MongoRepositoryRepository) SoftDelete(ctx context.Context, identifier uint64) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{
			"delete_time": now,
			"update_time": now,
			"state":       int32(repositoryv1.RepositoryState_REPOSITORY_STATE_DISCONNECTED),
		}},
	)
	if err != nil {
		return fmt.Errorf("repository: soft delete failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrRepositoryNotFound
	}
	return nil
}

func (r *MongoRepositoryRepository) UpdateConnectionStatus(ctx context.Context, identifier uint64, status domain.ConnectionStatus) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	_, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{"connection_status": int32(status), "update_time": now}},
	)
	if err != nil {
		return fmt.Errorf("repository: update connection_status failed: %w", err)
	}
	return nil
}

func repoToDoc(r *domain.Repository) *mongoRepositoryDoc {
	return &mongoRepositoryDoc{
		OwnerUserID:      r.GetOwnerUserId(),
		Provider:         int32(r.GetProvider()),
		RemoteURL:        r.GetRemoteUrl(),
		DefaultBranch:    r.GetDefaultBranch(),
		State:            int32(r.GetState()),
		ConnectionStatus: int32(r.GetConnectionStatus()),
		CredentialRef:    r.GetCredentialRef(),
	}
}

func repoDocToDomain(d *mongoRepositoryDoc) *domain.Repository {
	if d == nil {
		return nil
	}
	out := &domain.Repository{
		Identifier:       d.Identifier,
		OwnerUserId:      d.OwnerUserID,
		Provider:         repositoryv1.GitProvider(d.Provider),
		RemoteUrl:        d.RemoteURL,
		DefaultBranch:    d.DefaultBranch,
		State:            repositoryv1.RepositoryState(d.State),
		ConnectionStatus: repositoryv1.ConnectionStatus(d.ConnectionStatus),
		CredentialRef:    d.CredentialRef, // populated for internal use; handler strips it before any API response
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
