// Package domain holds the billing service's domain types and typed errors.
// Following the Go profile, domain types are aliases of the generated proto
// types — there is no separate mapping layer.
package domain

import (
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
)

// Type aliases — the proto types are the single source of truth.
type (
	UsageRecord    = billingv1.UsageRecord
	UsageTotals    = billingv1.UsageTotals
	OperationUsage = billingv1.OperationUsage
	UsageOperation = billingv1.UsageOperation
	Plan           = billingv1.Plan
)

// Operation enum re-exports for ergonomic use at the call sites.
const (
	OperationUnspecified = billingv1.UsageOperation_USAGE_OPERATION_UNSPECIFIED
	OperationAssessment  = billingv1.UsageOperation_USAGE_OPERATION_ASSESSMENT
	OperationAnalysis    = billingv1.UsageOperation_USAGE_OPERATION_ANALYSIS
	OperationMigration   = billingv1.UsageOperation_USAGE_OPERATION_MIGRATION
	OperationGeneration  = billingv1.UsageOperation_USAGE_OPERATION_GENERATION
)

// Unlimited is the sentinel value used in Plan limits to mean "no cap".
const Unlimited int64 = -1
