// Package analysis wires the hexagonal analysis service onto a gRPC server.
package analysis

import (
	"context"

	services "milton_prism/core/internal/svc"
	analysisapp "milton_prism/core/services/analysis/application"
	analysisgrpc "milton_prism/core/services/analysis/infrastructure/grpc_handlers"
	analysisrepo "milton_prism/core/services/analysis/infrastructure/repositories"
	"milton_prism/core/services/analysis/ports"
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

	// Wire migrability assessor when ANTHROPIC_API_KEY is present.
	// Opt-in LLM call (~$0.003/call); gracefully absent when key is missing.
	if assessor, aErr := analysisrepo.NewAnalysisMigrabilityAssessorAdapter(db, repo); aErr == nil {
		app.WithMigrabilityAssessor(assessor)
		applog.Infof("analysis: migrability assessor wired (ANTHROPIC_API_KEY present)")
	} else {
		applog.Infof("analysis: migrability assessor disabled — %v", aErr)
	}

	handler := analysisgrpc.NewAnalysisHandler(app, svc.ExtractUserIDAndRoleFromContext)
	anlsvcv1.RegisterAnalysisServiceServer(server, handler)
	return nil
}
