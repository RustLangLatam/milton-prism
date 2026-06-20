// Package grpc_client_sdk provides gRPC client constructors for inter-service
// communication within the milton-prism platform.
package grpc_client_sdk

import (
	"milton_prism/core/shared/auth_token"
	"sync"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/metadata"
)

var CustomConnectParams = grpc.ConnectParams{
	MinConnectTimeout: 10 * time.Second,
	Backoff: backoff.Config{
		BaseDelay:  1.0 * time.Second,
		Multiplier: 1.6,
		Jitter:     0.2,
		MaxDelay:   120 * time.Second,
	},
}

type authContext struct {
	mu           sync.RWMutex
	accessToken  string
	refreshToken string
	ctxId        string
}

func (a *authContext) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	md := map[string]string{
		auth_token.TokenAccessName:  a.accessToken,
		auth_token.TokenRefreshName: a.refreshToken,
	}
	if a.ctxId != "" {
		md[auth_token.CtxIdName] = a.ctxId
	}
	return md, nil
}

func (a *authContext) RequireTransportSecurity() bool {
	return false // use insecure.NewCredentials
}

func (a *authContext) ForwardableOutgoingContext(ctx context.Context) context.Context {
	md := metadata.New(map[string]string{
		auth_token.ForwardedName: "true",
	})

	// Attach the metadata to the context. This creates a *new* context for outgoing calls.
	return metadata.NewOutgoingContext(ctx, md)
}

func calculateBackoff(attempt uint, cfg backoff.Config) time.Duration {
	backoffDuration := cfg.BaseDelay * time.Duration(1<<attempt)
	if backoffDuration > cfg.MaxDelay {
		backoffDuration = cfg.MaxDelay
	}
	// Add jitter
	jitter := time.Duration(cfg.Jitter * float64(backoffDuration))
	return backoffDuration + jitter
}
