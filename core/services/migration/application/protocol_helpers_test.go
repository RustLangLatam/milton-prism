package application

import (
	"testing"

	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

// TestGeneratorPromptRef_GoHTTP proves the (go, http) cell selects the dedicated
// HTTP-native generator prompt, while (go, grpc) keeps the established gRPC prompt
// and the non-Go profiles are unaffected by the transport. MUST stay in lockstep
// with the worker's profileAndPromptForLanguage.
func TestGeneratorPromptRef_GoHTTP(t *testing.T) {
	cases := []struct {
		name      string
		profile   string
		transport migrationv1.Transport
		want      string
	}{
		{"go_http", "go", migrationv1.Transport_TRANSPORT_HTTP, "docs/prism/milton-prism-service-generator-prompt-go-http.md"},
		{"go_grpc", "go", migrationv1.Transport_TRANSPORT_GRPC, "docs/prism/milton-prism-service-generator-prompt.md"},
		{"go_unspecified", "go", migrationv1.Transport_TRANSPORT_UNSPECIFIED, "docs/prism/milton-prism-service-generator-prompt.md"},
		// Python + HTTP selects the FastAPI-native prompt; Python + gRPC keeps the gRPC prompt.
		{"python_http", "python", migrationv1.Transport_TRANSPORT_HTTP, "docs/prism/milton-prism-service-generator-prompt-python-http.md"},
		{"python_grpc", "python", migrationv1.Transport_TRANSPORT_GRPC, "docs/prism/milton-prism-service-generator-prompt-python.md"},
		// Node + HTTP selects the Fastify-native prompt; Node + gRPC keeps the gRPC prompt.
		{"node_http", "node", migrationv1.Transport_TRANSPORT_HTTP, "docs/prism/milton-prism-service-generator-prompt-node-http.md"},
		{"node_grpc", "node", migrationv1.Transport_TRANSPORT_GRPC, "docs/prism/milton-prism-service-generator-prompt-node.md"},
		// Rust + HTTP selects the axum-native prompt; Rust + gRPC keeps the Tonic prompt.
		{"rust_http", "rust", migrationv1.Transport_TRANSPORT_HTTP, "docs/prism/milton-prism-service-generator-prompt-rust-http.md"},
		{"rust_grpc", "rust", migrationv1.Transport_TRANSPORT_GRPC, "docs/prism/milton-prism-service-generator-prompt-rust.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := generatorPromptRef(tc.profile, tc.transport); got != tc.want {
				t.Errorf("generatorPromptRef(%q, %v) = %q, want %q", tc.profile, tc.transport, got, tc.want)
			}
		})
	}
}

// TestProtocolLabel proves the assembler/worker protocol label derivation:
// TRANSPORT_HTTP→"http", everything else (incl. UNSPECIFIED and nil target)→"grpc".
func TestProtocolLabel(t *testing.T) {
	if got := protocolLabel(nil); got != "grpc" {
		t.Errorf("protocolLabel(nil) = %q, want grpc", got)
	}
	cases := []struct {
		name      string
		transport migrationv1.Transport
		want      string
	}{
		{"http", migrationv1.Transport_TRANSPORT_HTTP, "http"},
		{"grpc", migrationv1.Transport_TRANSPORT_GRPC, "grpc"},
		{"unspecified", migrationv1.Transport_TRANSPORT_UNSPECIFIED, "grpc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc2 := &migrationv1.TargetConfig{InterServiceTransport: tc.transport}
			if got := protocolLabel(tc2); got != tc.want {
				t.Errorf("protocolLabel(%v) = %q, want %q", tc.transport, got, tc.want)
			}
		})
	}
}

// TestStoreLabel proves the deliverable-side store label derivation feeding the
// assembler: TARGET_DATABASE_POSTGRES→"postgres", MARIADB→"mysql", and everything
// else (MONGODB, UNSPECIFIED, nil target)→"mongodb" — the established default.
func TestStoreLabel(t *testing.T) {
	if got := storeLabel(nil); got != "mongodb" {
		t.Errorf("storeLabel(nil) = %q, want mongodb", got)
	}
	cases := []struct {
		name string
		db   migrationv1.TargetDatabase
		want string
	}{
		{"postgres", migrationv1.TargetDatabase_TARGET_DATABASE_POSTGRES, "postgres"},
		{"mariadb_mysql", migrationv1.TargetDatabase_TARGET_DATABASE_MARIADB, "mysql"},
		{"mongodb", migrationv1.TargetDatabase_TARGET_DATABASE_MONGODB, "mongodb"},
		{"unspecified", migrationv1.TargetDatabase_TARGET_DATABASE_UNSPECIFIED, "mongodb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc2 := &migrationv1.TargetConfig{Database: tc.db}
			if got := storeLabel(tc2); got != tc.want {
				t.Errorf("storeLabel(%v) = %q, want %q", tc.db, got, tc.want)
			}
		})
	}
}
