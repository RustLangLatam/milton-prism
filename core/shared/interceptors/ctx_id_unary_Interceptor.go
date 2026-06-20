// Package interceptors provides gRPC unary server interceptors for
// request-ID propagation, structured logging, and panic recovery.
package interceptors

import (
	"context"
	"math/rand"
	"milton_prism/pkg/log"
	"time"

	"github.com/oklog/ulid/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// requestCtxIDKey is the key used in context for storing request Identifier.
const requestCtxIDKey = "x-ctx-id"

// CtxIdUnaryInterceptor is a gRPC middleware that ensures each request has a unique `ctx_id`.
// If the `ctx_id` is not present in the metadata, it generates a new one and adds it to both:
//   - The context's metadata (for gRPC propagation)
//   - The context's values (for internal use)
//
// Implements idempotency following AIP-155 and request tracing following AIP-4232.
func CtxIdUnaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	// Extract or generate request Identifier
	requestID := getOrGenerateRequestID(ctx)

	// Create new context with the request Identifier in both metadata and values
	newCtx := propagateRequestID(ctx, requestID)

	// Set in logger context
	log.SetContextID(requestID)

	// Continue handling the gRPC call
	return handler(newCtx, req)
}

// getOrGenerateRequestID extracts or creates a request Identifier
func getOrGenerateRequestID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if requestIDs := md.Get(requestCtxIDKey); len(requestIDs) > 0 && requestIDs[0] != "" {
			return requestIDs[0] // Use existing Identifier
		}
	}
	return generateRequestCtxID() // Generate new Identifier
}

// propagateRequestID ensures the request Identifier exists in both metadata and context values
func propagateRequestID(ctx context.Context, requestID string) context.Context {
	// Add to context values
	ctx = context.WithValue(ctx, requestCtxIDKey, requestID)

	// Add to outgoing metadata if not already present
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if requestIDs := md.Get(requestCtxIDKey); len(requestIDs) == 0 || requestIDs[0] == "" {
			md = md.Copy()
			md.Set(requestCtxIDKey, requestID)
			ctx = metadata.NewIncomingContext(ctx, md)
		}
	} else {
		md := metadata.New(map[string]string{requestCtxIDKey: requestID})
		ctx = metadata.NewIncomingContext(ctx, md)
	}

	// Ensure it's available for outgoing calls too
	ctx = metadata.AppendToOutgoingContext(ctx, requestCtxIDKey, requestID)

	return ctx
}

// generateRequestCtxID generates a unique request Identifier using the current timestamp.
// Replace this with a UUID generation for more robust request IDs if needed.
func generateRequestCtxID() string {
	// Create a source of entropy (seeded from the current time).
	t := time.Now().UTC()
	entropy := ulid.Monotonic(rand.New(rand.NewSource(t.UnixNano())), 0)

	// Generate a new ULID.
	id, err := ulid.New(ulid.Timestamp(t), entropy)
	if err != nil {
		log.Error("Failed to generate ULID")
	}

	return id.String()
}
