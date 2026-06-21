package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"milton_prism/core/services/analysis/domain"
	"milton_prism/core/services/analysis/ports"
	applog "milton_prism/pkg/log"
	analysissvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
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

const analysisSummariesCollName = "analysis_summaries"

var _ ports.AnalysisSummaryRepository = (*MongoAnalysisSummaryRepository)(nil)

// mongoAnalysisSummaryDoc is the BSON representation of an AnalysisSummary.
// Repeated proto sub-messages are serialised as bytes to avoid maintaining
// parallel BSON structs for complex nested types.
type mongoAnalysisSummaryDoc struct {
	ID                         primitive.ObjectID  `bson:"_id,omitempty"`
	Identifier                 uint64              `bson:"identifier"`
	RepositoryID               uint64              `bson:"repository_id"`
	MigrationID                uint64              `bson:"migration_id,omitempty"`
	OwnerUserID                uint64              `bson:"owner_user_id,omitempty"`
	RepositoryURL              string              `bson:"repository_url,omitempty"`
	SourceBranch               string              `bson:"source_branch,omitempty"`
	CommitSHA                  string              `bson:"commit_sha,omitempty"`
	State                      int32               `bson:"state"`
	TechnologiesBytes          []byte              `bson:"technologies_bytes,omitempty"`
	VulnerabilitiesBytes       []byte              `bson:"vulnerabilities_bytes,omitempty"`
	DependencyGraphBytes       []byte              `bson:"dependency_graph_bytes,omitempty"`
	ModuleCardsBytes           []byte              `bson:"module_cards_bytes,omitempty"`
	BlueprintsBytes            []byte              `bson:"blueprints_bytes,omitempty"`
	ModuleClassificationBytes  []byte              `bson:"module_classification_bytes,omitempty"`
	MigrabilityScoreBytes      []byte              `bson:"migrability_score_bytes,omitempty"`
	MigrabilityAssessmentBytes []byte              `bson:"migrability_assessment_bytes,omitempty"`
	SharedStateHubsBytes       []byte              `bson:"shared_state_hubs_bytes,omitempty"`
	UnreachableModulesBytes    []byte              `bson:"unreachable_modules_bytes,omitempty"`
	DatabaseDetectionBytes     []byte              `bson:"database_detection_bytes,omitempty"`
	ArchitecturalPatternBytes  []byte              `bson:"architectural_pattern_bytes,omitempty"`
	IntakeAssessmentBytes      []byte              `bson:"intake_assessment_bytes,omitempty"`
	SecurityFindingsBytes      []byte              `bson:"security_findings_bytes,omitempty"`
	DeepAnalysisAvailable      bool                `bson:"deep_analysis_available,omitempty"`
	TotalFiles                 uint64              `bson:"total_files,omitempty"`
	TotalLines                 uint64              `bson:"total_lines,omitempty"`
	ModuleCountProduction      int64               `bson:"module_count_production,omitempty"`
	ModuleCountTest            int64               `bson:"module_count_test,omitempty"`
	CreateTime                 primitive.DateTime  `bson:"create_time"`
	UpdateTime                 *primitive.DateTime `bson:"update_time,omitempty"`
	DeleteTime                 *primitive.DateTime `bson:"delete_time,omitempty"`
	PurgeTime                  *primitive.DateTime `bson:"purge_time,omitempty"`
}

// MongoAnalysisSummaryRepository persists AnalysisSummary records in MongoDB.
type MongoAnalysisSummaryRepository struct {
	db   *mongo.Database
	coll *mongo.Collection
}

func NewMongoAnalysisSummaryRepository(db *mongo.Database) *MongoAnalysisSummaryRepository {
	r := &MongoAnalysisSummaryRepository{db: db, coll: db.Collection(analysisSummariesCollName)}
	if _, err := r.coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{Keys: bson.D{{Key: "identifier", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "repository_id", Value: 1}}},
		{Keys: bson.D{{Key: "migration_id", Value: 1}}},
		{Keys: bson.D{{Key: "state", Value: 1}}},
		// Supports "latest COMPLETED analysis for repo+branch" dedup lookup.
		{Keys: bson.D{
			{Key: "repository_id", Value: 1},
			{Key: "source_branch", Value: 1},
			{Key: "state", Value: 1},
			{Key: "create_time", Value: -1},
		}},
	}); err != nil {
		applog.Warningf("mongo: create indexes on %s: error=%v", analysisSummariesCollName, err)
	}
	return r
}

func (r *MongoAnalysisSummaryRepository) Create(ctx context.Context, s *domain.AnalysisSummary) (*domain.AnalysisSummary, error) {
	id, err := generateIdentifier(ctx, r.db, analysisSummariesCollName)
	if err != nil {
		return nil, fmt.Errorf("analysis: identifier: %w", err)
	}
	doc, err := summaryToDoc(s)
	if err != nil {
		return nil, fmt.Errorf("analysis: serialize: %w", err)
	}
	doc.Identifier = id
	doc.CreateTime = primitive.NewDateTimeFromTime(time.Now().UTC())
	if _, err := r.coll.InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("analysis: insert failed: %w", err)
	}
	return summaryDocToDomain(doc)
}

func (r *MongoAnalysisSummaryRepository) GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.AnalysisSummary, error) {
	q := bson.M{"identifier": identifier}
	if !includeDeleted {
		q["delete_time"] = nil
	}
	var doc mongoAnalysisSummaryDoc
	if err := r.coll.FindOne(ctx, q).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, domain.ErrAnalysisSummaryNotFound
		}
		return nil, fmt.Errorf("analysis: find one failed: %w", err)
	}
	return summaryDocToDomain(&doc)
}

func (r *MongoAnalysisSummaryRepository) List(ctx context.Context, filter *analysissvcv1.AnalysisSummariesFilter, params *queryparamsv1.PageQueryParams) ([]*domain.AnalysisSummary, *paginationv1.Pagination, error) {
	q := bson.M{"delete_time": nil}
	if filter != nil {
		if filter.RepositoryId != nil && filter.GetRepositoryId() != 0 {
			q["repository_id"] = filter.GetRepositoryId()
		}
		// standalone=true takes precedence over migration_id: return only analyses
		// with no migration link (field absent or explicitly 0, which omitempty skips).
		if filter.GetStandalone() {
			q["$or"] = bson.A{
				bson.M{"migration_id": bson.M{"$exists": false}},
				bson.M{"migration_id": 0},
			}
		} else if filter.MigrationId != nil && filter.GetMigrationId() != 0 {
			q["migration_id"] = filter.GetMigrationId()
		}
		if len(filter.GetStates()) > 0 {
			in := make(bson.A, 0, len(filter.GetStates()))
			for _, s := range filter.GetStates() {
				in = append(in, int32(s))
			}
			q["state"] = bson.M{"$in": in}
		} else if filter.State != nil && filter.GetState() != analysisv1.AnalysisState_ANALYSIS_STATE_UNSPECIFIED {
			q["state"] = int32(filter.GetState())
		}
		if filter.SourceBranch != nil && filter.GetSourceBranch() != "" {
			q["source_branch"] = filter.GetSourceBranch()
		}
		if filter.OwnerUserId != nil && filter.GetOwnerUserId() != 0 {
			q["owner_user_id"] = filter.GetOwnerUserId()
		}
	}
	pageNumber := params.GetPageNumber()
	if pageNumber == 0 {
		pageNumber = 1
	}
	skip := int64((pageNumber - 1) * params.GetPageSize())
	sortOrder := -1 // descending (newest first) by default
	if params.GetOrder() == queryparamsv1.PageQueryParams_ORDER_ASC {
		sortOrder = 1
	}
	sortField := params.GetSortBy()
	if sortField == "" {
		sortField = "create_time"
	}
	// Exclude the heavy blob fields (graph, cards, classification, blueprints,
	// LLM assessment) from list responses. summaryDocToDomain skips nil slices,
	// so only the lightweight fields travel on the wire per page. Callers that
	// need the full payload use GetByID instead.
	listProjection := bson.M{
		"dependency_graph_bytes":       0,
		"module_cards_bytes":           0,
		"blueprints_bytes":             0,
		"module_classification_bytes":  0,
		"migrability_assessment_bytes": 0,
	}
	opts := options.Find().
		SetSort(bson.D{{Key: sortField, Value: sortOrder}}).
		SetSkip(skip).
		SetLimit(int64(params.GetPageSize())).
		SetProjection(listProjection)
	cur, err := r.coll.Find(ctx, q, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("analysis: list failed: %w", err)
	}
	defer cur.Close(ctx)
	var docs []mongoAnalysisSummaryDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("analysis: decode failed: %w", err)
	}
	total, _ := r.coll.CountDocuments(ctx, q)
	out := make([]*domain.AnalysisSummary, 0, len(docs))
	for i := range docs {
		s, err := summaryDocToDomain(&docs[i])
		if err != nil {
			return nil, nil, fmt.Errorf("analysis: deserialize: %w", err)
		}
		out = append(out, s)
	}
	return out, impl.NewPagination(params.GetPageNumber(), params.GetPageSize(), uint64(total)), nil
}

func (r *MongoAnalysisSummaryRepository) SoftDelete(ctx context.Context, identifier uint64) error {
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{"delete_time": now, "update_time": now}},
	)
	if err != nil {
		return fmt.Errorf("analysis: soft delete failed: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrAnalysisSummaryNotFound
	}
	return nil
}

// wrapperForTechnologies is a scratch proto message used to batch-marshal
// a slice of Technology into a single bytes field.
type wrapperForTechnologies struct {
	Items []*analysisv1.Technology `protobuf:"bytes,1,rep,name=items,proto3" json:"items,omitempty"`
}

func (m *wrapperForTechnologies) ProtoMessage()               {}
func (m *wrapperForTechnologies) ProtoReflect() proto.Message { return nil }
func (m *wrapperForTechnologies) Reset()                      {}
func (m *wrapperForTechnologies) String() string              { return "" }

func summaryToDoc(s *domain.AnalysisSummary) (*mongoAnalysisSummaryDoc, error) {
	doc := &mongoAnalysisSummaryDoc{
		RepositoryID:  s.GetRepositoryId(),
		MigrationID:   s.GetMigrationId(),
		OwnerUserID:   s.GetOwnerUserId(),
		RepositoryURL: s.GetRepositoryUrl(),
		SourceBranch:  s.GetSourceBranch(),
		CommitSHA:     s.GetCommitSha(),
		State:         int32(s.GetState()),
		TotalFiles:    s.GetTotalFiles(),
		TotalLines:    s.GetTotalLines(),
	}
	// Encode repeated fields as wrapped proto bytes.
	if len(s.GetTechnologies()) > 0 {
		b, err := marshalTechnologies(s.GetTechnologies())
		if err != nil {
			return nil, fmt.Errorf("marshal technologies: %w", err)
		}
		doc.TechnologiesBytes = b
	}
	if len(s.GetVulnerabilities()) > 0 {
		b, err := marshalVulnerabilities(s.GetVulnerabilities())
		if err != nil {
			return nil, fmt.Errorf("marshal vulnerabilities: %w", err)
		}
		doc.VulnerabilitiesBytes = b
	}
	if len(s.GetDependencyGraph()) > 0 {
		b, err := marshalDependencyGraph(s.GetDependencyGraph())
		if err != nil {
			return nil, fmt.Errorf("marshal dependency_graph: %w", err)
		}
		doc.DependencyGraphBytes = b
	}
	return doc, nil
}

func summaryDocToDomain(d *mongoAnalysisSummaryDoc) (*domain.AnalysisSummary, error) {
	if d == nil {
		return nil, nil
	}
	out := &domain.AnalysisSummary{
		Identifier:            d.Identifier,
		RepositoryId:          d.RepositoryID,
		MigrationId:           d.MigrationID,
		OwnerUserId:           d.OwnerUserID,
		RepositoryUrl:         d.RepositoryURL,
		SourceBranch:          d.SourceBranch,
		CommitSha:             d.CommitSHA,
		State:                 analysisv1.AnalysisState(d.State),
		TotalFiles:            d.TotalFiles,
		TotalLines:            d.TotalLines,
		ModuleCountProduction: uint32(d.ModuleCountProduction),
		ModuleCountTest:       uint32(d.ModuleCountTest),
		DeepAnalysisAvailable: d.DeepAnalysisAvailable,
	}
	if len(d.TechnologiesBytes) > 0 {
		items, err := unmarshalTechnologies(d.TechnologiesBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal technologies: %w", err)
		}
		out.Technologies = items
	}
	if len(d.VulnerabilitiesBytes) > 0 {
		items, err := unmarshalVulnerabilities(d.VulnerabilitiesBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal vulnerabilities: %w", err)
		}
		out.Vulnerabilities = items
	}
	if len(d.DependencyGraphBytes) > 0 {
		items, err := unmarshalDependencyGraph(d.DependencyGraphBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal dependency_graph: %w", err)
		}
		out.DependencyGraph = items
	}
	if len(d.ModuleCardsBytes) > 0 {
		items, err := unmarshalModuleCards(d.ModuleCardsBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal module_cards: %w", err)
		}
		out.ModuleCards = items
	}
	if len(d.BlueprintsBytes) > 0 {
		items, err := unmarshalBlueprints(d.BlueprintsBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal blueprints: %w", err)
		}
		out.Blueprints = items
	}
	if len(d.ModuleClassificationBytes) > 0 {
		mc, err := unmarshalModuleClassification(d.ModuleClassificationBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal module_classification: %w", err)
		}
		out.ModuleClassification = mc
	}
	if len(d.MigrabilityScoreBytes) > 0 {
		ms, err := unmarshalMigrabilityScore(d.MigrabilityScoreBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal migrability_score: %w", err)
		}
		out.MigrabilityScore = ms
	}
	if len(d.MigrabilityAssessmentBytes) > 0 {
		ma, err := unmarshalMigrabilityAssessment(d.MigrabilityAssessmentBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal migrability_assessment: %w", err)
		}
		out.MigrabilityAssessment = ma
	}
	if len(d.SharedStateHubsBytes) > 0 {
		hubs, err := unmarshalSharedStateHubs(d.SharedStateHubsBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal shared_state_hubs: %w", err)
		}
		out.SharedStateHubs = hubs
	}
	if len(d.UnreachableModulesBytes) > 0 {
		unreachable, err := unmarshalUnreachableModules(d.UnreachableModulesBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal unreachable_modules: %w", err)
		}
		out.UnreachableModules = unreachable
	}
	if len(d.DatabaseDetectionBytes) > 0 {
		dd, err := unmarshalDatabaseDetection(d.DatabaseDetectionBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal database_detection: %w", err)
		}
		out.DatabaseDetection = dd
	}
	if len(d.ArchitecturalPatternBytes) > 0 {
		ap, err := unmarshalArchitecturalPattern(d.ArchitecturalPatternBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal architectural_pattern: %w", err)
		}
		out.ArchitecturalPattern = ap
	}
	if len(d.IntakeAssessmentBytes) > 0 {
		ia, err := unmarshalIntakeAssessment(d.IntakeAssessmentBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal intake_assessment: %w", err)
		}
		out.IntakeAssessment = ia
	}
	if len(d.SecurityFindingsBytes) > 0 {
		sf, err := unmarshalSecurityFindings(d.SecurityFindingsBytes)
		if err != nil {
			return nil, fmt.Errorf("unmarshal security_findings: %w", err)
		}
		out.SecurityFindings = sf
	}
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

// marshalTechnologies / unmarshalTechnologies encode a slice by individually
// marshalling each item and concatenating with a length-prefix so they can be
// stored as a single []byte BSON field.
//
// For a scaffold implementation the simplest correct approach is to marshal the
// slice via a wrapper AnalysisSummary carrying only the technologies field,
// then extract that field on unmarshal.
func marshalTechnologies(items []*analysisv1.Technology) ([]byte, error) {
	wrapper := &analysisv1.AnalysisSummary{Technologies: items}
	b, err := proto.Marshal(wrapper)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func unmarshalTechnologies(b []byte) ([]*analysisv1.Technology, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetTechnologies(), nil
}

func marshalVulnerabilities(items []*analysisv1.Vulnerability) ([]byte, error) {
	wrapper := &analysisv1.AnalysisSummary{Vulnerabilities: items}
	b, err := proto.Marshal(wrapper)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func unmarshalVulnerabilities(b []byte) ([]*analysisv1.Vulnerability, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetVulnerabilities(), nil
}

func marshalDependencyGraph(items []*analysisv1.DependencyEdge) ([]byte, error) {
	wrapper := &analysisv1.AnalysisSummary{DependencyGraph: items}
	b, err := proto.Marshal(wrapper)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func unmarshalDependencyGraph(b []byte) ([]*analysisv1.DependencyEdge, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetDependencyGraph(), nil
}

func unmarshalModuleCards(b []byte) ([]*analysisv1.ModuleCard, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetModuleCards(), nil
}

func unmarshalBlueprints(b []byte) ([]*analysisv1.BlueprintInfo, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetBlueprints(), nil
}

func unmarshalModuleClassification(b []byte) (*analysisv1.ModuleClassification, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetModuleClassification(), nil
}

// UpdateMigrabilityScore persists the full MigrabilityScore proto (including
// score_band, structural_findings, typed_blockers) on an existing summary.
// Called from the assessment RPC path so the score bytes carry the new fields
// even when the decomposition worker pre-dated the contract extension.
func (r *MongoAnalysisSummaryRepository) UpdateMigrabilityScore(ctx context.Context, identifier uint64, score *commonv1.MigrabilityScore) error {
	b, err := proto.Marshal(&analysisv1.AnalysisSummary{MigrabilityScore: score})
	if err != nil {
		return fmt.Errorf("analysis: marshal migrability_score: %w", err)
	}
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{
			"migrability_score_bytes": b,
			"update_time":             now,
		}},
	)
	if err != nil {
		return fmt.Errorf("analysis: update migrability_score: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrAnalysisSummaryNotFound
	}
	return nil
}

// UpdateMigrabilityAssessment persists the LLM assessment on an existing summary.
func (r *MongoAnalysisSummaryRepository) UpdateMigrabilityAssessment(ctx context.Context, identifier uint64, assessment *domain.MigrabilityAssessment) error {
	b, err := proto.Marshal(&analysisv1.AnalysisSummary{MigrabilityAssessment: assessment})
	if err != nil {
		return fmt.Errorf("analysis: marshal migrability_assessment: %w", err)
	}
	now := primitive.NewDateTimeFromTime(time.Now().UTC())
	res, err := r.coll.UpdateOne(
		ctx,
		bson.M{"identifier": identifier, "delete_time": nil},
		bson.M{"$set": bson.M{
			"migrability_assessment_bytes": b,
			"update_time":                  now,
		}},
	)
	if err != nil {
		return fmt.Errorf("analysis: update migrability_assessment: %w", err)
	}
	if res.MatchedCount == 0 {
		return domain.ErrAnalysisSummaryNotFound
	}
	return nil
}

func unmarshalSharedStateHubs(b []byte) ([]*analysisv1.SharedStateHub, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetSharedStateHubs(), nil
}

func unmarshalUnreachableModules(b []byte) ([]*analysisv1.UnreachableModule, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetUnreachableModules(), nil
}

func unmarshalMigrabilityScore(b []byte) (*commonv1.MigrabilityScore, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	ms := wrapper.GetMigrabilityScore()
	if ms != nil {
		ms.StructuralVerdict = deriveStructuralVerdict(ms)
	}
	return ms, nil
}

// deriveStructuralVerdict maps score signals to a deterministic verdict string
// for cases that can be classified without an LLM. Returned string is empty
// when the signals are ambiguous (use migrability_assessment.verdict instead).
//
// Rule: domain_presence penalty=40 means DomainEmpty=true — no domain layer
// exists, so automatic decomposition is structurally impossible. This is the
// only signal that maps unambiguously to a verdict without LLM involvement.
func deriveStructuralVerdict(ms *commonv1.MigrabilityScore) string {
	for _, s := range ms.GetSignals() {
		if s.GetSignal() == "domain_presence" && s.GetPenalty() == 40 {
			return "NO_SERVICE_BOUNDARIES"
		}
	}
	return ""
}

func unmarshalMigrabilityAssessment(b []byte) (*commonv1.MigrabilityAssessment, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetMigrabilityAssessment(), nil
}

func unmarshalDatabaseDetection(b []byte) (*analysisv1.DatabaseDetection, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetDatabaseDetection(), nil
}

func unmarshalArchitecturalPattern(b []byte) (*analysisv1.ArchitecturalPattern, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetArchitecturalPattern(), nil
}

func unmarshalIntakeAssessment(b []byte) (*analysisv1.IntakeAssessment, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetIntakeAssessment(), nil
}

func unmarshalSecurityFindings(b []byte) ([]*analysisv1.SecurityFinding, error) {
	wrapper := &analysisv1.AnalysisSummary{}
	if err := proto.Unmarshal(b, wrapper); err != nil {
		return nil, err
	}
	return wrapper.GetSecurityFindings(), nil
}
