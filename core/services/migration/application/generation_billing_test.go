package application_test

import (
	"context"
	"testing"

	billingdomain "milton_prism/core/services/billing/domain"
	"milton_prism/core/services/migration/application"
	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/mocks"
	"milton_prism/core/services/migration/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newSvcWithGenBilling wires a Service with a billing client AND a generation
// result reader so the GENERATION finalize path is exercised.
func newSvcWithGenBilling(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockGenerationResultReader, *mocks.MockBillingClient) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	reader := &mocks.MockGenerationResultReader{}
	billing := &mocks.MockBillingClient{}
	svc := application.NewService(repo, tx, nil, nil, nil, nil, nil, nil, reader, nil, nil, nil, nil, nil, "").
		WithBillingClient(billing)
	return svc, repo, reader, billing
}

// TestFinalizeGenerationBilling_RecordsEstimatedSpend verifies that on a READY
// migration with token totals and NO real API cost, GetMigration records a
// GENERATION spend with the estimated cost attributed to the owner.
func TestFinalizeGenerationBilling_RecordsEstimatedSpend(t *testing.T) {
	svc, repo, reader, billing := newSvcWithGenBilling(t)

	m := &domain.Migration{Identifier: 28, OwnerUserId: 10001, State: domain.MigrationStateReady}
	repo.On("GetByID", mock.Anything, uint64(28), false).Return(m, nil)
	reader.On("ReadResults", mock.Anything, uint64(28)).Return(nil, nil)
	// No existing GENERATION record → not idempotent-skipped.
	billing.On("CountUsageRecords", mock.Anything, uint64(28), billingdomain.OperationGeneration).Return(0, nil)
	// Subscription mode: real cost 0 → estimate by token.
	reader.On("ReadUsageTotals", mock.Anything, uint64(28)).Return(ports.GenerationUsageTotals{
		TokensIn: 1_000_000, TokensOut: 1_000_000, RealCostUSD: 0, Model: "claude-opus-4-8[1m]", Records: 2,
	}, nil)
	billing.On("RecordUsage", mock.Anything, mock.MatchedBy(func(s ports.UsageSpend) bool {
		return s.UserID == 10001 &&
			s.MigrationID == 28 &&
			s.Operation == billingdomain.OperationGeneration &&
			s.TokensIn == 1_000_000 &&
			s.TokensOut == 1_000_000 &&
			s.Model == "claude-opus-4-8[1m]" &&
			s.CostUSD > 0 && // estimated (5 input + 25 output = 30)
			s.CostEstimated // subscription mode ⇒ estimated flag set
	})).Return(nil)

	_, err := svc.GetMigration(context.Background(), 28)
	require.NoError(t, err)
	billing.AssertExpectations(t)
	reader.AssertExpectations(t)
}

// TestFinalizeGenerationBilling_UsesRealCostWhenPresent verifies apikey mode:
// the real API cost is recorded verbatim (no estimation).
func TestFinalizeGenerationBilling_UsesRealCostWhenPresent(t *testing.T) {
	svc, repo, reader, billing := newSvcWithGenBilling(t)

	m := &domain.Migration{Identifier: 24, OwnerUserId: 10002, State: domain.MigrationStateReady}
	repo.On("GetByID", mock.Anything, uint64(24), false).Return(m, nil)
	reader.On("ReadResults", mock.Anything, uint64(24)).Return(nil, nil)
	billing.On("CountUsageRecords", mock.Anything, uint64(24), billingdomain.OperationGeneration).Return(0, nil)
	reader.On("ReadUsageTotals", mock.Anything, uint64(24)).Return(ports.GenerationUsageTotals{
		TokensIn: 500, TokensOut: 600, RealCostUSD: 5.1753, Model: "claude-opus-4-8[1m]", Records: 1,
	}, nil)
	billing.On("RecordUsage", mock.Anything, mock.MatchedBy(func(s ports.UsageSpend) bool {
		return s.CostUSD == 5.1753 && s.MigrationID == 24 && !s.CostEstimated // real cost ⇒ estimated flag false
	})).Return(nil)

	_, err := svc.GetMigration(context.Background(), 24)
	require.NoError(t, err)
	billing.AssertExpectations(t)
}

// TestFinalizeGenerationBilling_IdempotentSkip verifies a second observation does
// NOT record again when a GENERATION record already exists.
func TestFinalizeGenerationBilling_IdempotentSkip(t *testing.T) {
	svc, repo, reader, billing := newSvcWithGenBilling(t)

	m := &domain.Migration{Identifier: 34, OwnerUserId: 10003, State: domain.MigrationStateReady}
	repo.On("GetByID", mock.Anything, uint64(34), false).Return(m, nil)
	reader.On("ReadResults", mock.Anything, uint64(34)).Return(nil, nil)
	// Already recorded → skip; ReadUsageTotals/RecordUsage must NOT be called.
	billing.On("CountUsageRecords", mock.Anything, uint64(34), billingdomain.OperationGeneration).Return(1, nil)

	_, err := svc.GetMigration(context.Background(), 34)
	require.NoError(t, err)
	billing.AssertExpectations(t)
	billing.AssertNotCalled(t, "RecordUsage", mock.Anything, mock.Anything)
	reader.AssertNotCalled(t, "ReadUsageTotals", mock.Anything, mock.Anything)
}

// TestFinalizeGenerationBilling_NoTokensNoRecord verifies that a terminal
// migration with no generated tokens records nothing.
func TestFinalizeGenerationBilling_NoTokensNoRecord(t *testing.T) {
	svc, repo, reader, billing := newSvcWithGenBilling(t)

	m := &domain.Migration{Identifier: 99, OwnerUserId: 10004, State: domain.MigrationStateFailed}
	repo.On("GetByID", mock.Anything, uint64(99), false).Return(m, nil)
	reader.On("ReadResults", mock.Anything, uint64(99)).Return(nil, nil)
	billing.On("CountUsageRecords", mock.Anything, uint64(99), billingdomain.OperationGeneration).Return(0, nil)
	reader.On("ReadUsageTotals", mock.Anything, uint64(99)).Return(ports.GenerationUsageTotals{Records: 0}, nil)

	_, err := svc.GetMigration(context.Background(), 99)
	require.NoError(t, err)
	billing.AssertNotCalled(t, "RecordUsage", mock.Anything, mock.Anything)
}

// TestFinalizeGenerationBilling_RecorderErrorSwallowed verifies a billing failure
// never breaks the read.
func TestFinalizeGenerationBilling_RecorderErrorSwallowed(t *testing.T) {
	svc, repo, reader, billing := newSvcWithGenBilling(t)

	m := &domain.Migration{Identifier: 38, OwnerUserId: 10005, State: domain.MigrationStateReady}
	repo.On("GetByID", mock.Anything, uint64(38), false).Return(m, nil)
	reader.On("ReadResults", mock.Anything, uint64(38)).Return(nil, nil)
	billing.On("CountUsageRecords", mock.Anything, uint64(38), billingdomain.OperationGeneration).Return(0, nil)
	reader.On("ReadUsageTotals", mock.Anything, uint64(38)).Return(ports.GenerationUsageTotals{
		TokensIn: 100, TokensOut: 200, Model: "claude-opus-4-8[1m]", Records: 1,
	}, nil)
	billing.On("RecordUsage", mock.Anything, mock.Anything).Return(assert.AnError)

	got, err := svc.GetMigration(context.Background(), 38)
	require.NoError(t, err, "billing failure must be swallowed")
	assert.NotNil(t, got)
}
