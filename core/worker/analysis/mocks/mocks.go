// Package mocks provides testify mock implementations of the analysis worker ports.
package mocks

import (
	"context"

	analysisdomain "milton_prism/core/services/analysis/domain"
	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"

	"github.com/stretchr/testify/mock"
)

// MockSummaryWriter is a testify mock for ports.SummaryWriter.
type MockSummaryWriter struct{ mock.Mock }

func (m *MockSummaryWriter) Write(ctx context.Context, summary *analysisdomain.AnalysisSummary) error {
	args := m.Called(ctx, summary)
	return args.Error(0)
}

func (m *MockSummaryWriter) MarkFailed(ctx context.Context, migrationID uint64, reason string) error {
	args := m.Called(ctx, migrationID, reason)
	return args.Error(0)
}

func (m *MockSummaryWriter) MarkAnalysisFailed(ctx context.Context, summaryID uint64, reason string) error {
	args := m.Called(ctx, summaryID, reason)
	return args.Error(0)
}

func (m *MockSummaryWriter) FindCompletedForBranch(ctx context.Context, repositoryID uint64, branch string) (*analysisdomain.AnalysisSummary, error) {
	args := m.Called(ctx, repositoryID, branch)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*analysisdomain.AnalysisSummary), args.Error(1)
}

func (m *MockSummaryWriter) WriteReuse(ctx context.Context, existingSummaryID, migrationID uint64) error {
	args := m.Called(ctx, existingSummaryID, migrationID)
	return args.Error(0)
}

// MockSourceAcquirer is a testify mock for ports.SourceAcquirer.
type MockSourceAcquirer struct{ mock.Mock }

func (m *MockSourceAcquirer) Acquire(ctx context.Context, source, branch string) (string, string, func(), error) {
	args := m.Called(ctx, source, branch)
	return args.String(0), args.String(1), func() {}, args.Error(2)
}

// MockLanguageDetector is a testify mock for ports.LanguageDetector.
type MockLanguageDetector struct{ mock.Mock }

func (m *MockLanguageDetector) Detect(ctx context.Context, workspacePath string) ([]workerdomain.DetectedLanguage, error) {
	args := m.Called(ctx, workspacePath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]workerdomain.DetectedLanguage), args.Error(1)
}

// MockManifestParser is a testify mock for ports.ManifestParser.
type MockManifestParser struct{ mock.Mock }

func (m *MockManifestParser) Parse(ctx context.Context, workspacePath string, ecosystem workerdomain.Ecosystem) ([]workerdomain.Dependency, error) {
	args := m.Called(ctx, workspacePath, ecosystem)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]workerdomain.Dependency), args.Error(1)
}

// MockVersionResolver is a testify mock for ports.VersionResolver.
type MockVersionResolver struct{ mock.Mock }

func (m *MockVersionResolver) Latest(ctx context.Context, ecosystem workerdomain.Ecosystem, pkg string) (workerdomain.VersionCurrency, error) {
	args := m.Called(ctx, ecosystem, pkg)
	return args.Get(0).(workerdomain.VersionCurrency), args.Error(1)
}

// MockVulnerabilityScanner is a testify mock for ports.VulnerabilityScanner.
type MockVulnerabilityScanner struct{ mock.Mock }

func (m *MockVulnerabilityScanner) Scan(ctx context.Context, deps []workerdomain.Dependency) ([]*analysisdomain.Vulnerability, error) {
	args := m.Called(ctx, deps)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*analysisdomain.Vulnerability), args.Error(1)
}

// MockDependencyGraphBuilder is a testify mock for ports.DependencyGraphBuilder.
type MockDependencyGraphBuilder struct{ mock.Mock }

func (m *MockDependencyGraphBuilder) Build(ctx context.Context, workspacePath string, lang string) ([]*analysisdomain.DependencyEdge, error) {
	args := m.Called(ctx, workspacePath, lang)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*analysisdomain.DependencyEdge), args.Error(1)
}

// MockModuleClassifier is a testify mock for ports.ModuleClassifier.
var _ ports.ModuleClassifier = (*MockModuleClassifier)(nil)

type MockModuleClassifier struct{ mock.Mock }

func (m *MockModuleClassifier) Classify(ctx context.Context, edges []*analysisdomain.DependencyEdge) (*analysisdomain.ModuleClassification, error) {
	args := m.Called(ctx, edges)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*analysisdomain.ModuleClassification), args.Error(1)
}

// MockSemanticClusterer is a testify mock for ports.SemanticClusterer.
type MockSemanticClusterer struct{ mock.Mock }

func (m *MockSemanticClusterer) Cluster(ctx context.Context, graph []*analysisdomain.DependencyEdge, sources []string) ([]workerdomain.BoundedContext, error) {
	args := m.Called(ctx, graph, sources)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]workerdomain.BoundedContext), args.Error(1)
}

// MockStructuralFrameworkDetector is a testify mock for ports.StructuralFrameworkDetector.
var _ ports.StructuralFrameworkDetector = (*MockStructuralFrameworkDetector)(nil)

type MockStructuralFrameworkDetector struct{ mock.Mock }

func (m *MockStructuralFrameworkDetector) Detect(ctx context.Context, workspacePath string, existing []*analysisdomain.Technology) ([]*analysisdomain.Technology, error) {
	args := m.Called(ctx, workspacePath, existing)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*analysisdomain.Technology), args.Error(1)
}

// MockModelClient is a testify mock for ports.ModelClient.
var _ ports.ModelClient = (*MockModelClient)(nil)

type MockModelClient struct{ mock.Mock }

func (m *MockModelClient) Complete(ctx context.Context, req ports.ModelRequest) (ports.ModelResponse, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(ports.ModelResponse), args.Error(1)
}
