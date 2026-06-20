package utils

import (
	"context"
	"errors"
	"milton_prism/pkg/config"
	"milton_prism/pkg/log"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	// import grpc/health to enable transparent client side checking
	_ "google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
)

var HealthServiceConfig = `{
	"loadBalancingPolicy": "round_robin",
	"healthCheckConfig": {
		"serviceName": ""
	}
}`

var HealthServiceConfigWithRetry = `{
	"loadBalancingPolicy": "round_robin",
	"healthCheckConfig": {
		"serviceName": ""
	},
	"methodConfig": [{
		"name": [{}],
		"retryPolicy": {
			"maxAttempts": 5,
			"initialBackoff": "0.1s",
			"maxBackoff": "1s",
			"backoffMultiplier": 2,
			"retryableStatusCodes": ["UNAVAILABLE"]
		}
	}]
}`

// HealthCheckFunc defines a generic function type for performing a health check on a gRPC connection.
type HealthCheckFunc func(ctx context.Context, conn *grpc.ClientConn) error

// CheckGrpcConnection attempts to connect to a gRPC endpoint and performs a basic health check.
func CheckGrpcConnection(ctx context.Context, endpoint string, opts []grpc.DialOption) (healthgrpc.HealthClient, error) {
	conn, err := grpc.NewClient(endpoint, opts...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			if cerr := conn.Close(); cerr != nil {
				log.Errorf("Failed to close conn to %s: %v", endpoint, cerr)
			}
			return
		}
		go func() {
			<-ctx.Done()
			if cerr := conn.Close(); cerr != nil {
				log.Errorf("Failed to close conn to %s: %v", endpoint, cerr)
			}
		}()
	}()

	// Perform a basic health check
	md := metadata.New(map[string]string{
		"x-health-check-type": "health-check",
	})

	// Add the metadata to the context
	ctx = metadata.NewOutgoingContext(ctx, md)

	healthClient := healthgrpc.NewHealthClient(conn)
	status, err := healthClient.Check(ctx, &healthgrpc.HealthCheckRequest{Service: ""})
	if err != nil {
		return nil, extractErrorDescription(err)
	}
	log.Infof("Health check result for %s: %s", endpoint, status.String())
	return healthClient, nil
}

// BuildDialOption builds grpc dial options
// https://grpc.io/docs/languages/go/basics/#dial-options
func BuildDialOption(config *config.ServerOptionCgf, healthCheck bool) []grpc.DialOption {
	// set up appropriate service settings_config
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(config.MaxRecvMsgSizeMB),
			grpc.MaxCallSendMsgSize(config.MaxSendMsgSizeMB),
		),
	}

	if healthCheck {
		opts = append(opts, grpc.WithDefaultServiceConfig(HealthServiceConfig))
	}

	return opts
}

func extractErrorDescription(err error) error {
	if err == nil {
		return nil
	}

	// Convert the error to a string
	errorMessage := err.Error()

	// Find the position of "desc ="
	descIndex := strings.Index(errorMessage, "desc =")
	if descIndex == -1 {
		// If "desc =" is not found, return the original error
		return err
	}

	// Find the starting quote after "desc ="
	startQuote := strings.Index(errorMessage[descIndex:], "\"")
	if startQuote == -1 {
		// If there's no starting quote, return the full substring after "desc ="
		return errors.New(strings.TrimSpace(errorMessage[descIndex+len("desc ="):]))
	}

	// Find the ending quote after the starting quote
	endQuote := strings.Index(errorMessage[descIndex+startQuote+1:], "\"")
	if endQuote == -1 {
		// If there's no ending quote, return the full substring after the starting quote
		return errors.New(strings.TrimSpace(errorMessage[descIndex+startQuote+1:]))
	}

	// Extract the content inside the quotes
	extracted := errorMessage[descIndex+startQuote+1 : descIndex+startQuote+1+endQuote]
	return errors.New(extracted)
}
