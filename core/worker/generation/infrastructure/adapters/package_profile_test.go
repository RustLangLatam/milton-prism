package adapters

import (
	"testing"

	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

// TestProfileAndPromptForLanguage_Node proves the generation-package reader maps
// the Node target language to the "node" output profile and the Node generator
// prompt — the worker-side lockstep with the migration service's
// outputProfileLabel/generatorPromptRef. A regression here makes a Node migration
// silently generate Go (the bug that profile=go in the worker logs revealed).
func TestProfileAndPromptForLanguage_Node(t *testing.T) {
	cases := []struct {
		name       string
		lang       migrationv1.TargetLanguage
		wantProf   string
		wantPrompt string
	}{
		{"go", migrationv1.TargetLanguage_TARGET_LANGUAGE_GO, "go", "docs/prism/milton-prism-service-generator-prompt.md"},
		{"python", migrationv1.TargetLanguage_TARGET_LANGUAGE_PYTHON, "python", "docs/prism/milton-prism-service-generator-prompt-python.md"},
		{"node", migrationv1.TargetLanguage_TARGET_LANGUAGE_NODE, "node", "docs/prism/milton-prism-service-generator-prompt-node.md"},
		{"rust", migrationv1.TargetLanguage_TARGET_LANGUAGE_RUST, "rust", "docs/prism/milton-prism-service-generator-prompt-rust.md"},
		{"unspecified_defaults_go", migrationv1.TargetLanguage_TARGET_LANGUAGE_UNSPECIFIED, "go", "docs/prism/milton-prism-service-generator-prompt.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prof, prompt := profileAndPromptForLanguage(tc.lang, migrationv1.Transport_TRANSPORT_GRPC)
			if prof != tc.wantProf {
				t.Errorf("profile = %q, want %q", prof, tc.wantProf)
			}
			if prompt != tc.wantPrompt {
				t.Errorf("promptRef = %q, want %q", prompt, tc.wantPrompt)
			}
		})
	}
}

// TestProfileAndPromptForLanguage_GoHTTP proves the worker selects the HTTP-native
// generator prompt for the (Go, HTTP) cell, keeps the gRPC prompt for (Go, gRPC),
// and that the transport does not affect the non-Go profiles. Worker-side lockstep
// with the migration service's generatorPromptRef.
func TestProfileAndPromptForLanguage_GoHTTP(t *testing.T) {
	cases := []struct {
		name       string
		lang       migrationv1.TargetLanguage
		transport  migrationv1.Transport
		wantProf   string
		wantPrompt string
	}{
		{"go_http", migrationv1.TargetLanguage_TARGET_LANGUAGE_GO, migrationv1.Transport_TRANSPORT_HTTP, "go", "docs/prism/milton-prism-service-generator-prompt-go-http.md"},
		{"go_grpc", migrationv1.TargetLanguage_TARGET_LANGUAGE_GO, migrationv1.Transport_TRANSPORT_GRPC, "go", "docs/prism/milton-prism-service-generator-prompt.md"},
		{"go_unspecified", migrationv1.TargetLanguage_TARGET_LANGUAGE_GO, migrationv1.Transport_TRANSPORT_UNSPECIFIED, "go", "docs/prism/milton-prism-service-generator-prompt.md"},
		{"python_http", migrationv1.TargetLanguage_TARGET_LANGUAGE_PYTHON, migrationv1.Transport_TRANSPORT_HTTP, "python", "docs/prism/milton-prism-service-generator-prompt-python-http.md"},
		{"python_grpc", migrationv1.TargetLanguage_TARGET_LANGUAGE_PYTHON, migrationv1.Transport_TRANSPORT_GRPC, "python", "docs/prism/milton-prism-service-generator-prompt-python.md"},
		// Node + HTTP selects the Fastify-native prompt; Node + gRPC keeps the gRPC prompt.
		{"node_http", migrationv1.TargetLanguage_TARGET_LANGUAGE_NODE, migrationv1.Transport_TRANSPORT_HTTP, "node", "docs/prism/milton-prism-service-generator-prompt-node-http.md"},
		{"node_grpc", migrationv1.TargetLanguage_TARGET_LANGUAGE_NODE, migrationv1.Transport_TRANSPORT_GRPC, "node", "docs/prism/milton-prism-service-generator-prompt-node.md"},
		// Rust + HTTP selects the axum-native prompt; Rust + gRPC keeps the Tonic prompt.
		{"rust_http", migrationv1.TargetLanguage_TARGET_LANGUAGE_RUST, migrationv1.Transport_TRANSPORT_HTTP, "rust", "docs/prism/milton-prism-service-generator-prompt-rust-http.md"},
		{"rust_grpc", migrationv1.TargetLanguage_TARGET_LANGUAGE_RUST, migrationv1.Transport_TRANSPORT_GRPC, "rust", "docs/prism/milton-prism-service-generator-prompt-rust.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prof, prompt := profileAndPromptForLanguage(tc.lang, tc.transport)
			if prof != tc.wantProf {
				t.Errorf("profile = %q, want %q", prof, tc.wantProf)
			}
			if prompt != tc.wantPrompt {
				t.Errorf("promptRef = %q, want %q", prompt, tc.wantPrompt)
			}
		})
	}
}

// TestProtocolLabel proves the worker maps the Transport to the protocol label
// ("grpc" | "http") with UNSPECIFIED canonicalising to "grpc".
func TestProtocolLabel(t *testing.T) {
	cases := []struct {
		transport migrationv1.Transport
		want      string
	}{
		{migrationv1.Transport_TRANSPORT_HTTP, "http"},
		{migrationv1.Transport_TRANSPORT_GRPC, "grpc"},
		{migrationv1.Transport_TRANSPORT_UNSPECIFIED, "grpc"},
	}
	for _, tc := range cases {
		if got := protocolLabel(tc.transport); got != tc.want {
			t.Errorf("protocolLabel(%v) = %q, want %q", tc.transport, got, tc.want)
		}
	}
}
