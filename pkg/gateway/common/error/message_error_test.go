package message_error

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLooksLikeErrorCode(t *testing.T) {
	good := []string{"MIG107", "ANL301", "MIG222", "ANL103", "ANL204"}
	bad := []string{"Plan Limit Reached", "", "MIG", "ABCDEF12345", "mig107", "Failure Unsupported", "M1", "MIGRATION"}
	for _, s := range good {
		if !looksLikeErrorCode(s) {
			t.Errorf("looksLikeErrorCode(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if looksLikeErrorCode(s) {
			t.Errorf("looksLikeErrorCode(%q) = true, want false", s)
		}
	}
}

func TestHandlerErrorMessage_EmitsDomainCode(t *testing.T) {
	// Domain errors arrive as "CODE: message" in the gRPC status message.
	st := status.New(codes.FailedPrecondition, "ANL301: Plan Limit Reached: 5 Analyses Per Month. Upgrade Your Plan.")
	got := HandlerErrorMessage(*st)
	if got.Code != "ANL301" {
		t.Fatalf("expected Code=ANL301, got %q (detail=%q)", got.Code, got.Detail)
	}
}

func TestHandlerErrorMessage_NoCodeForPlainMessage(t *testing.T) {
	// A non-domain error with no "CODE: " prefix must not leak a bogus code.
	st := status.New(codes.Internal, "something broke: deep in the stack")
	got := HandlerErrorMessage(*st)
	if got.Code != "" {
		t.Fatalf("expected empty Code for plain message, got %q", got.Code)
	}
}
