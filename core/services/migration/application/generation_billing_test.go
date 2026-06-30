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
// result reader so the per-service GENERATION finalize path is exercised.
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

// TestFinalizeGenerationBilling_RecordsEstimatedSpendPerService verifies that on
// a READY migration with two done services and NO real API cost, GetMigration
// records ONE estimated GENERATION spend per service, attributed to the owner and
// keyed by service name.
func TestFinalizeGenerationBilling_RecordsEstimatedSpendPerService(t *testing.T) {
	svc, repo, reader, billing := newSvcWithGenBilling(t)

	m := &domain.Migration{Identifier: 28, OwnerUserId: 10001, State: domain.MigrationStateReady}
	repo.On("GetByID", mock.Anything, uint64(28), false).Return(m, nil)
	reader.On("ReadResults", mock.Anything, uint64(28)).Return(nil, nil)
	// Nothing billed yet → both services are eligible.
	billing.On("ListBilledServiceNames", mock.Anything, uint64(28), billingdomain.OperationGeneration).
		Return(map[string]bool{}, nil)
	reader.On("ReadServiceUsages", mock.Anything, uint64(28)).Return([]ports.ServiceGenerationUsage{
		{ServiceName: "user", Status: "done", TokensIn: 1_000_000, TokensOut: 1_000_000, RealCostUSD: 0, Model: "claude-opus-4-8[1m]"},
		{ServiceName: "order", Status: "done", TokensIn: 1_000_000, TokensOut: 1_000_000, RealCostUSD: 0, Model: "claude-opus-4-8[1m]"},
	}, nil)
	billing.On("RecordUsage", mock.Anything, mock.MatchedBy(func(s ports.UsageSpend) bool {
		return s.UserID == 10001 &&
			s.MigrationID == 28 &&
			(s.ServiceName == "user" || s.ServiceName == "order") &&
			s.Operation == billingdomain.OperationGeneration &&
			s.TokensIn == 1_000_000 && s.TokensOut == 1_000_000 &&
			s.Model == "claude-opus-4-8[1m]" &&
			s.CostUSD > 0 && // estimated
			s.CostEstimated // subscription mode ⇒ estimated flag set
	})).Return(nil).Twice()

	_, err := svc.GetMigration(context.Background(), 28)
	require.NoError(t, err)
	billing.AssertExpectations(t)
	reader.AssertExpectations(t)
}

// TestFinalizeGenerationBilling_UsesRealCostWhenPresent verifies apikey mode:
// the real per-service API cost is recorded verbatim (no estimation).
func TestFinalizeGenerationBilling_UsesRealCostWhenPresent(t *testing.T) {
	svc, repo, reader, billing := newSvcWithGenBilling(t)

	m := &domain.Migration{Identifier: 24, OwnerUserId: 10002, State: domain.MigrationStateReady}
	repo.On("GetByID", mock.Anything, uint64(24), false).Return(m, nil)
	reader.On("ReadResults", mock.Anything, uint64(24)).Return(nil, nil)
	billing.On("ListBilledServiceNames", mock.Anything, uint64(24), billingdomain.OperationGeneration).
		Return(map[string]bool{}, nil)
	reader.On("ReadServiceUsages", mock.Anything, uint64(24)).Return([]ports.ServiceGenerationUsage{
		{ServiceName: "user", Status: "done", TokensIn: 500, TokensOut: 600, RealCostUSD: 5.1753, Model: "claude-opus-4-8[1m]"},
	}, nil)
	billing.On("RecordUsage", mock.Anything, mock.MatchedBy(func(s ports.UsageSpend) bool {
		return s.CostUSD == 5.1753 && s.MigrationID == 24 && s.ServiceName == "user" && !s.CostEstimated
	})).Return(nil)

	_, err := svc.GetMigration(context.Background(), 24)
	require.NoError(t, err)
	billing.AssertExpectations(t)
}

// TestFinalizeGenerationBilling_IdempotentSkipPerService verifies a service that
// already has a GENERATION record is NOT billed again (per-(migration,service)
// idempotency), and that only done services are billed.
func TestFinalizeGenerationBilling_IdempotentSkipPerService(t *testing.T) {
	svc, repo, reader, billing := newSvcWithGenBilling(t)

	m := &domain.Migration{Identifier: 34, OwnerUserId: 10003, State: domain.MigrationStateReady}
	repo.On("GetByID", mock.Anything, uint64(34), false).Return(m, nil)
	reader.On("ReadResults", mock.Anything, uint64(34)).Return(nil, nil)
	// "user" already billed; "order" still failed (not done) → neither is recorded.
	billing.On("ListBilledServiceNames", mock.Anything, uint64(34), billingdomain.OperationGeneration).
		Return(map[string]bool{"user": true}, nil)
	reader.On("ReadServiceUsages", mock.Anything, uint64(34)).Return([]ports.ServiceGenerationUsage{
		{ServiceName: "user", Status: "done", TokensIn: 10, TokensOut: 10, Model: "claude-opus-4-8[1m]"},
		{ServiceName: "order", Status: "failed", TokensIn: 10, TokensOut: 10, Model: "claude-opus-4-8[1m]"},
	}, nil)

	_, err := svc.GetMigration(context.Background(), 34)
	require.NoError(t, err)
	billing.AssertNotCalled(t, "RecordUsage", mock.Anything, mock.Anything)
}

// TestFinalizeGenerationBilling_NoTokensNoRecord verifies a done service with no
// token data records nothing.
func TestFinalizeGenerationBilling_NoTokensNoRecord(t *testing.T) {
	svc, repo, reader, billing := newSvcWithGenBilling(t)

	m := &domain.Migration{Identifier: 99, OwnerUserId: 10004, State: domain.MigrationStateFailed}
	repo.On("GetByID", mock.Anything, uint64(99), false).Return(m, nil)
	reader.On("ReadResults", mock.Anything, uint64(99)).Return(nil, nil)
	billing.On("ListBilledServiceNames", mock.Anything, uint64(99), billingdomain.OperationGeneration).
		Return(map[string]bool{}, nil)
	reader.On("ReadServiceUsages", mock.Anything, uint64(99)).Return([]ports.ServiceGenerationUsage{
		{ServiceName: "user", Status: "done", TokensIn: 0, TokensOut: 0},
	}, nil)

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
	billing.On("ListBilledServiceNames", mock.Anything, uint64(38), billingdomain.OperationGeneration).
		Return(map[string]bool{}, nil)
	reader.On("ReadServiceUsages", mock.Anything, uint64(38)).Return([]ports.ServiceGenerationUsage{
		{ServiceName: "user", Status: "done", TokensIn: 100, TokensOut: 200, Model: "claude-opus-4-8[1m]"},
	}, nil)
	billing.On("RecordUsage", mock.Anything, mock.Anything).Return(assert.AnError)

	got, err := svc.GetMigration(context.Background(), 38)
	require.NoError(t, err, "billing failure must be swallowed")
	assert.NotNil(t, got)
}
