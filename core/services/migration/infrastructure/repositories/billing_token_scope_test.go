package repositories

import (
	"context"
	"testing"

	"milton_prism/core/shared/auth_token"

	"google.golang.org/grpc/metadata"
)

// TestSystemOutgoingContext_UsesSystemTokenNotInbound verifies that the system
// outgoing context carries ONLY the system token and drops the inbound user
// token — the scope guarantee for RecordUsage (B4).
func TestSystemOutgoingContext_UsesSystemTokenNotInbound(t *testing.T) {
	called := 0
	a := &BillingClientAdapter{
		tokenProvider: func(_ context.Context) (string, error) {
			called++
			return "SYSTEM-TOKEN", nil
		},
	}

	// Inbound context carries a USER token that must NOT be forwarded.
	in := metadata.NewIncomingContext(context.Background(),
		metadata.New(map[string]string{auth_token.TokenAccessName: "USER-TOKEN"}))

	out, err := a.systemOutgoingContext(in)
	if err != nil {
		t.Fatalf("systemOutgoingContext: %v", err)
	}
	if called != 1 {
		t.Fatalf("tokenProvider called %d times, want 1", called)
	}
	md, ok := metadata.FromOutgoingContext(out)
	if !ok {
		t.Fatal("no outgoing metadata")
	}
	got := md.Get(auth_token.TokenAccessName)
	if len(got) != 1 || got[0] != "SYSTEM-TOKEN" {
		t.Fatalf("outgoing auth = %v, want [SYSTEM-TOKEN]", got)
	}
	for _, v := range got {
		if v == "USER-TOKEN" {
			t.Fatal("inbound user token leaked into the system outgoing context")
		}
	}
}

// TestSystemOutgoingContext_NoProviderFallsBackToInbound verifies the legacy
// fallback (no provider) forwards the inbound metadata unchanged.
func TestSystemOutgoingContext_NoProviderFallsBackToInbound(t *testing.T) {
	a := &BillingClientAdapter{tokenProvider: nil}
	in := metadata.NewIncomingContext(context.Background(),
		metadata.New(map[string]string{auth_token.TokenAccessName: "USER-TOKEN"}))

	out, err := a.systemOutgoingContext(in)
	if err != nil {
		t.Fatalf("systemOutgoingContext: %v", err)
	}
	md, _ := metadata.FromOutgoingContext(out)
	got := md.Get(auth_token.TokenAccessName)
	if len(got) != 1 || got[0] != "USER-TOKEN" {
		t.Fatalf("fallback outgoing auth = %v, want [USER-TOKEN]", got)
	}
}
