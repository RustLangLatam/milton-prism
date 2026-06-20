package main

import (
	services "milton_prism/core/internal/svc"
	"milton_prism/core/services/identity"
	"milton_prism/core/shared/grpc_health"
	"milton_prism/pkg/config"
	"milton_prism/pkg/log"
	identitysvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/identity/v1"
)

func main() {
	log.InitLogger("microservice")

	cfg, err := config.LoadMicroserviceCfg(config.TokenRoleGenerator, nil)
	if err != nil {
		log.Fatalf("Failed load cfg: %v", err)
	}

	if err := cfg.ValidateWithFlags(config.RequiredFields{
		RequireAuth:    true,
		RequireCache:   true,
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

	if err = identity.BuildIdentityServer(newServices, grpcSrv); err != nil {
		log.Fatalf("Failed to create identity server: %v", err)
	}

	serverGroup := services.NewServerGroup(
		cfg,
		grpcSrv,
		metricsSrv,
		identitysvcv1.RegisterIdentityServiceHandlerFromEndpoint,
		"/health:identity",
	)

	if err := serverGroup.Run(); err != nil {
		log.Fatalf("Server terminated: %v", err)
	}
}
