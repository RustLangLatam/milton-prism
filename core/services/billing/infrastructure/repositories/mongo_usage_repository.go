// Package repositories contains MongoDB-backed implementations of the billing
// service's driven ports.
package repositories

import (
	"context"
	"fmt"
	"time"

	"milton_prism/core/services/billing/domain"
	"milton_prism/core/services/billing/ports"
	applog "milton_prism/pkg/log"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"
	"milton_prism/pkg/pb/impl"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const usageRecordsCollName = "usage_records"

var _ ports.UsageRepository = (*MongoUsageRepository)(nil)

type mongoUsageDoc struct {
	ID            primitive.ObjectID `bson:"_id,omitempty"`
	Identifier    uint64             `bson:"identifier"`
	UserID        uint64             `bson:"user_id"`
	AnalysisID    uint64             `bson:"analysis_id,omitempty"`
	MigrationID   uint64             `bson:"migration_id,omitempty"`
	ServiceName   string             `bson:"service_name,omitempty"`
	Operation     int32              `bson:"operation"`
	TokensIn      int64              `bson:"tokens_in"`
	TokensOut     int64              `bson:"tokens_out"`
	CostUSD       float64            `bson:"cost_usd"`
	Model         string             `bson:"model,omitempty"`
	CostEstimated bool               `bson:"cost_estimated,omitempty"`
	CreateTime    primitive.DateTime `bson:"create_time"`
}

// MongoUsageRepository persists usage records in MongoDB.
type MongoUsageRepository struct {
	db   *mongo.Database
	coll *mongo.Collection
}

// NewMongoUsageRepository constructs the repository and ensures attribution
// indexes exist (best-effort; index errors are logged, not fatal).
func NewMongoUsageRepository(db *mongo.Database) *MongoUsageRepository {
	r := &MongoUsageRepository{db: db, coll: db.Collection(usageRecordsCollName)}
	if _, err := r.coll.Indexes().CreateMany(context.Background(), []mongo.IndexModel{
		{Keys: bson.D{{Key: "identifier", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "user_id", Value: 1}}},
		{Keys: bson.D{{Key: "analysis_id", Value: 1}}},
		{Keys: bson.D{{Key: "migration_id", Value: 1}}},
	}); err != nil {
		applog.Warningf("mongo: create indexes on %s: error=%v", usageRecordsCollName, err)
	}
	return r
}

func (r *MongoUsageRepository) Record(ctx context.Context, rec *domain.UsageRecord) (*domain.UsageRecord, error) {
	id, err := generateIdentifier(ctx, r.db, usageRecordsCollName)
	if err != nil {
		return nil, fmt.Errorf("usage: identifier: %w", err)
	}
	now := time.Now().UTC()
	doc := mongoUsageDoc{
		Identifier:    id,
		UserID:        rec.GetUserId(),
		AnalysisID:    rec.GetAnalysisId(),
		MigrationID:   rec.GetMigrationId(),
		ServiceName:   rec.GetServiceName(),
		Operation:     int32(rec.GetOperation()),
		TokensIn:      rec.GetTokensIn(),
		TokensOut:     rec.GetTokensOut(),
		CostUSD:       rec.GetCostUsd(),
		Model:         rec.GetModel(),
		CostEstimated: rec.GetCostEstimated(),
		CreateTime:    primitive.NewDateTimeFromTime(now),
	}
	if _, err := r.coll.InsertOne(ctx, doc); err != nil {
		return nil, fmt.Errorf("usage: insert failed: %w", err)
	}
	return usageDocToDomain(&doc), nil
}

func (r *MongoUsageRepository) List(ctx context.Context, filter ports.UsageFilter, params *queryparamsv1.PageQueryParams) ([]*domain.UsageRecord, *paginationv1.Pagination, error) {
	q := buildUsageQuery(filter)
	pageNumber := params.GetPageNumber()
	pageSize := params.GetPageSize()
	if pageNumber == 0 {
		pageNumber = 1
	}
	if pageSize == 0 {
		pageSize = 50
	}
	skip := int64((pageNumber - 1) * pageSize)
	opts := options.Find().
		SetSkip(skip).
		SetLimit(int64(pageSize)).
		SetSort(bson.D{{Key: "create_time", Value: -1}})
	cur, err := r.coll.Find(ctx, q, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("usage: list failed: %w", err)
	}
	defer cur.Close(ctx)
	var docs []mongoUsageDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, nil, fmt.Errorf("usage: decode failed: %w", err)
	}
	total, _ := r.coll.CountDocuments(ctx, q)
	out := make([]*domain.UsageRecord, 0, len(docs))
	for i := range docs {
		out = append(out, usageDocToDomain(&docs[i]))
	}
	return out, impl.NewPagination(pageNumber, pageSize, uint64(total)), nil
}

func (r *MongoUsageRepository) Aggregate(ctx context.Context, filter ports.UsageFilter) (*domain.UsageTotals, []*domain.OperationUsage, error) {
	q := buildUsageQuery(filter)
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: q}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$operation"},
			{Key: "record_count", Value: bson.D{{Key: "$sum", Value: 1}}},
			{Key: "tokens_in", Value: bson.D{{Key: "$sum", Value: "$tokens_in"}}},
			{Key: "tokens_out", Value: bson.D{{Key: "$sum", Value: "$tokens_out"}}},
			{Key: "cost_usd", Value: bson.D{{Key: "$sum", Value: "$cost_usd"}}},
		}}},
	}
	cur, err := r.coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, nil, fmt.Errorf("usage: aggregate failed: %w", err)
	}
	defer cur.Close(ctx)

	type aggRow struct {
		Operation   int32   `bson:"_id"`
		RecordCount int64   `bson:"record_count"`
		TokensIn    int64   `bson:"tokens_in"`
		TokensOut   int64   `bson:"tokens_out"`
		CostUSD     float64 `bson:"cost_usd"`
	}
	var rows []aggRow
	if err := cur.All(ctx, &rows); err != nil {
		return nil, nil, fmt.Errorf("usage: aggregate decode failed: %w", err)
	}

	total := &domain.UsageTotals{}
	byOp := make([]*domain.OperationUsage, 0, len(rows))
	for _, row := range rows {
		t := &domain.UsageTotals{
			RecordCount: row.RecordCount,
			TokensIn:    row.TokensIn,
			TokensOut:   row.TokensOut,
			CostUsd:     row.CostUSD,
		}
		byOp = append(byOp, &domain.OperationUsage{
			Operation: billingv1.UsageOperation(row.Operation),
			Totals:    t,
		})
		total.RecordCount += row.RecordCount
		total.TokensIn += row.TokensIn
		total.TokensOut += row.TokensOut
		total.CostUsd += row.CostUSD
	}
	return total, byOp, nil
}

func buildUsageQuery(filter ports.UsageFilter) bson.M {
	q := bson.M{}
	if filter.UserID != 0 {
		q["user_id"] = filter.UserID
	}
	if filter.AnalysisID != 0 {
		q["analysis_id"] = filter.AnalysisID
	}
	if filter.MigrationID != 0 {
		q["migration_id"] = filter.MigrationID
	}
	return q
}

func usageDocToDomain(d *mongoUsageDoc) *domain.UsageRecord {
	if d == nil {
		return nil
	}
	out := &billingv1.UsageRecord{
		Identifier:    d.Identifier,
		UserId:        d.UserID,
		AnalysisId:    d.AnalysisID,
		MigrationId:   d.MigrationID,
		ServiceName:   d.ServiceName,
		Operation:     billingv1.UsageOperation(d.Operation),
		TokensIn:      d.TokensIn,
		TokensOut:     d.TokensOut,
		CostUsd:       d.CostUSD,
		Model:         d.Model,
		CostEstimated: d.CostEstimated,
	}
	if d.CreateTime != 0 {
		out.CreateTime = timestamppb.New(d.CreateTime.Time())
	}
	return out
}
