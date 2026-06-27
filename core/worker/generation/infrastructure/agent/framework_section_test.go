package agent_test

import (
	"strings"
	"testing"

	"milton_prism/core/worker/generation/infrastructure/agent"
)

// TestFrameworkSection_GRPCInjectsNothing pins the contract that the
// HTTP-framework sub-axis is inert for gRPC: regardless of framework, a gRPC
// protocol yields an empty block.
func TestFrameworkSection_GRPCInjectsNothing(t *testing.T) {
	for _, fw := range []string{"", "net_http", "gin", "echo"} {
		if got := agent.FrameworkSection("go", "grpc", fw); got != "" {
			t.Errorf("FrameworkSection(go, grpc, %q) = %q, want empty", fw, got)
		}
	}
}

// TestFrameworkSection_HTTPDefaultInjectsNothing pins the non-regression contract:
// the Go HTTP default (empty or net_http) injects nothing so the established
// net/http HTTP-native prompt/skeleton is unchanged.
func TestFrameworkSection_HTTPDefaultInjectsNothing(t *testing.T) {
	for _, fw := range []string{"", "net_http"} {
		if got := agent.FrameworkSection("go", "http", fw); got != "" {
			t.Errorf("FrameworkSection(go, http, %q) = %q, want empty (default)", fw, got)
		}
	}
}

// TestFrameworkSection_GoGin asserts the Go + HTTP + Gin block pins Gin as the
// router/handler framework with the idiomatic constraints and the go.mod
// dependency, and does NOT leak gRPC/gateway prose.
func TestFrameworkSection_GoGin(t *testing.T) {
	got := agent.FrameworkSection("go", "http", "gin")
	if got == "" {
		t.Fatal("FrameworkSection(go, http, gin) is empty, want a Gin block")
	}
	for _, want := range []string{
		"HTTP Framework: Gin",
		"github.com/gin-gonic/gin",
		"gin.Engine",
		"gin.Context",
		"google.api.http",
		"go build ./...",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Gin framework block missing %q", want)
		}
	}
	// The block is HTTP-native: it must explicitly disclaim a gRPC server and an API
	// gateway, never instruct emitting one.
	if !strings.Contains(got, "NO gRPC server") {
		t.Errorf("Gin block must disclaim a gRPC server (HTTP-native): %q", got)
	}
}
