package repositories

import (
	"testing"

	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"

	"github.com/stretchr/testify/assert"
)

func TestOperationFromString(t *testing.T) {
	t.Parallel()
	cases := map[string]billingv1.UsageOperation{
		"assessment": billingv1.UsageOperation_USAGE_OPERATION_ASSESSMENT,
		"analysis":   billingv1.UsageOperation_USAGE_OPERATION_ANALYSIS,
		"migration":  billingv1.UsageOperation_USAGE_OPERATION_MIGRATION,
		"generation": billingv1.UsageOperation_USAGE_OPERATION_GENERATION,
		"":           billingv1.UsageOperation_USAGE_OPERATION_UNSPECIFIED,
		"bogus":      billingv1.UsageOperation_USAGE_OPERATION_UNSPECIFIED,
	}
	for in, want := range cases {
		assert.Equal(t, want, operationFromString(in), "operationFromString(%q)", in)
	}
}
