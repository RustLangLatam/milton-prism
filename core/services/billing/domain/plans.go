package domain

import billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"

// Plan codes. These are the stable machine identifiers for the catalog tiers.
const (
	PlanCodeFree       = "free"
	PlanCodePro        = "pro"
	PlanCodeEnterprise = "enterprise"
)

// DefaultPlanCode is the plan assigned to any user without an explicit
// association.
const DefaultPlanCode = PlanCodeFree

// UnlimitedCost is the float64 sentinel mirroring Unlimited (-1) for the
// monthly cost cap.
const UnlimitedCost float64 = -1

// PlanCatalog is the canonical, code-defined catalog of usage plans (≥3 tiers).
// Limits use Unlimited (-1) for "no cap". This is a deterministic backend fact;
// the frontend renders it and never invents tiers.
var PlanCatalog = []*Plan{
	{
		Code:                  PlanCodeFree,
		DisplayName:           "Free",
		MaxAnalysesPerMonth:   5,
		MaxMigrationsPerMonth: 1,
		MaxTokensPerMonth:     500_000,
		MaxConcurrency:        1,
		MonthlyCostCapUsd:     1.0,
	},
	{
		Code:                  PlanCodePro,
		DisplayName:           "Pro",
		MaxAnalysesPerMonth:   100,
		MaxMigrationsPerMonth: 25,
		MaxTokensPerMonth:     25_000_000,
		MaxConcurrency:        4,
		MonthlyCostCapUsd:     100.0,
	},
	{
		Code:                  PlanCodeEnterprise,
		DisplayName:           "Enterprise",
		MaxAnalysesPerMonth:   Unlimited,
		MaxMigrationsPerMonth: Unlimited,
		MaxTokensPerMonth:     Unlimited,
		MaxConcurrency:        Unlimited,
		MonthlyCostCapUsd:     UnlimitedCost,
	},
}

// PlanByCode returns the catalog plan with the given code, or nil when absent.
func PlanByCode(code string) *billingv1.Plan {
	for _, p := range PlanCatalog {
		if p.GetCode() == code {
			return p
		}
	}
	return nil
}

// DefaultPlan returns the plan assigned to users without an explicit
// association.
func DefaultPlan() *billingv1.Plan {
	return PlanByCode(DefaultPlanCode)
}
