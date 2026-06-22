package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/ports"
	applog "milton_prism/pkg/log"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
	"milton_prism/pkg/pb/impl"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const migrationsCollName = "migrations"

var _ ports.MigrationRepository = (*MongoMigrationRepository)(nil)

// mongoMigrationDoc is the BSON representation of a Migration record.
// Nested proto messages are serialised as bytes to avoid maintaining parallel structs.
type mongoMigrationDoc struct {
	ID                      primitive.ObjectID  `bson:"_id,omitempty"`
	Identifier              uint64              `bson:"identifier"`
	RepositoryID            uint64              `bson:"repository_id"`
	RepositoryURL           string              `bson:"repository_url,omitempty"`
	OwnerUserID             uint64              `bson:"owner_user_id"`
	SourceBranch            string              `bson:"source_branch,omitempty"`
	RootSubdirectory        string              `bson:"root_subdirectory,omitempty"`
	State                   int32               `bson:"state"`
	TargetBytes             []byte              `bson:"target_bytes,omitempty"`
	AnalysisSummaryID       uint64              `bson:"analysis_summary_id,omitempty"`
	SourceAnalysisSummaryID uint64              `bson:"source_analysis_summary_id,omitempty"`
	PlanBytes               []byte              `bson:"plan_bytes,omitempty"`
	OutputBytes             []byte              `bson:"output_bytes,omitempty"`
	AssessmentBytes         []byte              `bson:"assessment_bytes,omitempty"`
	MigrabilityOverride     bool                `bson:"migrability_override,omitempty"`
	AnalysisReused          bool                `bson:"analysis_reused,omitempty"`
	AutoApprove             bool                `bson:"auto_approve,omitempty"`
	RoadmapBytes            []byte              `bson:"roadmap_bytes,omitempty"`
	EnrichmentBytes         []byte              `bson:"enrichment_bytes,omitempty"`
	BlueprintBytes          []byte              `bson:"blueprint_bytes,omitempty"`
	CreateTime              primitive.DateTime  `bson:"create_time"`
	UpdateTime              *primitive.DateTime `bson:"update_time,omitempty"`
	DeleteTime              *primitive.DateTime `bson:"delete_time,omitempty"`
	PurgeTime               *primitive.DateTime `bson:"purge_time,omitempty"`
}

// MongoMigrationRepository persists Migration records in MongoDB.
type MongoMigrationRepository struct {
	db   *mongo.Database
	coll *mongo.Collection
}

func NewMongoMigrationRepository(db *mongo.Database) *MongoMigrationRepository {
	r := &MongoMigrationRepository{db: db, coll: db.Collection(migrationsCollName)}
	if _, err := r.coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{Keys: bson.D{{Key: "identifier", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "owner_user_id", Value: 1}}},
		{Keys: bson.D{{Key: "repository_id", Value: 1}}},
		{Keys: bson.D{{Key: "state", Value: 1}}},
	}); err != nil {
		applog.Warningf("mongo: create indexes on %s: error=%v", migrationsCollName, err)
	}
	return r
}

func (r *MongoMigrationRepository) Create(ctx context.Context, m *domain.Migration) (*domain.Migration, error) {
	id, err := generateIdentifier(ctx, r.db, migrationsCollName)
	if err != nil {
		return nil, fmt.Errorf("migration: identifier: %w", err)
	}
	doc, err := migrationToDoc(m)
	if err != nil {
		return nil, fmt.Errorf("migration: serialize: %w", err)
	}
	doc.Identifier = id
	doc.CreateTime = primitive.NewDateTimeFromTime(time.Now().UTC())
	if _, err := r.coll.InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("migration: insert failed: %w", err)
	}
	return migrationDocToDomain(doc)
}

func (r *MongoMigrationRepository) GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.Migration, error) {
	q := bson.M{"identifier": identifier}
	if !includeDeleted {
		q["delete_time"] = nil
	}
	var doc mongoMigrationDoc
	if err := r.coll.FindOne(ctx, q).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, domain.ErrMigrationNotFound
		}
		return nil, fmt.Errorf("migration: find one failed: %w", err)
	}
	return migrationDocToDomain(&doc)
}

func (r *MongoMigrationRepository) List(ctx context.Context, filter *domain.MigrationsFilter, params *queryparamsv1.PageQueryParams) ([]*domain.Migration, *paginationv1.Pagination, error) {
	q := bson.M{"delete_time": nil}
	if filter != nil {
		if filter.OwnerUserId != nil && filter.GetOwnerUserId() != 0 {
			q["owner_user_id"] = filter.GetOwnerUserId()
		}
		if filter.RepositoryId != nil && filter.GetRepositoryId() != 0 {
			q["repository_id"] = filter.GetRepositoryId()
		}
		if len(filter.GetStates()) > 0 {
			in := make(bson.A, 0, len(filter.GetStates()))
			for _, s := range filter.GetStates() {
				in = append(in, int32(s))
			}
			q["state"] = bson.M{"$in": in}
		} else if filter.State != nil && filter.GetState() != migrationv1.MigrationState_MIGRATION_STATE_UNSPECIFIED {
			q["state"] = int32(filter.GetState())
		}
	}
	pageNumber := params.GetPageNumber()
	if pageNumber == 0 {
		pageNumber = 1
	}
	skip := int64((pageNumber - 1) * params.GetPageSize())
	opts := options.Find().SetSkip(skip).SetLimit(int64(params.GetPageSize())).SetSort(bson.D{{Key: "create_time", Value: -1}})
	cur, err := r.coll.Find(ctx, q, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("migration: list failed: %w", err)
	}
	defer cur.Close(ctx)
	var docs []mongoMigrationDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("migration: decode failed: %w", err)
	}
	total, _ := r.coll.CountDocuments(ctx, q)
	out := make([]*domain.Migration, 0, len(docs))
	for i := range docs {
		m, err := migrationDocToDomain(&docs[i])
		if err != nil {
			return nil, nil, fmt.Errorf("migration: deserialize: %w", err)
		}
		out = append(out, m)
	}
	return out, impl.NewPagination(params.GetPageNumber(), params.GetPageSize(), uint64(total)), nil
}

func (r *MongoMigrationRepository) UpdateState(ctx context.Context, identifier uint64, state domain.MigrationState) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{"state": int32(state), "update_time": now}},
	)
	if err != nil {
		return fmt.Errorf("migration: update_state failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrMigrationNotFound
	}
	return nil
}

func (r *MongoMigrationRepository) SetRepositoryURL(ctx context.Context, identifier uint64, url string) error {
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier},
		bson.M{"$set": bson.M{"repository_url": url}},
	)
	if err != nil {
		return fmt.Errorf("migration: set_repository_url failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrMigrationNotFound
	}
	return nil
}

func (r *MongoMigrationRepository) SetMigrabilityAssessment(ctx context.Context, identifier uint64, assessment *domain.MigrabilityAssessment) error {
	b, err := proto.Marshal(assessment)
	if err != nil {
		return fmt.Errorf("migration: marshal assessment: %w", err)
	}
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{"assessment_bytes": b, "update_time": now}},
	)
	if err != nil {
		return fmt.Errorf("migration: set_migrability_assessment failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrMigrationNotFound
	}
	return nil
}

func (r *MongoMigrationRepository) SetMigrabilityOverride(ctx context.Context, identifier uint64, override bool) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{"migrability_override": override, "update_time": now}},
	)
	if err != nil {
		return fmt.Errorf("migration: set_migrability_override failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrMigrationNotFound
	}
	return nil
}

func (r *MongoMigrationRepository) SetAutoApprove(ctx context.Context, identifier uint64, autoApprove bool) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{"auto_approve": autoApprove, "update_time": now}},
	)
	if err != nil {
		return fmt.Errorf("migration: set_auto_approve failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrMigrationNotFound
	}
	return nil
}

func (r *MongoMigrationRepository) SetRestructuringRoadmap(ctx context.Context, identifier uint64, roadmap *domain.RestructuringRoadmap) error {
	b, err := proto.Marshal(roadmap)
	if err != nil {
		return fmt.Errorf("migration: marshal roadmap: %w", err)
	}
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "state": int32(domain.MigrationStateAwaitingApproval), "delete_time": nil},
		bson.M{"$set": bson.M{
			"state":         int32(domain.MigrationStateRestructuringReady),
			"roadmap_bytes": b,
			"update_time":   now,
		}},
	)
	if err != nil {
		return fmt.Errorf("migration: set_restructuring_roadmap failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrMigrationNotFound
	}
	return nil
}

func (r *MongoMigrationRepository) SetRoadmapEnrichment(ctx context.Context, identifier uint64, enrichment *domain.RoadmapEnrichment) error {
	b, err := proto.Marshal(enrichment)
	if err != nil {
		return fmt.Errorf("migration: marshal enrichment: %w", err)
	}
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{"enrichment_bytes": b, "update_time": now}},
	)
	if err != nil {
		return fmt.Errorf("migration: set_roadmap_enrichment failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrMigrationNotFound
	}
	return nil
}

func (r *MongoMigrationRepository) SetServiceBlueprint(ctx context.Context, identifier uint64, blueprint *domain.ServiceBlueprint) error {
	b, err := proto.Marshal(blueprint)
	if err != nil {
		return fmt.Errorf("migration: marshal blueprint: %w", err)
	}
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{"blueprint_bytes": b, "update_time": now}},
	)
	if err != nil {
		return fmt.Errorf("migration: set_service_blueprint failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrMigrationNotFound
	}
	return nil
}

func (r *MongoMigrationRepository) SoftDelete(ctx context.Context, identifier uint64) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{"delete_time": now, "update_time": now}},
	)
	if err != nil {
		return fmt.Errorf("migration: soft delete failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrMigrationNotFound
	}
	return nil
}

// CountByOwnerSince counts non-deleted migrations owned by ownerID with
// create_time >= since. Used for billing plan quota enforcement.
func (r *MongoMigrationRepository) CountByOwnerSince(ctx context.Context, ownerID uint64, since time.Time) (int64, error) {
	q := bson.M{
		"owner_user_id": ownerID,
		"delete_time":   nil,
		"create_time":   bson.M{"$gte": primitive.NewDateTimeFromTime(since.UTC())},
	}
	n, err := r.coll.CountDocuments(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("migration: count by owner since: %w", err)
	}
	return n, nil
}

func (r *MongoMigrationRepository) AdoptAnalysis(ctx context.Context, migrationID, analysisSummaryID uint64, sourceBranch string) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	set := bson.M{
		"state":               int32(domain.MigrationStateDesigning),
		"analysis_summary_id": analysisSummaryID,
		"analysis_reused":     true,
		"update_time":         now,
	}
	if sourceBranch != "" {
		set["source_branch"] = sourceBranch
	}
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{
			"identifier":  migrationID,
			"state":       int32(domain.MigrationStateAnalyzing),
			"delete_time": nil,
		},
		bson.M{"$set": set},
	)
	if err != nil {
		return fmt.Errorf("migration: adopt_analysis failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrMigrationNotFound
	}
	return nil
}

func migrationToDoc(m *domain.Migration) (*mongoMigrationDoc, error) {
	doc := &mongoMigrationDoc{
		RepositoryID:            m.GetRepositoryId(),
		RepositoryURL:           m.GetRepositoryUrl(),
		OwnerUserID:             m.GetOwnerUserId(),
		SourceBranch:            m.GetSourceBranch(),
		RootSubdirectory:        m.GetRootSubdirectory(),
		State:                   int32(m.GetState()),
		AnalysisSummaryID:       m.GetAnalysisSummaryId(),
		SourceAnalysisSummaryID: m.GetSourceAnalysisSummaryId(),
		MigrabilityOverride:     m.GetMigrabilityOverride(),
		AutoApprove:             m.GetAutoApprove(),
	}
	if m.GetTarget() != nil {
		b, err := proto.Marshal(m.GetTarget())
		if err != nil {
			return nil, fmt.Errorf("marshal target: %w", err)
		}
		doc.TargetBytes = b
	}
	if m.GetPlan() != nil {
		b, err := proto.Marshal(m.GetPlan())
		if err != nil {
			return nil, fmt.Errorf("marshal plan: %w", err)
		}
		doc.PlanBytes = b
	}
	if m.GetOutput() != nil {
		b, err := proto.Marshal(m.GetOutput())
		if err != nil {
			return nil, fmt.Errorf("marshal output: %w", err)
		}
		doc.OutputBytes = b
	}
	if m.GetMigrabilityAssessment() != nil {
		b, err := proto.Marshal(m.GetMigrabilityAssessment())
		if err != nil {
			return nil, fmt.Errorf("marshal assessment: %w", err)
		}
		doc.AssessmentBytes = b
	}
	if m.GetRestructuringRoadmap() != nil {
		b, err := proto.Marshal(m.GetRestructuringRoadmap())
		if err != nil {
			return nil, fmt.Errorf("marshal roadmap: %w", err)
		}
		doc.RoadmapBytes = b
	}
	if m.GetRestructuringRoadmap().GetEnrichment() != nil {
		b, err := proto.Marshal(m.GetRestructuringRoadmap().GetEnrichment())
		if err != nil {
			return nil, fmt.Errorf("marshal enrichment: %w", err)
		}
		doc.EnrichmentBytes = b
	}
	if m.GetRestructuringRoadmap().GetBlueprint() != nil {
		b, err := proto.Marshal(m.GetRestructuringRoadmap().GetBlueprint())
		if err != nil {
			return nil, fmt.Errorf("marshal blueprint: %w", err)
		}
		doc.BlueprintBytes = b
	}
	return doc, nil
}

func migrationDocToDomain(d *mongoMigrationDoc) (*domain.Migration, error) {
	if d == nil {
		return nil, nil
	}
	out := &domain.Migration{
		Identifier:              d.Identifier,
		RepositoryId:            d.RepositoryID,
		RepositoryUrl:           d.RepositoryURL,
		OwnerUserId:             d.OwnerUserID,
		SourceBranch:            d.SourceBranch,
		RootSubdirectory:        d.RootSubdirectory,
		State:                   migrationv1.MigrationState(d.State),
		AnalysisSummaryId:       d.AnalysisSummaryID,
		SourceAnalysisSummaryId: d.SourceAnalysisSummaryID,
	}
	if len(d.TargetBytes) > 0 {
		tc := &migrationv1.TargetConfig{}
		if err := proto.Unmarshal(d.TargetBytes, tc); err != nil {
			return nil, fmt.Errorf("unmarshal target: %w", err)
		}
		out.Target = tc
	}
	if len(d.PlanBytes) > 0 {
		plan := &migrationv1.RestructurePlan{}
		if err := proto.Unmarshal(d.PlanBytes, plan); err != nil {
			return nil, fmt.Errorf("unmarshal plan: %w", err)
		}
		out.Plan = plan
	}
	if len(d.OutputBytes) > 0 {
		output := &migrationv1.MigrationOutput{}
		if err := proto.Unmarshal(d.OutputBytes, output); err != nil {
			return nil, fmt.Errorf("unmarshal output: %w", err)
		}
		out.Output = output
	}
	if len(d.AssessmentBytes) > 0 {
		a := &commonv1.MigrabilityAssessment{}
		if err := proto.Unmarshal(d.AssessmentBytes, a); err != nil {
			return nil, fmt.Errorf("unmarshal assessment: %w", err)
		}
		out.MigrabilityAssessment = a
	}
	if len(d.RoadmapBytes) > 0 {
		rm := &migrationv1.RestructuringRoadmap{}
		if err := proto.Unmarshal(d.RoadmapBytes, rm); err != nil {
			return nil, fmt.Errorf("unmarshal roadmap: %w", err)
		}
		if len(d.EnrichmentBytes) > 0 {
			enr := &migrationv1.RoadmapEnrichment{}
			if err := proto.Unmarshal(d.EnrichmentBytes, enr); err != nil {
				return nil, fmt.Errorf("unmarshal enrichment: %w", err)
			}
			rm.Enrichment = enr
		}
		if len(d.BlueprintBytes) > 0 {
			bp := &migrationv1.ServiceBlueprint{}
			if err := proto.Unmarshal(d.BlueprintBytes, bp); err != nil {
				return nil, fmt.Errorf("unmarshal blueprint: %w", err)
			}
			rm.Blueprint = bp
		}
		out.RestructuringRoadmap = rm
	}
	out.MigrabilityOverride = d.MigrabilityOverride
	out.AnalysisReused = d.AnalysisReused
	out.AutoApprove = d.AutoApprove
	if d.CreateTime != 0 {
		out.CreateTime = timestamppb.New(d.CreateTime.Time())
	}
	if d.UpdateTime != nil {
		out.UpdateTime = timestamppb.New(d.UpdateTime.Time())
	}
	if d.DeleteTime != nil {
		out.DeleteTime = timestamppb.New(d.DeleteTime.Time())
	}
	if d.PurgeTime != nil {
		out.PurgeTime = timestamppb.New(d.PurgeTime.Time())
	}
	return out, nil
}
