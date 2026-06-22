package adapters

import (
	"context"
	"errors"
	"fmt"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

var _ ports.TargetTopologyLoader = (*MongoTopologyLoader)(nil)

// MongoTopologyLoader reads the architectural target topology selected for a
// migration. The TargetConfig is persisted on the migration document as the
// marshalled proto bytes in target_bytes (mirroring the migration service's
// mongo repository). An absent/unspecified topology maps to MICROSERVICES so
// the existing flow is never broken.
type MongoTopologyLoader struct {
	coll *mongo.Collection
}

// NewMongoTopologyLoader returns a loader backed by the migration database.
func NewMongoTopologyLoader(db *mongo.Database) *MongoTopologyLoader {
	return &MongoTopologyLoader{coll: db.Collection("migrations")}
}

// topologyDoc is the minimal projection needed to read the target config.
type topologyDoc struct {
	TargetBytes []byte `bson:"target_bytes"`
}

// LoadTopology reads target_bytes for the migration, unmarshals the TargetConfig,
// and returns its topology. A missing migration, absent target, or unspecified
// topology all resolve to MICROSERVICES — the default must never error the flow.
func (l *MongoTopologyLoader) LoadTopology(ctx context.Context, migrationID uint64) (workerdomain.TargetTopology, error) {
	var doc topologyDoc
	err := l.coll.FindOne(ctx,
		bson.M{"identifier": migrationID, "delete_time": nil},
		// Only fetch target_bytes — status polling must stay cheap.
	).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// Unknown migration → default flow.
			return workerdomain.TopologyMicroservices, nil
		}
		return workerdomain.TopologyMicroservices, fmt.Errorf("topology-loader: find migration %d: %w", migrationID, err)
	}
	if len(doc.TargetBytes) == 0 {
		return workerdomain.TopologyMicroservices, nil
	}
	tc := &migrationv1.TargetConfig{}
	if err := proto.Unmarshal(doc.TargetBytes, tc); err != nil {
		return workerdomain.TopologyMicroservices, fmt.Errorf("topology-loader: unmarshal target for migration %d: %w", migrationID, err)
	}
	if tc.GetTopology() == workerdomain.TopologyUnspecified {
		return workerdomain.TopologyMicroservices, nil
	}
	return tc.GetTopology(), nil
}
