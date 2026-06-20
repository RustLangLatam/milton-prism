package repositories

import (
	"context"
	"fmt"

	"milton_prism/core/services/migration/ports"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var _ ports.GenerationResultReader = (*MongoGenerationResultReader)(nil)

// MongoGenerationResultReader reads per-service generation records produced by
// the autonomous generation worker.
type MongoGenerationResultReader struct {
	coll *mongo.Collection
}

// NewMongoGenerationResultReader returns a reader bound to the generation_results collection.
func NewMongoGenerationResultReader(db *mongo.Database) *MongoGenerationResultReader {
	return &MongoGenerationResultReader{coll: db.Collection("generation_results")}
}

type generationResultDoc struct {
	ServiceName        string  `bson:"service_name"`
	Status             string  `bson:"status"`
	GatesPassed        bool    `bson:"gates_passed"`
	FailureReason      string  `bson:"failure_reason"`
	TotalCostUSD       float64 `bson:"total_cost_usd"`
	GeneratedFileCount int     `bson:"generated_file_count"`
	AgentRawResult     string  `bson:"agent_raw_result,omitempty"`
}

func (r *MongoGenerationResultReader) ReadResults(ctx context.Context, migrationID uint64) ([]*migrationv1.ServiceGenerationRecord, error) {
	cur, err := r.coll.Find(ctx, bson.M{"migration_id": migrationID})
	if err != nil {
		return nil, fmt.Errorf("generation-result-reader: find migration_id=%d: %w", migrationID, err)
	}
	defer cur.Close(ctx)

	var docs []generationResultDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("generation-result-reader: decode migration_id=%d: %w", migrationID, err)
	}

	records := make([]*migrationv1.ServiceGenerationRecord, len(docs))
	for i, d := range docs {
		records[i] = &migrationv1.ServiceGenerationRecord{
			ServiceName:        d.ServiceName,
			Status:             d.Status,
			GatesPassed:        d.GatesPassed,
			FailureReason:      d.FailureReason,
			TotalCostUsd:       d.TotalCostUSD,
			GeneratedFileCount: int32(d.GeneratedFileCount),
			AgentRawResult:     d.AgentRawResult,
		}
	}
	return records, nil
}
