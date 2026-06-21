package gateway

import (
	"context"
	"fmt"
	"milton_prism/pkg/config"
	"milton_prism/pkg/gateway/handlers"
	"milton_prism/pkg/gateway/metrics_collector"
	"milton_prism/pkg/utils"
	"net/http"

	"milton_prism/pkg/log"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// RegisterServiceFunc is a function type used to register a gRPC service handler with the REST API Gateway.
// It registers a service to a given endpoint on a `runtime.ServeMux` with the specified options for dialing.
type RegisterServiceFunc func(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error

// RestApiBuilder is an interface defining methods for registering services and building the REST API.
// It provides functions for service registration and middleware application.
type RestApiBuilder interface {
	RegisterService(msConfig *config.GrpcClientCfg, registerFn RegisterServiceFunc, healthPath string) error
	Build(apiKey *string, corsCfg *config.CORSCfg) RestApi
}

// RestApi is an interface that defines the Start method to launch the REST API server on a specified port.
type RestApi interface {
	Start(port, metricsPort string) error
}

// restApiBuilder is a concrete implementation of RestApiBuilder, containing the mux to handle REST API requests.
type restApiBuilder struct {
	mux *runtime.ServeMux
}

// restApi represents the final API server, wrapped with middleware handlers.
type restApi struct {
	handler http.Handler
}

// RegisterService registers a gRPC service with the API Gateway using the provided configuration and registration function.
// It performs a health check if enabled, and registers a health check endpoint.
//   - msConfig: Configuration details for the microservice
//   - registerFn: Function to register the service handler
//   - healthPath: Path to the health check endpoint if enabled
//
// Uses the gRPC-Gateway `ServeMux` to map gRPC methods to REST endpoints. For more on `ServeMux`, see:
//   - https://pkg.go.dev/github.com/grpc-ecosystem/grpc-gateway/v2/runtime#ServeMux
func (r *restApiBuilder) RegisterService(msConfig *config.GrpcClientCfg, registerFn RegisterServiceFunc, healthPath string) error {
	// Build gRPC connection options
	ctx := context.Background()
	opts := utils.BuildDialOption(msConfig.ServerOptionCgf, msConfig.IsHealthCheckEnabled())

	// Register gRPC service as REST endpoint
	if err := registerFn(ctx, r.mux, msConfig.Endpoint(), opts); err != nil {
		return fmt.Errorf("gRPC registration failed: %w", err)
	}

	// Perform health check if enabled
	if msConfig.IsHealthCheckEnabled() {
		client, err := utils.CheckGrpcConnection(ctx, msConfig.Endpoint(), opts)
		if err != nil {
			return fmt.Errorf("health check failed: %w", err)
		}

		// Register health check endpoint
		if err := r.registerHealthEndpoint(client, healthPath); err != nil {
			return fmt.Errorf("register health endpoint failed: %w", err)
		}
	}

	log.Infof("MaxRecvMsgSizeMB: %d", msConfig.MaxRecvMsgSizeMB/(1024*1024))
	log.Infof("MaxSendMsgSizeMB: %d", msConfig.MaxSendMsgSizeMB/(1024*1024))
	log.Infof("%s microservices gRPC registered.", msConfig.Name)

	return nil
}

// registerHealthEndpoint adds a health check endpoint to the ServeMux for a registered gRPC service.
// This endpoint is useful for monitoring and service health reporting in production environments.
//
// Health check functionality is part of the gRPC Health Checking UseSsl:
//   - https://github.com/grpc/grpc/blob/master/doc/health-checking.md
func (r *restApiBuilder) registerHealthEndpoint(client grpc_health_v1.HealthClient, healthPath string) error {
	// Register health check endpoint on the mux
	mux := runtime.NewServeMux(runtime.WithHealthEndpointAt(client, healthPath))
	return r.mux.HandlePath(http.MethodGet, healthPath, func(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
		mux.ServeHTTP(w, r)
	})
}

// Build assembles the REST API server by applying a sequence of middlewares for enhanced security, logging, and monitoring.
//   - apiKey: API key for authenticating requests
//
// Middleware sequence:
//   - CORS Middleware: Allows cross-origin requests based on specified policies
//   - Security Middleware: Adds security headers
//   - API Key Middleware: Checks requests for a valid API key
//   - LoggingCfg Middleware: Logs incoming requests and responses
//   - MetricsCfg Middleware: Collects and exports metrics for monitoring
//
// For middleware configuration, see gRPC-Gateway documentation:
//   - https://github.com/grpc-ecosystem/grpc-gateway
func (r *restApiBuilder) Build(apiKey *string, corsCfg *config.CORSCfg) RestApi {
	httpMetricsCollector := metrics_collector.NewHttpApiMetrics()

	// Apply middlewares in order
	handler := handlers.HandlerEnableCors(
		handlers.HandlerGenerateContextIdMiddleware(
			handlers.HandlerSecurityMiddleware()(
				handlers.HandlerApiKeyMiddleware(apiKey)(
					handlers.HandlerLoggingMiddleware()(
						handlers.HandlerMetricsMiddleware(httpMetricsCollector)(r.mux),
					),
				),
			),
		), corsCfg,
	)

	return restApi{handler: handler}
}

// Start launches the REST API server on the specified ports.
//   - port: Port for the main REST API
//   - metricsPort: Port for the metrics server
//
// This function also starts a metrics server in a separate goroutine.
func (r restApi) Start(port, metricsPort string) error {
	go func() {
		if err := startMetricsServer(metricsPort); err != nil {
			log.Fatalf("MetricsCfg server error: %v", err)
		}
	}()
	return http.ListenAndServe(":"+port, r.handler)
}

// NewRestApiBuilder initializes and returns a new RestApiBuilder with default configurations for the mux.
//
// gRPC-Gateway runtime settings are used to control how the REST API maps HTTP/JSON to gRPC/protobuf.
// For more on runtime configuration, refer to the runtime package:
//   - https://pkg.go.dev/github.com/grpc-ecosystem/grpc-gateway/v2/runtime
func NewRestApiBuilder() RestApiBuilder {
	return &restApiBuilder{
		mux: runtime.NewServeMux(
			runtime.WithMarshalerOption(runtime.MIMEWildcard, handlers.NewHttpBodyAwareJSONPb()),
			runtime.WithErrorHandler(handlers.CustomHTTPError),
			runtime.WithMetadata(handlers.HeadersIntoMetadata),
			runtime.WithForwardResponseOption(handlers.HttpResponseModifier),
			runtime.WithIncomingHeaderMatcher(handlers.IncomingHeaderMatcher),
			runtime.WithOutgoingHeaderMatcher(handlers.OutgoingHeaderMatcher),
			runtime.WithUnescapingMode(runtime.UnescapingModeAllCharacters),
		),
	}
}
