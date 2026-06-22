package repositories

import (
	"context"

	"milton_prism/core/services/migration/ports"
	"milton_prism/core/shared/grpc_client_sdk"
	applog "milton_prism/pkg/log"
	analysissvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/types/analysis/v1"

	"google.golang.org/grpc/metadata"
)

var _ ports.AnalysisClient = (*AnalysisClientAdapter)(nil)

// AnalysisClientAdapter wraps the analysis gRPC client and exposes the
// driven-port operations needed by the migration service.
type AnalysisClientAdapter struct {
	client *grpc_client_sdk.AnalysisGrpcClient
}

// NewAnalysisClientAdapter wraps an AnalysisGrpcClient behind the driven port.
func NewAnalysisClientAdapter(c *grpc_client_sdk.AnalysisGrpcClient) *AnalysisClientAdapter {
	return &AnalysisClientAdapter{client: c}
}

// RunAnalysis dispatches an asynchronous analysis run to the analysis service.
// sourceBranch is forwarded so the worker clones the chosen branch instead of
// the repository's default_branch. The returned summary is not used here;
// only transport errors are surfaced to the caller.
func (a *AnalysisClientAdapter) RunAnalysis(ctx context.Context, repositoryID, migrationID, ownerUserID uint64, sourceBranch, rootSubdirectory string) error {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	_, err := a.client.RunAnalysis(ctx, &analysissvcv1.RunAnalysisRequest{
		RepositoryId:     repositoryID,
		MigrationId:      migrationID,
		SourceBranch:     sourceBranch,
		OwnerUserId:      ownerUserID,
		RootSubdirectory: rootSubdirectory,
	})
	if err != nil {
		applog.Warningf("migration: RunAnalysis dispatch failed repository_id=%d migration_id=%d: %v",
			repositoryID, migrationID, err)
		return err
	}
	return nil
}

// GetAnalysisSummary fetches an AnalysisSummary by identifier, forwarding the
// caller's auth token so the analysis service can enforce ownership. Returns
// the summary or a gRPC status error (NOT_FOUND when ownership fails or the
// record does not exist).
func (a *AnalysisClientAdapter) GetAnalysisSummary(ctx context.Context, identifier uint64) (*analysisv1.AnalysisSummary, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	s, err := a.client.GetAnalysisSummary(ctx, &analysissvcv1.GetAnalysisSummaryRequest{
		Identifier: identifier,
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}
