package grpc_client_sdk

import (
	"context"
	"fmt"
	"time"

	"milton_prism/pkg/config"
	applog "milton_prism/pkg/log"
	migrationsvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/migration/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// MigrationGrpcClient is a gRPC client for the migration service.
type MigrationGrpcClient struct {
	migrationsvcv1.MigrationServiceClient
	conn   *grpc.ClientConn
	health healthpb.HealthClient
	target string
	cancel context.CancelFunc
}

// NewMigrationGRPCClient connects to the migration service endpoint described by cfg.
func NewMigrationGRPCClient(ctx context.Context, cfg *config.GrpcClientCfg) (*MigrationGrpcClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("migration grpc client: cfg is nil")
	}

	conn, err := grpc.NewClient(
		cfg.Endpoint(),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(CustomConnectParams),
	)
	if err != nil {
		return nil, fmt.Errorf("migration grpc client: dial %s: %w", cfg.Endpoint(), err)
	}

	ctx, cancel := context.WithCancel(ctx)
	c := &MigrationGrpcClient{
		MigrationServiceClient: migrationsvcv1.NewMigrationServiceClient(conn),
		conn:                   conn,
		health:                 healthpb.NewHealthClient(conn),
		target:                 cfg.Endpoint(),
		cancel:                 cancel,
	}

	if cfg.IsHealthCheckEnabled() {
		go c.monitorConnection(ctx)
	}
	return c, nil
}

// Conn returns the underlying gRPC client connection.
func (c *MigrationGrpcClient) Conn() *grpc.ClientConn {
	return c.conn
}

// Close shuts down the background monitor and releases the connection.
func (c *MigrationGrpcClient) Close() {
	c.cancel()
	_ = c.conn.Close()
}

func (c *MigrationGrpcClient) monitorConnection(ctx context.Context) {
	backoffCfg := backoff.Config{
		BaseDelay:  1.0 * time.Second,
		Multiplier: 1.6,
		Jitter:     0.2,
		MaxDelay:   120 * time.Second,
	}
	var attempt uint
	for {
		select {
		case <-ctx.Done():
			return
		default:
			streamCtx, streamCancel := context.WithCancel(ctx)
			stream, err := c.health.Watch(streamCtx, &healthpb.HealthCheckRequest{Service: ""})
			if err != nil {
				streamCancel()
				if ctx.Err() != nil {
					return
				}
				delay := calculateBackoff(attempt, backoffCfg)
				attempt++
				applog.Infof("migration grpc: health watch failed for %s: %v (retry in %v)", c.target, err, delay)
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
					continue
				}
			}
			attempt = 0
			for {
				resp, err := stream.Recv()
				if err != nil {
					if ctx.Err() == nil {
						applog.Warningf("migration grpc: health stream error for %s: %v", c.target, err)
					}
					streamCancel()
					break
				}
				if resp.Status != healthpb.HealthCheckResponse_SERVING {
					applog.Warningf("migration grpc: server %s unhealthy", c.target)
					if c.conn.GetState() != connectivity.TransientFailure {
						c.conn.ResetConnectBackoff()
					}
				}
				select {
				case <-ctx.Done():
					streamCancel()
					return
				default:
				}
			}
			time.Sleep(1 * time.Second)
		}
	}
}
