// Package repositories contains MongoDB-backed implementations of the driven
// ports defined for the identity service.
package repositories

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	systemCountersCollName = "system_counters"
	initialSequenceValue   = 10001
	maxIdentifierRetries   = 3
)

func generateIdentifier(ctx context.Context, db *mongo.Database, collName string) (uint64, error) {
	countersColl := db.Collection(systemCountersCollName)
	seqName := fmt.Sprintf("%s_id_seq", collName)
	incrementOpts := options.FindOneAndUpdate().
		SetReturnDocument(options.After).
		SetProjection(bson.M{"seq": 1})

	var result struct {
		Seq uint64 `bson:"seq"`
	}
	err := countersColl.FindOneAndUpdate(ctx, bson.M{"_id": seqName}, bson.M{"$inc": bson.M{"seq": 1}}, incrementOpts).Decode(&result)
	if err == nil {
		return result.Seq, nil
	}
	for attempt := 1; attempt <= maxIdentifierRetries; attempt++ {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		_, insertErr := countersColl.InsertOne(ctx, bson.M{"_id": seqName, "seq": initialSequenceValue})
		switch {
		case insertErr == nil:
			return initialSequenceValue, nil
		case mongo.IsDuplicateKeyError(insertErr):
			incErr := countersColl.FindOneAndUpdate(ctx, bson.M{"_id": seqName}, bson.M{"$inc": bson.M{"seq": 1}}, incrementOpts).Decode(&result)
			if incErr == nil {
				return result.Seq, nil
			}
			time.Sleep(time.Duration(attempt*attempt) * 10 * time.Millisecond)
		default:
			return 0, fmt.Errorf("counters: failed to initialize sequence %q: %w", seqName, insertErr)
		}
	}
	return 0, fmt.Errorf("counters: max retries (%d) exceeded for %q", maxIdentifierRetries, seqName)
}
