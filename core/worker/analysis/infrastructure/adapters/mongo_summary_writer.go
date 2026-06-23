// Package adapters contains the infrastructure adapters for the analysis worker.
package adapters

import (
	"context"
	"time"

	analysisdomain "milton_prism/core/services/analysis/domain"
	migrationdomain "milton_prism/core/services/migration/domain"
	"milton_prism/core/worker/analysis/ports"
	applog "milton_prism/pkg/log"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	mongoOptions "go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

var _ ports.SummaryWriter = (*MongoSummaryWriter)(nil)

// MongoSummaryWriter implements ports.SummaryWriter by writing directly to the
// shared MongoDB instance. It updates the analysis_summaries collection to
// COMPLETED and, when a migration_id is present, advances the migration from
// ANALYZING to DESIGNING.
//
// Both updates are guarded by the current state so re-running the job is safe:
// a matched-count of zero means the record was already processed and we skip.
type MongoSummaryWriter struct {
	analysisColl  *mongo.Collection
	migrationColl *mongo.Collection
}

// NewMongoSummaryWriter returns a MongoSummaryWriter. analysisDB is the
// analysis service database; migrationDB is the migration service database —
// they are distinct databases in the same MongoDB instance.
func NewMongoSummaryWriter(analysisDB, migrationDB *mongo.Database) *MongoSummaryWriter {
	return &MongoSummaryWriter{
		analysisColl:  analysisDB.Collection("analysis_summaries"),
		migrationColl: migrationDB.Collection("migrations"),
	}
}

// Write marks the AnalysisSummary as COMPLETED, persists the detected
// technologies, and advances the associated Migration from ANALYZING to
// DESIGNING. It is idempotent: if the summary is already COMPLETED the update
// matches nothing and returns nil.
func (w *MongoSummaryWriter) Write(ctx context.Context, summary *analysisdomain.AnalysisSummary) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())

	setDoc := bson.M{
		"state":       int32(analysisdomain.AnalysisStateCompleted),
		"total_files": summary.GetTotalFiles(),
		"total_lines": summary.GetTotalLines(),
		"update_time": now,
		// Persisted unconditionally (including false) so the deep-analysis-availability
		// signal is authoritative for the assessor and the UI, never absent-as-false.
		"deep_analysis_available": summary.GetDeepAnalysisAvailable(),
	}
	if prod := summary.GetModuleCountProduction(); prod > 0 {
		setDoc["module_count_production"] = prod
	}
	if test := summary.GetModuleCountTest(); test > 0 {
		setDoc["module_count_test"] = test
	}
	if sha := summary.GetCommitSha(); sha != "" {
		setDoc["commit_sha"] = sha
	}
	// Persist the monorepo scope the worker actually analysed. The field was set
	// at summary creation (RUNNING) by the analysis service; re-asserting it here
	// keeps the completion write self-describing even if creation predated it.
	if sub := summary.GetRootSubdirectory(); sub != "" {
		setDoc["root_subdirectory"] = sub
	}

	// Persist repeated fields as wrapped proto bytes — same encoding as the
	// analysis service repository so both readers produce consistent output.
	if techs := summary.GetTechnologies(); len(techs) > 0 {
		techBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{Technologies: techs})
		if err != nil {
			return err
		}
		setDoc["technologies_bytes"] = techBytes
	}
	if vulns := summary.GetVulnerabilities(); len(vulns) > 0 {
		vulnBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{Vulnerabilities: vulns})
		if err != nil {
			return err
		}
		setDoc["vulnerabilities_bytes"] = vulnBytes
	}
	if graph := summary.GetDependencyGraph(); len(graph) > 0 {
		graphBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{DependencyGraph: graph})
		if err != nil {
			return err
		}
		setDoc["dependency_graph_bytes"] = graphBytes
	}
	if cards := summary.GetModuleCards(); len(cards) > 0 {
		cardBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{ModuleCards: cards})
		if err != nil {
			return err
		}
		setDoc["module_cards_bytes"] = cardBytes
	}
	if bps := summary.GetBlueprints(); len(bps) > 0 {
		bpBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{Blueprints: bps})
		if err != nil {
			return err
		}
		setDoc["blueprints_bytes"] = bpBytes
	}
	if mc := summary.GetModuleClassification(); mc != nil {
		mcBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{ModuleClassification: mc})
		if err != nil {
			return err
		}
		setDoc["module_classification_bytes"] = mcBytes
	}
	if ms := summary.GetMigrabilityScore(); ms != nil {
		msBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{MigrabilityScore: ms})
		if err != nil {
			return err
		}
		setDoc["migrability_score_bytes"] = msBytes
	}
	if hubs := summary.GetSharedStateHubs(); len(hubs) > 0 {
		hubBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{SharedStateHubs: hubs})
		if err != nil {
			return err
		}
		setDoc["shared_state_hubs_bytes"] = hubBytes
	}
	if unreachable := summary.GetUnreachableModules(); len(unreachable) > 0 {
		unreachableBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{UnreachableModules: unreachable})
		if err != nil {
			return err
		}
		setDoc["unreachable_modules_bytes"] = unreachableBytes
	}
	if dd := summary.GetDatabaseDetection(); dd != nil {
		ddBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{DatabaseDetection: dd})
		if err != nil {
			return err
		}
		setDoc["database_detection_bytes"] = ddBytes
	}
	if ap := summary.GetArchitecturalPattern(); ap != nil {
		apBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{ArchitecturalPattern: ap})
		if err != nil {
			return err
		}
		setDoc["architectural_pattern_bytes"] = apBytes
	}
	if ia := summary.GetIntakeAssessment(); ia != nil {
		iaBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{IntakeAssessment: ia})
		if err != nil {
			return err
		}
		setDoc["intake_assessment_bytes"] = iaBytes
	}
	if sf := summary.GetSecurityFindings(); len(sf) > 0 {
		sfBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{SecurityFindings: sf})
		if err != nil {
			return err
		}
		setDoc["security_findings_bytes"] = sfBytes
	}
	if asd := summary.GetAuthSchemeDetection(); asd != nil {
		asdBytes, err := proto.Marshal(&analysisdomain.AnalysisSummary{AuthSchemeDetection: asd})
		if err != nil {
			return err
		}
		setDoc["auth_scheme_detection_bytes"] = asdBytes
	}

	res, err := w.analysisColl.UpdateOne(
		ctx,
		bson.M{
			"identifier": summary.GetIdentifier(),
			// Only match RUNNING; a second run with COMPLETED will match nothing.
			"state": int32(analysisdomain.AnalysisStateRunning),
		},
		bson.M{"$set": setDoc},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		applog.Infof("analysis-worker: summary %d already processed — skipping state advance",
			summary.GetIdentifier())
		return nil
	}

	if summary.GetMigrationId() == 0 {
		return nil
	}

	return w.advanceMigration(ctx, summary.GetMigrationId(), summary.GetIdentifier(), summary.GetCommitSha(), now)
}

// advanceMigration links a completed analysis to its migration: it stamps the
// migration's commit_sha (resolved during the analysis clone → resolveHEAD) and
// advances ANALYZING → DESIGNING. Filtering by current state makes this safe to
// re-run (if already DESIGNING the filter matches nothing).
//
// MIG223 enforcement (commit-resolution / decision point D2): a migration must
// not re-run an unchanged branch. If another migration already exists for the
// same (repository_id, source_branch, commit_sha) — guarded by the
// uniq_repo_branch_commit partial unique index — this migration is moved to
// FAILED with the MIG223 reason instead of advancing. The partial unique index
// is the hard DB net: a duplicate-key error on the commit_sha write is mapped to
// the same FAILED transition so a race cannot slip a second migration through.
func (w *MongoSummaryWriter) advanceMigration(ctx context.Context, migrationID, summaryID uint64, commitSHA string, now primitive.DateTime) error {
	set := bson.M{
		"state":               int32(migrationdomain.MigrationStateDesigning),
		"analysis_summary_id": summaryID,
		"update_time":         now,
	}
	if commitSHA != "" {
		set["commit_sha"] = commitSHA
	}

	_, err := w.migrationColl.UpdateOne(
		ctx,
		bson.M{
			"identifier":  migrationID,
			"state":       int32(migrationdomain.MigrationStateAnalyzing),
			"delete_time": nil,
		},
		bson.M{"$set": set},
	)
	if err != nil {
		// Partial-unique-index collision: another migration already covers this
		// (repo, branch, commit). Mark THIS migration FAILED with MIG223 and stop —
		// never leave it half-run in ANALYZING.
		if mongo.IsDuplicateKeyError(err) {
			applog.Infof("analysis-worker: migration_id=%d branch unchanged (same commit=%s) — failing with MIG223",
				migrationID, commitSHA)
			return w.failBranchUnchanged(ctx, migrationID, now)
		}
		return err
	}
	return nil
}

// failBranchUnchanged transitions a migration ANALYZING → FAILED with the
// MIG223 reason. Used when the branch's commit is unchanged since a prior
// migration (no new commits). Guarded on ANALYZING for idempotency.
func (w *MongoSummaryWriter) failBranchUnchanged(ctx context.Context, migrationID uint64, now primitive.DateTime) error {
	_, err := w.migrationColl.UpdateOne(
		ctx,
		bson.M{
			"identifier":  migrationID,
			"state":       int32(migrationdomain.MigrationStateAnalyzing),
			"delete_time": nil,
		},
		bson.M{"$set": bson.M{
			"state":          int32(migrationdomain.MigrationStateFailed),
			"failure_reason": migrationdomain.ErrBranchUnchanged.Message + ": a migration already exists for this branch at the same commit; no new commits since the last migration",
			"update_time":    now,
		}},
	)
	return err
}

// MarkAnalysisFailed transitions the AnalysisSummary from RUNNING to FAILED and
// records the failure reason. Guarded on RUNNING state so an already-COMPLETED
// summary is never overwritten (idempotent on re-runs).
func (w *MongoSummaryWriter) MarkAnalysisFailed(ctx context.Context, summaryID uint64, reason string) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	_, err := w.analysisColl.UpdateOne(
		ctx,
		bson.M{
			"identifier": summaryID,
			"state":      int32(analysisdomain.AnalysisStateRunning),
		},
		bson.M{"$set": bson.M{
			"state":          int32(analysisdomain.AnalysisStateFailed),
			"failure_reason": reason,
			"update_time":    now,
		}},
	)
	return err
}

// MarkAwaitingRootSelection transitions the AnalysisSummary from RUNNING to
// AWAITING_ROOT_SELECTION and persists the detected candidate roots so the
// client can present them. Guarded on RUNNING state for idempotency. The
// associated migration (if any) is intentionally left in ANALYZING: it advances
// only once a root is chosen and the analysis re-runs to COMPLETED.
func (w *MongoSummaryWriter) MarkAwaitingRootSelection(ctx context.Context, summaryID uint64, candidates []string) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	_, err := w.analysisColl.UpdateOne(
		ctx,
		bson.M{
			"identifier": summaryID,
			"state":      int32(analysisdomain.AnalysisStateRunning),
		},
		bson.M{"$set": bson.M{
			"state":           int32(analysisdomain.AnalysisStateAwaitingRootSelection),
			"root_candidates": candidates,
			"update_time":     now,
		}},
	)
	return err
}

// FindCompletedForBranch returns the most recent COMPLETED AnalysisSummary for
// the given repository and branch. Returns nil, nil when none exists.
func (w *MongoSummaryWriter) FindCompletedForBranch(ctx context.Context, repositoryID uint64, branch string) (*analysisdomain.AnalysisSummary, error) {
	var doc struct {
		Identifier   uint64 `bson:"identifier"`
		RepositoryID uint64 `bson:"repository_id"`
		SourceBranch string `bson:"source_branch"`
		CommitSHA    string `bson:"commit_sha,omitempty"`
		State        int32  `bson:"state"`
	}
	err := w.analysisColl.FindOne(ctx,
		bson.M{
			"repository_id": repositoryID,
			"source_branch": branch,
			"state":         int32(analysisdomain.AnalysisStateCompleted),
			"delete_time":   nil,
		},
		mongoOptions.FindOne().SetSort(bson.D{{Key: "create_time", Value: -1}}).
			SetProjection(bson.M{"identifier": 1, "repository_id": 1, "source_branch": 1, "commit_sha": 1, "state": 1}),
	).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &analysisdomain.AnalysisSummary{
		Identifier:   doc.Identifier,
		RepositoryId: doc.RepositoryID,
		SourceBranch: doc.SourceBranch,
		CommitSha:    doc.CommitSHA,
		State:        analysisdomain.AnalysisStateCompleted,
	}, nil
}

// WriteReuse advances the migration from ANALYZING to DESIGNING by linking it
// to an existing COMPLETED AnalysisSummary, skipping re-analysis. Sets
// analysis_reused=true so the frontend can communicate the reuse to the user.
// It also back-links the AnalysisSummary to this migration when the summary has
// no migration_id yet (i.e. it was created as a standalone analysis); analyses
// that already carry a migration_id retain their original ownership.
func (w *MongoSummaryWriter) WriteReuse(ctx context.Context, existingSummaryID, migrationID uint64) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())

	// Resolve the reused summary's commit_sha so the migration carries the commit
	// it was actually analysed at — the same field set on the normal path. This
	// also lets the uniq_repo_branch_commit partial index enforce MIG223 for
	// reuse-path migrations (two migrations on the same repo+branch+commit collide).
	var commitSHA string
	var sumDoc struct {
		CommitSHA string `bson:"commit_sha,omitempty"`
	}
	if findErr := w.analysisColl.FindOne(ctx,
		bson.M{"identifier": existingSummaryID},
		mongoOptions.FindOne().SetProjection(bson.M{"commit_sha": 1}),
	).Decode(&sumDoc); findErr == nil {
		commitSHA = sumDoc.CommitSHA
	}

	set := bson.M{
		"state":               int32(migrationdomain.MigrationStateDesigning),
		"analysis_summary_id": existingSummaryID,
		"analysis_reused":     true,
		"update_time":         now,
	}
	if commitSHA != "" {
		set["commit_sha"] = commitSHA
	}
	res, err := w.migrationColl.UpdateOne(ctx,
		bson.M{
			"identifier":  migrationID,
			"state":       int32(migrationdomain.MigrationStateAnalyzing),
			"delete_time": nil,
		},
		bson.M{"$set": set},
	)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			applog.Infof("analysis-worker: WriteReuse migration_id=%d branch unchanged (same commit=%s) — failing with MIG223",
				migrationID, commitSHA)
			return w.failBranchUnchanged(ctx, migrationID, now)
		}
		return err
	}
	if res.MatchedCount == 0 {
		applog.Infof("analysis-worker: WriteReuse migration_id=%d already advanced — skipping", migrationID)
	}

	// Back-link the reused analysis to this migration only if it has no
	// migration_id yet (standalone origin). Analyses born from another migration
	// keep their original migration_id.
	linkRes, err := w.analysisColl.UpdateOne(ctx,
		bson.M{
			"identifier": existingSummaryID,
			"$or": bson.A{
				bson.M{"migration_id": bson.M{"$exists": false}},
				bson.M{"migration_id": 0},
			},
		},
		bson.M{"$set": bson.M{
			"migration_id": migrationID,
			"update_time":  now,
		}},
	)
	if err != nil {
		return err
	}
	if linkRes.MatchedCount == 0 {
		applog.Infof("analysis-worker: WriteReuse summary_id=%d already has a migration_id — skipping back-link", existingSummaryID)
	}
	return nil
}

// MarkFailed transitions the migration from ANALYZING to FAILED and writes the
// human-readable failure reason. Filtered by ANALYZING state so a migration
// that already advanced (or was already failed) is not affected.
func (w *MongoSummaryWriter) MarkFailed(ctx context.Context, migrationID uint64, reason string) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	_, err := w.migrationColl.UpdateOne(
		ctx,
		bson.M{
			"identifier":  migrationID,
			"state":       int32(migrationdomain.MigrationStateAnalyzing),
			"delete_time": nil,
		},
		bson.M{"$set": bson.M{
			"state":          int32(migrationdomain.MigrationStateFailed),
			"failure_reason": reason,
			"update_time":    now,
		}},
	)
	return err
}
