package gateway

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"milton_prism/pkg/config"
	"milton_prism/pkg/log"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
)

// StartGatewayWithService initializes and starts a gRPC-Gateway HTTP server that proxies
// requests to the corresponding gRPC service. It handles:
// - Configuration validation
// - Service registration
// - Middleware setup
// - Server startup
//
// Parameters:
//   - httpCfg: HTTP server configuration (host, port, CORS, etc.)
//   - grpcClientCfg: gRPC client configuration for the target service
//   - registerHandlerFunc: The generated gRPC-Gateway registration function
//   - healthEndpoint: Health check endpoint path (format: "/health:service:component")
//
// Returns:
//   - error: If any initialization or startup step fails
func StartGatewayWithService(
	httpCfg *config.HttpListenCfg,
	grpcClientCfg *config.GrpcClientCfg,
	registerHandlerFunc func(context.Context, *runtime.ServeMux, string, []grpc.DialOption) error,
	healthEndpoint string,
) error {
	// Validate HTTP configuration
	if err := httpCfg.Validate(); err != nil {
		return fmt.Errorf("invalid HTTP configuration: %w", err)
	}

	// Initialize API builder with default middleware chain
	apiBuilder := NewRestApiBuilder()

	// Register gRPC service handler if enabled
	if grpcClientCfg != nil && grpcClientCfg.Enabled {
		if err := grpcClientCfg.Validate(); err != nil {
			return fmt.Errorf("invalid gRPC client configuration: %w", err)
		}

		if err := apiBuilder.RegisterService(grpcClientCfg, registerHandlerFunc, healthEndpoint); err != nil {
			return fmt.Errorf("service registration failed for %s: %w", grpcClientCfg.Name, err)
		}
	} else {
		return errors.New("gRPC client configuration disabled or missing")
	}

	// Build REST API with configured middleware and routes. Per-service inline
	// gateways do not expose the SSE endpoint (nil handler ⇒ unchanged chain).
	restApi := apiBuilder.Build(httpCfg.ApiKey, httpCfg.Cors, nil)

	// Log startup information
	log.Infof("Starting HTTP [%s] on %s", grpcClientCfg.Name, httpCfg.FullURL())

	// Start HTTP and metrics servers
	if err := restApi.Start(
		strconv.Itoa(int(*httpCfg.Port)),
		strconv.Itoa(httpCfg.Metrics.Port),
	); err != nil {
		return err
	}

	return nil
}
