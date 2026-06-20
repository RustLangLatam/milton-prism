package adapters

import (
	"context"
	"errors"
	"fmt"

	"milton_prism/core/worker/generation/ports"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

var _ ports.GenerationPackageReader = (*MongoGenerationPackageReader)(nil)

// MongoGenerationPackageReader assembles the generation package from the
// migrations and design_artifacts collections. It mirrors
// migration.Service.GetGenerationPackage without going through gRPC,
// which lets the generation worker stay decoupled from the migration service.
type MongoGenerationPackageReader struct {
	migrations *mongo.Collection
	artifacts  *mongo.Collection
}

// NewMongoGenerationPackageReader returns a reader backed by db.
func NewMongoGenerationPackageReader(db *mongo.Database) *MongoGenerationPackageReader {
	return &MongoGenerationPackageReader{
		migrations: db.Collection("migrations"),
		artifacts:  db.Collection("design_artifacts"),
	}
}

type migrationDocMinimal struct {
	Identifier  uint64 `bson:"identifier"`
	PlanBytes   []byte `bson:"plan_bytes,omitempty"`
	TargetBytes []byte `bson:"target_bytes,omitempty"`
}

type artifactDocMinimal struct {
	ServiceName      string `bson:"service_name"`
	ProtoContent     string `bson:"proto_content"`
	BoundarySpec     string `bson:"boundary_spec"`
	Incomplete       bool   `bson:"incomplete"`
	IncompleteReason string `bson:"incomplete_reason"`
}

func (r *MongoGenerationPackageReader) ReadPackage(ctx context.Context, migrationID uint64) (*ports.GenerationPackage, error) {
	var migDoc migrationDocMinimal
	err := r.migrations.FindOne(ctx, bson.M{"identifier": migrationID, "delete_time": nil}).Decode(&migDoc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("generation-package: migration %d not found", migrationID)
		}
		return nil, fmt.Errorf("generation-package: find migration %d: %w", migrationID, err)
	}

	// Decode plan to build the error-prefix index by service name.
	prefixByName := make(map[string]string)
	if len(migDoc.PlanBytes) > 0 {
		var plan migrationv1.RestructurePlan
		if err := proto.Unmarshal(migDoc.PlanBytes, &plan); err != nil {
			return nil, fmt.Errorf("generation-package: unmarshal plan: %w", err)
		}
		for _, svc := range plan.GetServices() {
			prefixByName[svc.GetName()] = svc.GetErrorPrefix()
		}
	}

	// Decode target config to determine the output profile and prompt reference.
	profile := "go"
	promptRef := "docs/prism/milton-prism-service-generator-prompt.md"
	if len(migDoc.TargetBytes) > 0 {
		var tc migrationv1.TargetConfig
		if err := proto.Unmarshal(migDoc.TargetBytes, &tc); err == nil {
			if tc.GetLanguage() == migrationv1.TargetLanguage_TARGET_LANGUAGE_PYTHON {
				profile = "python"
				promptRef = "docs/prism/milton-prism-python-service-generator-prompt.md"
			}
		}
	}

	// Read per-service design artifacts.
	cur, err := r.artifacts.Find(ctx, bson.M{"migration_id": migrationID})
	if err != nil {
		return nil, fmt.Errorf("generation-package: find artifacts migration_id=%d: %w", migrationID, err)
	}
	defer cur.Close(ctx)
	var artDocs []artifactDocMinimal
	if err := cur.All(ctx, &artDocs); err != nil {
		return nil, fmt.Errorf("generation-package: decode artifacts migration_id=%d: %w", migrationID, err)
	}
	if len(artDocs) == 0 {
		return nil, fmt.Errorf("generation-package: no design artifacts for migration %d", migrationID)
	}

	services := make([]ports.ServiceSpec, len(artDocs))
	for i, a := range artDocs {
		services[i] = ports.ServiceSpec{
			Name:               a.ServiceName,
			ErrorPrefix:        prefixByName[a.ServiceName],
			ProtoContent:       a.ProtoContent,
			BoundarySpec:       a.BoundarySpec,
			Incomplete:         a.Incomplete,
			IncompleteReason:   a.IncompleteReason,
			GeneratorPromptRef: promptRef,
		}
	}

	return &ports.GenerationPackage{
		MigrationID:   migrationID,
		OutputProfile: profile,
		Services:      services,
	}, nil
}
