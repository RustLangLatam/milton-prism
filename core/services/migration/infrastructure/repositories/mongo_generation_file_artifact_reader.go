package repositories

import (
	"context"
	"fmt"
	"sort"

	"milton_prism/core/services/migration/ports"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var _ ports.GenerationFileArtifactReader = (*MongoGenerationFileArtifactReader)(nil)

// MongoGenerationFileArtifactReader reads generated source files from the
// generation_file_artifacts collection written by the generation worker.
type MongoGenerationFileArtifactReader struct {
	coll *mongo.Collection
}

// NewMongoGenerationFileArtifactReader returns a reader bound to the
// generation_file_artifacts collection.
func NewMongoGenerationFileArtifactReader(db *mongo.Database) *MongoGenerationFileArtifactReader {
	return &MongoGenerationFileArtifactReader{coll: db.Collection("generation_file_artifacts")}
}

type fileArtifactDoc struct {
	ServiceName string `bson:"service_name"`
	Path        string `bson:"path"`
	Content     string `bson:"content"`
}

// ListArtifacts returns generated files for the given migration. When
// serviceName is non-empty only that service's files are returned; when empty
// all services are included. Results are sorted by (service_name, path).
func (r *MongoGenerationFileArtifactReader) ListArtifacts(ctx context.Context, migrationID uint64, serviceName string) ([]ports.GeneratedFile, error) {
	filter := bson.M{"migration_id": migrationID}
	if serviceName != "" {
		filter["service_name"] = serviceName
	}
	cur, err := r.coll.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("file-artifact-reader: find migration_id=%d: %w", migrationID, err)
	}
	defer cur.Close(ctx)

	var docs []fileArtifactDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("file-artifact-reader: decode migration_id=%d: %w", migrationID, err)
	}

	out := make([]ports.GeneratedFile, len(docs))
	for i, d := range docs {
		out[i] = ports.GeneratedFile{ServiceName: d.ServiceName, Path: d.Path, Content: d.Content}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServiceName != out[j].ServiceName {
			return out[i].ServiceName < out[j].ServiceName
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}
