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
	FailureClass       string  `bson:"failure_class,omitempty"`
	TotalCostUSD       float64 `bson:"total_cost_usd"`
	GeneratedFileCount int     `bson:"generated_file_count"`
	AgentRawResult     string  `bson:"agent_raw_result,omitempty"`

	InputTokens              int64 `bson:"input_tokens"`
	CacheCreationInputTokens int64 `bson:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `bson:"cache_read_input_tokens"`
	OutputTokens             int64 `bson:"output_tokens"`

	Model string `bson:"model,omitempty"`
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
			FailureClass:       failureClassFromString(d.FailureClass),
			TotalCostUsd:       d.TotalCostUSD,
			GeneratedFileCount: int32(d.GeneratedFileCount),
			AgentRawResult:     d.AgentRawResult,

			InputTokens:              d.InputTokens,
			CacheCreationInputTokens: d.CacheCreationInputTokens,
			CacheReadInputTokens:     d.CacheReadInputTokens,
			OutputTokens:             d.OutputTokens,
		}
	}
	return records, nil
}

// failureClassFromString maps the persisted failure_class token (written by the
// generation worker) to the proto FailureClass enum surfaced read-only on each
// ServiceGenerationRecord. An empty/unknown token maps to UNSPECIFIED.
func failureClassFromString(s string) migrationv1.FailureClass {
	switch s {
	case "transient":
		return migrationv1.FailureClass_FAILURE_CLASS_TRANSIENT
	case "design":
		return migrationv1.FailureClass_FAILURE_CLASS_DESIGN
	default:
		return migrationv1.FailureClass_FAILURE_CLASS_UNSPECIFIED
	}
}

// ReadServiceUsages returns the per-service token/cost footprint of every
// generation record for a migration. TokensIn sums all input tiers (fresh +
// cache-creation + cache-read); TokensOut is the output tokens; RealCostUSD is
// the agent-reported total_cost_usd (>0 only in apikey mode); Model is the model
// id reported for the run, used to estimate cost by token when RealCostUSD is 0.
// Status is carried so the biller only charges done services.
func (r *MongoGenerationResultReader) ReadServiceUsages(ctx context.Context, migrationID uint64) ([]ports.ServiceGenerationUsage, error) {
	cur, err := r.coll.Find(ctx, bson.M{"migration_id": migrationID})
	if err != nil {
		return nil, fmt.Errorf("generation-result-reader: usage find migration_id=%d: %w", migrationID, err)
	}
	defer cur.Close(ctx)

	var docs []generationResultDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("generation-result-reader: usage decode migration_id=%d: %w", migrationID, err)
	}

	out := make([]ports.ServiceGenerationUsage, len(docs))
	for i, d := range docs {
		out[i] = ports.ServiceGenerationUsage{
			ServiceName: d.ServiceName,
			Status:      d.Status,
			TokensIn:    d.InputTokens + d.CacheCreationInputTokens + d.CacheReadInputTokens,
			TokensOut:   d.OutputTokens,
			RealCostUSD: d.TotalCostUSD,
			Model:       d.Model,
		}
	}
	return out, nil
}
