package services

import (
	"context"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"

	"milton_prism/pkg/config"
	"milton_prism/pkg/gateway"
	"milton_prism/pkg/log"
)

// ServiceRegisterFunc defines a function signature used to register gRPC services with the gRPC-Gateway mux.
type ServiceRegisterFunc func(context.Context, *runtime.ServeMux, string, []grpc.DialOption) error

// ServerGroup encapsulates all components of a microservice: gRPC server, HTTP gateway, and metrics server.
type ServerGroup struct {
	grpcServer        *grpc.Server
	metricsServer     *http.Server
	metricsRegistry   prometheus.Gatherer
	cfg               *config.MicroserviceServerCfg
	gatewayRegisterFn ServiceRegisterFunc
	healthCheckPath   string
	gatewayStopChan   chan struct{}
}

// NewServerGroup initializes a new instance of ServerGroup with all required components.
func NewServerGroup(
	cfg *config.MicroserviceServerCfg,
	grpcSrv *grpc.Server,
	metricsRegistry prometheus.Gatherer,
	registerFn ServiceRegisterFunc,
	healthPath string,
) *ServerGroup {
	return &ServerGroup{
		grpcServer:        grpcSrv,
		metricsRegistry:   metricsRegistry,
		cfg:               cfg,
		gatewayRegisterFn: registerFn,
		healthCheckPath:   healthPath,
		gatewayStopChan:   make(chan struct{}),
	}
}

// Run starts all enabled services (gRPC server, HTTP gateway, and metrics) and handles graceful shutdown.
func (sg *ServerGroup) Run() error {
	var g run.Group
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Add gRPC server to run group
	g.Add(
		func() error { return sg.runGRPCServer() },
		func(err error) { sg.stopGRPCServer(ctx, err) },
	)

	// Add metrics server if enabled
	if sg.cfg.MetricsEnabled() {
		g.Add(
			func() error { return sg.runMetricsServer() },
			func(err error) { sg.stopMetricsServer(ctx, err) },
		)
	}

	// Add HTTP gateway if enabled
	if sg.cfg.HttpEnabled() {
		g.Add(
			func() error { return sg.runHttpService(ctx) },
			func(err error) { sg.stopHTTPService(err) },
		)
	}

	// Add signal handler for graceful termination
	g.Add(run.SignalHandler(ctx, syscall.SIGINT, syscall.SIGTERM))

	return g.Run()
}

// runGRPCServer starts the gRPC server on the configured address.
func (sg *ServerGroup) runGRPCServer() error {
	lis, err := net.Listen("tcp", sg.cfg.Server.FullHost())
	if err != nil {
		return err
	}
	log.Infof("Starting gRPC server [%s] on %s", sg.cfg.Server.Name, lis.Addr())
	return sg.grpcServer.Serve(lis)
}

// stopGRPCServer gracefully stops the gRPC server or forces stop if timeout is reached.
func (sg *ServerGroup) stopGRPCServer(ctx context.Context, err error) {
	sg.logShutdown("gRPC server", err)

	stopped := make(chan struct{})
	go func() {
		sg.grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		log.Info("gRPC server shut down gracefully.")
	case <-ctx.Done():
		log.Warning("Forcing gRPC server shutdown...")
		sg.grpcServer.Stop()
	}
}

// runMetricsServer starts the Prometheus metrics HTTP server.
func (sg *ServerGroup) runMetricsServer() error {
	sg.metricsServer = &http.Server{
		Addr: sg.cfg.MetricsListen.FullHost(),
		Handler: promhttp.HandlerFor(
			sg.metricsRegistry,
			promhttp.HandlerOpts{EnableOpenMetrics: true},
		),
	}
	log.Infof("Starting Metrics server [%s] on %s", sg.cfg.Server.Name, sg.cfg.MetricsListen.FullURL())
	return sg.metricsServer.ListenAndServe()
}

// stopMetricsServer gracefully shuts down the metrics server with a timeout.
func (sg *ServerGroup) stopMetricsServer(ctx context.Context, err error) {
	sg.logShutdown("Metrics server", err)

	if sg.metricsServer != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := sg.metricsServer.Shutdown(shutdownCtx); err != nil {
			log.Errorf("Failed to shut down Metrics server: %v", err)
		}
	}
}

// runHttpService starts the HTTP gRPC-Gateway in a separate goroutine.
func (sg *ServerGroup) runHttpService(ctx context.Context) error {
	// Ensure the gRPC server is fully started before the gateway connects
	time.Sleep(500 * time.Millisecond)

	go func() {
		err := gateway.StartGatewayWithService(
			sg.cfg.HttpListen,
			sg.cfg.Server.ToClient(),
			sg.gatewayRegisterFn,
			sg.healthCheckPath,
		)
		if err != nil {
			log.Errorf("Failed to start HTTP gRPC-Gateway: %v", err)
		}
	}()

	// Wait until context is canceled or stop signal is received
	select {
	case <-ctx.Done():
	case <-sg.gatewayStopChan:
	}
	return nil
}

// stopHTTPService signals the HTTP gRPC-Gateway loop to shut down.
func (sg *ServerGroup) stopHTTPService(err error) {
	sg.logShutdown("HTTP service", err)
	close(sg.gatewayStopChan)
}

// logShutdown provides consistent shutdown logging behavior based on the shutdown reason.
func (sg *ServerGroup) logShutdown(serviceName string, err error) {
	switch {
	case isSignalInterrupt(err):
		log.Warningf("%s stopped by signal: %v", serviceName, err)
	case err != nil:
		log.Errorf("%s stopped with error: %v", serviceName, err)
	default:
		log.Infof("%s stopped gracefully", serviceName)
	}
}

// isSignalInterrupt checks whether the error represents a signal-based or context-canceled shutdown.
func isSignalInterrupt(err error) bool {
	if err == nil {
		return false
	}
	switch err.Error() {
	case "received signal interrupt", "received signal terminated", "context canceled":
		return true
	default:
		return false
	}
}
