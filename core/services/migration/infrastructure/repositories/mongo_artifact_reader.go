package repositories

import (
	"context"
	"fmt"

	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/ports"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var _ ports.ArtifactReader = (*MongoArtifactReader)(nil)

// MongoArtifactReader reads design_artifacts documents written by the
// decomposition engine for a given migration.
type MongoArtifactReader struct {
	coll *mongo.Collection
}

// NewMongoArtifactReader returns a MongoArtifactReader backed by the given database.
func NewMongoArtifactReader(db *mongo.Database) *MongoArtifactReader {
	return &MongoArtifactReader{coll: db.Collection("design_artifacts")}
}

type artifactDoc struct {
	ServiceName      string `bson:"service_name"`
	ProtoContent     string `bson:"proto_content"`
	BoundarySpec     string `bson:"boundary_spec"`
	Incomplete       bool   `bson:"incomplete"`
	IncompleteReason string `bson:"incomplete_reason"`
}

// ReadArtifacts returns all design artifacts for the given migration.
func (r *MongoArtifactReader) ReadArtifacts(ctx context.Context, migrationID uint64) ([]domain.ServiceArtifact, error) {
	cur, err := r.coll.Find(ctx, bson.M{"migration_id": migrationID})
	if err != nil {
		return nil, fmt.Errorf("artifact-reader: find migration_id=%d: %w", migrationID, err)
	}
	defer cur.Close(ctx)

	var docs []artifactDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("artifact-reader: decode migration_id=%d: %w", migrationID, err)
	}

	out := make([]domain.ServiceArtifact, len(docs))
	for i, d := range docs {
		out[i] = domain.ServiceArtifact{
			ServiceName:      d.ServiceName,
			ProtoContent:     d.ProtoContent,
			BoundarySpec:     d.BoundarySpec,
			Incomplete:       d.Incomplete,
			IncompleteReason: d.IncompleteReason,
		}
	}
	return out, nil
}
