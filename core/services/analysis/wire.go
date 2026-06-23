// Package analysis wires the hexagonal analysis service onto a gRPC server.
package analysis

import (
	"context"

	services "milton_prism/core/internal/svc"
	analysisapp "milton_prism/core/services/analysis/application"
	analysisgrpc "milton_prism/core/services/analysis/infrastructure/grpc_handlers"
	analysisrepo "milton_prism/core/services/analysis/infrastructure/repositories"
	"milton_prism/core/services/analysis/ports"
	"milton_prism/core/services/billing"
	"milton_prism/core/shared/grpc_client_sdk"
	applog "milton_prism/pkg/log"
	anlsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"

	"google.golang.org/grpc"
)

// BuildAnalysisServer wires the hexagonal analysis application service and
// registers it on server.
func BuildAnalysisServer(ctx context.Context, svc *services.Services, server *grpc.Server) error {
	db := svc.Mongo().GetDatabase()
	cfg := svc.Config()

	repo := analysisrepo.NewMongoAnalysisSummaryRepository(db)

	var enqueuer ports.JobEnqueuer
	if cfg.Cache != nil {
		enqueuer = analysisrepo.NewAsynqJobEnqueuer(cfg.Cache)
	} else {
		enqueuer = analysisrepo.NewNoOpJobEnqueuer()
	}

	var repositoryClient ports.RepositoryClient
	if cfg.GrpcServices != nil && cfg.GrpcServices.RepositoryClientConfig != nil && cfg.GrpcServices.RepositoryClientConfig.Enabled {
		grpcRepo, err := grpc_client_sdk.NewRepositoryGRPCClient(ctx, cfg.GrpcServices.RepositoryClientConfig)
		if err != nil {
			return err
		}
		repositoryClient = analysisrepo.NewRepositoryClientAdapter(grpcRepo)
	}

	app := analysisapp.NewService(repo, repositoryClient, enqueuer)

	// Cross-service migration client: powers the live-migration guard in
	// DeleteAnalysisSummary (refuse to delete an analysis still referenced by an
	// active migration). When the migration endpoint is not configured the guard
	// degrades CLOSED (delete is refused) rather than risk orphaning a migration.
	if cfg.GrpcServices != nil && cfg.GrpcServices.MigrationClientConfig != nil && cfg.GrpcServices.MigrationClientConfig.Enabled {
		grpcMig, err := grpc_client_sdk.NewMigrationGRPCClient(ctx, cfg.GrpcServices.MigrationClientConfig)
		if err != nil {
			return err
		}
		app.WithMigrationClient(analysisrepo.NewMigrationClientAdapter(grpcMig))
		applog.Infof("analysis: migration client wired (delete live-migration guard enabled)")
	} else {
		applog.Warningf("analysis: migration client NOT configured — DeleteAnalysisSummary will refuse (degrade closed)")
	}

	// Co-locate the billing capability on this gRPC server and share its usage
	// repository (same milton_prism_analysis database) so the assessment spend is
	// recorded in-process. BillingService also exposes RecordUsage for the
	// migration / generation workers to report their spend best-effort.
	usageRepo, billingSvc, bErr := billing.BuildBillingServer(svc, server)
	if bErr != nil {
		return bErr
	}
	usageRecorder := analysisrepo.NewBillingUsageRecorderAdapter(usageRepo)
	// Enforce per-month analysis plan quotas in-process against the co-located
	// billing service (hard block; Unlimited plans never blocked).
	app.WithPlanProvider(analysisrepo.NewBillingPlanProviderAdapter(billingSvc))
	applog.Infof("analysis: billing service co-located (usage accounting + plan quota enforcement enabled)")

	// Wire migrability assessor when ANTHROPIC_API_KEY is present.
	// Opt-in LLM call (~$0.003/call); gracefully absent when key is missing.
	if assessor, aErr := analysisrepo.NewAnalysisMigrabilityAssessorAdapter(db, repo); aErr == nil {
		assessor.WithUsageRecorder(usageRecorder)
		app.WithMigrabilityAssessor(assessor)
		applog.Infof("analysis: migrability assessor wired (ANTHROPIC_API_KEY present)")
	} else {
		applog.Infof("analysis: migrability assessor disabled — %v", aErr)
	}

	handler := analysisgrpc.NewAnalysisHandler(app, svc.ExtractUserIDAndRoleFromContext)
	anlsvcv1.RegisterAnalysisServiceServer(server, handler)
	return nil
}
