// Package migration wires the hexagonal migration service onto a gRPC server.
package migration

import (
	"context"
	"os"

	services "milton_prism/core/internal/svc"
	migrationapp "milton_prism/core/services/migration/application"
	migrationgrpc "milton_prism/core/services/migration/infrastructure/grpc_handlers"
	migrationrepo "milton_prism/core/services/migration/infrastructure/repositories"
	"milton_prism/core/services/migration/ports"
	"milton_prism/core/shared/grpc_client_sdk"
	applog "milton_prism/pkg/log"
	migsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/migration/v1"

	"google.golang.org/grpc"
)

// BuildMigrationServer wires the hexagonal migration application service and
// registers it on server.
func BuildMigrationServer(ctx context.Context, svc *services.Services, server *grpc.Server) error {
	db := svc.Mongo().GetDatabase()
	mongoClient := svc.Mongo().GetClient()
	cfg := svc.Config()

	repo := migrationrepo.NewMongoMigrationRepository(db)
	tx := migrationrepo.NewMongoTransactionManager(mongoClient)
	artifactReader := migrationrepo.NewMongoArtifactReader(db)
	generationResultReader := migrationrepo.NewMongoGenerationResultReader(db)
	fileArtifactReader := migrationrepo.NewMongoGenerationFileArtifactReader(db)

	var analysisClient ports.AnalysisClient
	var billingClient ports.BillingClient
	if cfg.GrpcServices != nil && cfg.GrpcServices.AnalysisClientConfig != nil && cfg.GrpcServices.AnalysisClientConfig.Enabled {
		grpcAnalysis, err := grpc_client_sdk.NewAnalysisGRPCClient(ctx, cfg.GrpcServices.AnalysisClientConfig)
		if err != nil {
			return err
		}
		analysisClient = migrationrepo.NewAnalysisClientAdapter(grpcAnalysis)

		// BillingService is co-served on the analysis-services gRPC endpoint, so
		// build the billing client over the SAME connection — no new config or dial.
		grpcBilling, err := grpc_client_sdk.NewBillingGRPCClientOnConn(grpcAnalysis.Conn())
		if err != nil {
			return err
		}
		billingClient = migrationrepo.NewBillingClientAdapter(grpcBilling)
	}

	var identityClient ports.IdentityClient
	if cfg.GrpcServices != nil && cfg.GrpcServices.IdentityClientConfig != nil && cfg.GrpcServices.IdentityClientConfig.Enabled {
		grpcIdentity, err := grpc_client_sdk.NewIdentityGRPCClient(ctx, cfg.GrpcServices.IdentityClientConfig)
		if err != nil {
			return err
		}
		identityClient = migrationrepo.NewIdentityClientAdapter(grpcIdentity)
	}

	var repositoryClient ports.RepositoryClient
	if cfg.GrpcServices != nil && cfg.GrpcServices.RepositoryClientConfig != nil && cfg.GrpcServices.RepositoryClientConfig.Enabled {
		grpcRepo, err := grpc_client_sdk.NewRepositoryGRPCClient(ctx, cfg.GrpcServices.RepositoryClientConfig)
		if err != nil {
			return err
		}
		repositoryClient = migrationrepo.NewRepositoryClientAdapter(grpcRepo)
	}

	var generationEnqueuer ports.GenerationJobEnqueuer
	if cfg.Cache != nil {
		generationEnqueuer = migrationrepo.NewAsynqGenerationEnqueuer(cfg.Cache)
	} else {
		generationEnqueuer = migrationrepo.NewNoOpGenerationEnqueuer()
	}

	var decomposeEnqueuer ports.DecomposeJobEnqueuer
	if cfg.Cache != nil {
		decomposeEnqueuer = migrationrepo.NewAsynqDecomposeEnqueuer(cfg.Cache)
	} else {
		decomposeEnqueuer = migrationrepo.NewNoOpDecomposeEnqueuer()
	}

	var migrabilityAssessor ports.MigrabilityAssessor
	analysisDB := mongoClient.Database("milton_prism_analysis")
	if adapter, err := migrationrepo.NewMigrabilityAssessorAdapter(analysisDB); err != nil {
		applog.Warningf("migration: migrability assessor disabled (ANTHROPIC_API_KEY not set): error=%v", err)
	} else {
		migrabilityAssessor = adapter
	}

	var roadmapEnricher ports.RoadmapEnricher
	if adapter, err := migrationrepo.NewRoadmapEnricherAdapter(); err != nil {
		applog.Warningf("migration: roadmap enricher disabled (ANTHROPIC_API_KEY not set): error=%v", err)
	} else {
		roadmapEnricher = adapter
	}

	var blueprintGenerator ports.BlueprintGenerator
	if adapter, err := migrationrepo.NewBlueprintGeneratorAdapter(analysisDB); err != nil {
		applog.Warningf("migration: blueprint generator disabled (ANTHROPIC_API_KEY not set): error=%v", err)
	} else {
		blueprintGenerator = adapter
	}

	stackDetector := ports.StackDetector(migrationrepo.NewStackDetectorAdapter(analysisDB))

	app := migrationapp.NewService(repo, tx, identityClient, repositoryClient, analysisClient, artifactReader, generationEnqueuer, decomposeEnqueuer, generationResultReader, fileArtifactReader, migrabilityAssessor, roadmapEnricher, blueprintGenerator, stackDetector, os.Getenv("PRISM_MONOREPO_PATH"))

	// Enforce per-month migration plan quotas against the co-served billing
	// service (hard block; Unlimited plans never blocked). No-op when no analysis/
	// billing endpoint is configured.
	if billingClient != nil {
		app.WithBillingClient(billingClient)
		applog.Infof("migration: plan quota enforcement enabled (billing client over analysis conn)")
	}

	// Backfill repository_url for records created before the snapshot feature.
	// Runs in the background; the service is ready to handle requests immediately.
	go app.BackfillRepositoryURLs(context.Background())

	handler := migrationgrpc.NewMigrationHandler(app, svc.ExtractUserIDAndRoleFromContext)
	migsvcv1.RegisterMigrationServiceServer(server, handler)
	return nil
}
