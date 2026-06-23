// Package mocks contains testify-based stand-ins for the migration service
// driven ports. They live next to the real implementations so they can be used
// from any test in this module without a circular import.
package mocks

import (
	"context"
	"time"

	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/ports"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
	paginationv1 "milton_prism/pkg/pb/gen/milton_prism/types/pagination/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"github.com/stretchr/testify/mock"
)

// Compile-time interface checks.
var (
	_ ports.MigrationRepository          = (*MockMigrationRepository)(nil)
	_ ports.TransactionManager           = (*MockTransactionManager)(nil)
	_ ports.IdentityClient               = (*MockIdentityClient)(nil)
	_ ports.RepositoryClient             = (*MockRepositoryClient)(nil)
	_ ports.AnalysisClient               = (*MockAnalysisClient)(nil)
	_ ports.ArtifactReader               = (*MockArtifactReader)(nil)
	_ ports.GenerationJobEnqueuer        = (*MockGenerationJobEnqueuer)(nil)
	_ ports.DecomposeJobEnqueuer         = (*MockDecomposeJobEnqueuer)(nil)
	_ ports.GenerationFileArtifactReader = (*MockGenerationFileArtifactReader)(nil)
	_ ports.MigrabilityAssessor          = (*MockMigrabilityAssessor)(nil)
	_ ports.RoadmapEnricher              = (*MockRoadmapEnricher)(nil)
	_ ports.BlueprintGenerator           = (*MockBlueprintGenerator)(nil)
	_ ports.StackDetector                = (*MockStackDetector)(nil)
	_ ports.BillingClient                = (*MockBillingClient)(nil)
	_ ports.GenerationResultReader       = (*MockGenerationResultReader)(nil)
)

// MockGenerationResultReader is a testify mock for ports.GenerationResultReader.
type MockGenerationResultReader struct {
	mock.Mock
}

func (m *MockGenerationResultReader) ReadResults(ctx context.Context, migrationID uint64) ([]*migrationv1.ServiceGenerationRecord, error) {
	args := m.Called(ctx, migrationID)
	v, _ := args.Get(0).([]*migrationv1.ServiceGenerationRecord)
	return v, args.Error(1)
}

func (m *MockGenerationResultReader) ReadUsageTotals(ctx context.Context, migrationID uint64) (ports.GenerationUsageTotals, error) {
	args := m.Called(ctx, migrationID)
	v, _ := args.Get(0).(ports.GenerationUsageTotals)
	return v, args.Error(1)
}

// MockMigrationRepository is a testify mock for ports.MigrationRepository.
type MockMigrationRepository struct {
	mock.Mock
}

func (m *MockMigrationRepository) Create(ctx context.Context, migration *domain.Migration) (*domain.Migration, error) {
	args := m.Called(ctx, migration)
	v, _ := args.Get(0).(*domain.Migration)
	return v, args.Error(1)
}

func (m *MockMigrationRepository) GetByID(ctx context.Context, identifier uint64, includeDeleted bool) (*domain.Migration, error) {
	args := m.Called(ctx, identifier, includeDeleted)
	v, _ := args.Get(0).(*domain.Migration)
	return v, args.Error(1)
}

func (m *MockMigrationRepository) List(ctx context.Context, filter *domain.MigrationsFilter, orderBy string, params *queryparamsv1.PageQueryParams) ([]*domain.Migration, *paginationv1.Pagination, error) {
	args := m.Called(ctx, filter, orderBy, params)
	items, _ := args.Get(0).([]*domain.Migration)
	pag, _ := args.Get(1).(*paginationv1.Pagination)
	return items, pag, args.Error(2)
}

func (m *MockMigrationRepository) UpdateState(ctx context.Context, identifier uint64, state domain.MigrationState) error {
	return m.Called(ctx, identifier, state).Error(0)
}

func (m *MockMigrationRepository) SetRepositoryURL(ctx context.Context, identifier uint64, url string) error {
	return m.Called(ctx, identifier, url).Error(0)
}

func (m *MockMigrationRepository) SetMigrabilityAssessment(ctx context.Context, identifier uint64, assessment *domain.MigrabilityAssessment) error {
	return m.Called(ctx, identifier, assessment).Error(0)
}

func (m *MockMigrationRepository) SetMigrabilityOverride(ctx context.Context, identifier uint64, override bool) error {
	return m.Called(ctx, identifier, override).Error(0)
}

func (m *MockMigrationRepository) SetAutoApprove(ctx context.Context, identifier uint64, autoApprove bool) error {
	return m.Called(ctx, identifier, autoApprove).Error(0)
}

func (m *MockMigrationRepository) SetRestructuringRoadmap(ctx context.Context, identifier uint64, roadmap *domain.RestructuringRoadmap) error {
	return m.Called(ctx, identifier, roadmap).Error(0)
}

func (m *MockMigrationRepository) SetRoadmapEnrichment(ctx context.Context, identifier uint64, enrichment *domain.RoadmapEnrichment) error {
	return m.Called(ctx, identifier, enrichment).Error(0)
}

func (m *MockMigrationRepository) SetServiceBlueprint(ctx context.Context, identifier uint64, blueprint *domain.ServiceBlueprint) error {
	return m.Called(ctx, identifier, blueprint).Error(0)
}

func (m *MockMigrationRepository) AdoptAnalysis(ctx context.Context, migrationID, analysisSummaryID uint64, sourceBranch string) error {
	return m.Called(ctx, migrationID, analysisSummaryID, sourceBranch).Error(0)
}

func (m *MockMigrationRepository) SoftDelete(ctx context.Context, identifier uint64) error {
	return m.Called(ctx, identifier).Error(0)
}

func (m *MockMigrationRepository) CountByOwnerSince(ctx context.Context, ownerID uint64, since time.Time) (int64, error) {
	args := m.Called(ctx, ownerID, since)
	return args.Get(0).(int64), args.Error(1)
}

// MockBillingClient is a testify mock for ports.BillingClient.
type MockBillingClient struct {
	mock.Mock
}

func (m *MockBillingClient) GetUserPlan(ctx context.Context, userID uint64) (*billingv1.Plan, error) {
	args := m.Called(ctx, userID)
	v, _ := args.Get(0).(*billingv1.Plan)
	return v, args.Error(1)
}

func (m *MockBillingClient) RecordUsage(ctx context.Context, spend ports.UsageSpend) error {
	return m.Called(ctx, spend).Error(0)
}

func (m *MockBillingClient) CountUsageRecords(ctx context.Context, migrationID uint64, op billingv1.UsageOperation) (int, error) {
	args := m.Called(ctx, migrationID, op)
	return args.Int(0), args.Error(1)
}

// MockTransactionManager is a pass-through implementation of ports.TransactionManager.
type MockTransactionManager struct {
	mock.Mock
}

func (m *MockTransactionManager) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	args := m.Called(ctx, fn)
	if args.Get(0) == nil {
		return fn(ctx)
	}
	return args.Error(0)
}

// MockIdentityClient is a testify mock for ports.IdentityClient.
type MockIdentityClient struct {
	mock.Mock
}

func (m *MockIdentityClient) ValidateUserExists(ctx context.Context, userID uint64) error {
	return m.Called(ctx, userID).Error(0)
}

// MockRepositoryClient is a testify mock for ports.RepositoryClient.
type MockRepositoryClient struct {
	mock.Mock
}

func (m *MockRepositoryClient) FetchRepositoryURL(ctx context.Context, repositoryID uint64) (string, error) {
	args := m.Called(ctx, repositoryID)
	return args.String(0), args.Error(1)
}

func (m *MockRepositoryClient) PushFiles(ctx context.Context, targetURL, writeToken string, files []ports.PushFile, commitMessage string) (string, error) {
	args := m.Called(ctx, targetURL, writeToken, files, commitMessage)
	return args.String(0), args.Error(1)
}

func (m *MockRepositoryClient) ProbeConnection(ctx context.Context, repositoryID uint64) error {
	return m.Called(ctx, repositoryID).Error(0)
}

// MockAnalysisClient is a testify mock for ports.AnalysisClient.
type MockAnalysisClient struct {
	mock.Mock
}

func (m *MockAnalysisClient) RunAnalysis(ctx context.Context, repositoryID, migrationID, ownerUserID uint64, sourceBranch, rootSubdirectory string) error {
	return m.Called(ctx, repositoryID, migrationID, ownerUserID, sourceBranch, rootSubdirectory).Error(0)
}

func (m *MockAnalysisClient) GetAnalysisSummary(ctx context.Context, identifier uint64) (*analysisv1.AnalysisSummary, error) {
	args := m.Called(ctx, identifier)
	v, _ := args.Get(0).(*analysisv1.AnalysisSummary)
	return v, args.Error(1)
}

// MockArtifactReader is a testify mock for ports.ArtifactReader.
type MockArtifactReader struct {
	mock.Mock
}

func (m *MockArtifactReader) ReadArtifacts(ctx context.Context, migrationID uint64) ([]domain.ServiceArtifact, error) {
	args := m.Called(ctx, migrationID)
	v, _ := args.Get(0).([]domain.ServiceArtifact)
	return v, args.Error(1)
}

// MockGenerationJobEnqueuer is a testify mock for ports.GenerationJobEnqueuer.
type MockGenerationJobEnqueuer struct {
	mock.Mock
}

func (m *MockGenerationJobEnqueuer) EnqueueGeneration(ctx context.Context, migrationID uint64, serviceFilter []string) error {
	return m.Called(ctx, migrationID, serviceFilter).Error(0)
}

// MockDecomposeJobEnqueuer is a testify mock for ports.DecomposeJobEnqueuer.
type MockDecomposeJobEnqueuer struct {
	mock.Mock
}

func (m *MockDecomposeJobEnqueuer) EnqueueDecompose(ctx context.Context, migrationID, summaryID uint64, remoteURL, defaultBranch string) error {
	return m.Called(ctx, migrationID, summaryID, remoteURL, defaultBranch).Error(0)
}

// MockGenerationFileArtifactReader is a testify mock for ports.GenerationFileArtifactReader.
type MockGenerationFileArtifactReader struct {
	mock.Mock
}

func (m *MockGenerationFileArtifactReader) ListArtifacts(ctx context.Context, migrationID uint64, serviceName string) ([]ports.GeneratedFile, error) {
	args := m.Called(ctx, migrationID, serviceName)
	v, _ := args.Get(0).([]ports.GeneratedFile)
	return v, args.Error(1)
}

// MockMigrabilityAssessor is a testify mock for ports.MigrabilityAssessor.
type MockMigrabilityAssessor struct {
	mock.Mock
}

func (m *MockMigrabilityAssessor) Assess(ctx context.Context, userID, migrationID, analysisSummaryID uint64, language string) (*domain.MigrabilityAssessment, error) {
	args := m.Called(ctx, userID, migrationID, analysisSummaryID, language)
	v, _ := args.Get(0).(*domain.MigrabilityAssessment)
	return v, args.Error(1)
}

// MockRoadmapEnricher is a testify mock for ports.RoadmapEnricher.
type MockRoadmapEnricher struct {
	mock.Mock
}

func (m *MockRoadmapEnricher) Enrich(ctx context.Context, userID, migrationID uint64, roadmap *domain.RestructuringRoadmap) (*domain.RoadmapEnrichment, error) {
	args := m.Called(ctx, userID, migrationID, roadmap)
	v, _ := args.Get(0).(*domain.RoadmapEnrichment)
	return v, args.Error(1)
}

// MockBlueprintGenerator is a testify mock for ports.BlueprintGenerator.
type MockBlueprintGenerator struct {
	mock.Mock
}

func (m *MockBlueprintGenerator) Generate(ctx context.Context, userID, migrationID, analysisSummaryID uint64, roadmap *domain.RestructuringRoadmap) (*domain.ServiceBlueprint, error) {
	args := m.Called(ctx, userID, migrationID, analysisSummaryID, roadmap)
	v, _ := args.Get(0).(*domain.ServiceBlueprint)
	return v, args.Error(1)
}

// MockStackDetector is a testify mock for ports.StackDetector.
type MockStackDetector struct {
	mock.Mock
}

func (m *MockStackDetector) Detect(ctx context.Context, analysisSummaryID uint64) (string, []string, error) {
	args := m.Called(ctx, analysisSummaryID)
	techs, _ := args.Get(1).([]string)
	return args.String(0), techs, args.Error(2)
}
