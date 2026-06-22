package repositories

import (
	"context"

	"milton_prism/core/services/analysis/ports"
	billingrepo "milton_prism/core/services/billing/infrastructure/repositories"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
)

var _ ports.UsageRecorder = (*BillingUsageRecorderAdapter)(nil)

// BillingUsageRecorderAdapter implements ports.UsageRecorder by writing through
// the billing usage repository (shared analysis database). It maps the analysis
// service's operation string to the billing UsageOperation enum.
type BillingUsageRecorderAdapter struct {
	repo *billingrepo.MongoUsageRepository
}

// NewBillingUsageRecorderAdapter constructs the adapter over the shared billing
// usage repository.
func NewBillingUsageRecorderAdapter(repo *billingrepo.MongoUsageRepository) *BillingUsageRecorderAdapter {
	return &BillingUsageRecorderAdapter{repo: repo}
}

func (a *BillingUsageRecorderAdapter) RecordSpend(ctx context.Context, spend ports.UsageSpend) error {
	rec := &billingv1.UsageRecord{
		UserId:      spend.UserID,
		AnalysisId:  spend.AnalysisID,
		MigrationId: spend.MigrationID,
		Operation:   operationFromString(spend.Operation),
		TokensIn:    spend.TokensIn,
		TokensOut:   spend.TokensOut,
		CostUsd:     spend.CostUSD,
		Model:       spend.Model,
	}
	_, err := a.repo.Record(ctx, rec)
	return err
}

func operationFromString(op string) billingv1.UsageOperation {
	switch op {
	case "assessment":
		return billingv1.UsageOperation_USAGE_OPERATION_ASSESSMENT
	case "analysis":
		return billingv1.UsageOperation_USAGE_OPERATION_ANALYSIS
	case "migration":
		return billingv1.UsageOperation_USAGE_OPERATION_MIGRATION
	case "generation":
		return billingv1.UsageOperation_USAGE_OPERATION_GENERATION
	default:
		return billingv1.UsageOperation_USAGE_OPERATION_UNSPECIFIED
	}
}
