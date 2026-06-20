package main

import (
	"fmt"
	"strconv"

	"milton_prism/pkg/config"
	"milton_prism/pkg/gateway"
	"milton_prism/pkg/log"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"
	identityv1 "milton_prism/pkg/pb/gen/milton_prism/services/identity/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/services/migration/v1"
	repositoryv1 "milton_prism/pkg/pb/gen/milton_prism/services/repository/v1"
)

// serviceHandlers maps the service name (from config.toml) to its generated
// gRPC-Gateway registration function.
var serviceHandlers = map[string]gateway.RegisterServiceFunc{
	"analysis":   analysisv1.RegisterAnalysisServiceHandlerFromEndpoint,
	"identity":   identityv1.RegisterIdentityServiceHandlerFromEndpoint,
	"repository": repositoryv1.RegisterRepositoryServiceHandlerFromEndpoint,
	"migration":  migrationv1.RegisterMigrationServiceHandlerFromEndpoint,
}

func main() {
	log.InitLogger("gateway")

	cfg, err := config.LoadGatewayCfg()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	apiBuilder := gateway.NewRestApiBuilder()

	for _, svc := range cfg.GrpcServices {
		if !svc.Enabled {
			log.Infof("Skipping disabled service: %s", svc.Name)
			continue
		}
		registerFn, ok := serviceHandlers[svc.Name]
		if !ok {
			log.Fatalf("Unknown gRPC service in config: %s", svc.Name)
		}
		healthPath := fmt.Sprintf("/health/%s", svc.Name)
		if err := apiBuilder.RegisterService(svc, registerFn, healthPath); err != nil {
			log.Fatalf("Failed to register service %s: %v", svc.Name, err)
		}
	}

	restApi := apiBuilder.Build(cfg.Server.ApiKey, cfg.Cors)

	log.Infof("Listening on %s:%d", cfg.Server.Host, *cfg.Server.Port)

	if err := restApi.Start(
		strconv.Itoa(int(*cfg.Server.Port)),
		strconv.Itoa(cfg.Metrics.Port),
	); err != nil {
		log.Fatalf("Failed to start REST API: %v", err)
	}
}
