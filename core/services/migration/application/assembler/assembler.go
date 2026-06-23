// Package assembler builds a complete, standalone Go monorepo deliverable
// from the static skeleton of the Milton Prism monorepo and the generated
// artifacts of a specific migration.
//
// Callers supply a list of InputFile records (path + UTF-8 content, already
// loaded from generation_file_artifacts) and the path to PRISM_MONOREPO_PATH.
// The assembler reads the static skeleton from disk and merges it with the
// generated files. Generated files always win when a path collides with a
// skeleton file (e.g. pkg/gateway/common/error/message_error.go).
//
// The output is a []File slice suitable for writing to a ZIP archive or to a
// git push. No service-specific logic lives here — callers own DB access.
package assembler

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// InputFile is one generated source file from generation_file_artifacts.
type InputFile struct {
	Path    string // canonical monorepo-relative path, e.g. core/services/user/wire.go
	Content string // UTF-8 source
}

// File is one file in the assembled deliverable.
type File struct {
	Path    string // monorepo-relative path
	Content []byte // file content
}

// Assembler merges a static skeleton from a monorepo root with generated artifacts.
type Assembler struct {
	skeletonRoot  string // absolute path to PRISM_MONOREPO_PATH
	useApiGateway bool   // whether to include the generated API gateway entrypoint
	profile       string // output profile: "go" (default) or "python"
	protocol      string // transport: "grpc" (default) or "http"
	store         string // persistence engine: "mongodb" (default) | "postgres" | "mysql"
}

// New returns an Assembler that reads skeleton files from skeletonRoot.
// useApiGateway controls whether api-gateway/cmd/... is synthesised; false omits it.
// profile selects the skeleton and post-Assemble behaviour: "python" emits a
// Python deliverable (Python shared scaffolding + protos only, zero Go);
// "node" emits a TypeScript/gRPC deliverable (generated TS + protos only, zero
// Go/Python); any other value (including "" or "go") emits the Go deliverable
// unchanged.
//
// protocol selects the transport variant of a deliverable. For Go, "http" emits a
// Go HTTP-native deliverable: the pkg/gateway/ subtree is excluded EXCEPT
// pkg/gateway/common/error/ (pure error maps reused by the REST handlers), since
// an HTTP-native service is its own entry point and never wires the gRPC gateway.
// For Python, "http" emits a FastAPI-native deliverable: the gRPC server bootstrap
// (grpc.server/add_*Servicer_to_server) and the runtime gRPC proto stubs
// (*_pb2_grpc.py) are excluded from the generated artifacts, since the FastAPI app
// is the sole entry point and the messages are modelled with pydantic. For Node,
// "http" emits a Fastify-native deliverable (the @grpc/grpc-js server bootstrap
// and *_grpc_pb stubs are excluded). For Rust, "http" emits an axum-native
// deliverable: the tonic gRPC server bootstrap (tonic::transport::Server /
// add_service) and the tonic-generated server trait impl (infrastructure/grpc/)
// are excluded, since the axum app is the sole entry point and the messages are
// modelled with plain Rust/serde structs.
// Empty or "grpc" keeps the gateway subtree / gRPC server (the established gRPC
// behaviour).
// store selects the persistence-config variant of a Go deliverable. "postgres"
// makes the synthesised per-service config a PostgreSQL `.env.example`
// (DATABASE_URL / DB_HOST/PORT/USER/PASSWORD/NAME) instead of the Mongo
// config.toml.example, matching the raw-SQL repos the generator emits for
// Go + PostgreSQL. Empty / "mongodb" keeps the Mongo config.toml.example (the
// established behaviour). The store is consumed only by the Go config-example
// step; the Python/Node/Rust deliverables are MongoDB-only in v1.
func New(skeletonRoot string, useApiGateway bool, profile, protocol, store string) *Assembler {
	return &Assembler{skeletonRoot: skeletonRoot, useApiGateway: useApiGateway, profile: profile, protocol: protocol, store: store}
}

// isGoPostgres reports whether this Assembler targets the Go + PostgreSQL
// deliverable: the per-service config example is a PostgreSQL .env.example rather
// than the Mongo config.toml.example. Only the Go profile carries a SQL store in v1.
func (a *Assembler) isGoPostgres() bool {
	return !a.isPython() && !a.isNode() && !a.isRust() && a.store == "postgres"
}

// isGoHTTP reports whether this Assembler targets the Go HTTP-native deliverable
// (Go profile + HTTP transport). The gateway subtree is excluded for this cell.
func (a *Assembler) isGoHTTP() bool {
	return a.protocol == "http" && !a.isPython() && !a.isNode() && !a.isRust()
}

// isPython reports whether this Assembler targets the Python output profile.
func (a *Assembler) isPython() bool { return a.profile == "python" }

// isPythonHTTP reports whether this Assembler targets the Python HTTP-native
// (FastAPI) deliverable (Python profile + HTTP transport). The gRPC server
// bootstrap and runtime gRPC proto stubs are excluded for this cell — the FastAPI
// app is the sole entrypoint and the messages are modelled with pydantic, so no
// grpc.server/add_*Servicer_to_server bootstrap and no *_pb2_grpc.py runtime stub
// belong in the package. The grpc-api-gateway is already excluded for HTTP by the
// download path (useApiGateway = micro && gRPC).
func (a *Assembler) isPythonHTTP() bool {
	return a.isPython() && a.protocol == "http"
}

// isPythonGRPCArtifact reports whether a generated artifact is gRPC-specific and
// therefore must NOT ship in a Python HTTP-native (FastAPI) deliverable. Two cases:
//   - *_pb2_grpc.py — the generated gRPC client/server proto stubs, unused when the
//     messages are modelled with pydantic and the transport is FastAPI.
//   - any .py whose body bootstraps a gRPC server (grpc.server( / grpc.aio.server(
//     or an add_*Servicer_to_server call) — the gRPC server __main__/entrypoint,
//     which the FastAPI app replaces. Identified by content so a FastAPI __main__
//     (uvicorn runner) is kept while a gRPC __main__ is dropped.
func isPythonGRPCArtifact(path, content string) bool {
	if strings.HasSuffix(path, "_pb2_grpc.py") {
		return true
	}
	if !strings.HasSuffix(path, ".py") {
		return false
	}
	if strings.Contains(content, "grpc.server(") || strings.Contains(content, "grpc.aio.server(") {
		return true
	}
	// add_<Name>Servicer_to_server( — the servicer registration call.
	if i := strings.Index(content, "Servicer_to_server("); i >= 0 &&
		strings.LastIndex(content[:i], "add_") >= 0 {
		return true
	}
	return false
}

// isNode reports whether this Assembler targets the Node (TypeScript) profile.
func (a *Assembler) isNode() bool { return a.profile == "node" }

// isNodeHTTP reports whether this Assembler targets the Node HTTP-native (Fastify)
// deliverable (Node profile + HTTP transport). The gRPC server bootstrap
// (new Server()/addService over @grpc/grpc-js) and the runtime gRPC proto stubs
// (*_grpc_pb) are excluded for this cell — the Fastify app is the sole entrypoint
// and the messages are modelled with plain TypeScript types, so no gRPC server
// bootstrap and no *_grpc_pb runtime stub belong in the package. The
// grpc-api-gateway is already excluded for HTTP by the download path
// (useApiGateway = micro && gRPC).
func (a *Assembler) isNodeHTTP() bool {
	return a.isNode() && a.protocol == "http"
}

// isNodeGRPCArtifact reports whether a generated artifact is gRPC-specific and
// therefore must NOT ship in a Node HTTP-native (Fastify) deliverable. Two cases:
//   - *_grpc_pb.{ts,js,d.ts} — the generated gRPC client/server proto stubs, unused
//     when the messages are plain TS types and the transport is Fastify.
//   - any .ts/.js whose body bootstraps a @grpc/grpc-js server: a `new Server(`
//     (the grpc.Server constructor) or an `.addService(` call (servicer
//     registration) — the gRPC server bootstrap/entrypoint, which the Fastify app
//     replaces. Identified by content so a Fastify main.ts (listen runner) is kept
//     while a gRPC bootstrap is dropped.
func isNodeGRPCArtifact(path, content string) bool {
	if strings.Contains(path, "_grpc_pb") {
		return true
	}
	if !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".js") {
		return false
	}
	if strings.Contains(content, "new Server(") || strings.Contains(content, ".addService(") {
		return true
	}
	return false
}

// isRust reports whether this Assembler targets the Rust profile.
func (a *Assembler) isRust() bool { return a.profile == "rust" }

// isRustHTTP reports whether this Assembler targets the Rust HTTP-native (axum)
// deliverable (Rust profile + HTTP transport). The tonic gRPC server bootstrap
// (tonic::transport::Server / add_service) and the tonic-generated server trait
// impl are excluded for this cell — the axum app is the sole entrypoint and the
// messages are modelled with plain Rust/serde structs, so no tonic server
// bootstrap and no tonic-build server codegen belong in the package. The
// grpc-api-gateway is already excluded for HTTP by the download path
// (useApiGateway = micro && gRPC).
func (a *Assembler) isRustHTTP() bool {
	return a.isRust() && a.protocol == "http"
}

// isRustGRPCArtifact reports whether a generated artifact is tonic-gRPC-specific
// and therefore must NOT ship in a Rust HTTP-native (axum) deliverable. Two cases:
//   - any .rs whose body bootstraps a tonic server: a `tonic::transport::Server`
//     / `transport::Server::builder(` (the server builder) or an `.add_service(`
//     call (servicer registration) — the gRPC server bootstrap/entrypoint, which
//     the axum app replaces. Identified by content so a `main.rs` that runs axum
//     (`axum::serve` / `Router`) is kept while a tonic bootstrap is dropped.
//   - any .rs under an `infrastructure/grpc/` path — the tonic generated-service
//     trait impl (the gRPC handlers), replaced by `infrastructure/http/` for axum.
func isRustGRPCArtifact(path, content string) bool {
	if !strings.HasSuffix(path, ".rs") {
		return false
	}
	if strings.Contains(path, "/infrastructure/grpc/") {
		return true
	}
	if strings.Contains(content, "transport::Server") || strings.Contains(content, ".add_service(") {
		return true
	}
	return false
}

// isInternalBufTemplate reports whether path is a platform-INTERNAL buf template
// that must never ship in a user deliverable, no matter the profile or source:
//   - protobuf/buf.docs.gen.yaml         — generates the PLATFORM panel openapi via
//                                           the `../milton-prism-panel` symlink.
//   - protobuf/buf.deliverable.openapi.yaml — the platform pipeline template that
//                                           emits docs/openapi.yaml during generation.
//
// Both are Milton Prism tooling. The user-facing buf configs (buf.yaml, and for Go
// buf.go.gen.yaml) are NOT matched here and continue to ship. The generated
// docs/openapi.yaml artifact is also not matched and still ships.
func isInternalBufTemplate(path string) bool {
	switch path {
	case "protobuf/buf.docs.gen.yaml", "protobuf/buf.deliverable.openapi.yaml":
		return true
	}
	return false
}

// sourceRoot returns the directory the agent writes generated code under for a
// non-Go profile (python/, node/ or rust/), which step 3b renames to core/.
// Returns "" for the Go profile (no rename). Must stay in lockstep with
// profileSourceRoot in the migration application layer.
func (a *Assembler) sourceRoot() string {
	switch a.profile {
	case "python":
		return "python"
	case "node":
		return "node"
	case "rust":
		return "rust"
	default:
		return ""
	}
}

// Assemble returns the full set of files for a standalone, compilable deliverable.
// Generated artifacts override any skeleton file at the same relative path.
func (a *Assembler) Assemble(artifacts []InputFile) ([]File, error) {
	// 1. Collect skeleton files into a map keyed by relative path.
	skeleton := make(map[string][]byte)
	if err := a.walkSkeleton(skeleton); err != nil {
		return nil, fmt.Errorf("assembler: read skeleton: %w", err)
	}

	// 2. Merge: generated artifacts override skeleton at the same path.
	// Use a map to deduplicate; artifacts win over skeleton.
	merged := make(map[string][]byte, len(skeleton)+len(artifacts))
	for p, c := range skeleton {
		merged[p] = c
	}
	for _, f := range artifacts {
		if f.Path == "" {
			continue
		}
		// Platform-internal buf templates must NEVER ship, regardless of source
		// (skeleton OR artifacts). The generation agent runs
		// buf.deliverable.openapi.yaml to emit docs/openapi.yaml; if the agent ever
		// persists the template itself (or the panel-only buf.docs.gen.yaml) as an
		// artifact, drop it here so it cannot leak into any deliverable. These are
		// Milton Prism tooling, not part of the user's exported project. The
		// generated docs/openapi.yaml itself is fine and still ships.
		if isInternalBufTemplate(f.Path) {
			continue
		}
		// Profile guard: a Python deliverable must never carry Go artifacts. The
		// generation pipeline's __pipeline__ aggregator emits a Go gateway error
		// map (pkg/gateway/common/error/message_error.go) regardless of profile;
		// for Python that file is irrelevant (errors are handled by
		// python/shared/errors) and must not leak into the package.
		if a.isPython() && (strings.HasSuffix(f.Path, ".go") ||
			f.Path == "go.mod" || f.Path == "go.sum" || f.Path == "Makefile") {
			continue
		}
		// Profile+protocol guard: a Python HTTP-native (FastAPI) deliverable is its
		// own entry point and models messages with pydantic, so the gRPC server
		// bootstrap (grpc.server/add_*Servicer_to_server) and the runtime gRPC proto
		// stubs (*_pb2_grpc.py) must not ship — only the FastAPI app and its support
		// code. A FastAPI __main__ (uvicorn runner) is kept (it carries no gRPC
		// server call); only gRPC-bootstrap .py files are dropped.
		if a.isPythonHTTP() && isPythonGRPCArtifact(f.Path, f.Content) {
			continue
		}
		// Profile guard: a Node (TypeScript) deliverable must never carry Go or
		// Python artifacts. Same rationale as Python — the Go error aggregator is
		// skipped for the node profile in the pipeline, but defend here too so a
		// stray .go/.py (or Go go.mod) can never leak into a TS package.
		if a.isNode() && (strings.HasSuffix(f.Path, ".go") ||
			strings.HasSuffix(f.Path, ".py") ||
			f.Path == "go.mod" || f.Path == "go.sum" || f.Path == "Makefile") {
			continue
		}
		// Profile+protocol guard: a Node HTTP-native (Fastify) deliverable is its own
		// entry point and models messages with plain TS types, so the gRPC server
		// bootstrap (new Server()/addService over @grpc/grpc-js) and the runtime gRPC
		// proto stubs (*_grpc_pb) must not ship — only the Fastify app and its support
		// code. A Fastify main.ts (listen runner) is kept (it carries no gRPC server
		// call); only gRPC-bootstrap .ts/.js files are dropped.
		if a.isNodeHTTP() && isNodeGRPCArtifact(f.Path, f.Content) {
			continue
		}
		// Profile guard: a Rust (Tonic) deliverable must never carry Go, Python or
		// Node artifacts. Same rationale as Python/Node — the Go error aggregator
		// is skipped for the rust profile in the pipeline, but defend here too so a
		// stray .go/.py/.ts (or Go/Node manifest) can never leak into a Rust crate.
		if a.isRust() && (strings.HasSuffix(f.Path, ".go") ||
			strings.HasSuffix(f.Path, ".py") ||
			strings.HasSuffix(f.Path, ".ts") ||
			f.Path == "go.mod" || f.Path == "go.sum" || f.Path == "Makefile" ||
			strings.HasSuffix(f.Path, "/package.json") || f.Path == "package.json" ||
			strings.HasSuffix(f.Path, "/package-lock.json") || f.Path == "package-lock.json" ||
			// cargo build output and lockfile: defence-in-depth (the worker's
			// artifact collector already drops these, but a stray one must never
			// land in the deliverable).
			isCargoBuildArtifact(f.Path)) {
			continue
		}
		// Profile+protocol guard: a Rust HTTP-native (axum) deliverable is its own
		// entry point and models messages with plain Rust/serde structs, so the
		// tonic gRPC server bootstrap (tonic::transport::Server / add_service) and
		// the tonic-generated server trait impl (infrastructure/grpc/) must not ship
		// — only the axum app and its support code. An axum main.rs (serve runner)
		// is kept (it carries no tonic server call); only tonic-bootstrap .rs files
		// and the grpc handler dir are dropped.
		if a.isRustHTTP() && isRustGRPCArtifact(f.Path, f.Content) {
			continue
		}
		// Profile guard: a Go HTTP-native deliverable is its own entry point and
		// never wires the gRPC gateway, so the grpc-gateway transcoder
		// (*.pb.gw.go) and the gRPC server stub (*_grpc.pb.go) must not ship —
		// the message *.pb.go types are kept. Their imports (pkg/gateway runtime,
		// grpc server) are excluded from the HTTP skeleton, so shipping them would
		// break go build.
		if a.isGoHTTP() && (strings.HasSuffix(f.Path, ".pb.gw.go") || strings.HasSuffix(f.Path, "_grpc.pb.go")) {
			continue
		}
		merged[f.Path] = []byte(f.Content)
	}

	// 2a. Rust guardrail: a generated Rust gRPC service must never ship `.proto`
	// files under its source tree (which becomes core/services/<svc>/ after the
	// rename in step 3b). The agent image's protoc carries no well-known-type or
	// google.api includes, so the agent tends to vendor those third-party protos
	// into rust/services/<svc>/proto_include/google/… and add that dir as a second
	// tonic-build include path. That violates the invariant that `core/services/`
	// is source code only and that every `.proto` lives under the canonical
	// `protobuf/proto/` tree. Relocate any such vendored proto to the top-level
	// protobuf/proto/<import-path> (the path under proto_include/ IS the protoc
	// import string), dedup, drop the per-service copies, and rewrite each rust
	// build.rs to stop referencing proto_include — the google deps now resolve via
	// the canonical protobuf/proto include root that build.rs already passes.
	if a.isRust() {
		relocateRustVendoredProtos(merged)
	}

	// 3. Append config.toml.example files (per-service, always), per-service
	// Makefiles, and the API gateway entrypoint (conditional on useApiGateway).
	// Neither ever contains real credentials.
	//
	// All three post-Assemble steps synthesise Go (Go config.toml.example with
	// the milton_prism/pkg/config CONFIG_PACKAGE, Go service Makefiles, and the
	// Go gateway main.go). They are skipped for the Python and Node profiles,
	// which must contain zero Go scaffolding; the language-appropriate extras
	// arrive via the generated artifacts list plus the per-profile .env.example.
	if !a.isPython() && !a.isNode() && !a.isRust() {
		// Persistence-config variant: Go + PostgreSQL emits a per-service
		// PostgreSQL .env.example (DATABASE_URL / DB_*) matching the raw-SQL repos
		// the generator wrote; Go + MongoDB (default) keeps the Mongo
		// config.toml.example. The auth section is identical (EdDSA tokens) in
		// both — only the data-store config differs.
		if a.isGoPostgres() {
			if err := generatePostgresConfigExamples(merged); err != nil {
				return nil, fmt.Errorf("assembler: postgres config examples: %w", err)
			}
		} else {
			if err := generateConfigExamples(merged); err != nil {
				return nil, fmt.Errorf("assembler: config examples: %w", err)
			}
		}
		if err := generateServiceMakefiles(merged); err != nil {
			return nil, fmt.Errorf("assembler: service makefiles: %w", err)
		}
		if a.useApiGateway {
			if err := generateGatewayCode(merged, a.skeletonRoot); err != nil {
				return nil, fmt.Errorf("assembler: gateway code: %w", err)
			}
		}
	}

	// 3a. Python profile: append a per-service .env.example (the pydantic homologue
	// of the Go config.toml.example) BEFORE the python/ → core/ rename, so the
	// generator sees service dirs keyed under python/services/<svc>/. The emitted
	// .env.example paths are rewritten to core/services/<svc>/.env.example by the
	// rename step below, matching the Go per-service placement.
	if a.isPython() {
		if err := generatePythonConfigExamples(merged); err != nil {
			return nil, fmt.Errorf("assembler: python config examples: %w", err)
		}
	}

	// 3a-node. Node profile: append a per-service .env.example (the TypeScript
	// homologue of the Go config.toml.example / Python .env.example) BEFORE the
	// node/ → core/ rename, so service dirs are still keyed under
	// node/services/<svc>/. The emitted .env.example paths are rewritten to
	// core/services/<svc>/.env.example by the rename step below.
	if a.isNode() {
		if err := generateNodeConfigExamples(merged); err != nil {
			return nil, fmt.Errorf("assembler: node config examples: %w", err)
		}
	}

	// 3a-rust. Rust profile: append a per-service .env.example (the Tonic homologue
	// of the Go config.toml.example / Python / Node .env.example) BEFORE the
	// rust/ → core/ rename, so service dirs are still keyed under
	// rust/services/<svc>/. The emitted .env.example paths are rewritten to
	// core/services/<svc>/.env.example by the rename step below.
	if a.isRust() {
		if err := generateRustConfigExamples(merged); err != nil {
			return nil, fmt.Errorf("assembler: rust config examples: %w", err)
		}
	}

	// 3b. Python/Node/Rust profile: rename the source-root dir (python/, node/ or rust/) →
	// core/ to homologate with the Go deliverable layout (Go uses core/). The
	// Python imports are top-level packages relative to the source root and the
	// Node imports are relative paths within the source root, so renaming the
	// root dir does NOT change any import — only the directory name. Protos
	// (protobuf/…) and any other paths are untouched.
	if root := a.sourceRoot(); root != "" {
		renamed := make(map[string][]byte, len(merged))
		for p, c := range merged {
			if p == root || strings.HasPrefix(p, root+"/") {
				p = "core" + strings.TrimPrefix(p, root)
			}
			renamed[p] = c
		}
		merged = renamed
	}

	// 4. Flatten to sorted slice for deterministic output.
	out := make([]File, 0, len(merged))
	for p, c := range merged {
		out = append(out, File{Path: p, Content: c})
	}
	sortFiles(out)
	return out, nil
}

// walkSkeleton reads all skeleton-eligible files from a.skeletonRoot.
func (a *Assembler) walkSkeleton(dst map[string][]byte) error {
	return filepath.WalkDir(a.skeletonRoot, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(a.skeletonRoot, abs)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel) // canonical forward-slash paths

		if d.IsDir() {
			if a.skipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}

		if !a.isSkeletonFile(rel) {
			return nil
		}

		content, readErr := os.ReadFile(abs)
		if readErr != nil {
			return fmt.Errorf("assembler: read %s: %w", rel, readErr)
		}
		dst[rel] = content
		return nil
	})
}

// skipDir dispatches to the per-profile directory filter.
func (a *Assembler) skipDir(rel string) bool {
	if a.isPython() {
		return skipDirPython(rel)
	}
	if a.isNode() {
		return skipDirNode(rel)
	}
	if a.isRust() {
		return skipDirRust(rel)
	}
	return skipDir(rel)
}

// isSkeletonFile dispatches to the per-profile file filter.
func (a *Assembler) isSkeletonFile(rel string) bool {
	if a.isPython() {
		return isSkeletonFilePython(rel)
	}
	if a.isNode() {
		return isSkeletonFileNode(rel)
	}
	if a.isRust() {
		return isSkeletonFileRust(rel)
	}
	// Go HTTP-native: exclude the whole pkg/gateway/ subtree EXCEPT
	// pkg/gateway/common/error/ (pure error maps the REST handlers reuse). The
	// HTTP service is its own entry point and never wires the gRPC gateway, so the
	// gateway runtime/transcoder code has no place in the deliverable.
	if a.isGoHTTP() && strings.HasPrefix(rel, "pkg/gateway/") &&
		!strings.HasPrefix(rel, "pkg/gateway/common/error/") {
		return false
	}
	// Go HTTP-native: drop the gRPC-only skeleton file whose imports are not
	// shipped in this cell — build_server_group.go wires the gRPC server + the
	// excluded pkg/gateway runtime. It is not referenced by the HTTP-native entry
	// point, so excluding it keeps go build green. (The platform grpc_*_client.go
	// files are already dropped for every Go cell by isSkeletonFile.)
	if a.isGoHTTP() && rel == "core/internal/svc/build_server_group.go" {
		return false
	}
	return isSkeletonFile(rel)
}

// skipDir returns true for directories that should be skipped entirely.
// This prunes large subtrees that never contribute skeleton files.
func skipDir(rel string) bool {
	// Top-level dirs to skip wholesale.
	skip := []string{
		".git", "infra", "docs", "python", "bin", "node_modules",
		"milton-prism-panel",
		// core/cmd and core/services contain platform service entrypoints —
		// only the generated service cmd dirs arrive via artifacts, not the skeleton.
		"core/cmd", "core/services",
		// api-gateway is the Milton Prism HTTP gateway, not part of the deliverable.
		"api-gateway",
		// Platform-only pb/gen subtrees: identity, migration, analysis, repository.
		// Shared types (pagination, query_params) are allowed via isSkeletonFile.
		"pkg/pb/gen/milton_prism/services",
		"pkg/pb/gen/milton_prism/types/identity",
		"pkg/pb/gen/milton_prism/types/migration",
		"pkg/pb/gen/milton_prism/types/analysis",
		"pkg/pb/gen/milton_prism/types/repository",
		"pkg/pb/gen/milton_prism/types/common",
		// protobuf source trees for platform services — buf configs at
		// protobuf/ root are included as exact files in isSkeletonFile.
		"protobuf/proto",
	}
	for _, s := range skip {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return false
}

// isSkeletonFile returns true when the file at rel should be included in the
// deliverable skeleton. Generated artifacts may still override it at merge time.
func isSkeletonFile(rel string) bool {
	// ── Exact root-level files ──────────────────────────────────────────────
	switch rel {
	case "go.mod", "go.sum", "Makefile":
		return true
	}

	// ── buf config files ────────────────────────────────────────────────────
	// Ship ONLY the user-facing buf configs:
	//   - buf.yaml       — the proto module (lint/breaking/deps); lets the user
	//                       regenerate their own stubs against the shipped protos.
	//   - buf.go.gen.yaml — the Go codegen template, so a Go user can `buf generate`
	//                       their *.pb.go / *_grpc.pb.go from protobuf/proto.
	// The two platform-INTERNAL templates are NEVER shipped (they are Milton Prism
	// tooling, not part of the user's exported project):
	//   - buf.docs.gen.yaml          — generates the PLATFORM panel openapi via the
	//                                    `../milton-prism-panel` symlink (panel-only).
	//   - buf.deliverable.openapi.yaml — the platform pipeline template that emits
	//                                    docs/openapi.yaml; the agent runs it during
	//                                    generation, but the template itself is
	//                                    internal tooling. The resulting
	//                                    docs/openapi.yaml still ships as an artifact.
	switch rel {
	case "protobuf/buf.yaml", "protobuf/buf.go.gen.yaml":
		return true
	}

	// ── pkg/pb/gen — shared types and proto-registration packages ────────────
	// openapiv3 provides the blank-import side-effect used by every generated
	// .pb.go file; token/v1 is required by core/shared/auth_token.
	for _, dir := range []string{
		"pkg/pb/gen/openapiv3/",
		"pkg/pb/gen/milton_prism/types/token/",
		"pkg/pb/gen/milton_prism/types/articles/",
		"pkg/pb/gen/milton_prism/types/pagination/",
		"pkg/pb/gen/milton_prism/types/query_params/",
	} {
		if strings.HasPrefix(rel, dir) && strings.HasSuffix(rel, ".go") {
			return true
		}
	}

	// ── grpc_client_sdk exclusions ───────────────────────────────────────────
	// builder.go is generic; the platform-specific clients import platform
	// service stubs (pkg/pb/gen/.../services/{analysis,identity,repository,
	// billing,migration}/v1) that skipDir prunes from EVERY deliverable
	// (pkg/pb/gen/milton_prism/services). Generated services never call these
	// clients directly (they use only builder.go's generic helpers), nor does
	// the shipped gateway/internal code, so they are safe to drop — and MUST be,
	// or `go build` fails with "package … not in std". This applies to all Go
	// cells, including gRPC+monolith (which ships the in-process gateway).
	switch rel {
	case "core/shared/grpc_client_sdk/grpc_analysis_client.go",
		"core/shared/grpc_client_sdk/grpc_identity_client.go",
		"core/shared/grpc_client_sdk/grpc_repository_client.go",
		"core/shared/grpc_client_sdk/grpc_billing_client.go",
		"core/shared/grpc_client_sdk/grpc_migration_client.go":
		return false
	}

	// ── pkg/gateway/common/error — all *_errors.go, not message_error.go ────
	// All *_errors.go files are pure map[string]string with no imports — safe
	// to include. message_error.go is generated by __pipeline__ and arrives
	// via artifacts (it references variables from all *_errors.go files).
	if strings.HasPrefix(rel, "pkg/gateway/common/error/") {
		return strings.HasSuffix(rel, "_errors.go")
	}

	// ── Recursive directories — all .go files ──────────────────────────────
	// pkg/gateway/ is included here (minus the error/ sub-dir handled above).
	for _, dir := range []string{
		"core/internal/",
		"core/shared/",
		"pkg/config/",
		"pkg/log/",
		"pkg/pb/impl/",
		"pkg/utils/",
		"pkg/gateway/",
	} {
		if strings.HasPrefix(rel, dir) && strings.HasSuffix(rel, ".go") {
			return true
		}
	}

	return false
}

// ── Python profile skeleton filters ─────────────────────────────────────────

// skipDirPython returns true for directories that should be skipped entirely
// when assembling a Python deliverable. It prunes the whole Go monorepo
// (core/, pkg/, api-gateway/), all proto source trees, the generated python
// subtrees (python/services, python/gen — these arrive via artifacts), and
// every Python cache/junk dir.
func skipDirPython(rel string) bool {
	skip := []string{
		// Repo-wide noise.
		".git", "infra", "docs", "bin", "node_modules", "milton-prism-panel",
		// Entire Go monorepo — never in a Python deliverable.
		"core", "pkg", "api-gateway",
		// protobuf source trees for platform services; the neutral buf configs
		// at protobuf/ root are included as exact files in isSkeletonFilePython.
		"protobuf/proto",
		// Generated Python artifacts arrive via the artifacts list, not skeleton.
		"python/services", "python/gen",
		// Python cache / junk dirs anywhere under python/.
		"python/.ruff_cache", "python/.pytest_cache",
		"python/.import_linter_cache", "python/.mypy_cache",
		"python/.coverage", "python/.venv", "python/__pycache__",
	}
	for _, s := range skip {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	// Prune any __pycache__ / cache dir at any depth under python/shared etc.
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	switch base {
	case "__pycache__", ".ruff_cache", ".pytest_cache",
		".import_linter_cache", ".mypy_cache", ".venv":
		return true
	}
	return false
}

// isSkeletonFilePython returns true when the file at rel belongs in the Python
// deliverable skeleton. It admits ONLY the shared Python scaffolding under
// python/ plus the neutral buf configs. No Go file can pass this filter: every
// admitted path is rooted at python/ or is an explicit non-Go buf config.
func isSkeletonFilePython(rel string) bool {
	// Hard exclude: never emit Go or Go-tree files in a Python deliverable.
	if strings.HasSuffix(rel, ".go") {
		return false
	}
	switch rel {
	case "go.mod", "go.sum", "Makefile":
		return false
	}

	// Never re-emit cache/junk files (defensive; dirs are pruned in skipDir).
	if strings.HasSuffix(rel, ".pyc") {
		return false
	}

	// ── User-facing buf module config ────────────────────────────────────────
	// Only buf.yaml (the proto module: lint/breaking/deps) ships, so a Python user
	// can regenerate their stubs against the shipped protos with their own gen
	// template. The Go gen config (buf.go.gen.yaml) and the two platform-INTERNAL
	// templates (buf.docs.gen.yaml → panel symlink, buf.deliverable.openapi.yaml →
	// platform pipeline) are all excluded — none belong in a Python project.
	switch rel {
	case "protobuf/buf.yaml":
		return true
	}

	// ── Top-level shared Python scaffolding ─────────────────────────────────
	switch rel {
	case "python/__init__.py",
		"python/conftest.py",
		"python/pyproject.toml",
		"python/poetry.lock",
		"python/.importlinter":
		return true
	}

	// ── python/shared/**/*.py and python/scripts/*.py ───────────────────────
	if strings.HasPrefix(rel, "python/shared/") && strings.HasSuffix(rel, ".py") {
		return true
	}
	if strings.HasPrefix(rel, "python/scripts/") && strings.HasSuffix(rel, ".py") {
		return true
	}

	return false
}

// ── Node (TypeScript) profile skeleton filters ───────────────────────────────

// skipDirNode returns true for directories that should be skipped entirely when
// assembling a Node deliverable. The monorepo has NO node/ skeleton tree: a Node
// deliverable is built entirely from generated artifacts (the agent writes a
// complete TS workspace under node/) plus the neutral buf configs at protobuf/
// root. So this prunes the whole Go monorepo (core/, pkg/, api-gateway/), the
// whole Python tree (python/), all proto source trees, and repo-wide noise.
func skipDirNode(rel string) bool {
	skip := []string{
		// Repo-wide noise.
		".git", "infra", "docs", "bin", "node_modules", "milton-prism-panel",
		// Entire Go monorepo — never in a Node deliverable.
		"core", "pkg", "api-gateway",
		// Entire Python tree — never in a Node deliverable.
		"python",
		// protobuf source trees for platform services; the neutral buf configs
		// at protobuf/ root are included as exact files in isSkeletonFileNode.
		"protobuf/proto",
	}
	for _, s := range skip {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	// Prune any node_modules / cache dir at any depth (defensive).
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	switch base {
	case "node_modules", "dist", ".turbo", "coverage":
		return true
	}
	return false
}

// isSkeletonFileNode returns true when the file at rel belongs in the Node
// deliverable skeleton. It admits ONLY the neutral buf configs at protobuf/
// root — there is no node/ source skeleton in the monorepo. No Go or Python
// file can pass this filter: every admitted path is an explicit non-code buf
// config. All TypeScript source, package.json, tsconfig, and protos arrive via
// the generated artifacts list, never from the repo skeleton.
func isSkeletonFileNode(rel string) bool {
	// Hard exclude: never emit Go or Python files in a Node deliverable.
	if strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, ".py") ||
		strings.HasSuffix(rel, ".pyc") {
		return false
	}
	switch rel {
	case "go.mod", "go.sum", "Makefile":
		return false
	}

	// ── User-facing buf module config ────────────────────────────────────────
	// Only buf.yaml (the proto module: lint/breaking/deps) ships, so a Node user
	// can regenerate their stubs against the shipped protos with their own gen
	// template. The Go gen config (buf.go.gen.yaml) and the two platform-INTERNAL
	// templates (buf.docs.gen.yaml → panel symlink, buf.deliverable.openapi.yaml →
	// platform pipeline) are all excluded — none belong in a Node project.
	switch rel {
	case "protobuf/buf.yaml":
		return true
	}

	return false
}

// isCargoBuildArtifact reports whether p is cargo build output (a target/ tree
// entry), the cargo home / crate registry (.cargo/, registry/, .rustup/), a
// compiled rust artifact (.rlib/.rmeta), or the Cargo.lock lockfile — none of
// which belong in a Rust deliverable. The "target/" check matches a target
// segment at any depth (rust/target/…, rust/services/user/target/…), and the
// lockfile is matched by base name.
//
// DEFECT 4 defence-in-depth: mig38 persisted 8552 .cargo/registry artifacts
// (cargo's CARGO_HOME resolved inside the agent workspace, so `cargo build`
// downloaded every crate's source under .cargo/registry/src/…). The collector
// fix drops these at capture time going forward; this guard ALSO drops any
// already-persisted ones at assembly, so the deliverable ZIP for mig38 (and any
// pre-fix Rust migration) ships only real generated source.
func isCargoBuildArtifact(p string) bool {
	p = strings.TrimPrefix(p, "rust/")
	if p == "Cargo.lock" || strings.HasSuffix(p, "/Cargo.lock") {
		return true
	}
	if p == "target" || strings.HasPrefix(p, "target/") || strings.Contains(p, "/target/") {
		return true
	}
	if strings.HasSuffix(p, ".rlib") || strings.HasSuffix(p, ".rmeta") ||
		strings.HasSuffix(p, ".rs.bk") {
		return true
	}
	// Cargo home / rustup home at any depth: match by path SEGMENT. The whole
	// crate registry lives UNDER .cargo (.cargo/registry/{index,src,cache}/…), so
	// the .cargo segment alone covers it. A bare "registry" segment is NOT matched
	// here on purpose: a legitimate generated service could be named "registry"
	// (rust/services/registry/…), and dropping it would corrupt the deliverable.
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case ".cargo", ".rustup", ".fingerprint",
			".package-cache", ".package-cache-mutate", "CACHEDIR.TAG":
			return true
		}
	}
	return false
}

// ── Rust (Tonic) profile skeleton filters ────────────────────────────────────

// skipDirRust returns true for directories that should be skipped entirely when
// assembling a Rust deliverable. The monorepo has NO rust/ skeleton tree: a Rust
// deliverable is built entirely from generated artifacts (the agent writes a
// complete Cargo workspace under rust/) plus the neutral buf configs at
// protobuf/ root. So this prunes the whole Go monorepo (core/, pkg/,
// api-gateway/), the whole Python tree (python/), all proto source trees, and
// repo-wide noise.
func skipDirRust(rel string) bool {
	skip := []string{
		// Repo-wide noise.
		".git", "infra", "docs", "bin", "node_modules", "milton-prism-panel",
		// Entire Go monorepo — never in a Rust deliverable.
		"core", "pkg", "api-gateway",
		// Entire Python tree — never in a Rust deliverable.
		"python",
		// protobuf source trees for platform services; the neutral buf configs
		// at protobuf/ root are included as exact files in isSkeletonFileRust.
		"protobuf/proto",
	}
	for _, s := range skip {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	// Prune any cargo build output / cache dir at any depth (defensive).
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	switch base {
	case "target", "node_modules":
		return true
	}
	return false
}

// isSkeletonFileRust returns true when the file at rel belongs in the Rust
// deliverable skeleton. It admits ONLY the neutral buf configs at protobuf/
// root — there is no rust/ source skeleton in the monorepo. No Go, Python or
// Node file can pass this filter: every admitted path is an explicit non-code
// buf config. All Rust source, Cargo.toml, build.rs, and protos arrive via the
// generated artifacts list, never from the repo skeleton.
func isSkeletonFileRust(rel string) bool {
	// Hard exclude: never emit Go, Python or Node files in a Rust deliverable.
	if strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, ".py") ||
		strings.HasSuffix(rel, ".pyc") || strings.HasSuffix(rel, ".ts") {
		return false
	}
	switch rel {
	case "go.mod", "go.sum", "Makefile":
		return false
	}

	// ── User-facing buf module config ────────────────────────────────────────
	// Only buf.yaml (the proto module: lint/breaking/deps) ships, so a Rust user
	// can regenerate their stubs against the shipped protos with their own gen
	// template. The Go gen config (buf.go.gen.yaml) and the two platform-INTERNAL
	// templates (buf.docs.gen.yaml → panel symlink, buf.deliverable.openapi.yaml →
	// platform pipeline) are all excluded — none belong in a Rust project.
	switch rel {
	case "protobuf/buf.yaml":
		return true
	}

	return false
}

// relocateRustVendoredProtos enforces the invariant "no `.proto` under the Rust
// service source tree" on the merged file map (keys still carry the rust/ source
// root prefix — the rust/→core/ rename runs later in step 3b). The agent image's
// protoc carries no bundled includes, so the agent vendors the well-known-type
// and google.api protos into rust/services/<svc>/proto_include/<import-path> and
// adds proto_include as a second tonic-build include. This relocates every such
// vendored proto to the canonical top-level protobuf/proto/<import-path> (the
// suffix after proto_include/ IS the protoc import string, e.g.
// google/protobuf/timestamp.proto → protobuf/proto/google/protobuf/timestamp.proto),
// dedups across services, drops the per-service proto_include copies, and rewrites
// every rust/services/<svc>/build.rs to remove the proto_include include path so
// tonic-build resolves the google deps via the protobuf/proto include root it
// already passes. The result: 0 `.proto` under core/services/ and a build.rs that
// still compiles.
func relocateRustVendoredProtos(merged map[string][]byte) {
	const marker = "/proto_include/"
	for p, content := range merged {
		// Only act on generated Rust service trees (rust/services/<svc>/…).
		if !strings.HasPrefix(p, "rust/services/") {
			continue
		}
		if !strings.HasSuffix(p, ".proto") {
			continue
		}
		idx := strings.Index(p, marker)
		if idx < 0 {
			continue
		}
		// importPath is the protoc import string (e.g. google/api/http.proto).
		importPath := p[idx+len(marker):]
		canonical := "protobuf/proto/" + importPath
		// Move to canonical location (first writer wins; the vendored copies are
		// byte-identical google sources, so dedup is safe).
		if _, exists := merged[canonical]; !exists {
			merged[canonical] = content
		}
		delete(merged, p)
	}

	// Rewrite each rust build.rs to drop the proto_include include path now that
	// the google deps resolve via protobuf/proto.
	for p, content := range merged {
		if !strings.HasPrefix(p, "rust/services/") || !strings.HasSuffix(p, "/build.rs") {
			continue
		}
		if rewritten, changed := stripProtoIncludeFromBuildRs(string(content)); changed {
			merged[p] = []byte(rewritten)
		}
	}
}

// stripProtoIncludeFromBuildRs removes any reference to a per-service
// `proto_include` include directory from a Rust build.rs body, so tonic-build
// resolves proto imports solely through the canonical `protobuf/proto` include
// root. It handles the two shapes the agent emits: a separate `let … =
// "proto_include";` binding fed into the include slice, and an inline
// "proto_include" literal inside the &[…] include slice passed to
// compile_protos. Returns the rewritten body and whether any change was made.
func stripProtoIncludeFromBuildRs(body string) (string, bool) {
	if !strings.Contains(body, "proto_include") {
		return body, false
	}
	changed := false
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		// Drop a `let <name> = "proto_include";` (or "./proto_include") binding line.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "let ") &&
			strings.Contains(trimmed, "=") &&
			(strings.Contains(trimmed, `"proto_include"`) || strings.Contains(trimmed, `"./proto_include"`)) {
			changed = true
			continue
		}
		keep = append(keep, line)
	}
	body = strings.Join(keep, "\n")

	// Remove any leftover inline include-slice entries referencing proto_include
	// (the literal itself, or a binding identifier we just deleted). Strip common
	// `&[proto_root, vendored_includes]` / `&[proto_root, "proto_include"]` shapes
	// down to `&[proto_root]` by deleting the trailing include arg.
	replacements := []string{
		`, vendored_includes`, `,vendored_includes`,
		`, "proto_include"`, `,"proto_include"`,
		`, "./proto_include"`, `,"./proto_include"`,
		`, &vendored_includes`,
	}
	for _, r := range replacements {
		if strings.Contains(body, r) {
			body = strings.ReplaceAll(body, r, "")
			changed = true
		}
	}
	return body, changed
}

// sortFiles sorts a File slice by path for deterministic output.
func sortFiles(files []File) {
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].Path < files[j-1].Path; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
}
