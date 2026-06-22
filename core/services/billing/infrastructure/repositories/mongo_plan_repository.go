package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"milton_prism/core/services/billing/ports"
	applog "milton_prism/pkg/log"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const userPlansCollName = "user_plans"

var _ ports.PlanRepository = (*MongoPlanRepository)(nil)

type mongoUserPlanDoc struct {
	UserID     uint64             `bson:"user_id"`
	PlanCode   string             `bson:"plan_code"`
	UpdateTime primitive.DateTime `bson:"update_time"`
}

// MongoPlanRepository persists the user→plan association. The plan catalog
// itself is code-defined; only the association is stored here.
type MongoPlanRepository struct {
	coll *mongo.Collection
}

func NewMongoPlanRepository(db *mongo.Database) *MongoPlanRepository {
	r := &MongoPlanRepository{coll: db.Collection(userPlansCollName)}
	if _, err := r.coll.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.D{{Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		applog.Warningf("mongo: create indexes on %s: error=%v", userPlansCollName, err)
	}
	return r
}

func (r *MongoPlanRepository) GetUserPlanCode(ctx context.Context, userID uint64) (string, bool, error) {
	var doc mongoUserPlanDoc
	if err := r.coll.FindOne(ctx, bson.M{"user_id": userID}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("plan: find one failed: %w", err)
	}
	return doc.PlanCode, true, nil
}

func (r *MongoPlanRepository) SetUserPlanCode(ctx context.Context, userID uint64, code string) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	_, err := r.coll.UpdateOne(
		ctx,
		bson.M{"user_id": userID},
		bson.M{"$set": bson.M{"plan_code": code, "update_time": now}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("plan: upsert failed: %w", err)
	}
	return nil
}
