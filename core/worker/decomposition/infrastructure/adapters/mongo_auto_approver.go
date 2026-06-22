package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"milton_prism/core/worker/decomposition/ports"
	applog "milton_prism/pkg/log"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"github.com/hibiken/asynq"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// migrabilityVerdictNotMigrable mirrors the migration service's gate constant.
// Kept local so the worker does not import the migration service's domain.
const migrabilityVerdictNotMigrable = "NOT_MIGRABLE"

var _ ports.AutoApprover = (*MongoAutoApprover)(nil)

// MongoAutoApprover implements the one-shot RunMigration continuation in the
// decomposition worker. After the plan reaches AWAITING_APPROVAL it reads the
// auto_approve intent from the shared migrations collection and, when the gates
// allow, advances the migration to GENERATING and enqueues a generation job —
// exactly what a manual ApproveDesign(approved=true) would do, minus the human.
//
// It deliberately re-implements the same gates as the migration service's
// ApproveDesign (no service boundaries → skip; NOT_MIGRABLE without override →
// skip) by reading the persisted plan and assessment, so the platform's
// human-protection rules are never bypassed by the one-shot path. The final
// publish (git push) is never triggered here.
type MongoAutoApprover struct {
	coll       *mongo.Collection
	asynqQueue *asynq.Client
}

// NewMongoAutoApprover returns an auto-approver bound to the migration database
// and the shared Asynq client used to enqueue generation jobs.
func NewMongoAutoApprover(db *mongo.Database, asynqClient *asynq.Client) *MongoAutoApprover {
	return &MongoAutoApprover{coll: db.Collection("migrations"), asynqQueue: asynqClient}
}

// taskTypeGenerationRun must match jobs.TaskTypeGenerationRun in the generation
// worker and the migration service's AsynqGenerationEnqueuer.
const taskTypeGenerationRun = "generation:run"

// generationTaskTimeout mirrors the migration service enqueuer's timeout so the
// worker-dispatched job gets the same wall-clock budget as a manual approval.
const generationTaskTimeout = 90 * time.Minute

// generationAsynqPayload mirrors the payload the migration service enqueues so
// the generation worker decodes either source identically.
type generationAsynqPayload struct {
	MigrationID   uint64   `json:"migration_id"`
	ServiceFilter []string `json:"service_filter,omitempty"`
}

// autoApproveDoc is the minimal projection needed to apply the approval gate.
type autoApproveDoc struct {
	AutoApprove         bool   `bson:"auto_approve"`
	State               int32  `bson:"state"`
	PlanBytes           []byte `bson:"plan_bytes"`
	AssessmentBytes     []byte `bson:"assessment_bytes"`
	MigrabilityOverride bool   `bson:"migrability_override"`
}

// MaybeAutoApprove advances a migration from AWAITING_APPROVAL to GENERATING and
// enqueues generation when an auto_approve intent is set and the gates pass.
// It is a no-op (approved=false, nil error) when:
//   - auto_approve is not set;
//   - the migration is no longer in AWAITING_APPROVAL (already advanced);
//   - the plan has no service boundaries (nothing to generate);
//   - the migrability verdict is NOT_MIGRABLE without an override.
func (a *MongoAutoApprover) MaybeAutoApprove(ctx context.Context, migrationID uint64) (bool, error) {
	var doc autoApproveDoc
	err := a.coll.FindOne(ctx, bson.M{"identifier": migrationID, "delete_time": nil}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil
		}
		return false, fmt.Errorf("auto-approver: find migration %d: %w", migrationID, err)
	}

	proceed, reason, err := autoApproveDecision(doc)
	if err != nil {
		return false, fmt.Errorf("auto-approver: decide migration %d: %w", migrationID, err)
	}
	if !proceed {
		if reason != "" {
			applog.Infof("auto-approver: migration_id=%d skipped — %s", migrationID, reason)
		}
		return false, nil
	}

	// Atomically advance to GENERATING, guarded by AWAITING_APPROVAL so a
	// concurrent manual approval or a re-run cannot double-transition.
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := a.coll.UpdateOne(ctx,
		bson.M{
			"identifier":  migrationID,
			"state":       int32(migrationv1.MigrationState_MIGRATION_STATE_AWAITING_APPROVAL),
			"delete_time": nil,
		},
		bson.M{"$set": bson.M{
			"state":       int32(migrationv1.MigrationState_MIGRATION_STATE_GENERATING),
			"update_time": now,
		}},
	)
	if err != nil {
		return false, fmt.Errorf("auto-approver: advance migration %d to GENERATING: %w", migrationID, err)
	}
	if res.MatchedCount == 0 {
		// Lost the race to a manual approval — that path enqueues generation.
		return false, nil
	}

	if a.asynqQueue != nil {
		if err := a.enqueueGeneration(ctx, migrationID); err != nil {
			// State is already GENERATING. Surface the enqueue failure so the
			// caller logs it; the migration is recoverable via a manual
			// re-approval / re-enqueue path.
			return false, fmt.Errorf("auto-approver: enqueue generation migration %d: %w", migrationID, err)
		}
	}
	return true, nil
}

// autoApproveDecision is the pure gate that decides whether a migration in the
// auto-approve flow may advance to GENERATING. It returns proceed=true only when
// every gate passes; otherwise proceed=false with a human-readable reason. It
// mirrors the migration service's ApproveDesign gates exactly so the one-shot
// path never bypasses a human-protection rule. err is non-nil only on a corrupt
// plan blob (a corrupt assessment fails closed instead).
func autoApproveDecision(doc autoApproveDoc) (proceed bool, reason string, err error) {
	if !doc.AutoApprove {
		return false, "", nil
	}
	if doc.State != int32(migrationv1.MigrationState_MIGRATION_STATE_AWAITING_APPROVAL) {
		return false, "", nil // already advanced (or never reached approval)
	}

	plan := &migrationv1.RestructurePlan{}
	if len(doc.PlanBytes) > 0 {
		if uerr := proto.Unmarshal(doc.PlanBytes, plan); uerr != nil {
			return false, "", fmt.Errorf("unmarshal plan: %w", uerr)
		}
	}
	if plan.GetNoServiceBoundaries() {
		return false, "no_service_boundaries (nothing to generate)", nil
	}

	if migrabilityBlocked(doc) {
		return false, "NOT_MIGRABLE verdict without override (manual override required)", nil
	}
	return true, "", nil
}

// migrabilityBlocked mirrors the migration service's gate: a NOT_MIGRABLE
// verdict blocks generation unless an override is set. Absent verdict never
// blocks. A corrupt assessment fails closed (blocks) — the one-shot path must
// never silently lift the gate.
func migrabilityBlocked(doc autoApproveDoc) bool {
	if len(doc.AssessmentBytes) == 0 {
		return false
	}
	assessment := &commonv1.MigrabilityAssessment{}
	if err := proto.Unmarshal(doc.AssessmentBytes, assessment); err != nil {
		return true
	}
	return assessment.GetVerdict() == migrabilityVerdictNotMigrable && !doc.MigrabilityOverride
}

// enqueueGeneration dispatches a generation:run job for the migration, mirroring
// the migration service's AsynqGenerationEnqueuer so either source is identical
// to the generation worker.
func (a *MongoAutoApprover) enqueueGeneration(ctx context.Context, migrationID uint64) error {
	payload, err := json.Marshal(generationAsynqPayload{MigrationID: migrationID})
	if err != nil {
		return err
	}
	task := asynq.NewTask(taskTypeGenerationRun, payload)
	info, err := a.asynqQueue.EnqueueContext(ctx, task,
		asynq.Queue("generation"),
		asynq.Timeout(generationTaskTimeout),
	)
	if err != nil {
		return err
	}
	applog.Infof("auto-approver: generation job enqueued queue=%s task_id=%s migration_id=%d", info.Queue, info.ID, migrationID)
	return nil
}
