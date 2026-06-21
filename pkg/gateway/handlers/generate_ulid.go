package handlers

import (
	"context"
	"milton_prism/pkg/log"
	"net/http"

	"google.golang.org/grpc/metadata"

	"math/rand"
	"time"

	"github.com/oklog/ulid/v2"
)

// ContextKey is a custom type to avoid context key collisions
type ContextKey string

// CtxIDKey is the context key for the ULID
const CtxIDKey ContextKey = "x-ctx-id"

// HandlerGenerateContextIdMiddleware generates a ULID for each request and attaches it to the request's context.
func HandlerGenerateContextIdMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Create a source of entropy (seeded from the current time).
		t := time.Now().UTC()
		entropy := ulid.Monotonic(rand.New(rand.NewSource(t.UnixNano())), 0)

		// Generate a new ULID.
		id, err := ulid.New(ulid.Timestamp(t), entropy)
		if err != nil {
			http.Error(w, "Failed to generate context id", http.StatusInternalServerError)
			return
		}

		// Attach the ULID to the context.
		newCtx := context.WithValue(ctx, CtxIDKey, id.String())

		log.SetContextID(id.String())

		// Pass the newly created context with the request.
		next.ServeHTTP(w, r.WithContext(newCtx))
	})
}

// HeadersIntoMetadata creates a metadata.MD object from the headers of the request
func HeadersIntoMetadata(ctx context.Context, req *http.Request) metadata.MD {
	// Retrieve ctx_id from the context if it exists
	ctxID, ok := req.Context().Value(CtxIDKey).(string)
	if !ok {
		ctxID = "unknown"
	}

	// Create a metadata.MD object to store the header to be forwarded
	return metadata.Pairs(string(CtxIDKey), ctxID)
}
