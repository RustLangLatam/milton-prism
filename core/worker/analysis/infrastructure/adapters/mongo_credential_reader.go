package adapters

import (
	"context"
	"errors"
	"fmt"

	"milton_prism/core/worker/analysis/ports"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var _ ports.RepositoryCredentialReader = (*MongoCredentialReader)(nil)

// MongoCredentialReader reads the credential_ref field from the repository
// collection. It never logs the credential value.
type MongoCredentialReader struct {
	coll *mongo.Collection
}

// NewMongoCredentialReader returns a reader backed by the repositories
// collection in the provided database.
func NewMongoCredentialReader(db *mongo.Database) *MongoCredentialReader {
	return &MongoCredentialReader{coll: db.Collection("repositories")}
}

// GetCredentialRef returns the credential_ref stored for repositoryID, or
// an empty string when no credential has been registered.
func (r *MongoCredentialReader) GetCredentialRef(ctx context.Context, repositoryID uint64) (string, error) {
	var doc struct {
		CredentialRef string `bson:"credential_ref"`
	}
	err := r.coll.FindOne(ctx, bson.M{"identifier": repositoryID}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("mongo credential reader: %w", err)
	}
	return doc.CredentialRef, nil
}
