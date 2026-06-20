package main

import (
	"context"

	services "milton_prism/core/internal/svc"
	"milton_prism/core/services/migration"
	"milton_prism/core/shared/grpc_health"
	"milton_prism/pkg/config"
	"milton_prism/pkg/log"
	migsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/migration/v1"
)

func main() {
	log.InitLogger("microservice")

	cfg, err := config.LoadMicroserviceCfg(config.TokenRoleGenerator, nil)
	if err != nil {
		log.Fatalf("Failed load cfg: %v", err)
	}

	if err := cfg.ValidateWithFlags(config.RequiredFields{
		RequireAuth:    true,
		RequireMongoDb: true,
	}); err != nil {
		log.Fatalf("Failed validate cfg: %v", err)
	}

	newServices, err := services.NewServicesFromConfig(cfg)
	if err != nil {
		log.Fatalf("Failed initialize services: %v", err)
	}

	grpcSrv, metricsSrv, err := newServices.NewGRPCServer(cfg.Server.ServerOptionCgf)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	grpc_health.SetupHealthCheck(grpcSrv, nil)

	ctx := context.Background()
	if err = migration.BuildMigrationServer(ctx, newServices, grpcSrv); err != nil {
		log.Fatalf("Failed to create migration server: %v", err)
	}

	serverGroup := services.NewServerGroup(
		cfg,
		grpcSrv,
		metricsSrv,
		migsvcv1.RegisterMigrationServiceHandlerFromEndpoint,
		"/health:migration",
	)

	if err := serverGroup.Run(); err != nil {
		log.Fatalf("Server terminated: %v", err)
	}
}
