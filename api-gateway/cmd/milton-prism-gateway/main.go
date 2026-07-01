package main

import (
	"fmt"
	"net/http"
	"strconv"

	"milton_prism/core/shared/auth_token"
	"milton_prism/core/shared/cache_client"
	"milton_prism/pkg/config"
	"milton_prism/pkg/gateway"
	"milton_prism/pkg/gateway/sse"
	"milton_prism/pkg/log"
	analysisv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/services/billing/v1"
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
	// billing is co-located on the analysis service's gRPC server; its REST
	// surface maps to the same backend endpoint (see config.toml).
	"billing": billingv1.RegisterBillingServiceHandlerFromEndpoint,
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

	// SSE real-time endpoint (feature-flagged by config-presence): mount
	// /v1/events only when BOTH [auth] and [cache] are configured. Absent ⇒
	// sseHandler stays nil and the gateway boots exactly as before.
	var sseHandler http.Handler
	if cfg.SSEEnabled() {
		// Schema-aware validator (JWT or PASETO per [auth.tokenValidator].schemaType).
		// The live identity service issues EdDSA JWTs; verification is signature-only
		// against the configured public key (blacklist/issuer/audience off).
		validator, err := auth_token.NewTokenValidator(cfg.Auth.TokenValidatorConfig, nil)
		if err != nil {
			log.Fatalf("Failed to init SSE token validator: %v", err)
		}
		pool, err := cache_client.NewPool(cfg.Cache)
		if err != nil {
			log.Fatalf("Failed to init SSE cache pool: %v", err)
		}
		// Pass the same [cors] config the middleware uses: the SSE route bypasses
		// the CORS middleware, so the handler must emit Access-Control-* itself.
		sseHandler = sse.NewHandler(pool, validator, cfg.Cors)
		log.Info("SSE real-time notifications ENABLED (GET /v1/events)")
	} else {
		log.Info("SSE real-time notifications DISABLED (no [auth]+[cache] in gateway config)")
	}

	restApi := apiBuilder.Build(cfg.Server.ApiKey, cfg.Cors, sseHandler)

	log.Infof("Listening on %s:%d", cfg.Server.Host, *cfg.Server.Port)

	if err := restApi.Start(
		strconv.Itoa(int(*cfg.Server.Port)),
		strconv.Itoa(cfg.Metrics.Port),
	); err != nil {
		log.Fatalf("Failed to start REST API: %v", err)
	}
}
