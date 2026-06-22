package domain_test

import (
	"testing"

	"milton_prism/core/services/billing/domain"

	"github.com/stretchr/testify/assert"
)

func TestPlanCatalog_HasAtLeastThreeTiers(t *testing.T) {
	t.Parallel()
	assert.GreaterOrEqual(t, len(domain.PlanCatalog), 3)
}

func TestPlanCatalog_CodesAreUniqueAndKnown(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, p := range domain.PlanCatalog {
		assert.NotEmpty(t, p.GetCode(), "plan code must be set")
		assert.NotEmpty(t, p.GetDisplayName(), "plan display name must be set")
		assert.False(t, seen[p.GetCode()], "duplicate plan code: %s", p.GetCode())
		seen[p.GetCode()] = true
	}
	assert.True(t, seen[domain.PlanCodeFree])
	assert.True(t, seen[domain.PlanCodePro])
	assert.True(t, seen[domain.PlanCodeEnterprise])
}

func TestPlanByCode(t *testing.T) {
	t.Parallel()
	assert.NotNil(t, domain.PlanByCode(domain.PlanCodePro))
	assert.Nil(t, domain.PlanByCode("nope"))
}

func TestDefaultPlan_IsFree(t *testing.T) {
	t.Parallel()
	p := domain.DefaultPlan()
	assert.NotNil(t, p)
	assert.Equal(t, domain.PlanCodeFree, p.GetCode())
}

func TestEnterprise_IsUnlimited(t *testing.T) {
	t.Parallel()
	ent := domain.PlanByCode(domain.PlanCodeEnterprise)
	assert.Equal(t, domain.Unlimited, ent.GetMaxAnalysesPerMonth())
	assert.Equal(t, domain.Unlimited, ent.GetMaxTokensPerMonth())
}
