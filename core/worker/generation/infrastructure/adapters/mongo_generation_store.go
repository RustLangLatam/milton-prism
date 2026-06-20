// Package adapters contains the MongoDB-backed driven adapters for the generation worker.
package adapters

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	workerdomain "milton_prism/core/worker/generation/domain"
	"milton_prism/core/worker/generation/ports"
	applog "milton_prism/pkg/log"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	generationResultsColl   = "generation_results"
	generationArtifactsColl = "generation_file_artifacts"
)

var _ ports.GenerationStore = (*MongoGenerationStore)(nil)

// MongoGenerationStore persists per-service generation records and file
// artifacts in MongoDB with upsert semantics.
//
// Records use (migration_id, service_name) as the natural key.
// Artifacts use (migration_id, service_name, path) as the natural key so each
// file can be retrieved individually and re-runs overwrite without duplicating.
type MongoGenerationStore struct {
	coll         *mongo.Collection
	artifactColl *mongo.Collection
}

// NewMongoGenerationStore returns a store and ensures both compound unique
// indexes exist (created idempotently on startup).
func NewMongoGenerationStore(db *mongo.Database) *MongoGenerationStore {
	s := &MongoGenerationStore{
		coll:         db.Collection(generationResultsColl),
		artifactColl: db.Collection(generationArtifactsColl),
	}
	bg := context.Background()
	_, _ = s.coll.Indexes().CreateOne(bg, mongo.IndexModel{
		Keys:    bson.D{{Key: "migration_id", Value: 1}, {Key: "service_name", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	_, _ = s.artifactColl.Indexes().CreateOne(bg, mongo.IndexModel{
		Keys:    bson.D{{Key: "migration_id", Value: 1}, {Key: "service_name", Value: 1}, {Key: "path", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	return s
}

type generationResultDoc struct {
	MigrationID              uint64             `bson:"migration_id"`
	ServiceName              string             `bson:"service_name"`
	Status                   string             `bson:"status"`
	GatesPassed              bool               `bson:"gates_passed"`
	FailureReason            string             `bson:"failure_reason,omitempty"`
	TotalCostUSD             float64            `bson:"total_cost_usd"`
	GeneratedFileCount       int                `bson:"generated_file_count"`
	InputTokens              int64              `bson:"input_tokens"`
	CacheCreationInputTokens int64              `bson:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64              `bson:"cache_read_input_tokens"`
	OutputTokens             int64              `bson:"output_tokens"`
	AgentRawResult           string             `bson:"agent_raw_result,omitempty"`
	UpdatedAt                primitive.DateTime `bson:"updated_at"`
}

// generationFileArtifactDoc is one row in generation_file_artifacts.
// Content is stored as a UTF-8 string; Go source files and go.sum are always
// valid UTF-8/ASCII so no base64 wrapper is needed at the BSON layer.
type generationFileArtifactDoc struct {
	MigrationID uint64             `bson:"migration_id"`
	ServiceName string             `bson:"service_name"`
	Path        string             `bson:"path"`
	Content     string             `bson:"content"`
	UpdatedAt   primitive.DateTime `bson:"updated_at"`
}

func (s *MongoGenerationStore) UpsertRecord(ctx context.Context, rec workerdomain.ServiceGenerationRecord) error {
	doc := generationResultDoc{
		MigrationID:              rec.MigrationID,
		ServiceName:              rec.ServiceName,
		Status:                   string(rec.Status),
		GatesPassed:              rec.GatesPassed,
		FailureReason:            rec.FailureReason,
		TotalCostUSD:             rec.TotalCostUSD,
		GeneratedFileCount:       rec.GeneratedFileCount,
		InputTokens:              rec.InputTokens,
		CacheCreationInputTokens: rec.CacheCreationInputTokens,
		CacheReadInputTokens:     rec.CacheReadInputTokens,
		OutputTokens:             rec.OutputTokens,
		AgentRawResult:           rec.AgentRawResult,
		UpdatedAt:                primitive.NewDateTimeFromTime(time.Now().UTC()),
	}
	filter := bson.M{"migration_id": rec.MigrationID, "service_name": rec.ServiceName}
	_, err := s.coll.UpdateOne(ctx, filter, bson.M{"$set": doc}, options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("generation-store: upsert migration_id=%d service=%s: %w", rec.MigrationID, rec.ServiceName, err)
	}
	return nil
}

func (s *MongoGenerationStore) ListRecords(ctx context.Context, migrationID uint64) ([]workerdomain.ServiceGenerationRecord, error) {
	cur, err := s.coll.Find(ctx, bson.M{"migration_id": migrationID})
	if err != nil {
		return nil, fmt.Errorf("generation-store: find migration_id=%d: %w", migrationID, err)
	}
	defer cur.Close(ctx)
	var docs []generationResultDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("generation-store: decode migration_id=%d: %w", migrationID, err)
	}
	out := make([]workerdomain.ServiceGenerationRecord, len(docs))
	for i, d := range docs {
		out[i] = workerdomain.ServiceGenerationRecord{
			MigrationID:              d.MigrationID,
			ServiceName:              d.ServiceName,
			Status:                   workerdomain.ServiceStatus(d.Status),
			GatesPassed:              d.GatesPassed,
			FailureReason:            d.FailureReason,
			TotalCostUSD:             d.TotalCostUSD,
			GeneratedFileCount:       d.GeneratedFileCount,
			InputTokens:              d.InputTokens,
			CacheCreationInputTokens: d.CacheCreationInputTokens,
			CacheReadInputTokens:     d.CacheReadInputTokens,
			OutputTokens:             d.OutputTokens,
			AgentRawResult:           d.AgentRawResult,
		}
	}
	return out, nil
}

// UpsertArtifacts persists the generated file contents for one service.
// It first attempts a bulk write for efficiency. If the bulk write fails
// (e.g., one document exceeds MongoDB's 16 MiB limit causing a command-level
// rejection), it falls back to individual writes so the good artifacts are
// saved and only the oversized ones are dropped with a warning.
// Each document is keyed by (migration_id, service_name, path) — re-running
// overwrites, never duplicates.
func (s *MongoGenerationStore) UpsertArtifacts(ctx context.Context, migrationID uint64, serviceName string, artifacts []workerdomain.FileArtifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	models := make([]mongo.WriteModel, len(artifacts))
	for i, a := range artifacts {
		doc := generationFileArtifactDoc{
			MigrationID: migrationID,
			ServiceName: serviceName,
			Path:        a.Path,
			Content:     string(a.Content),
			UpdatedAt:   now,
		}
		filter := bson.M{"migration_id": migrationID, "service_name": serviceName, "path": a.Path}
		models[i] = mongo.NewUpdateOneModel().
			SetFilter(filter).
			SetUpdate(bson.M{"$set": doc}).
			SetUpsert(true)
	}
	_, err := s.artifactColl.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	if err != nil {
		// With SetOrdered(false), per-document failures (BulkWriteException.WriteErrors)
		// mean the other documents were already written — log each rejection and continue.
		var bwe mongo.BulkWriteException
		if errors.As(err, &bwe) {
			for _, we := range bwe.WriteErrors {
				path := "unknown"
				if we.Index >= 0 && we.Index < len(artifacts) {
					path = artifacts[we.Index].Path
				}
				applog.Warningf("generation-store: artifact rejected migration_id=%d service=%s path=%s code=%d: %s",
					migrationID, serviceName, path, we.Code, we.Message)
			}
			// WriteConcernError is cluster-level — caller must know about it.
			if bwe.WriteConcernError != nil {
				return fmt.Errorf("generation-store: upsert artifacts write-concern migration_id=%d service=%s: %v",
					migrationID, serviceName, bwe.WriteConcernError)
			}
			return nil
		}
		// Command-level failure (e.g., one document is too large, causing the
		// entire batch to be rejected by the server). Fall back to individual
		// writes so the good artifacts survive — only the oversized one(s) fail.
		applog.Warningf("generation-store: bulk write failed migration_id=%d service=%s (%v) — retrying individually",
			migrationID, serviceName, err)
		return s.upsertArtifactsOneByOne(ctx, migrationID, serviceName, artifacts, now)
	}
	return nil
}

// upsertArtifactsOneByOne writes each artifact in a separate UpdateOne, so a
// single oversized document can never block the rest. Called only when the
// bulk write fails at the command level.
func (s *MongoGenerationStore) upsertArtifactsOneByOne(
	ctx context.Context,
	migrationID uint64,
	serviceName string,
	artifacts []workerdomain.FileArtifact,
	now primitive.DateTime,
) error {
	for _, a := range artifacts {
		doc := generationFileArtifactDoc{
			MigrationID: migrationID,
			ServiceName: serviceName,
			Path:        a.Path,
			Content:     string(a.Content),
			UpdatedAt:   now,
		}
		filter := bson.M{"migration_id": migrationID, "service_name": serviceName, "path": a.Path}
		_, writeErr := s.artifactColl.UpdateOne(ctx, filter, bson.M{"$set": doc}, options.Update().SetUpsert(true))
		if writeErr != nil {
			applog.Warningf("generation-store: artifact rejected migration_id=%d service=%s path=%s: %v",
				migrationID, serviceName, a.Path, writeErr)
			// Intentionally continue — save the artifacts that can be saved.
		}
	}
	return nil
}

// ListArtifacts returns all persisted file artifacts for one service within a
// migration, sorted by path.
func (s *MongoGenerationStore) ListArtifacts(ctx context.Context, migrationID uint64, serviceName string) ([]workerdomain.FileArtifact, error) {
	cur, err := s.artifactColl.Find(ctx, bson.M{"migration_id": migrationID, "service_name": serviceName})
	if err != nil {
		return nil, fmt.Errorf("generation-store: list artifacts migration_id=%d service=%s: %w", migrationID, serviceName, err)
	}
	defer cur.Close(ctx)
	var docs []generationFileArtifactDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("generation-store: decode artifacts migration_id=%d service=%s: %w", migrationID, serviceName, err)
	}
	out := make([]workerdomain.FileArtifact, len(docs))
	for i, d := range docs {
		out[i] = workerdomain.FileArtifact{Path: d.Path, Content: []byte(d.Content)}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
