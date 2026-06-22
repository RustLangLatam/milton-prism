package grpc_client_sdk

import (
	"context"
	"fmt"
	"time"

	"milton_prism/pkg/config"
	applog "milton_prism/pkg/log"
	analysissvcv1 "milton_prism/pkg/pb/gen/milton_prism/services/analysis/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// AnalysisGrpcClient is a gRPC client for the analysis service.
type AnalysisGrpcClient struct {
	analysissvcv1.AnalysisServiceClient
	conn   *grpc.ClientConn
	health healthpb.HealthClient
	target string
	cancel context.CancelFunc
}

// NewAnalysisGRPCClient connects to the analysis service endpoint described by cfg.
func NewAnalysisGRPCClient(ctx context.Context, cfg *config.GrpcClientCfg) (*AnalysisGrpcClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("analysis grpc client: cfg is nil")
	}

	conn, err := grpc.NewClient(
		cfg.Endpoint(),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(CustomConnectParams),
	)
	if err != nil {
		return nil, fmt.Errorf("analysis grpc client: dial %s: %w", cfg.Endpoint(), err)
	}

	ctx, cancel := context.WithCancel(ctx)
	c := &AnalysisGrpcClient{
		AnalysisServiceClient: analysissvcv1.NewAnalysisServiceClient(conn),
		conn:                  conn,
		health:                healthpb.NewHealthClient(conn),
		target:                cfg.Endpoint(),
		cancel:                cancel,
	}

	if cfg.IsHealthCheckEnabled() {
		go c.monitorConnection(ctx)
	}
	return c, nil
}

// Conn returns the underlying gRPC client connection. It is exposed so a
// co-served service (e.g. BillingService, which is registered on the same
// analysis-services gRPC endpoint) can build a client over the SAME connection
// without opening a second dial or requiring extra config.
func (c *AnalysisGrpcClient) Conn() *grpc.ClientConn {
	return c.conn
}

// Close shuts down the background monitor and releases the connection.
func (c *AnalysisGrpcClient) Close() {
	c.cancel()
	_ = c.conn.Close()
}

func (c *AnalysisGrpcClient) monitorConnection(ctx context.Context) {
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
				applog.Infof("analysis grpc: health watch failed for %s: %v (retry in %v)", c.target, err, delay)
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
						applog.Warningf("analysis grpc: health stream error for %s: %v", c.target, err)
					}
					streamCancel()
					break
				}
				if resp.Status != healthpb.HealthCheckResponse_SERVING {
					applog.Warningf("analysis grpc: server %s unhealthy", c.target)
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
