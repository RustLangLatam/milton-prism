package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"milton_prism/core/services/identity/domain"
	"milton_prism/core/services/identity/ports"
	applog "milton_prism/pkg/log"
	identityv1 "milton_prism/pkg/pb/gen/milton_prism/types/identity/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
	"milton_prism/pkg/pb/impl"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const usersCollName = "users"

var _ ports.UserRepository = (*MongoUserRepository)(nil)

type mongoUserDoc struct {
	ID           primitive.ObjectID  `bson:"_id,omitempty"`
	Identifier   uint64              `bson:"identifier"`
	Email        string              `bson:"email"`
	DisplayName  string              `bson:"display_name,omitempty"`
	SystemUser   bool                `bson:"system_user,omitempty"`
	State        int32               `bson:"state,omitempty"`
	PasswordHash *string             `bson:"password_hash,omitempty"`
	CreateTime   primitive.DateTime  `bson:"create_time,omitempty"`
	UpdateTime   *primitive.DateTime `bson:"update_time,omitempty"`
	DeleteTime   *primitive.DateTime `bson:"delete_time,omitempty"`
	PurgeTime    *primitive.DateTime `bson:"purge_time,omitempty"`
}

// MongoUserRepository persists user accounts in MongoDB.
type MongoUserRepository struct {
	db   *mongo.Database
	coll *mongo.Collection
}

func NewMongoUserRepository(db *mongo.Database) *MongoUserRepository {
	r := &MongoUserRepository{db: db, coll: db.Collection(usersCollName)}
	if _, err := r.coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{Keys: bson.D{{Key: "identifier", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
	}); err != nil {
		applog.Warningf("mongo: create indexes on %s: error=%v", usersCollName, err)
	}
	return r
}

func (r *MongoUserRepository) Create(ctx context.Context, u *domain.User, passwordHash string) (*domain.User, error) {
	id, err := generateIdentifier(ctx, r.db, usersCollName)
	if err != nil {
		return nil, fmt.Errorf("user: identifier: %w", err)
	}
	doc := userToDoc(u)
	doc.Identifier = id
	doc.PasswordHash = &passwordHash
	doc.CreateTime = primitive.NewDateTimeFromTime(time.Now().UTC())
	if _, err := r.coll.InsertOne(ctx, doc); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, domain.ErrEmailAlreadyExists
		}
		return nil, fmt.Errorf("user: insert failed: %w", err)
	}
	return userDocToDomain(doc), nil
}

func (r *MongoUserRepository) GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.User, error) {
	q := bson.M{"identifier": identifier}
	if !includeDeleted {
		q["delete_time"] = nil
	}
	var doc mongoUserDoc
	if err := r.coll.FindOne(ctx, q).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("user: find one failed: %w", err)
	}
	return userDocToDomain(&doc), nil
}

func (r *MongoUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	var doc mongoUserDoc
	if err := r.coll.FindOne(ctx, bson.M{"email": email, "delete_time": nil}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("user: find by email failed: %w", err)
	}
	return userDocToDomain(&doc), nil
}

func (r *MongoUserRepository) GetCredentialsByEmail(ctx context.Context, email string) (*domain.User, string, error) {
	var doc mongoUserDoc
	if err := r.coll.FindOne(ctx, bson.M{"email": email, "delete_time": nil}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, "", domain.ErrUserNotFound
		}
		return nil, "", fmt.Errorf("user: find credentials by email failed: %w", err)
	}
	hash := ""
	if doc.PasswordHash != nil {
		hash = *doc.PasswordHash
	}
	return userDocToDomain(&doc), hash, nil
}

func (r *MongoUserRepository) List(ctx context.Context, filter *domain.UsersFilter, params *queryparamsv1.PageQueryParams) ([]*domain.User, *paginationv1.Pagination, error) {
	q := bson.M{"delete_time": nil}
	if filter != nil {
		if filter.State != nil && filter.GetState() != identityv1.UserState_USER_STATE_UNSPECIFIED {
			q["state"] = int32(filter.GetState())
		}
		if filter.Email != nil && filter.GetEmail() != "" {
			q["email"] = filter.GetEmail()
		}
	}
	skip := int64((params.GetPageNumber() - 1) * params.GetPageSize())
	opts := options.Find().SetSkip(skip).SetLimit(int64(params.GetPageSize()))
	cur, err := r.coll.Find(ctx, q, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("user: list failed: %w", err)
	}
	defer cur.Close(ctx)
	var docs []mongoUserDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("user: decode failed: %w", err)
	}
	total, _ := r.coll.CountDocuments(ctx, q)
	out := make([]*domain.User, 0, len(docs))
	for i := range docs {
		out = append(out, userDocToDomain(&docs[i]))
	}
	return out, impl.NewPagination(params.GetPageNumber(), params.GetPageSize(), uint64(total)), nil
}

func (r *MongoUserRepository) Update(ctx context.Context, u *domain.User) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	set := bson.M{
		"email":        u.GetEmail(),
		"display_name": u.GetDisplayName(),
		"system_user":  u.GetSystemUser(),
		"state":        int32(u.GetState()),
		"update_time":  now,
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"identifier": u.GetIdentifier(), "delete_time": nil}, bson.M{"$set": set})
	if err != nil {
		return fmt.Errorf("user: update failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

func (r *MongoUserRepository) SoftDelete(ctx context.Context, identifier uint64) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{"delete_time": now, "update_time": now, "state": int32(identityv1.UserState_USER_STATE_DELETED)}},
	)
	if err != nil {
		return fmt.Errorf("user: soft delete failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrUserNotFound
	}
	return nil
}

func userToDoc(u *domain.User) *mongoUserDoc {
	return &mongoUserDoc{
		Identifier:  u.GetIdentifier(),
		Email:       u.GetEmail(),
		DisplayName: u.GetDisplayName(),
		SystemUser:  u.GetSystemUser(),
		State:       int32(u.GetState()),
	}
}

func userDocToDomain(d *mongoUserDoc) *domain.User {
	if d == nil {
		return nil
	}
	out := &domain.User{
		Identifier:  d.Identifier,
		Email:       d.Email,
		DisplayName: d.DisplayName,
		SystemUser:  d.SystemUser,
		State:       identityv1.UserState(d.State),
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
