// Package grpc_health wraps the gRPC health-check protocol to expose
// service readiness and liveness state.
package grpc_health

import (
	"context"
	"time"

	applog "milton_prism/pkg/log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
)

var (
	checkInterval = time.Second * 5
	system        = "" // empty string represents the health of the system

	// HealthStatus represents the current health status of the server.
	// By default, it is set to SERVING.
	HealthStatus = healthgrpc.HealthCheckResponse_SERVING
)

// CheckFunc is a type for functions that check the health of a dependency.
type CheckFunc func(ctx context.Context) bool

// SetupHealthCheck sets up a gRPC health check server and runs dependency checks periodically.
// If the CheckFunc returns false or HealthStatus is set to NOT_SERVING, the server is marked as unhealthy.
func SetupHealthCheck(server *grpc.Server, checkFunc CheckFunc) {
	applog.Info("Setting up health check")
	healthcheck := health.NewServer()
	healthgrpc.RegisterHealthServer(server, healthcheck)

	go func() {
		ctx := context.Background()

		for {
			status := HealthStatus

			// Check dependencies if a CheckFunc is provided and status is currently SERVING.
			if status == healthgrpc.HealthCheckResponse_SERVING && checkFunc != nil && !checkFunc(ctx) {
				status = healthgrpc.HealthCheckResponse_NOT_SERVING
				applog.Warningf("health check status updated: system=%s status=%v", system, status)
			}

			// Set the serving status for the system
			healthcheck.SetServingStatus(system, status)
			time.Sleep(checkInterval)
		}
	}()
}
