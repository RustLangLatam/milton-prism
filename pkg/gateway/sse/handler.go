// Package sse implements the GET /v1/events Server-Sent Events endpoint for the
// API gateway. It is mounted as a route-level sibling ABOVE the gateway's
// CORS→apiKey→logging→metrics middleware chain so that:
//   - it is reachable by a header-less browser EventSource (no apiKey 401), and
//   - it streams unbuffered (the chain's responseWriter wrappers lack Flush()).
//
// Auth is verify-only against the gateway's configured public key: the handler
// verifies the ?access_token= query parameter and derives the channel from the
// VERIFIED token owner only — never from a request parameter. The token schema
// (JWT or PASETO) is selected by the gateway's [auth.tokenValidator].schemaType;
// both carry the owner in user_properties.user_id.
package sse

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"milton_prism/core/shared/auth_token"
	"milton_prism/core/shared/event_bus"
	"milton_prism/pkg/log"

	"github.com/gomodule/redigo/redis"
)

const (
	// heartbeatInterval is the cadence of `:keepalive` comment frames that keep
	// the connection (and intermediary proxies) alive.
	heartbeatInterval = 20 * time.Second
	// retryMillis is the EventSource reconnect backoff advertised on connect.
	retryMillis = 3000
)

// tokenClaims is the minimal external-claims target for token verification. The
// JSON field name matches both JWTClaims and PasetoClaims, so a single struct
// works regardless of the configured schema.
type tokenClaims struct {
	UserProperties map[string]interface{} `json:"user_properties"`
}

// Handler serves GET /v1/events as a Server-Sent Events stream backed by a
// KeyDB/Redis pub-sub subscription on the authenticated user's channel.
type Handler struct {
	pool      *redis.Pool
	validator auth_token.TokenValidator
}

// NewHandler builds an SSE handler over a redigo pool and a token validator
// (JWT or PASETO, per gateway config).
func NewHandler(pool *redis.Pool, validator auth_token.TokenValidator) *Handler {
	return &Handler{pool: pool, validator: validator}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The ResponseWriter MUST support flushing for streaming. The gateway mounts
	// this handler outside the middleware chain precisely so the raw,
	// flush-capable ResponseWriter reaches us. Guard anyway.
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Error("sse: ResponseWriter does not support flushing — streaming unsupported")
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Auth: verify the token and derive the owner. The channel is computed from
	// the verified owner ONLY (cross-user leak guard).
	ownerUserID, err := h.authenticate(r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":401,"title":"Unauthorized","detail":"invalid or missing access_token"}`))
		return
	}

	channel := event_bus.UserChannel(ownerUserID)

	// SSE response headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Belt-and-braces: instruct nginx not to buffer even if the location block
	// were misconfigured.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Advertise the reconnect backoff.
	fmt.Fprintf(w, "retry: %d\n\n", retryMillis)
	flusher.Flush()

	// Dedicated pooled connection for the pub-sub subscription. Released on
	// teardown; closing it also unblocks the reader goroutine's Receive().
	conn := h.pool.Get()
	psc := redis.PubSubConn{Conn: conn}
	if err := psc.Subscribe(channel); err != nil {
		log.Errorf("sse: subscribe failed owner_user_id=%d channel=%s: %v", ownerUserID, channel, err)
		_ = conn.Close()
		return
	}
	defer func() { _ = conn.Close() }()

	// Optional Last-Event-ID is accepted but ignored — no replay in Phase 1.
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		log.Infof("sse: Last-Event-ID=%s ignored (no replay) owner_user_id=%d", lastID, ownerUserID)
	}

	log.Infof("sse: stream opened owner_user_id=%d channel=%s", ownerUserID, channel)

	// Reader goroutine relays pub-sub messages onto buffered channels so the
	// main loop can multiplex heartbeats + context cancellation. Buffered sends
	// guarantee the goroutine never blocks after teardown.
	msgCh := make(chan []byte, 16)
	errCh := make(chan error, 1)
	go func() {
		for {
			// ReceiveWithTimeout(0) clears the per-connection read deadline that
			// the shared cache pool bakes in via DialReadTimeout. Without this the
			// long-lived subscriber would hit that timeout (~10s) on a quiet
			// channel and tear the stream down between heartbeats. A zero timeout
			// blocks until a message arrives or the conn is closed on teardown.
			switch v := psc.ReceiveWithTimeout(0).(type) {
			case redis.Message:
				msgCh <- v.Data
			case redis.Subscription:
				// subscribe/unsubscribe ack — ignore
			case error:
				errCh <- v
				return
			}
		}
	}()

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			log.Infof("sse: stream closed (client disconnected) owner_user_id=%d", ownerUserID)
			return
		case err := <-errCh:
			log.Warningf("sse: pub-sub receive error owner_user_id=%d: %v", ownerUserID, err)
			return
		case data := <-msgCh:
			if id := extractEventID(data); id != "" {
				fmt.Fprintf(w, "id: %s\n", id)
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ":keepalive\n\n")
			flusher.Flush()
		}
	}
}

// authenticate verifies the ?access_token= PASETO token and returns the owner
// user id taken from the verified claims. It never trusts a request parameter
// for identity.
func (h *Handler) authenticate(r *http.Request) (uint64, error) {
	token := r.URL.Query().Get("access_token")
	if token == "" {
		return 0, errors.New("missing access_token")
	}

	var claims tokenClaims
	ok, err := h.validator.Verify(token, false, &claims)
	if err != nil || !ok {
		return 0, errors.New("invalid token")
	}

	raw, exists := claims.UserProperties["user_id"]
	if !exists {
		return 0, errors.New("token missing user_id claim")
	}

	// JSON numbers decode into float64 when unmarshalled into interface{}.
	switch v := raw.(type) {
	case float64:
		if v < 0 {
			return 0, errors.New("invalid user_id claim")
		}
		return uint64(v), nil
	case json.Number:
		n, err := v.Int64()
		if err != nil || n < 0 {
			return 0, errors.New("invalid user_id claim")
		}
		return uint64(n), nil
	default:
		return 0, errors.New("unexpected user_id claim type")
	}
}

// extractEventID pulls the event_id out of a published JSON envelope for the
// SSE `id:` line. A missing/invalid id simply omits the line.
func extractEventID(data []byte) string {
	var env struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return ""
	}
	return env.EventID
}
