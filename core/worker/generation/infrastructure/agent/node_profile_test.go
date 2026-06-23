package agent_test

import (
	"os"
	"strings"
	"testing"

	"milton_prism/core/worker/generation/infrastructure/agent"
)

// TestPromptProfileBindings_Node proves the generation worker resolves the
// "node" output profile to the TypeScript language label, the Node profile doc,
// and a build-step hint that names tsc (the build gate). Go/Python/default are
// unchanged. This is the only place the worker is language-aware, so it must
// stay in lockstep with outputProfileLabel/generatorPromptRef in the migration
// service.
func TestPromptProfileBindings_Node(t *testing.T) {
	label, doc, steps := agent.PromptProfileBindings("node", "grpc")
	if !strings.Contains(label, "TypeScript") {
		t.Errorf("node langLabel = %q, want it to mention TypeScript", label)
	}
	if doc != "docs/prism/milton-prism-node-profile.md" {
		t.Errorf("node profileDoc = %q, want the node profile doc", doc)
	}
	if !strings.Contains(steps, "tsc") {
		t.Errorf("node buildSteps = %q, want it to name tsc (the build gate)", steps)
	}

	// Unchanged profiles.
	goLabel, goDoc, _ := agent.PromptProfileBindings("go", "grpc")
	if goLabel != "Go" || goDoc != "docs/prism/milton-prism-go-profile.md" {
		t.Errorf("go bindings drifted: label=%q doc=%q", goLabel, goDoc)
	}
	pyLabel, pyDoc, _ := agent.PromptProfileBindings("python", "grpc")
	if pyLabel != "Python" || pyDoc != "docs/prism/milton-prism-python-profile.md" {
		t.Errorf("python bindings drifted: label=%q doc=%q", pyLabel, pyDoc)
	}
	// Rust is now a filled profile: Rust (Tonic) label + Rust profile doc.
	rustLabel, rustDoc, rustSteps := agent.PromptProfileBindings("rust", "grpc")
	if !strings.Contains(rustLabel, "Rust") {
		t.Errorf("rust langLabel = %q, want it to mention Rust", rustLabel)
	}
	if rustDoc != "docs/prism/milton-prism-rust-profile.md" {
		t.Errorf("rust profileDoc = %q, want the rust profile doc", rustDoc)
	}
	if !strings.Contains(rustSteps, "cargo build") {
		t.Errorf("rust buildSteps = %q, want it to name cargo build (the build gate)", rustSteps)
	}
	// Unknown profile falls back to Go.
	unkLabel, _, _ := agent.PromptProfileBindings("erlang", "grpc")
	if unkLabel != "Go" {
		t.Errorf("unknown profile langLabel = %q, want Go fallback", unkLabel)
	}
}

// TestWriteCombinedPrompt_NodeReferencesNodeDoc proves the combined prompt for a
// node-profile run references the Node profile doc and the TypeScript label, so
// the agent reads the correct mechanism reference.
func TestWriteCombinedPrompt_NodeReferencesNodeDoc(t *testing.T) {
	dir := t.TempDir()
	path, err := agent.WriteCombinedPrompt(
		dir,
		"docs/prism/milton-prism-service-generator-prompt-node.md",
		"user", "USR", "node", "grpc", "none", "",
		"service: user\nstore: mongodb\n",
		"syntax = \"proto3\";\n",
	)
	if err != nil {
		t.Fatalf("WriteCombinedPrompt: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	data := string(raw)
	for _, want := range []string{
		"TypeScript (Node)",
		"docs/prism/milton-prism-node-profile.md",
		"docs/prism/milton-prism-service-generator-prompt-node.md",
		"Output Profile: node",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("node combined prompt missing %q", want)
		}
	}
}

// TestPromptProfileBindings_GoHTTP proves the (go, http) cell resolves to the
// HTTP-native Go label and a build-step hint that forbids the gRPC server and
// names the google.api.http annotation, while (go, grpc) is unchanged.
func TestPromptProfileBindings_GoHTTP(t *testing.T) {
	label, doc, steps := agent.PromptProfileBindings("go", "http")
	if !strings.Contains(label, "HTTP") {
		t.Errorf("go-http langLabel = %q, want it to mention HTTP", label)
	}
	if doc != "docs/prism/milton-prism-go-profile.md" {
		t.Errorf("go-http profileDoc = %q, want the go profile doc", doc)
	}
	if !strings.Contains(steps, "google.api.http") || !strings.Contains(steps, "NO gRPC server") {
		t.Errorf("go-http buildSteps = %q, want it to pin HTTP-native + google.api.http", steps)
	}
	// (go, grpc) unchanged.
	gLabel, _, gSteps := agent.PromptProfileBindings("go", "grpc")
	if gLabel != "Go" || strings.Contains(gSteps, "NO gRPC server") {
		t.Errorf("go-grpc bindings drifted: label=%q steps=%q", gLabel, gSteps)
	}
}

// TestWriteCombinedPrompt_GoHTTPInjectsTransportSection proves the combined prompt
// for a (go, http) run references the HTTP generator prompt and injects the HTTP
// transport section (router + handlers, no gRPC server, no gateway), while a (go,
// grpc) run carries no such section.
func TestWriteCombinedPrompt_GoHTTPInjectsTransportSection(t *testing.T) {
	dir := t.TempDir()
	path, err := agent.WriteCombinedPrompt(
		dir,
		"docs/prism/milton-prism-service-generator-prompt-go-http.md",
		"user", "USR", "go", "http", "none", "",
		"service: user\nstore: mongodb\n",
		"syntax = \"proto3\";\n",
	)
	if err != nil {
		t.Fatalf("WriteCombinedPrompt: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	data := string(raw)
	for _, want := range []string{
		"Go (HTTP-native)",
		"Transport: HTTP (native)",
		"google.api.http",
		"do NOT call any `RegisterXxxServer`",
		"docs/prism/milton-prism-service-generator-prompt-go-http.md",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("go-http combined prompt missing %q", want)
		}
	}

	// A gRPC run carries no HTTP transport section.
	grpcPath, err := agent.WriteCombinedPrompt(
		dir,
		"docs/prism/milton-prism-service-generator-prompt.md",
		"user", "USR", "go", "grpc", "none", "",
		"service: user\n", "syntax = \"proto3\";\n",
	)
	if err != nil {
		t.Fatalf("WriteCombinedPrompt grpc: %v", err)
	}
	grpcRaw, _ := os.ReadFile(grpcPath)
	if strings.Contains(string(grpcRaw), "Transport: HTTP (native)") {
		t.Errorf("go-grpc combined prompt must not carry the HTTP transport section")
	}
}

// TestPromptProfileBindings_PythonHTTP proves the (python, http) cell resolves to
// the FastAPI HTTP-native label and a build-step hint that forbids the gRPC server
// (grpc.server/add_*Servicer_to_server) and names google.api.http + compileall,
// while (python, grpc) is unchanged.
func TestPromptProfileBindings_PythonHTTP(t *testing.T) {
	label, doc, steps := agent.PromptProfileBindings("python", "http")
	if !strings.Contains(label, "FastAPI") {
		t.Errorf("python-http langLabel = %q, want it to mention FastAPI", label)
	}
	if doc != "docs/prism/milton-prism-python-profile.md" {
		t.Errorf("python-http profileDoc = %q, want the python profile doc", doc)
	}
	if !strings.Contains(steps, "google.api.http") || !strings.Contains(steps, "compileall") ||
		!strings.Contains(steps, "NO grpc.server") {
		t.Errorf("python-http buildSteps = %q, want it to pin FastAPI + google.api.http + compileall", steps)
	}
	// (python, grpc) unchanged.
	pLabel, _, pSteps := agent.PromptProfileBindings("python", "grpc")
	if pLabel != "Python" || strings.Contains(pSteps, "FastAPI") {
		t.Errorf("python-grpc bindings drifted: label=%q steps=%q", pLabel, pSteps)
	}
}

// TestWriteCombinedPrompt_PythonHTTPInjectsTransportSection proves the combined
// prompt for a (python, http) run references the FastAPI HTTP generator prompt and
// injects the Python HTTP transport section (FastAPI app, no grpc.server), while a
// (python, grpc) run carries no such section.
func TestWriteCombinedPrompt_PythonHTTPInjectsTransportSection(t *testing.T) {
	dir := t.TempDir()
	path, err := agent.WriteCombinedPrompt(
		dir,
		"docs/prism/milton-prism-service-generator-prompt-python-http.md",
		"user", "USR", "python", "http", "none", "",
		"service: user\nstore: mongodb\n",
		"syntax = \"proto3\";\n",
	)
	if err != nil {
		t.Fatalf("WriteCombinedPrompt: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	data := string(raw)
	for _, want := range []string{
		"Python (FastAPI HTTP-native)",
		"Transport: HTTP (native, FastAPI)",
		"google.api.http",
		"add_*Servicer_to_server",
		"docs/prism/milton-prism-service-generator-prompt-python-http.md",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("python-http combined prompt missing %q", want)
		}
	}
	// The Go net/http prose must not leak into a Python HTTP prompt.
	if strings.Contains(data, "RegisterXxxServer") {
		t.Errorf("python-http combined prompt leaked Go transport prose")
	}

	// A python gRPC run carries no HTTP transport section.
	grpcPath, err := agent.WriteCombinedPrompt(
		dir,
		"docs/prism/milton-prism-service-generator-prompt-python.md",
		"user", "USR", "python", "grpc", "none", "",
		"service: user\n", "syntax = \"proto3\";\n",
	)
	if err != nil {
		t.Fatalf("WriteCombinedPrompt grpc: %v", err)
	}
	grpcRaw, _ := os.ReadFile(grpcPath)
	if strings.Contains(string(grpcRaw), "Transport: HTTP") {
		t.Errorf("python-grpc combined prompt must not carry the HTTP transport section")
	}
}

// TestPromptProfileBindings_NodeHTTP proves the (node, http) cell resolves to the
// Fastify HTTP-native label and a build-step hint that forbids the @grpc/grpc-js
// server (new Server()/addService) and names google.api.http + tsc, while
// (node, grpc) is unchanged.
func TestPromptProfileBindings_NodeHTTP(t *testing.T) {
	label, doc, steps := agent.PromptProfileBindings("node", "http")
	if !strings.Contains(label, "Fastify") {
		t.Errorf("node-http langLabel = %q, want it to mention Fastify", label)
	}
	if doc != "docs/prism/milton-prism-node-profile.md" {
		t.Errorf("node-http profileDoc = %q, want the node profile doc", doc)
	}
	if !strings.Contains(steps, "google.api.http") || !strings.Contains(steps, "tsc --noEmit") ||
		!strings.Contains(steps, "NO new Server()") {
		t.Errorf("node-http buildSteps = %q, want it to pin Fastify + google.api.http + tsc", steps)
	}
	// (node, grpc) unchanged.
	nLabel, _, nSteps := agent.PromptProfileBindings("node", "grpc")
	if nLabel != "TypeScript (Node)" || strings.Contains(nSteps, "Fastify") {
		t.Errorf("node-grpc bindings drifted: label=%q steps=%q", nLabel, nSteps)
	}
}

// TestWriteCombinedPrompt_NodeHTTPInjectsTransportSection proves the combined
// prompt for a (node, http) run references the Fastify HTTP generator prompt and
// injects the Node HTTP transport section (Fastify app, no @grpc/grpc-js Server),
// while a (node, grpc) run carries no such section.
func TestWriteCombinedPrompt_NodeHTTPInjectsTransportSection(t *testing.T) {
	dir := t.TempDir()
	path, err := agent.WriteCombinedPrompt(
		dir,
		"docs/prism/milton-prism-service-generator-prompt-node-http.md",
		"user", "USR", "node", "http", "none", "",
		"service: user\nstore: mongodb\n",
		"syntax = \"proto3\";\n",
	)
	if err != nil {
		t.Fatalf("WriteCombinedPrompt: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	data := string(raw)
	for _, want := range []string{
		"TypeScript (Fastify HTTP-native)",
		"Transport: HTTP (native, Fastify)",
		"google.api.http",
		"new Server()",
		"docs/prism/milton-prism-service-generator-prompt-node-http.md",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("node-http combined prompt missing %q", want)
		}
	}
	// The Go net/http prose must not leak into a Node HTTP prompt.
	if strings.Contains(data, "RegisterXxxServer") {
		t.Errorf("node-http combined prompt leaked Go transport prose")
	}

	// A node gRPC run carries no HTTP transport section.
	grpcPath, err := agent.WriteCombinedPrompt(
		dir,
		"docs/prism/milton-prism-service-generator-prompt-node.md",
		"user", "USR", "node", "grpc", "none", "",
		"service: user\n", "syntax = \"proto3\";\n",
	)
	if err != nil {
		t.Fatalf("WriteCombinedPrompt grpc: %v", err)
	}
	grpcRaw, _ := os.ReadFile(grpcPath)
	if strings.Contains(string(grpcRaw), "Transport: HTTP") {
		t.Errorf("node-grpc combined prompt must not carry the HTTP transport section")
	}
}

// TestPromptProfileBindings_RustHTTP proves the (rust, http) cell resolves to the
// axum HTTP-native label and a build-step hint that forbids the tonic server
// (tonic::transport::Server / add_service) and names google.api.http + cargo build,
// while (rust, grpc) keeps the Tonic label.
func TestPromptProfileBindings_RustHTTP(t *testing.T) {
	label, doc, steps := agent.PromptProfileBindings("rust", "http")
	if !strings.Contains(label, "axum") {
		t.Errorf("rust-http langLabel = %q, want it to mention axum", label)
	}
	if doc != "docs/prism/milton-prism-rust-profile.md" {
		t.Errorf("rust-http profileDoc = %q, want the rust profile doc", doc)
	}
	if !strings.Contains(steps, "google.api.http") || !strings.Contains(steps, "cargo build") ||
		!strings.Contains(steps, "NO tonic::transport::Server") {
		t.Errorf("rust-http buildSteps = %q, want it to pin axum + google.api.http + cargo build", steps)
	}
	// (rust, grpc) unchanged.
	rLabel, _, rSteps := agent.PromptProfileBindings("rust", "grpc")
	if rLabel != "Rust (Tonic)" || strings.Contains(rSteps, "axum") {
		t.Errorf("rust-grpc bindings drifted: label=%q steps=%q", rLabel, rSteps)
	}
}

// TestWriteCombinedPrompt_RustHTTPInjectsTransportSection proves the combined
// prompt for a (rust, http) run references the axum HTTP generator prompt and
// injects the Rust HTTP transport section (axum app, no tonic server), while a
// (rust, grpc) run carries no such section.
func TestWriteCombinedPrompt_RustHTTPInjectsTransportSection(t *testing.T) {
	dir := t.TempDir()
	path, err := agent.WriteCombinedPrompt(
		dir,
		"docs/prism/milton-prism-service-generator-prompt-rust-http.md",
		"user", "USR", "rust", "http", "none", "",
		"service: user\nstore: mongodb\n",
		"syntax = \"proto3\";\n",
	)
	if err != nil {
		t.Fatalf("WriteCombinedPrompt: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	data := string(raw)
	for _, want := range []string{
		"Rust (axum HTTP-native)",
		"Transport: HTTP (native, axum)",
		"google.api.http",
		"tonic::transport::Server",
		"docs/prism/milton-prism-service-generator-prompt-rust-http.md",
	} {
		if !strings.Contains(data, want) {
			t.Errorf("rust-http combined prompt missing %q", want)
		}
	}
	// The Go net/http prose must not leak into a Rust HTTP prompt.
	if strings.Contains(data, "RegisterXxxServer") {
		t.Errorf("rust-http combined prompt leaked Go transport prose")
	}

	// A rust gRPC run carries no HTTP transport section.
	grpcPath, err := agent.WriteCombinedPrompt(
		dir,
		"docs/prism/milton-prism-service-generator-prompt-rust.md",
		"user", "USR", "rust", "grpc", "none", "",
		"service: user\n", "syntax = \"proto3\";\n",
	)
	if err != nil {
		t.Fatalf("WriteCombinedPrompt grpc: %v", err)
	}
	grpcRaw, _ := os.ReadFile(grpcPath)
	if strings.Contains(string(grpcRaw), "Transport: HTTP") {
		t.Errorf("rust-grpc combined prompt must not carry the HTTP transport section")
	}
}
