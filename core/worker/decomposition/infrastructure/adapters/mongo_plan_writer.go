package adapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
	applog "milton_prism/pkg/log"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

var _ ports.PlanWriter = (*MongoPlanWriter)(nil)

// MongoPlanWriter persists the assembled RestructurePlan into the
// milton_prism_migration database, writes YAML boundary specs to the workspace,
// and advances the migration state from DESIGNING to AWAITING_APPROVAL.
type MongoPlanWriter struct {
	coll *mongo.Collection
}

// NewMongoPlanWriter returns a MongoPlanWriter backed by the given migration database.
func NewMongoPlanWriter(db *mongo.Database) *MongoPlanWriter {
	return &MongoPlanWriter{coll: db.Collection("migrations")}
}

// WritePlan writes YAML boundary specs, persists the RestructurePlan, and
// transitions the migration to AWAITING_APPROVAL.
func (w *MongoPlanWriter) WritePlan(
	ctx context.Context,
	migrationID uint64,
	plan *workerdomain.RestructurePlan,
	workspacePath string,
	ownership workerdomain.DataOwnership,
) error {
	if workspacePath != "" {
		if err := writeBoundarySpecs(workspacePath, plan, ownership); err != nil {
			// Non-fatal: log but continue — the spec files are advisory artifacts.
			applog.Warningf("plan-writer: boundary specs skipped migration_id=%d: %v", migrationID, err)
		}
	}

	planBytes, err := proto.Marshal(plan)
	if err != nil {
		return fmt.Errorf("plan-writer: marshal plan: %w", err)
	}

	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := w.coll.UpdateOne(ctx,
		bson.M{"identifier": migrationID, "delete_time": nil},
		bson.M{"$set": bson.M{
			"plan_bytes":  planBytes,
			"state":       int32(migrationv1.MigrationState_MIGRATION_STATE_AWAITING_APPROVAL),
			"update_time": now,
		}},
	)
	if err != nil {
		return fmt.Errorf("plan-writer: update migration: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("plan-writer: migration %d not found or already deleted", migrationID)
	}
	applog.Infof("plan-writer: migration_id=%d state=AWAITING_APPROVAL plan_services=%d",
		migrationID, len(plan.GetServices()))
	return nil
}

// MarkFailed transitions the migration from DESIGNING to FAILED and persists
// the human-readable failure reason. Filtered by DESIGNING state so a migration
// that already advanced (or was already failed) is not affected.
func (w *MongoPlanWriter) MarkFailed(ctx context.Context, migrationID uint64, reason string) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	_, err := w.coll.UpdateOne(
		ctx,
		bson.M{
			"identifier":  migrationID,
			"state":       int32(migrationv1.MigrationState_MIGRATION_STATE_DESIGNING),
			"delete_time": nil,
		},
		bson.M{"$set": bson.M{
			"state":          int32(migrationv1.MigrationState_MIGRATION_STATE_FAILED),
			"failure_reason": reason,
			"update_time":    now,
		}},
	)
	return err
}

// writeBoundarySpecs writes one YAML file per proposed service into
// {workspacePath}/.milton_prism/boundary_specs/.
func writeBoundarySpecs(
	workspacePath string,
	plan *workerdomain.RestructurePlan,
	ownership workerdomain.DataOwnership,
) error {
	dir := filepath.Join(workspacePath, ".milton_prism", "boundary_specs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir boundary_specs: %w", err)
	}

	// Index cross-service FKs and operational couplings by owner service.
	fksByOwner := make(map[string][]workerdomain.CrossServiceFK)
	for _, fk := range ownership.CrossServiceFKs {
		fksByOwner[fk.OwnerService] = append(fksByOwner[fk.OwnerService], fk)
	}
	opByService := make(map[string][]workerdomain.OperationalCoupling)
	for _, oc := range ownership.OperationalCouplings {
		opByService[oc.FromService] = append(opByService[oc.FromService], oc)
	}

	for _, svc := range plan.GetServices() {
		content := workerdomain.BuildBoundarySpecYAML(
			svc, ownership.SharedDatabase,
			fksByOwner[svc.GetName()],
			opByService[svc.GetName()],
		)
		path := filepath.Join(dir, svc.GetName()+".yaml")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write spec %s: %w", svc.GetName(), err)
		}
	}
	return nil
}
