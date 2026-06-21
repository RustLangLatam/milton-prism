// Package mocks provides testify mock implementations of the decomposition worker ports.
package mocks

import (
	"context"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
	analysisports "milton_prism/core/worker/analysis/ports"

	"github.com/stretchr/testify/mock"
)

var _ ports.PlanWriter = (*MockPlanWriter)(nil)
var _ ports.GraphLoader = (*MockGraphLoader)(nil)

// MockPlanWriter is a testify mock for ports.PlanWriter.
type MockPlanWriter struct{ mock.Mock }

func (m *MockPlanWriter) WritePlan(ctx context.Context, migrationID uint64, plan *workerdomain.RestructurePlan, workspacePath string, ownership workerdomain.DataOwnership) error {
	args := m.Called(ctx, migrationID, plan, workspacePath, ownership)
	return args.Error(0)
}

func (m *MockPlanWriter) MarkFailed(ctx context.Context, migrationID uint64, reason string) error {
	args := m.Called(ctx, migrationID, reason)
	return args.Error(0)
}

// MockGraphLoader is a testify mock for ports.GraphLoader.
type MockGraphLoader struct{ mock.Mock }

func (m *MockGraphLoader) Load(ctx context.Context, summaryID uint64) (*workerdomain.Graph, error) {
	args := m.Called(ctx, summaryID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*workerdomain.Graph), args.Error(1)
}

// MockInfraDetector is a testify mock for ports.InfraDetector.
type MockInfraDetector struct{ mock.Mock }

func (m *MockInfraDetector) Detect(ctx context.Context, graph *workerdomain.Graph) (*workerdomain.Classification, error) {
	args := m.Called(ctx, graph)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*workerdomain.Classification), args.Error(1)
}

// MockSemanticClusterer is a testify mock for ports.SemanticClusterer.
var _ ports.SemanticClusterer = (*MockSemanticClusterer)(nil)

type MockSemanticClusterer struct{ mock.Mock }

func (m *MockSemanticClusterer) Cluster(ctx context.Context, input ports.ClusterInput) (*workerdomain.ClusteringResult, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*workerdomain.ClusteringResult), args.Error(1)
}

// MockModelClient is a testify mock for analysisports.ModelClient, used by the
// decomposition worker's assessor in tests.
var _ analysisports.ModelClient = (*MockModelClient)(nil)

type MockModelClient struct{ mock.Mock }

func (m *MockModelClient) Complete(ctx context.Context, req analysisports.ModelRequest) (analysisports.ModelResponse, error) {
	args := m.Called(ctx, req)
	return args.Get(0).(analysisports.ModelResponse), args.Error(1)
}
