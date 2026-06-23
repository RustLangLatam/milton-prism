package repositories

import (
	"context"

	"milton_prism/core/services/analysis/ports"
	"milton_prism/core/shared/grpc_client_sdk"
	migrationsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/migration/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
	queryparamsv1 "milton_prism/pkg/pb/gen/milton_prism/types/query_params/v1"

	"google.golang.org/grpc/metadata"
)

var _ ports.MigrationClient = (*MigrationClientAdapter)(nil)

// activeMigrationStates is the set of non-terminal migration states. A migration
// in any of these still owns its analysis, so the analysis cannot be deleted.
// Terminal states (PUSHED, FAILED, CANCELLED, RESTRUCTURING_READY) are excluded.
var activeMigrationStates = []migrationv1.MigrationState{
	migrationv1.MigrationState_MIGRATION_STATE_PENDING,
	migrationv1.MigrationState_MIGRATION_STATE_ANALYZING,
	migrationv1.MigrationState_MIGRATION_STATE_DESIGNING,
	migrationv1.MigrationState_MIGRATION_STATE_AWAITING_APPROVAL,
	migrationv1.MigrationState_MIGRATION_STATE_GENERATING,
	migrationv1.MigrationState_MIGRATION_STATE_TESTING,
	migrationv1.MigrationState_MIGRATION_STATE_READY,
}

// MigrationClientAdapter implements the analysis service's MigrationClient port
// by calling the migration service over gRPC. It forwards the caller's bearer
// token so the migration service scopes its results to the caller's migrations.
type MigrationClientAdapter struct {
	client *grpc_client_sdk.MigrationGrpcClient
}

// NewMigrationClientAdapter wraps a MigrationGrpcClient behind the driven port.
func NewMigrationClientAdapter(c *grpc_client_sdk.MigrationGrpcClient) *MigrationClientAdapter {
	return &MigrationClientAdapter{client: c}
}

// CountLiveMigrationsByAnalysis returns how many active (non-terminal) migrations
// reference analysisSummaryID. It asks the migration service for a single-row page
// filtered by analysis_summary_id and the active state set, then reads
// pagination.total — the count of the full matching set, not the page size.
func (a *MigrationClientAdapter) CountLiveMigrationsByAnalysis(ctx context.Context, analysisSummaryID uint64) (int64, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	resp, err := a.client.ListMigrations(ctx, &migrationsvcv1.ListMigrationsRequest{
		Filter: &migrationv1.MigrationsFilter{
			AnalysisSummaryId: &analysisSummaryID,
			States:            activeMigrationStates,
		},
		PageParams: &queryparamsv1.PageQueryParams{PageNumber: 1, PageSize: 1},
	})
	if err != nil {
		return 0, err
	}
	return int64(resp.GetPagination().GetTotalSize()), nil
}
