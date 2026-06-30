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
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	applog "milton_prism/pkg/log"
)

// embeddedBufLock is a build-time copy of the canonical protobuf/buf.lock,
// compiled INTO the migration-services binary. It is the perms-independent
// fallback used by shipBufLockAndCleanBufYaml when the on-disk buf.lock cannot
// be read at download time.
//
// Why this exists: PRISM_MONOREPO_PATH is bind-mounted read-only into the
// distroless container (uid 65532), but the on-disk buf.lock can carry a
// restrictive 0600 mode (e.g. right after `buf dep update` regenerates it under
// a tight umask) owned by a different host uid, so os.ReadFile returns EACCES.
// Both disk read paths (walkSkeleton and the shipBufLock re-read) tolerate that
// error and skip the file, which silently dropped buf.lock from EVERY downloaded
// deliverable even though buf.yaml still declared remote deps — leaving the
// module unresolvable offline. Embedding the lock guarantees it always ships.
//
// The embedded copy MUST stay byte-identical to protobuf/buf.lock; the
// TestEmbeddedBufLockMatchesCanonical guard fails if it drifts (e.g. after a buf
// dep bump that updates the real lock but not this copy).
//
//go:embed embedded/buf.lock
var embeddedBufLock []byte

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
// store selects the persistence-config variant of a deliverable. "postgres" or
// "mysql" makes the synthesised per-service config a SQL `.env.example`
// (DATABASE_URL / DB_HOST/PORT/USER/PASSWORD/NAME) instead of the Mongo
// config.toml.example (Go) / Mongo .env.example (Python/Node), matching the SQL
// repos the generator emits: Go via GORM, Python via SQLAlchemy, Node via Prisma
// (one schema/model set serves PostgreSQL and MySQL/MariaDB; only the
// driver/provider + DSN differ). Empty / "mongodb" keeps the Mongo config (the
// established behaviour). The store is consumed by the Go, Python and Node
// config-example steps; the Rust deliverable is MongoDB-only in v1.
func New(skeletonRoot string, useApiGateway bool, profile, protocol, store string) *Assembler {
	return &Assembler{skeletonRoot: skeletonRoot, useApiGateway: useApiGateway, profile: profile, protocol: protocol, store: store}
}

// isGoSQL reports whether this Assembler targets a Go + SQL deliverable (GORM):
// the per-service config example is a SQL .env.example rather than the Mongo
// config.toml.example. Both "postgres" and "mysql" (MySQL/MariaDB) are GORM cells
// in v1 and emit the same DATABASE_URL/DB_* .env. Only the Go profile carries a
// SQL store in v1.
func (a *Assembler) isGoSQL() bool {
	return !a.isPython() && !a.isNode() && !a.isRust() && (a.store == "postgres" || a.store == "mysql")
}

// isGoHTTP reports whether this Assembler targets the Go HTTP-native deliverable
// (Go profile + HTTP transport). The gateway subtree is excluded for this cell.
func (a *Assembler) isGoHTTP() bool {
	return a.protocol == "http" && !a.isPython() && !a.isNode() && !a.isRust()
}

// isPython reports whether this Assembler targets the Python output profile.
func (a *Assembler) isPython() bool { return a.profile == "python" }

// isPythonSQL reports whether this Assembler targets a Python + SQL deliverable
// (SQLAlchemy 2.0 async). Both "postgres" and "mysql" (MySQL/MariaDB) are SQLAlchemy
// cells in v1 and emit the same DATABASE_URL/DB_* .env (0 MONGO_*), matching the
// SQLAlchemy repos the generator wrote. It is the Python homologue of isGoSQL.
func (a *Assembler) isPythonSQL() bool {
	return a.isPython() && (a.store == "postgres" || a.store == "mysql")
}

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

// isNodeSQL reports whether this Assembler targets a Node + SQL deliverable
// (Prisma). Both "postgres" and "mysql" (MySQL/MariaDB) are Prisma cells in v1 and
// emit the same DATABASE_URL/DB_* .env (0 MONGO_*), matching the schema.prisma +
// @prisma/client repos the generator wrote. It is the Node homologue of isGoSQL /
// isPythonSQL. Node+Mongo (the default) keeps the native `mongodb` driver, NOT Prisma.
func (a *Assembler) isNodeSQL() bool {
	return a.isNode() && (a.store == "postgres" || a.store == "mysql")
}

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

// isNodeVendoredProto reports whether path is a `.proto` the Node agent vendored
// into its source tree (under node/, which becomes core/ after the rename). The
// agent copies the well-known-types + (often) the whole Milton Prism platform proto
// set into a per-workspace dir so proto-loader-gen-types can run offline; the dir
// name varies by migration — node/third_party/proto/… (mig65) or node/proto/…
// (mig21). None of these belong in a user deliverable: the only protos that ship are
// the canonical, scoped ones under protobuf/proto/ (the import closure is resolved
// from the skeleton in step 2b). The rule is therefore "any .proto NOT under
// protobuf/proto/ is a vendored copy and must be dropped", which is independent of
// the vendor dir name and of the node/ vs core/ root prefix.
func isNodeVendoredProto(path string) bool {
	return strings.HasSuffix(path, ".proto") &&
		!strings.HasPrefix(path, "protobuf/proto/")
}

// rewriteNodeGenProtoScript rewrites the proto-loader regeneration npm scripts in a
// Node package.json so their include dir and per-service .proto arguments resolve
// against the canonical protobuf/proto tree shipped at the deliverable root
// (../protobuf/proto relative to core/) instead of the dropped vendored proto dir.
// The vendored dir name varies by migration, so this handles the include forms the
// agent emits: `-I third_party/proto` / `-I proto`, `--includeDirs=third_party/proto`
// / `--includeDirs=proto`, and the bare `third_party/proto/…` / `proto/…` .proto file
// arguments. Without this the (optional) regeneration script would point at a
// directory that no longer ships; the committed TS stubs under gen/ are unaffected.
// Returns the rewritten content and whether a change was made.
func rewriteNodeGenProtoScript(content string) (string, bool) {
	out := content
	// Order matters: rewrite the more specific third_party/proto before the bare
	// proto so the replacements don't overlap incorrectly.
	repl := []struct{ from, to string }{
		{"third_party/proto", "../protobuf/proto"},
		{"--includeDirs=proto ", "--includeDirs=../protobuf/proto "},
		{"--includeDirs=proto/", "--includeDirs=../protobuf/proto/"},
		{"-I proto ", "-I ../protobuf/proto "},
		{"-I proto/", "-I ../protobuf/proto/"},
		{" proto/", " ../protobuf/proto/"},
	}
	for _, r := range repl {
		out = strings.ReplaceAll(out, r.from, r.to)
	}
	return out, out != content
}

// isRust reports whether this Assembler targets the Rust profile.
func (a *Assembler) isRust() bool { return a.profile == "rust" }

// isRustSQL reports whether this Assembler targets a Rust + SQL deliverable
// (SeaORM): the store is "postgres" or "mysql", so the .env.example is a
// DATABASE_URL / DB_* file (zero MONGO_*) matching the SeaORM entities/repos the
// generator wrote. It is the Rust homologue of isGoSQL / isPythonSQL / isNodeSQL.
// Rust+Mongo (the default) keeps the native `mongodb` crate, NOT SeaORM.
func (a *Assembler) isRustSQL() bool {
	return a.isRust() && (a.store == "postgres" || a.store == "mysql")
}

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

// isJava reports whether this Assembler targets the Java profile.
func (a *Assembler) isJava() bool { return a.profile == "java" }

// isJavaSQL reports whether this Assembler targets a Java + SQL deliverable
// (Spring Data JPA / Hibernate): the store is "postgres" or "mysql", so the
// .env.example is a DATABASE_URL / SPRING_DATASOURCE_* file (zero MONGO_*) matching
// the JPA entities/repos the generator wrote. It is the Java homologue of isGoSQL /
// isPythonSQL / isNodeSQL / isRustSQL. Java+Mongo (the default) keeps Spring Data
// MongoDB, NOT JPA.
func (a *Assembler) isJavaSQL() bool {
	return a.isJava() && (a.store == "postgres" || a.store == "mysql")
}

// isJavaHTTP reports whether this Assembler targets the Java HTTP-native (Spring
// Boot) deliverable (Java profile + HTTP transport). The grpc-java server bootstrap
// (io.grpc.Server / ServerBuilder / addService over a BindableService) and the
// grpc-java generated service base classes (infrastructure/grpc/) are excluded for
// this cell — the Spring Boot @RestController app is the sole entrypoint and the
// messages are modelled with POJOs/records, so no grpc server bootstrap and no
// generated grpc service base belong in the package. The grpc-api-gateway is already
// excluded for HTTP by the download path (useApiGateway = micro && gRPC).
func (a *Assembler) isJavaHTTP() bool {
	return a.isJava() && a.protocol == "http"
}

// isMavenBuildArtifact reports whether p is Maven build output or a vendored local
// repository — none of which belong in a Java deliverable: the target/ build tree
// (at any depth), compiled .class files, packaged .jar archives, and a vendored
// .m2/repository tree. It is the Java homologue of isCargoBuildArtifact: defence in
// depth so a stray build artifact persisted by the agent never lands in the package.
func isMavenBuildArtifact(p string) bool {
	p = strings.TrimPrefix(p, "java/")
	if p == "target" || strings.HasPrefix(p, "target/") || strings.Contains(p, "/target/") {
		return true
	}
	if strings.HasSuffix(p, ".class") || strings.HasSuffix(p, ".jar") {
		return true
	}
	// Vendored local Maven repository at any depth (.m2/repository/…).
	for _, seg := range strings.Split(p, "/") {
		if seg == ".m2" {
			return true
		}
	}
	if strings.Contains(p, "/repository/") || strings.HasPrefix(p, "repository/") {
		// Only when it is a Maven local-repo tree (under a .m2 parent already handled
		// above) — a bare "repository" segment elsewhere is NOT matched on purpose, so
		// a legitimate generated service named "repository" is never dropped.
		return strings.Contains(p, ".m2/repository/")
	}
	return false
}

// isJavaGRPCArtifact reports whether a generated artifact is grpc-java-specific and
// therefore must NOT ship in a Java HTTP-native (Spring Boot) deliverable. Two cases:
//   - any .java under an `infrastructure/grpc/` path — the grpc-java generated-service
//     base impl (the gRPC handlers), replaced by `infrastructure/http/` (Spring Boot
//     @RestController) for the HTTP cell.
//   - any .java whose body bootstraps a grpc-java server: a `ServerBuilder` /
//     `io.grpc.Server` (the server builder) or an `.addService(` call (servicer
//     registration) — the gRPC server bootstrap/entrypoint, which the Spring Boot
//     app replaces. Identified by content so the Spring Boot entrypoint
//     (`@SpringBootApplication` / `SpringApplication.run`) is kept while a grpc-java
//     bootstrap is dropped.
func isJavaGRPCArtifact(path, content string) bool {
	if !strings.HasSuffix(path, ".java") {
		return false
	}
	if strings.Contains(path, "/infrastructure/grpc/") {
		return true
	}
	if strings.Contains(content, "ServerBuilder") || strings.Contains(content, "io.grpc.Server") ||
		strings.Contains(content, ".addService(") {
		return true
	}
	return false
}

// isRuby reports whether this Assembler targets the Ruby profile.
func (a *Assembler) isRuby() bool { return a.profile == "ruby" }

// isRubySQL reports whether this Assembler targets a Ruby + SQL deliverable
// (ActiveRecord): the store is "postgres" or "mysql", so the .env.example is a
// DATABASE_URL file (zero MONGO_*) matching the ActiveRecord models/repos the
// generator wrote. It is the Ruby homologue of isGoSQL / isJavaSQL. Ruby+Mongo
// (the default) keeps Mongoid, NOT ActiveRecord.
func (a *Assembler) isRubySQL() bool {
	return a.isRuby() && (a.store == "postgres" || a.store == "mysql")
}

// isRubyHTTP reports whether this Assembler targets the Ruby HTTP-native (Rails
// API / Sinatra) deliverable (Ruby profile + HTTP transport). The grpc gem service
// bootstrap (GRPC::RpcServer) and the grpc generated service stubs (*_services_pb.rb)
// are excluded for this cell — the Rails/Sinatra app is the sole entrypoint and the
// messages are modelled with POROs (the *_pb.rb message classes may be kept), so no
// grpc server bootstrap and no generated grpc service stub belong in the package. The
// grpc-api-gateway is already excluded for HTTP by the download path.
func (a *Assembler) isRubyHTTP() bool {
	return a.isRuby() && a.protocol == "http"
}

// isRubyBuildArtifact reports whether p is Ruby/Bundler build output or vendored
// dependencies — none of which belong in a Ruby deliverable: the vendor/bundle/ and
// .bundle/ trees (bundled gems + config), the tmp/ scratch tree, packaged *.gem
// archives, and the coverage/ report tree. It is the Ruby homologue of
// isMavenBuildArtifact / isCargoBuildArtifact: defence in depth so a stray build
// artifact persisted by the agent never lands in the package.
func isRubyBuildArtifact(p string) bool {
	p = strings.TrimPrefix(p, "ruby/")
	if strings.HasSuffix(p, ".gem") {
		return true
	}
	// Directory prefixes (at the root or nested at any depth).
	for _, dir := range []string{"vendor/bundle", ".bundle", "tmp", "coverage"} {
		if p == dir || strings.HasPrefix(p, dir+"/") || strings.Contains(p, "/"+dir+"/") {
			return true
		}
	}
	return false
}

// isRubyGRPCArtifact reports whether a generated artifact is grpc-gem-specific and
// therefore must NOT ship in a Ruby HTTP-native (Rails/Sinatra) deliverable. Two cases:
//   - any `*_services_pb.rb` — the grpc gem generated-service stub (the gRPC service
//     base), replaced by the Rails/Sinatra controllers for the HTTP cell. The
//     `*_pb.rb` MESSAGE classes are NOT matched (they may back the POROs) and ship.
//   - any .rb whose body bootstraps a grpc server: `GRPC::RpcServer` (the server) or a
//     `GRPC::GenericService`-based service registration — the gRPC server
//     bootstrap/entrypoint, which the Rails/Sinatra app replaces.
func isRubyGRPCArtifact(path, content string) bool {
	if !strings.HasSuffix(path, ".rb") {
		return false
	}
	if strings.HasSuffix(path, "_services_pb.rb") {
		return true
	}
	if strings.Contains(content, "GRPC::RpcServer") || strings.Contains(content, "GRPC::GenericService") {
		return true
	}
	return false
}

// isCSharp reports whether this Assembler targets the C# / .NET profile.
func (a *Assembler) isCSharp() bool { return a.profile == "csharp" }

// isCSharpSQL reports whether this Assembler targets a C# + SQL deliverable (EF
// Core): the store is "postgres" or "mysql", so the .env.example is a connection
// string file (zero MONGO_*) matching the EF Core DbContext/entities the generator
// wrote. It is the C# homologue of isGoSQL / isJavaSQL / isRubySQL. C#+Mongo (the
// default) keeps MongoDB.Driver, NOT EF Core.
func (a *Assembler) isCSharpSQL() bool {
	return a.isCSharp() && (a.store == "postgres" || a.store == "mysql")
}

// isCSharpHTTP reports whether this Assembler targets the C# HTTP-native (ASP.NET
// Core Minimal API) deliverable (C# profile + HTTP transport). The grpc-dotnet
// server bootstrap (AddGrpc / MapGrpcService) and the generated gRPC service stub
// (the *.*Base service base from Grpc.Tools with GrpcServices=Server) are excluded
// for this cell — the ASP.NET Core app is the sole entrypoint and the messages are
// modelled with the proto-generated message classes, so no gRPC server bootstrap and
// no generated gRPC service base belong in the package.
func (a *Assembler) isCSharpHTTP() bool {
	return a.isCSharp() && a.protocol == "http"
}

// isDotnetBuildArtifact reports whether p is .NET/MSBuild build output — none of
// which belongs in a C# deliverable: the per-project bin/ and obj/ trees (compiled
// assemblies, intermediate objects, the restore graph) and any stray compiled
// artifact (*.dll, *.pdb, *.nupkg). It is the C# homologue of isMavenBuildArtifact /
// isCargoBuildArtifact / isRubyBuildArtifact: defence in depth so a stray build
// artifact persisted by the agent never lands in the package.
func isDotnetBuildArtifact(p string) bool {
	p = strings.TrimPrefix(p, "csharp/")
	if strings.HasSuffix(p, ".dll") || strings.HasSuffix(p, ".pdb") || strings.HasSuffix(p, ".nupkg") {
		return true
	}
	// Directory prefixes (at the root or nested at any depth).
	for _, dir := range []string{"bin", "obj"} {
		if p == dir || strings.HasPrefix(p, dir+"/") || strings.Contains(p, "/"+dir+"/") {
			return true
		}
	}
	return false
}

// isCSharpGRPCArtifact reports whether a generated artifact is grpc-dotnet-specific
// and therefore must NOT ship in a C# HTTP-native (ASP.NET Core) deliverable. Two
// cases:
//   - any generated gRPC service-base file `*Grpc.cs` — the grpc_csharp_plugin
//     service stub (the gRPC `*.*Base`), replaced by the ASP.NET Core endpoints for
//     the HTTP cell. (The `*.cs` MESSAGE classes are NOT matched and ship.)
//   - any .cs whose body bootstraps a grpc-dotnet server: `AddGrpc(` (the service
//     registration) or `MapGrpcService` (the endpoint mapping) — the gRPC server
//     bootstrap/entrypoint, which the ASP.NET Core app replaces.
func isCSharpGRPCArtifact(path, content string) bool {
	if !strings.HasSuffix(path, ".cs") {
		return false
	}
	if strings.HasSuffix(path, "Grpc.cs") {
		return true
	}
	if strings.Contains(content, "AddGrpc(") || strings.Contains(content, "MapGrpcService") {
		return true
	}
	return false
}

// isCpp reports whether this Assembler targets the C++ profile.
func (a *Assembler) isCpp() bool { return a.profile == "cpp" }

// isCppSQL reports whether this Assembler targets a C++ + SQL deliverable
// (artisanal parametrised SQL via libpqxx / mysql-connector-c++): the store is
// "postgres" or "mysql", so the .env.example is a connection-string file (zero
// MONGO_*) matching the hand-written SQL repos the generator wrote. It is the C++
// homologue of isGoSQL / isCSharpSQL. C++ + Mongo (the default) keeps mongocxx.
func (a *Assembler) isCppSQL() bool {
	return a.isCpp() && (a.store == "postgres" || a.store == "mysql")
}

// isCppHTTP reports whether this Assembler targets the C++ HTTP-native (Drogon)
// deliverable (C++ profile + HTTP transport). The grpc++ server bootstrap
// (grpc::ServerBuilder / grpc::Server) and the generated gRPC service base
// (*.grpc.pb.cc/.grpc.pb.h from grpc_cpp_plugin) are excluded for this cell — the
// Drogon app is the sole entrypoint and the messages are modelled with the
// protoc-generated *.pb.h classes, so no gRPC server bootstrap and no generated
// gRPC service base belong in the package.
func (a *Assembler) isCppHTTP() bool {
	return a.isCpp() && a.protocol == "http"
}

// isCMakeBuildArtifact reports whether p is CMake/compiler build output — none of
// which belongs in a C++ deliverable: the build/ tree, the CMakeFiles/ intermediate
// dir, CMakeCache.txt, and any compiled object/archive/shared-library (*.o, *.a,
// *.so). It is the C++ homologue of isMavenBuildArtifact / isDotnetBuildArtifact:
// defence in depth so a stray build artifact persisted by the agent never lands in
// the package.
func isCMakeBuildArtifact(p string) bool {
	p = strings.TrimPrefix(p, "cpp/")
	if strings.HasSuffix(p, ".o") || strings.HasSuffix(p, ".a") || strings.HasSuffix(p, ".so") ||
		strings.HasSuffix(p, ".obj") {
		return true
	}
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	if base == "CMakeCache.txt" {
		return true
	}
	// Directory prefixes (at the root or nested at any depth).
	for _, dir := range []string{"build", "CMakeFiles"} {
		if p == dir || strings.HasPrefix(p, dir+"/") || strings.Contains(p, "/"+dir+"/") {
			return true
		}
	}
	return false
}

// isCppGRPCArtifact reports whether a generated artifact is grpc++-specific and
// therefore must NOT ship in a C++ HTTP-native (Drogon) deliverable. Two cases:
//   - any generated gRPC service-base file `*.grpc.pb.cc` / `*.grpc.pb.h` — the
//     grpc_cpp_plugin service stub (the `*::Service` base), replaced by the Drogon
//     controllers for the HTTP cell. (The `*.pb.cc`/`*.pb.h` MESSAGE classes are NOT
//     matched and ship.)
//   - any .cc/.cpp/.cxx whose body bootstraps a grpc++ server: `grpc::ServerBuilder`
//     (the server builder) or a `BuildAndStart(` call — the gRPC server
//     bootstrap/entrypoint, which the Drogon app replaces.
func isCppGRPCArtifact(path, content string) bool {
	if strings.HasSuffix(path, ".grpc.pb.cc") || strings.HasSuffix(path, ".grpc.pb.h") {
		return true
	}
	if !strings.HasSuffix(path, ".cc") && !strings.HasSuffix(path, ".cpp") &&
		!strings.HasSuffix(path, ".cxx") {
		return false
	}
	if strings.Contains(content, "grpc::ServerBuilder") || strings.Contains(content, "BuildAndStart(") {
		return true
	}
	return false
}

// isInternalBufTemplate reports whether path is a platform-INTERNAL buf template
// that must never ship in a user deliverable, no matter the profile or source:
//   - protobuf/buf.docs.gen.yaml         — generates the PLATFORM panel openapi via
//     the `../milton-prism-panel` symlink.
//   - protobuf/buf.deliverable.openapi.yaml — the platform pipeline template that
//     emits docs/openapi.yaml during generation.
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
	case "java":
		return "java"
	case "ruby":
		return "ruby"
	case "csharp":
		return "csharp"
	case "cpp":
		return "cpp"
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
		// Profile guard: a Node deliverable must never carry `.proto` under its
		// source tree. The agent vendors the well-known-type + google.api protos AND
		// the entire Milton Prism PLATFORM proto set (analysis/billing/identity/
		// migration/repository/articles + openapiv3) into node/third_party/proto/ so
		// `proto-loader-gen-types` can regenerate the TS stubs offline. That leaks ~27
		// `.proto` — including every platform service — under core/ after the rename,
		// violating the invariant that the only protos a deliverable ships are the
		// canonical, scoped ones under protobuf/proto/. The TS stubs under node/gen/
		// are committed and self-contained (tsconfig even excludes third_party), so
		// dropping the vendored proto tree does not affect tsc. The gen:proto script's
		// include path is rewritten to the canonical protobuf/proto below.
		if a.isNode() && isNodeVendoredProto(f.Path) {
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
		// Profile guard: a Java (Spring Boot) deliverable must never carry Go, Python,
		// Node or Rust artifacts. Same rationale as the others — the Go error
		// aggregator is skipped for the java profile in the pipeline, but defend here
		// too so a stray .go/.py/.ts/.rs (or a Go/Node/Rust manifest) can never leak
		// into a Java package. Maven build output (target/, *.class, *.jar, vendored
		// .m2) is also dropped as defence-in-depth.
		if a.isJava() && (strings.HasSuffix(f.Path, ".go") ||
			strings.HasSuffix(f.Path, ".py") ||
			strings.HasSuffix(f.Path, ".ts") ||
			strings.HasSuffix(f.Path, ".rs") ||
			f.Path == "go.mod" || f.Path == "go.sum" || f.Path == "Makefile" ||
			strings.HasSuffix(f.Path, "/package.json") || f.Path == "package.json" ||
			strings.HasSuffix(f.Path, "/package-lock.json") || f.Path == "package-lock.json" ||
			strings.HasSuffix(f.Path, "/Cargo.toml") || f.Path == "Cargo.toml" ||
			strings.HasSuffix(f.Path, "/Cargo.lock") || f.Path == "Cargo.lock" ||
			isMavenBuildArtifact(f.Path)) {
			continue
		}
		// Profile+protocol guard: a Java HTTP-native (Spring Boot) deliverable is its
		// own entry point and models messages with POJOs/records, so the grpc-java
		// server bootstrap (io.grpc.Server / ServerBuilder / addService) and the
		// generated grpc service base impl (infrastructure/grpc/) must not ship — only
		// the Spring Boot app and its support code. The @SpringBootApplication entry
		// point is kept (it carries no grpc server call); only grpc-bootstrap .java
		// files and the grpc handler dir are dropped.
		if a.isJavaHTTP() && isJavaGRPCArtifact(f.Path, f.Content) {
			continue
		}
		// Profile guard: a Ruby (Rails/Sinatra or grpc-gem) deliverable must never
		// carry Go, Python, Node, Rust or Java artifacts. Same rationale as the others
		// — defend here so a stray .go/.py/.ts/.rs/.java (or a foreign manifest) can
		// never leak into a Ruby package. Ruby/Bundler build output (vendor/bundle/,
		// .bundle/, tmp/, *.gem, coverage/) is also dropped as defence-in-depth.
		if a.isRuby() && (strings.HasSuffix(f.Path, ".go") ||
			strings.HasSuffix(f.Path, ".py") ||
			strings.HasSuffix(f.Path, ".ts") ||
			strings.HasSuffix(f.Path, ".rs") ||
			strings.HasSuffix(f.Path, ".java") ||
			f.Path == "go.mod" || f.Path == "go.sum" || f.Path == "Makefile" ||
			strings.HasSuffix(f.Path, "/package.json") || f.Path == "package.json" ||
			strings.HasSuffix(f.Path, "/package-lock.json") || f.Path == "package-lock.json" ||
			strings.HasSuffix(f.Path, "/Cargo.toml") || f.Path == "Cargo.toml" ||
			strings.HasSuffix(f.Path, "/Cargo.lock") || f.Path == "Cargo.lock" ||
			strings.HasSuffix(f.Path, "/pom.xml") || f.Path == "pom.xml" ||
			isRubyBuildArtifact(f.Path)) {
			continue
		}
		// Profile+protocol guard: a Ruby HTTP-native (Rails/Sinatra) deliverable is its
		// own entry point and models messages with POROs, so the grpc gem server
		// bootstrap (GRPC::RpcServer / GRPC::GenericService) and the generated grpc
		// service stubs (*_services_pb.rb) must not ship — only the Rails/Sinatra app
		// and its support code. The *_pb.rb message classes are kept; only grpc-bootstrap
		// .rb files and *_services_pb.rb stubs are dropped.
		if a.isRubyHTTP() && isRubyGRPCArtifact(f.Path, f.Content) {
			continue
		}
		// Profile guard: a C# (.NET) deliverable must never carry Go, Python, Node,
		// Rust, Java or Ruby artifacts. Same rationale as the others — defend here so a
		// stray .go/.py/.ts/.rs/.java/.rb (or a foreign manifest) can never leak into a
		// C# package. .NET/MSBuild build output (bin/, obj/, *.dll, *.pdb, *.nupkg) is
		// also dropped as defence-in-depth.
		if a.isCSharp() && (strings.HasSuffix(f.Path, ".go") ||
			strings.HasSuffix(f.Path, ".py") ||
			strings.HasSuffix(f.Path, ".ts") ||
			strings.HasSuffix(f.Path, ".rs") ||
			strings.HasSuffix(f.Path, ".java") ||
			strings.HasSuffix(f.Path, ".rb") ||
			f.Path == "go.mod" || f.Path == "go.sum" || f.Path == "Makefile" ||
			strings.HasSuffix(f.Path, "/package.json") || f.Path == "package.json" ||
			strings.HasSuffix(f.Path, "/package-lock.json") || f.Path == "package-lock.json" ||
			strings.HasSuffix(f.Path, "/Cargo.toml") || f.Path == "Cargo.toml" ||
			strings.HasSuffix(f.Path, "/Cargo.lock") || f.Path == "Cargo.lock" ||
			strings.HasSuffix(f.Path, "/pom.xml") || f.Path == "pom.xml" ||
			strings.HasSuffix(f.Path, "/Gemfile") || f.Path == "Gemfile" ||
			strings.HasSuffix(f.Path, "/Gemfile.lock") || f.Path == "Gemfile.lock" ||
			isDotnetBuildArtifact(f.Path)) {
			continue
		}
		// Profile+protocol guard: a C# HTTP-native (ASP.NET Core) deliverable is its
		// own entry point and models messages with the proto-generated classes, so the
		// grpc-dotnet server bootstrap (AddGrpc / MapGrpcService) and the generated gRPC
		// service base (*Grpc.cs) must not ship — only the ASP.NET Core app and its
		// support code. The message *.cs classes are kept; only grpc-bootstrap .cs files
		// and *Grpc.cs service stubs are dropped.
		if a.isCSharpHTTP() && isCSharpGRPCArtifact(f.Path, f.Content) {
			continue
		}
		// Profile guard: a C++ deliverable must never carry Go, Python, Node, Rust,
		// Java, Ruby or C# artifacts. Same rationale as the others — defend here so a
		// stray .go/.py/.ts/.rs/.java/.rb/.cs (or a foreign manifest) can never leak
		// into a C++ package. CMake/compiler build output (build/, CMakeFiles/,
		// CMakeCache.txt, *.o/.a/.so) is also dropped as defence-in-depth. NOTE: the
		// generated protobuf C++ stubs are *.pb.cc/.pb.h/.grpc.pb.cc/.grpc.pb.h —
		// those carry a .cc/.h suffix, not the foreign suffixes matched here, so they
		// ship (they are the deliverable's own code, regenerated by protoc at build).
		if a.isCpp() && (strings.HasSuffix(f.Path, ".go") ||
			strings.HasSuffix(f.Path, ".py") ||
			strings.HasSuffix(f.Path, ".ts") ||
			strings.HasSuffix(f.Path, ".rs") ||
			strings.HasSuffix(f.Path, ".java") ||
			strings.HasSuffix(f.Path, ".rb") ||
			strings.HasSuffix(f.Path, ".cs") ||
			f.Path == "go.mod" || f.Path == "go.sum" || f.Path == "Makefile" ||
			strings.HasSuffix(f.Path, "/package.json") || f.Path == "package.json" ||
			strings.HasSuffix(f.Path, "/package-lock.json") || f.Path == "package-lock.json" ||
			strings.HasSuffix(f.Path, "/Cargo.toml") || f.Path == "Cargo.toml" ||
			strings.HasSuffix(f.Path, "/Cargo.lock") || f.Path == "Cargo.lock" ||
			strings.HasSuffix(f.Path, "/pom.xml") || f.Path == "pom.xml" ||
			strings.HasSuffix(f.Path, "/Gemfile") || f.Path == "Gemfile" ||
			strings.HasSuffix(f.Path, "/Gemfile.lock") || f.Path == "Gemfile.lock" ||
			isCMakeBuildArtifact(f.Path)) {
			continue
		}
		// Profile+protocol guard: a C++ HTTP-native (Drogon) deliverable is its own
		// entry point and models messages with the protoc-generated *.pb.h classes, so
		// the grpc++ server bootstrap (grpc::ServerBuilder / grpc::Server) and the
		// generated gRPC service base (*.grpc.pb.cc/.grpc.pb.h) must not ship — only
		// the Drogon app and its support code. The message *.pb.cc/.pb.h classes are
		// kept; only grpc-bootstrap .cc/.cpp files and *.grpc.pb.* service stubs are dropped.
		if a.isCppHTTP() && isCppGRPCArtifact(f.Path, f.Content) {
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

	// 2a-java. Java guardrail: a generated Java gRPC service must never ship `.proto`
	// files under its source tree (which becomes core/services/<svc>/ after the
	// rename in step 3b). The agent image's protoc carries no bundled google.api /
	// well-known-type includes, so the agent vendors those third-party protos into
	// java/services/<svc>/src/main/proto-include/<import-path> and wires that dir as
	// the protobuf-maven-plugin <additionalProtoPathElement>. That places `.proto`
	// under core/ after the rename — violating the invariant that EVERY `.proto`
	// lives ONLY under the canonical top-level protobuf/proto/ tree and that
	// `core/services/` is source-only. Relocate every such vendored proto to
	// protobuf/proto/<import-path> (the suffix after proto-include/ IS the protoc
	// import string), dedup across services, drop the per-service copies, and rewrite
	// each pom.xml to point its protoSourceRoot / additionalProtoPathElement at the
	// canonical protobuf/proto tree (the same ../../../protobuf/proto the service
	// protos already resolve through) so `mvn package` codegen still resolves. The
	// result: 0 `.proto` under core/ and a pom that still builds.
	if a.isJava() {
		relocateJavaVendoredProtos(merged)
	}

	// 2a-go/py. Go and Python guardrail: TASK A requires ZERO `.proto` under core/
	// for EVERY profile. Go ships generated code under core/services/<svc>/ directly
	// (no rename) and Python under python/services/<svc>/ (renamed to core/ in 3b);
	// the canonical home for every proto is protobuf/proto/. The Node/Rust/Java paths
	// already enforce "no .proto outside protobuf/proto/" (Node drops vendored protos
	// at merge time; Rust/Java relocate them above). Apply the same invariant to Go
	// and Python: relocate any `.proto` that is NOT already under protobuf/proto/ —
	// i.e. one the agent placed inside the service source tree (e.g.
	// core/services/<svc>/proto/<svc>.proto or python/services/<svc>/proto/…) — to the
	// canonical protobuf/proto/<import-path> tree, deduping against what already
	// ships, so no proto ever lands under core/. The path after a `…/proto/` segment
	// is the protoc import string; absent that, the basename is used as a safe
	// fallback. Go/Python codegen (buf generate / scripts/gen_proto.py) already
	// resolves protos from protobuf/proto/, so the relocation keeps codegen working.
	if !a.isPython() && !a.isNode() && !a.isRust() && !a.isJava() && !a.isRuby() && !a.isCSharp() && !a.isCpp() {
		relocateStraySourceProtos(merged, "core/services/")
	}
	if a.isPython() {
		relocateStraySourceProtos(merged, "python/")
	}
	if a.isRuby() {
		relocateStraySourceProtos(merged, "ruby/")
	}
	if a.isCSharp() {
		relocateStraySourceProtos(merged, "csharp/")
	}
	if a.isCpp() {
		relocateStraySourceProtos(merged, "cpp/")
	}

	// 2b. Proto import-closure resolution (ALL profiles). Every deliverable ships
	// the generated service's own protos under protobuf/proto/, but the agent does
	// NOT ship the transitive platform deps those protos import — e.g.
	// milton_prism/types/pagination, .../query_params and openapiv3/annotations
	// (+ their own transitive deps openapiv3/OpenAPIv3, google/protobuf/descriptor).
	// Without those imported .proto SOURCES the shipped proto tree is not
	// self-consistent: `buf generate` (Go), `scripts/gen_proto.py` (Python),
	// tonic-build (Rust), proto-loader (Node) and protoc (Java) all fail with
	// "Import … not found". Walk the import graph of every proto now under
	// protobuf/proto/ and pull any missing imported .proto from the canonical
	// skeleton tree so the deliverable's proto set is self-contained and
	// REGENERABLE. This was previously applied only to Rust/Node/Java; the audit
	// found Go and Python ship the service proto + buf config but NOT the imported
	// pagination/query_params/openapiv3 sources (only their generated stubs), so the
	// shipped tree cannot be regenerated. Applying it to every profile fixes that.
	// google/* imports resolve via the buf.build deps (buf.lock, shipped below), not
	// from disk, so the resolver correctly leaves them missing.
	resolveProtoImportClosure(merged, a.skeletonRoot)

	// 2c. Drop over-vendored protos: any .proto under protobuf/proto/ that is NOT
	// in the transitive import closure of a generated service proto. The audit found
	// Rust deliverables vendoring 14/26 protos that no service ever imports (e.g.
	// cpp_features.proto, go_features.proto, java_features.proto, and many
	// google/protobuf/* like api/type/struct/empty/wrappers/field_mask/duration/
	// source_context, plus google/api/{client,httpbody,launch_stage}.proto). The
	// agent over-vendors when it copies a whole well-known-type bundle instead of
	// only the imported subset. Pruning to the exact closure means every deliverable
	// ships precisely the protos its services import — no missing imports (2b) and no
	// dead vendored protos (2c).
	pruneOverVendoredProtos(merged)

	// 2d. Ship buf.lock alongside buf.yaml so the deliverable's buf module can
	// resolve its remote deps (buf.build/googleapis/googleapis,
	// buf.build/bufbuild/protovalidate) — the google/* imports (annotations,
	// field_behavior, descriptor, any, …) come from those deps, not from disk.
	// Without buf.lock `buf generate`/`buf build` fails to resolve them. The skeleton
	// filters never admit buf.lock (it is not a *.go / source file), so it is pulled
	// here directly from the canonical protobuf/ root for every profile that ships a
	// buf.yaml. Also strip the dangling lint-ignore entries from the shipped buf.yaml
	// (proto/openapi-spec.proto and, when the openapiv3 dir is absent from the
	// deliverable, proto/openapiv3) so `buf lint` does not error on missing paths.
	shipBufLockAndCleanBufYaml(merged, a.skeletonRoot)

	// 2e. Go profiles: prune platform/agent-only requires from go.mod. The
	// monorepo go.mod declares heavy deps that ONLY the analysis/decomposition/
	// generation workers and the platform service repositories import
	// (github.com/docker/docker, github.com/go-git/go-git/v5,
	// github.com/go-enry/go-enry/v2, github.com/smacker/go-tree-sitter,
	// github.com/hibiken/asynq) — none of those trees ship in a deliverable
	// (skipDir prunes core/cmd + core/services; core/worker is never admitted), so
	// the deliverable imports none of these. Left in, they drag a large indirect
	// tree (go-git's openpgp/ssh stack, docker's moby/containerd stack, asynq's
	// go-redis/cron) that the deliverable never compiles. Drop the 5 direct
	// requires plus the indirect deps that are reachable ONLY through them, so the
	// assembled go.mod is `go mod tidy`-clean against the shipped + generated Go
	// code. Only Go profiles carry a go.mod (the others exclude it).
	if !a.isPython() && !a.isNode() && !a.isRust() && !a.isJava() && !a.isRuby() && !a.isCSharp() && !a.isCpp() {
		tidyGoMod(merged)
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
	if !a.isPython() && !a.isNode() && !a.isRust() && !a.isJava() && !a.isRuby() && !a.isCSharp() && !a.isCpp() {
		// Persistence-config variant: Go + SQL (PostgreSQL or MySQL/MariaDB) emits
		// a per-service SQL .env.example (DATABASE_URL / DB_*) matching the GORM
		// repos the generator wrote; Go + MongoDB (default) keeps the Mongo
		// config.toml.example. The auth section is identical (EdDSA tokens) in
		// both — only the data-store config differs.
		if a.isGoSQL() {
			if err := generateSQLConfigExamples(merged, a.store); err != nil {
				return nil, fmt.Errorf("assembler: sql config examples: %w", err)
			}
			// Drop the mongo-driver require (+ its mongo-only indirect deps) from
			// go.mod: a Go+SQL (GORM) deliverable prunes builder_mongo.go and the
			// shared mongo_client package (see isSkeletonFile), so no shipped Go code
			// imports the mongo driver. The require is dead weight; removing it keeps
			// the assembled go.mod `go mod tidy`-clean and the deliverable mongo-free.
			pruneGoModMongoDriver(merged)
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
		// Persistence-config variant: Python + SQL (PostgreSQL or MySQL/MariaDB, both
		// via SQLAlchemy 2.0 async) emits a per-service SQL .env.example (DATABASE_URL
		// / DB_*, zero MONGO_*) matching the SQLAlchemy repos the generator wrote;
		// Python + MongoDB (default) keeps the Motor .env.example.
		if a.isPythonSQL() {
			if err := generatePythonSQLConfigExamples(merged, a.store); err != nil {
				return nil, fmt.Errorf("assembler: python sql config examples: %w", err)
			}
			// Drop the now-unused Motor/Mongo dependency from pyproject.toml: a Python
			// + SQL (SQLAlchemy) deliverable has no shared/mongo_client (excluded
			// above) and persists via SQLAlchemy, so the motor require is a dep for a
			// store the package never uses.
			prunePyprojectMotorDep(merged)
			// Drop the MongoConfig from shared/config/loader.py: a SQL deliverable has
			// no shared/mongo_client and persists via SQLAlchemy, so the MongoConfig
			// settings class + the BaseServiceConfig.mongo field model env (MONGO_URI/
			// MONGO_DATABASE) for a store the package never uses. Also prune the
			// MongoConfig assertion from the shared config test.
			dropMongoConfigForSQL(merged)
			// Prune the lockfile + mypy config of the Mongo stack the SQL deliverable
			// never installs. The agent ships poetry.lock with the platform's full
			// resolution, which still pins motor/pymongo (+ pymongo's dnspython) even
			// though the SQL pyproject no longer declares motor — a desynced lock that
			// re-installs the Mongo driver for a store the package never uses. And the
			// platform mypy overrides relax motor.*/mongomock_motor.*/shared.mongo_client.*
			// modules that a SQL deliverable does not ship (shared/mongo_client/ is
			// excluded above). Both are the Python homologue of pruneGoModMongoDriver:
			// drop ONLY the demonstrably-unused Mongo entries (guarded by a source-import
			// check, so nothing the deliverable imports is ever removed).
			prunePoetryLockMongo(merged)
			prunePythonMongoMypyOverrides(merged)
		} else if err := generatePythonConfigExamples(merged); err != nil {
			return nil, fmt.Errorf("assembler: python config examples: %w", err)
		}
		// Prune platform leakage from the shipped Python config:
		//   - pyproject.toml mypy `overrides` reference platform services
		//     (services.{identity,repository,migration}.*) and shared.mongo_client.*
		//     that DO NOT exist in a single-service deliverable. Rewrite the override
		//     blocks to the actually-generated service(s), dropping the platform names.
		//   - .importlinter contracts are entirely platform-service
		//     (identity/repository/migration); regenerate them for the generated
		//     service(s) so the architectural enforcement matches the deliverable.
		//   - fastapi + uvicorn are only used by a FastAPI (HTTP) deliverable; for a
		//     gRPC deliverable they are unused declared deps — drop them.
		prunePythonMypyOverrides(merged, discoverGeneratedPythonServices(merged))
		rewritePythonImportLinter(merged, discoverGeneratedPythonServices(merged))
		if !a.isPythonHTTP() {
			dropPyprojectDeps(merged, []string{"fastapi", "uvicorn"})
		}
	}

	// 3a-node. Node profile: append a per-service .env.example (the TypeScript
	// homologue of the Go config.toml.example / Python .env.example) BEFORE the
	// node/ → core/ rename, so service dirs are still keyed under
	// node/services/<svc>/. The emitted .env.example paths are rewritten to
	// core/services/<svc>/.env.example by the rename step below.
	if a.isNode() {
		// Persistence-config variant: Node + SQL (PostgreSQL or MySQL/MariaDB, both
		// via Prisma) emits a per-service SQL .env.example (DATABASE_URL / DB_*, zero
		// MONGO_*) matching the schema.prisma + @prisma/client repos the generator
		// wrote; Node + MongoDB (default) keeps the native-`mongodb`-driver .env.example.
		if a.isNodeSQL() {
			if err := generateNodeSQLConfigExamples(merged, a.store); err != nil {
				return nil, fmt.Errorf("assembler: node sql config examples: %w", err)
			}
			// Drop the native Mongo drivers (mongodb/mongoose + their @types) from every
			// package.json of a Node + SQL (Prisma) deliverable. The agent persists via
			// @prisma/client, so a leftover mongodb/mongoose dependency declares a store
			// the package never imports. Node homologue of pruneGoModMongoDriver: each
			// driver is removed ONLY when no shipped .ts/.js import/require references it
			// (so an actually-used driver is never dropped) — a no-op on the common case
			// where the agent already emitted a Mongo-free SQL package.json.
			pruneNodeMongoDeps(merged)
		} else if err := generateNodeConfigExamples(merged); err != nil {
			return nil, fmt.Errorf("assembler: node config examples: %w", err)
		}
		// The vendored node/third_party/proto tree was dropped at merge time; repoint
		// the package.json gen:proto regeneration script at the canonical
		// protobuf/proto so it still resolves after the node/→core/ rename.
		for p, c := range merged {
			if p == "node/package.json" || strings.HasSuffix(p, "/package.json") {
				if rewritten, changed := rewriteNodeGenProtoScript(string(c)); changed {
					merged[p] = []byte(rewritten)
				}
			}
		}
	}

	// 3a-rust. Rust profile: append a per-service .env.example (the Tonic homologue
	// of the Go config.toml.example / Python / Node .env.example) BEFORE the
	// rust/ → core/ rename, so service dirs are still keyed under
	// rust/services/<svc>/. The emitted .env.example paths are rewritten to
	// core/services/<svc>/.env.example by the rename step below.
	if a.isRust() {
		// Persistence-config variant: Rust + SQL (PostgreSQL or MySQL/MariaDB, both
		// via SeaORM) emits a per-service SQL .env.example (DATABASE_URL / DB_*, zero
		// MONGO_*) matching the SeaORM entities/repos the generator wrote; Rust +
		// MongoDB (default) keeps the native-`mongodb`-crate .env.example.
		if a.isRustSQL() {
			if err := generateRustSQLConfigExamples(merged, a.store); err != nil {
				return nil, fmt.Errorf("assembler: rust sql config examples: %w", err)
			}
			// Drop the `mongodb` crate from every Cargo.toml (workspace declaration +
			// each member's `mongodb.workspace = true`) of a Rust + SQL (SeaORM)
			// deliverable. The agent persists via sea-orm, so a leftover mongodb crate is
			// a store-mismatched dependency the workspace never compiles. Rust homologue
			// of pruneGoModMongoDriver: removed ONLY when no shipped .rs uses the crate
			// (`use mongodb` / `mongodb::` / `extern crate mongodb`) — a no-op on the
			// common case where the agent already emitted a Mongo-free SQL Cargo.toml.
			pruneCargoMongoDeps(merged)
		} else if err := generateRustConfigExamples(merged); err != nil {
			return nil, fmt.Errorf("assembler: rust config examples: %w", err)
		}
	}

	// 3a-java. Java profile: append a per-service .env.example (the Spring Boot
	// homologue of the Go config.toml.example / Python / Node / Rust .env.example)
	// BEFORE the java/ → core/ rename, so service dirs are still keyed under
	// java/services/<svc>/. The emitted .env.example paths are rewritten to
	// core/services/<svc>/.env.example by the rename step below.
	if a.isJava() {
		// Persistence-config variant: Java + SQL (PostgreSQL or MySQL/MariaDB, both
		// via Spring Data JPA / Hibernate) emits a per-service SQL .env.example
		// (DATABASE_URL / SPRING_DATASOURCE_*, zero MONGO_*) matching the JPA
		// entities/repos the generator wrote; Java + MongoDB (default) keeps the
		// Spring-Data-MongoDB .env.example.
		if a.isJavaSQL() {
			if err := generateJavaSQLConfigExamples(merged, a.store); err != nil {
				return nil, fmt.Errorf("assembler: java sql config examples: %w", err)
			}
		} else if err := generateJavaConfigExamples(merged); err != nil {
			return nil, fmt.Errorf("assembler: java config examples: %w", err)
		}
	}

	// 3a-ruby. Ruby profile: append a per-service .env.example (the Ruby homologue
	// of the Go config.toml.example / Python / Node / Rust / Java .env.example)
	// BEFORE the ruby/ → core/ rename, so service dirs are still keyed under
	// ruby/services/<svc>/. The emitted .env.example paths are rewritten to
	// core/services/<svc>/.env.example by the rename step below.
	if a.isRuby() {
		// Persistence-config variant: Ruby + SQL (PostgreSQL or MySQL/MariaDB, both
		// via ActiveRecord) emits a per-service SQL .env.example (DATABASE_URL, zero
		// MONGO_*) matching the ActiveRecord models/repos the generator wrote; Ruby +
		// MongoDB (default) keeps the Mongoid .env.example (MONGO_URI/MONGODB_DATABASE).
		if a.isRubySQL() {
			if err := generateRubySQLConfigExamples(merged, a.store); err != nil {
				return nil, fmt.Errorf("assembler: ruby sql config examples: %w", err)
			}
		} else if err := generateRubyConfigExamples(merged); err != nil {
			return nil, fmt.Errorf("assembler: ruby config examples: %w", err)
		}
	}

	// 3a-csharp. C# profile: append a per-service .env.example (the C# homologue of
	// the Go config.toml.example / Python / Node / Rust / Java / Ruby .env.example)
	// BEFORE the csharp/ → core/ rename, so service dirs are still keyed under
	// csharp/services/<svc>/. The emitted .env.example paths are rewritten to
	// core/services/<svc>/.env.example by the rename step below.
	if a.isCSharp() {
		// Persistence-config variant: C# + SQL (PostgreSQL or MySQL/MariaDB, both via
		// EF Core) emits a per-service SQL .env.example (connection string, zero
		// MONGO_*) matching the EF Core DbContext/entities the generator wrote; C# +
		// MongoDB (default) keeps the MongoDB.Driver .env.example (MONGO_URI/MONGO_DATABASE).
		if a.isCSharpSQL() {
			if err := generateCSharpSQLConfigExamples(merged, a.store); err != nil {
				return nil, fmt.Errorf("assembler: csharp sql config examples: %w", err)
			}
		} else if err := generateCSharpConfigExamples(merged); err != nil {
			return nil, fmt.Errorf("assembler: csharp config examples: %w", err)
		}
	}

	// 3a-cpp. C++ profile: append a per-service .env.example (the C++ homologue of
	// the Go config.toml.example / the other profiles' .env.example) BEFORE the
	// cpp/ → core/ rename, so service dirs are still keyed under cpp/services/<svc>/.
	// The emitted .env.example paths are rewritten to core/services/<svc>/.env.example
	// by the rename step below.
	if a.isCpp() {
		// Persistence-config variant: C++ + SQL (PostgreSQL or MySQL/MariaDB, both via
		// hand-written parametrised SQL — libpqxx / mysql-connector-c++) emits a
		// per-service SQL .env.example (connection string, zero MONGO_*) matching the
		// artisanal SQL repos the generator wrote; C++ + MongoDB (default) keeps the
		// mongocxx .env.example (MONGO_URI/MONGO_DATABASE).
		if a.isCppSQL() {
			if err := generateCppSQLConfigExamples(merged, a.store); err != nil {
				return nil, fmt.Errorf("assembler: cpp sql config examples: %w", err)
			}
		} else if err := generateCppConfigExamples(merged); err != nil {
			return nil, fmt.Errorf("assembler: cpp config examples: %w", err)
		}
	}

	// 3b. Python/Node/Rust/Java/Ruby/C#/C++ profile: rename the source-root dir (python/, node/, rust/, java/, ruby/, csharp/ or cpp/) →
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
			// A per-file read failure (permission denied on a 0600 file the
			// distroless uid 65532 can't read, a file that vanished mid-walk,
			// etc.) must NOT abort the whole deliverable: one unreadable
			// skeleton file (e.g. protobuf/buf.lock) used to 500 every
			// DownloadDeliverable. Skip the file and warn — the deliverable
			// still assembles with the remaining files, mirroring the
			// tolerance shipBufLockAndCleanBufYaml already has for its read.
			// A genuinely required file's absence surfaces later/clearly
			// (failed build), but an unreadable optional file no longer
			// breaks the download.
			applog.Warningf("assembler: skipping unreadable skeleton file %s: %v", rel, readErr)
			return nil
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
	if a.isJava() {
		return skipDirJava(rel)
	}
	if a.isRuby() {
		return skipDirRuby(rel)
	}
	if a.isCSharp() {
		return skipDirCSharp(rel)
	}
	if a.isCpp() {
		return skipDirCpp(rel)
	}
	return skipDir(rel)
}

// isSkeletonFile dispatches to the per-profile file filter.
func (a *Assembler) isSkeletonFile(rel string) bool {
	if a.isPython() {
		// Store guard: a Python + SQL (SQLAlchemy) deliverable persists via
		// SQLAlchemy, so the Mongo scaffolding (python/shared/mongo_client/) is dead
		// code — drop it from the skeleton so the package carries no motor/mongo
		// shared client. The SQLAlchemy session/engine arrives via generated artifacts.
		// Also drop the Mongo-specific transaction-manager test, which imports
		// shared.mongo_client + motor (both now removed) and would otherwise fail to
		// import. Unlike Go (whose store-agnostic builder.go hard-imports mongo_client),
		// no Python shared scaffold imports mongo_client, so this prune is safe.
		if a.isPythonSQL() &&
			(strings.HasPrefix(rel, "python/shared/mongo_client/") ||
				rel == "python/shared/tests/test_transaction_manager.py") {
			return false
		}
		return isSkeletonFilePython(rel)
	}
	if a.isNode() {
		return isSkeletonFileNode(rel)
	}
	if a.isRust() {
		return isSkeletonFileRust(rel)
	}
	if a.isJava() {
		return isSkeletonFileJava(rel)
	}
	if a.isRuby() {
		return isSkeletonFileRuby(rel)
	}
	if a.isCSharp() {
		return isSkeletonFileCSharp(rel)
	}
	if a.isCpp() {
		return isSkeletonFileCpp(rel)
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
	// Go + SQL (GORM): drop the Mongo footprint. The store-agnostic Services builder
	// no longer hard-imports mongo_client — the Mongo wiring + the typed Mongo()
	// accessor live in core/internal/svc/builder_mongo.go, the ONLY file that imports
	// core/shared/mongo_client. A GORM deliverable persists via its own gorm_client
	// and never calls Mongo(), so pruning builder_mongo.go AND the shared mongo_client
	// package leaves builder.go (and the whole deliverable) referencing no mongo type
	// — `go build` is clean with zero mongo footprint. The matching mongo-driver
	// go.mod require is dropped by pruneGoModMongoDriver in the Go+SQL config step.
	if a.isGoSQL() &&
		(strings.HasPrefix(rel, "core/shared/mongo_client/") ||
			rel == "core/internal/svc/builder_mongo.go") {
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
	case "protobuf/buf.yaml", "protobuf/buf.go.gen.yaml", "protobuf/buf.lock":
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

	// ── core/shared/phpclassify exclusion ────────────────────────────────────
	// phpclassify is platform analysis/decomposition worker code (PHP module
	// segmentation). It lives under core/shared/ so the recursive core/shared/
	// rule below would admit it, but NO shipped deliverable file imports it — its
	// only importers are core/worker/{analysis,decomposition}/… which skipDir
	// prunes from every deliverable. Shipping it would leak platform worker code
	// into a single-service deliverable for no build reason, so drop it.
	if strings.HasPrefix(rel, "core/shared/phpclassify/") {
		return false
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
	case "protobuf/buf.yaml", "protobuf/buf.lock":
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
	case "protobuf/buf.yaml", "protobuf/buf.lock":
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
		// Cargo home under any CARGO_HOME=$workspace/<name> convention (.cargo-home,
		// cargo-home, …): the whole registry/index/src tree lives under it. DEFECT 4b:
		// mig67 set CARGO_HOME to the workspace-local .cargo-home and persisted 12983
		// registry files there; the .cargo segment alone did not match it.
		if seg == "cargo-home" || strings.HasPrefix(seg, ".cargo-") ||
			strings.HasPrefix(seg, "cargo-home") {
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
	case "protobuf/buf.yaml", "protobuf/buf.lock":
		return true
	}

	return false
}

// ── Java (Spring Boot) profile skeleton filters ───────────────────────────────

// skipDirJava returns true for directories that should be skipped entirely when
// assembling a Java deliverable. The monorepo has NO java/ skeleton tree: a Java
// deliverable is built entirely from generated artifacts (the agent writes a
// complete Maven workspace under java/) plus the neutral buf configs at protobuf/
// root. So this prunes the whole Go monorepo (core/, pkg/, api-gateway/), the
// whole Python tree (python/), all proto source trees, and repo-wide noise. It is
// the Java homologue of skipDirRust.
func skipDirJava(rel string) bool {
	skip := []string{
		// Repo-wide noise.
		".git", "infra", "docs", "bin", "node_modules", "milton-prism-panel",
		// Entire Go monorepo — never in a Java deliverable.
		"core", "pkg", "api-gateway",
		// Entire Python tree — never in a Java deliverable.
		"python",
		// protobuf source trees for platform services; the neutral buf configs
		// at protobuf/ root are included as exact files in isSkeletonFileJava.
		"protobuf/proto",
	}
	for _, s := range skip {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	// Prune any Maven build output / cache dir at any depth (defensive).
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	switch base {
	case "target", "node_modules", ".m2":
		return true
	}
	return false
}

// isSkeletonFileJava returns true when the file at rel belongs in the Java
// deliverable skeleton. It admits ONLY the neutral buf configs at protobuf/
// root — there is no java/ source skeleton in the monorepo. No Go, Python, Node
// or Rust file can pass this filter: every admitted path is an explicit non-code
// buf config. All Java source, pom.xml, and protos arrive via the generated
// artifacts list, never from the repo skeleton. It is the Java homologue of
// isSkeletonFileRust.
func isSkeletonFileJava(rel string) bool {
	// Hard exclude: never emit Go, Python, Node or Rust files in a Java deliverable.
	if strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, ".py") ||
		strings.HasSuffix(rel, ".pyc") || strings.HasSuffix(rel, ".ts") ||
		strings.HasSuffix(rel, ".rs") {
		return false
	}
	switch rel {
	case "go.mod", "go.sum", "Makefile":
		return false
	}

	// ── User-facing buf module config ────────────────────────────────────────
	// Only buf.yaml (the proto module: lint/breaking/deps) ships, so a Java user
	// can regenerate their stubs against the shipped protos with their own gen
	// template. The Go gen config (buf.go.gen.yaml) and the two platform-INTERNAL
	// templates (buf.docs.gen.yaml → panel symlink, buf.deliverable.openapi.yaml →
	// platform pipeline) are all excluded — none belong in a Java project.
	switch rel {
	case "protobuf/buf.yaml", "protobuf/buf.lock":
		return true
	}

	return false
}

// ── Ruby (Rails/Sinatra + grpc gem) profile skeleton filters ──────────────────

// skipDirRuby returns true for directories that should be skipped entirely when
// assembling a Ruby deliverable. The monorepo has NO ruby/ skeleton tree: a Ruby
// deliverable is built entirely from generated artifacts (the agent writes a
// complete bundler workspace under ruby/) plus the neutral buf configs at protobuf/
// root. So this prunes the whole Go monorepo (core/, pkg/, api-gateway/), the
// whole Python tree (python/), all proto source trees, and repo-wide noise. It is
// the Ruby homologue of skipDirJava.
func skipDirRuby(rel string) bool {
	skip := []string{
		// Repo-wide noise.
		".git", "infra", "docs", "bin", "node_modules", "milton-prism-panel",
		// Entire Go monorepo — never in a Ruby deliverable.
		"core", "pkg", "api-gateway",
		// Entire Python tree — never in a Ruby deliverable.
		"python",
		// protobuf source trees for platform services; the neutral buf configs
		// at protobuf/ root are included as exact files in isSkeletonFileRuby.
		"protobuf/proto",
	}
	for _, s := range skip {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	// Prune any Ruby/Bundler build/cache dir at any depth (defensive).
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	switch base {
	case "vendor", ".bundle", "tmp", "coverage", "node_modules":
		return true
	}
	return false
}

// isSkeletonFileRuby returns true when the file at rel belongs in the Ruby
// deliverable skeleton. It admits ONLY the neutral buf configs at protobuf/
// root — there is no ruby/ source skeleton in the monorepo. No Go, Python, Node,
// Rust or Java file can pass this filter: every admitted path is an explicit
// non-code buf config. All Ruby source, Gemfile, and protos arrive via the
// generated artifacts list, never from the repo skeleton. It is the Ruby
// homologue of isSkeletonFileJava.
func isSkeletonFileRuby(rel string) bool {
	// Hard exclude: never emit Go, Python, Node, Rust or Java files in a Ruby deliverable.
	if strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, ".py") ||
		strings.HasSuffix(rel, ".pyc") || strings.HasSuffix(rel, ".ts") ||
		strings.HasSuffix(rel, ".rs") || strings.HasSuffix(rel, ".java") {
		return false
	}
	switch rel {
	case "go.mod", "go.sum", "Makefile":
		return false
	}

	// ── User-facing buf module config ────────────────────────────────────────
	// Only buf.yaml (the proto module: lint/breaking/deps) ships, so a Ruby user
	// can regenerate their stubs against the shipped protos with their own gen
	// template. The Go gen config (buf.go.gen.yaml) and the two platform-INTERNAL
	// templates (buf.docs.gen.yaml → panel symlink, buf.deliverable.openapi.yaml →
	// platform pipeline) are all excluded — none belong in a Ruby project.
	switch rel {
	case "protobuf/buf.yaml", "protobuf/buf.lock":
		return true
	}

	return false
}

// ── C# (.NET / grpc-dotnet + ASP.NET Core) profile skeleton filters ───────────

// skipDirCSharp returns true for directories that should be skipped entirely when
// assembling a C# deliverable. The monorepo has NO csharp/ skeleton tree: a C#
// deliverable is built entirely from generated artifacts (the agent writes a
// complete .NET workspace under csharp/) plus the neutral buf configs at protobuf/
// root. So this prunes the whole Go monorepo (core/, pkg/, api-gateway/), the whole
// Python tree (python/), all proto source trees, and repo-wide noise (including the
// .NET/MSBuild build dirs bin/ and obj/). It is the C# homologue of skipDirRuby.
func skipDirCSharp(rel string) bool {
	skip := []string{
		// Repo-wide noise.
		".git", "infra", "docs", "bin", "node_modules", "milton-prism-panel",
		// Entire Go monorepo — never in a C# deliverable.
		"core", "pkg", "api-gateway",
		// Entire Python tree — never in a C# deliverable.
		"python",
		// protobuf source trees for platform services; the neutral buf configs
		// at protobuf/ root are included as exact files in isSkeletonFileCSharp.
		"protobuf/proto",
	}
	for _, s := range skip {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	// Prune any .NET/MSBuild build dir at any depth (defensive).
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	switch base {
	case "bin", "obj", "node_modules":
		return true
	}
	return false
}

// isSkeletonFileCSharp returns true when the file at rel belongs in the C#
// deliverable skeleton. It admits ONLY the neutral buf configs at protobuf/ root —
// there is no csharp/ source skeleton in the monorepo. No Go, Python, Node, Rust,
// Java or Ruby file can pass this filter: every admitted path is an explicit
// non-code buf config. All C# source, .csproj, and protos arrive via the generated
// artifacts list, never from the repo skeleton. It is the C# homologue of
// isSkeletonFileRuby.
func isSkeletonFileCSharp(rel string) bool {
	// Hard exclude: never emit Go, Python, Node, Rust, Java or Ruby files in a C# deliverable.
	if strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, ".py") ||
		strings.HasSuffix(rel, ".pyc") || strings.HasSuffix(rel, ".ts") ||
		strings.HasSuffix(rel, ".rs") || strings.HasSuffix(rel, ".java") ||
		strings.HasSuffix(rel, ".rb") {
		return false
	}
	switch rel {
	case "go.mod", "go.sum", "Makefile":
		return false
	}

	// ── User-facing buf module config ────────────────────────────────────────
	// Only buf.yaml (the proto module: lint/breaking/deps) ships, so a C# user can
	// regenerate their stubs against the shipped protos with their own gen template.
	// The Go gen config (buf.go.gen.yaml) and the two platform-INTERNAL templates
	// (buf.docs.gen.yaml → panel symlink, buf.deliverable.openapi.yaml → platform
	// pipeline) are all excluded — none belong in a C# project.
	switch rel {
	case "protobuf/buf.yaml", "protobuf/buf.lock":
		return true
	}

	return false
}

// ── C++ (grpc++ + CMake / Drogon) profile skeleton filters ────────────────────

// skipDirCpp returns true for directories that should be skipped entirely when
// assembling a C++ deliverable. The monorepo has NO cpp/ skeleton tree: a C++
// deliverable is built entirely from generated artifacts (the agent writes a
// complete CMake workspace under cpp/) plus the neutral buf configs at protobuf/
// root. So this prunes the whole Go monorepo (core/, pkg/, api-gateway/), the whole
// Python tree (python/), all proto source trees, and repo-wide noise (including the
// CMake/compiler build dirs build/ and CMakeFiles/). It is the C++ homologue of
// skipDirCSharp.
func skipDirCpp(rel string) bool {
	skip := []string{
		// Repo-wide noise.
		".git", "infra", "docs", "bin", "node_modules", "milton-prism-panel",
		// Entire Go monorepo — never in a C++ deliverable.
		"core", "pkg", "api-gateway",
		// Entire Python tree — never in a C++ deliverable.
		"python",
		// protobuf source trees for platform services; the neutral buf configs
		// at protobuf/ root are included as exact files in isSkeletonFileCpp.
		"protobuf/proto",
	}
	for _, s := range skip {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	// Prune any CMake/compiler build dir at any depth (defensive).
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}
	switch base {
	case "build", "CMakeFiles", "node_modules":
		return true
	}
	return false
}

// isSkeletonFileCpp returns true when the file at rel belongs in the C++
// deliverable skeleton. It admits ONLY the neutral buf configs at protobuf/ root —
// there is no cpp/ source skeleton in the monorepo. No Go, Python, Node, Rust,
// Java, Ruby or C# file can pass this filter: every admitted path is an explicit
// non-code buf config. All C++ source, CMakeLists.txt, and protos arrive via the
// generated artifacts list, never from the repo skeleton. It is the C++ homologue
// of isSkeletonFileCSharp.
func isSkeletonFileCpp(rel string) bool {
	// Hard exclude: never emit Go, Python, Node, Rust, Java, Ruby or C# files in a C++ deliverable.
	if strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, ".py") ||
		strings.HasSuffix(rel, ".pyc") || strings.HasSuffix(rel, ".ts") ||
		strings.HasSuffix(rel, ".rs") || strings.HasSuffix(rel, ".java") ||
		strings.HasSuffix(rel, ".rb") || strings.HasSuffix(rel, ".cs") {
		return false
	}
	switch rel {
	case "go.mod", "go.sum", "Makefile":
		return false
	}

	// ── User-facing buf module config ────────────────────────────────────────
	// Only buf.yaml (the proto module: lint/breaking/deps) ships, so a C++ user can
	// regenerate their stubs against the shipped protos with their own gen template.
	// The Go gen config (buf.go.gen.yaml) and the two platform-INTERNAL templates
	// (buf.docs.gen.yaml → panel symlink, buf.deliverable.openapi.yaml → platform
	// pipeline) are all excluded — none belong in a C++ project.
	switch rel {
	case "protobuf/buf.yaml", "protobuf/buf.lock":
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
	// rustVendoredProtoMarkers are the per-service / per-workspace directory names
	// the agent has used to vendor the well-known-type + google.api protos (the
	// Alpine protoc carries no bundled includes). The convention varies by
	// migration: proto_include/ (early), third_party/ (mig68), proto_vendor/
	// (mig67). Any `.proto` whose path contains one of these segments is relocated
	// to the canonical protobuf/proto/<import-path> and the per-service copy dropped.
	markers := []string{"/proto_include/", "/third_party/", "/proto_vendor/"}
	for p, content := range merged {
		// Only act on generated Rust trees (rust/services/<svc>/… or rust/<vendor>/…
		// — mig67 vendored at the workspace root as rust/proto_vendor/).
		if !strings.HasPrefix(p, "rust/") {
			continue
		}
		if !strings.HasSuffix(p, ".proto") {
			continue
		}
		var importPath string
		for _, marker := range markers {
			if idx := strings.Index(p, marker); idx >= 0 {
				// importPath is the protoc import string (e.g. google/api/http.proto):
				// everything after the vendor marker.
				importPath = p[idx+len(marker):]
				break
			}
		}
		if importPath == "" {
			continue
		}
		canonical := "protobuf/proto/" + importPath
		// Move to canonical location (first writer wins; the vendored copies are
		// byte-identical google sources, so dedup is safe).
		if _, exists := merged[canonical]; !exists {
			merged[canonical] = content
		}
		delete(merged, p)
	}

	// Rewrite each rust build.rs to drop the vendored include path now that the
	// google deps resolve via protobuf/proto.
	for p, content := range merged {
		if !strings.HasPrefix(p, "rust/services/") || !strings.HasSuffix(p, "/build.rs") {
			continue
		}
		if rewritten, changed := stripProtoIncludeFromBuildRs(string(content)); changed {
			merged[p] = []byte(rewritten)
		}
	}
}

// javaVendoredProtoMarkers are the per-service directory names the agent uses to
// vendor the well-known-type + google.api protos for the protobuf-maven-plugin (the
// Alpine protoc carries no bundled includes). proto-include/ is the canonical one
// the audit found (wired as <additionalProtoPathElement>); proto_include/ and
// third_party/ are accepted as defence-in-depth (same convention drift the Rust
// path handles). Any `.proto` whose java/services/<svc>/… path contains one of
// these segments is relocated to the canonical protobuf/proto/<import-path>.
var javaVendoredProtoMarkers = []string{"/proto-include/", "/proto_include/", "/third_party/"}

// relocateJavaVendoredProtos enforces the invariant "no `.proto` under the Java
// service source tree" on the merged file map (keys still carry the java/ source
// root prefix — the java/→core/ rename runs later in step 3b). The agent vendors
// the google.api / well-known-type protos into
// java/services/<svc>/src/main/proto-include/<import-path> and wires that dir as the
// protobuf-maven-plugin <additionalProtoPathElement>; left in place those `.proto`
// land under core/services/<svc>/… after the rename. This relocates every such
// vendored proto to the canonical top-level protobuf/proto/<import-path> (the suffix
// after the vendor marker IS the protoc import string, e.g.
// google/api/http.proto → protobuf/proto/google/api/http.proto), dedups across
// services, drops the per-service copies, and rewrites every service pom.xml to
// repoint its proto source/include configuration at the canonical protobuf/proto
// tree so `mvn package` codegen still resolves. The result: 0 `.proto` under core/
// and a pom.xml that still builds.
func relocateJavaVendoredProtos(merged map[string][]byte) {
	for p, content := range merged {
		// Only act on generated Java service trees (java/services/<svc>/…).
		if !strings.HasPrefix(p, "java/services/") {
			continue
		}
		if !strings.HasSuffix(p, ".proto") {
			continue
		}
		var importPath string
		for _, marker := range javaVendoredProtoMarkers {
			if idx := strings.Index(p, marker); idx >= 0 {
				importPath = p[idx+len(marker):]
				break
			}
		}
		if importPath == "" {
			// A `.proto` directly under the service tree (not in a vendor dir) — e.g.
			// java/services/<svc>/src/main/proto/<svc>.proto. The agent occasionally
			// keeps the service proto inside the module instead of under the canonical
			// tree; relocate it too so NO `.proto` survives under core/. Use the path
			// after src/main/proto/ as the import path when present; otherwise fall back
			// to the basename so it still lands under protobuf/proto/.
			if idx := strings.Index(p, "/src/main/proto/"); idx >= 0 {
				importPath = p[idx+len("/src/main/proto/"):]
			} else {
				importPath = p[strings.LastIndex(p, "/")+1:]
			}
		}
		canonical := "protobuf/proto/" + importPath
		// Move to canonical location (first writer wins; vendored copies are
		// byte-identical google sources, so dedup is safe).
		if _, exists := merged[canonical]; !exists {
			merged[canonical] = content
		}
		delete(merged, p)
	}

	// Rewrite each service pom.xml to repoint its proto source/include config at the
	// canonical protobuf/proto tree now that the vendored proto-include dir is gone.
	for p, content := range merged {
		if !strings.HasPrefix(p, "java/") || !strings.HasSuffix(p, "pom.xml") {
			continue
		}
		if rewritten, changed := rewriteJavaPomProtoPaths(string(content)); changed {
			merged[p] = []byte(rewritten)
		}
	}
}

// canonicalJavaProtoPath is the relative path from a java/services/<svc>/ module to
// the canonical top-level protobuf/proto tree (svc → services → java → repo root,
// then into protobuf/proto). It is the SAME prefix the generated service protos
// already resolve through, so repointing the plugin here keeps codegen working with
// every `.proto` under protobuf/proto/ ONLY.
const canonicalJavaProtoPath = "../../../protobuf/proto"

// rewriteJavaPomProtoPaths rewrites a Java pom.xml's protobuf-maven-plugin proto
// path configuration so it resolves protos from the canonical top-level
// protobuf/proto tree instead of a per-service vendored proto-include directory.
// It:
//   - drops any <additionalProtoPathElement>…proto-include…</additionalProtoPathElement>
//     block whose value points at a vendored include dir (the protos it referenced
//     now live under protobuf/proto/, reachable via the protoSourceRoot/relative
//     include the service protos already use);
//   - repoints a <protoSourceRoot> that pointed at the module-local src/main/proto
//     (or a vendored proto dir) at the canonical ../../../protobuf/proto path so the
//     relocated service proto still drives codegen.
//
// Returns the rewritten body and whether any change was made. A pom with no proto
// path configuration (the common case once vendoring is removed) is returned
// unchanged.
func rewriteJavaPomProtoPaths(body string) (string, bool) {
	changed := false

	// 1. Drop <additionalProtoPathElement> blocks that reference a vendored include
	//    dir (proto-include / proto_include / third_party). The protos they pointed
	//    at now live under protobuf/proto/, resolved via the protoSourceRoot's
	//    include root, so the extra include path is dead and would break codegen
	//    (the dir no longer exists).
	for _, marker := range []string{"proto-include", "proto_include", "third_party"} {
		for {
			open := strings.Index(body, "<additionalProtoPathElement>")
			if open < 0 {
				break
			}
			closeTag := "</additionalProtoPathElement>"
			closeIdx := strings.Index(body[open:], closeTag)
			if closeIdx < 0 {
				break
			}
			end := open + closeIdx + len(closeTag)
			block := body[open:end]
			if !strings.Contains(block, marker) {
				// This block does not reference the current marker; stop scanning for
				// THIS marker (avoid an infinite loop on a non-matching block) and move
				// on to the next marker.
				break
			}
			// Drop the block plus any trailing whitespace/newline left behind.
			rest := body[end:]
			rest = strings.TrimLeft(rest, " \t")
			rest = strings.TrimPrefix(rest, "\n")
			body = strings.TrimRight(body[:open], " \t") + "\n" + rest
			changed = true
		}
	}

	// 2. Repoint a module-local / vendored <protoSourceRoot> at the canonical tree so
	//    the relocated service proto still drives codegen. Only rewrite when it does
	//    NOT already point at the canonical protobuf/proto path.
	const psrOpen = "<protoSourceRoot>"
	const psrClose = "</protoSourceRoot>"
	if open := strings.Index(body, psrOpen); open >= 0 {
		valStart := open + len(psrOpen)
		if closeIdx := strings.Index(body[valStart:], psrClose); closeIdx >= 0 {
			val := body[valStart : valStart+closeIdx]
			if !strings.Contains(val, "protobuf/proto") {
				body = body[:valStart] + canonicalJavaProtoPath + body[valStart+closeIdx:]
				changed = true
			}
		}
	}

	return body, changed
}

// relocateStraySourceProtos relocates any `.proto` under prefix that is NOT already
// in the canonical protobuf/proto/ tree to protobuf/proto/<import-path>, deduping
// against what already ships, and drops the in-tree copy. It is the Go/Python
// homologue of relocateRustVendoredProtos / relocateJavaVendoredProtos and the merge
// guard isNodeVendoredProto: the invariant is "the only protos a deliverable ships
// live under protobuf/proto/". The import path is taken from after a `…/proto/`
// segment (the protoc import string the agent used), falling back to the basename so
// the proto still lands under protobuf/proto/. Keys carry their pre-3b-rename prefix
// (core/services/ for Go — no rename; python/ for Python), which is why this runs
// BEFORE the rename.
func relocateStraySourceProtos(merged map[string][]byte, prefix string) {
	for p, content := range merged {
		if !strings.HasPrefix(p, prefix) || !strings.HasSuffix(p, ".proto") {
			continue
		}
		var importPath string
		if idx := strings.Index(p, "/proto/"); idx >= 0 {
			importPath = p[idx+len("/proto/"):]
		} else {
			importPath = p[strings.LastIndex(p, "/")+1:]
		}
		canonical := "protobuf/proto/" + importPath
		if _, exists := merged[canonical]; !exists {
			merged[canonical] = content
		}
		delete(merged, p)
	}
}

// rustVendorMarkerLiterals are the directory-name literals the agent uses when it
// vendors third-party protos and feeds the dir into the tonic-build include slice.
// They mirror the markers in relocateRustVendoredProtos but as the source-literal
// forms seen in build.rs (with/without ./, ../ and ../.. relative prefixes).
var rustVendorMarkerLiterals = []string{
	"proto_include", "third_party", "proto_vendor",
}

// rustVendorLineMentionsMarker reports whether a build.rs `let` binding line binds
// a vendored-proto include dir (proto_include / third_party / proto_vendor), in any
// relative form: `let third_party = PathBuf::from("third_party");`,
// `let wkt_include = "../../proto_vendor";`, `let p = "./proto_include";`, etc.
func rustVendorLineMentionsMarker(trimmed string) bool {
	for _, m := range rustVendorMarkerLiterals {
		if strings.Contains(trimmed, `"`+m+`"`) ||
			strings.Contains(trimmed, `"./`+m+`"`) ||
			strings.Contains(trimmed, `"../`+m+`"`) ||
			strings.Contains(trimmed, `"../../`+m+`"`) ||
			strings.Contains(trimmed, `"../../../`+m+`"`) {
			return true
		}
	}
	return false
}

// stripProtoIncludeFromBuildRs removes any reference to a vendored-proto include
// directory (proto_include / third_party / proto_vendor) from a Rust build.rs
// body, so tonic-build resolves proto imports solely through the canonical
// `protobuf/proto` include root. It handles the shapes the agent emits:
//   - a `let <name> = "<marker>";` (or PathBuf::from("<marker>")) binding fed into
//     the include slice — the binding line is dropped AND the <name> identifier is
//     removed from the include slice (incl. .to_str().unwrap() suffix);
//   - an inline "<marker>" literal directly inside the &[…] include slice.
//
// Returns the rewritten body and whether any change was made.
func stripProtoIncludeFromBuildRs(body string) (string, bool) {
	mentionsAnyMarker := false
	for _, m := range rustVendorMarkerLiterals {
		if strings.Contains(body, m) {
			mentionsAnyMarker = true
			break
		}
	}
	if !mentionsAnyMarker {
		return body, false
	}
	changed := false
	var deletedBindings []string
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		// Drop a `let <name> = …"<marker>"…;` binding line and remember <name> so its
		// uses in the include slice can be stripped below.
		if strings.HasPrefix(trimmed, "let ") && strings.Contains(trimmed, "=") &&
			rustVendorLineMentionsMarker(trimmed) {
			if name := strings.TrimSpace(strings.TrimPrefix(trimmed[:strings.Index(trimmed, "=")], "let ")); name != "" {
				// name may carry a `mut ` qualifier; keep only the identifier.
				name = strings.TrimPrefix(name, "mut ")
				name = strings.TrimSpace(name)
				deletedBindings = append(deletedBindings, name)
			}
			changed = true
			continue
		}
		keep = append(keep, line)
	}
	body = strings.Join(keep, "\n")

	// Remove the deleted binding identifiers from the include slice, in their
	// common slice forms (`, name.to_str().unwrap()`, `, name`, `, &name`).
	for _, name := range deletedBindings {
		for _, form := range []string{
			", " + name + ".to_str().unwrap()", "," + name + ".to_str().unwrap()",
			", &" + name, ", " + name, "," + name,
		} {
			if strings.Contains(body, form) {
				body = strings.ReplaceAll(body, form, "")
				changed = true
			}
		}
	}

	// Remove any leftover inline include-slice entries referencing a marker literal
	// directly (the literal itself, or a legacy vendored_includes identifier).
	replacements := []string{
		`, vendored_includes`, `,vendored_includes`, `, &vendored_includes`,
	}
	for _, m := range rustVendorMarkerLiterals {
		replacements = append(replacements,
			`, "`+m+`"`, `,"`+m+`"`,
			`, "./`+m+`"`, `,"./`+m+`"`,
			`, "../`+m+`"`, `,"../`+m+`"`,
			`, "../../`+m+`"`, `,"../../`+m+`"`,
			`, "../../../`+m+`"`, `,"../../../`+m+`"`,
		)
	}
	for _, r := range replacements {
		if strings.Contains(body, r) {
			body = strings.ReplaceAll(body, r, "")
			changed = true
		}
	}
	return body, changed
}

// prunePyprojectMotorDep removes the `motor = …` dependency line from the
// [tool.poetry.dependencies] section of a Python deliverable's pyproject.toml. For
// a Python + SQL (SQLAlchemy) cell Motor (the async Mongo driver) is unused once
// python/shared/mongo_client/ is excluded, so the dependency declares a store the
// package never uses. The mypy-override references to motor/mongo elsewhere in the
// file are inert (the modules no longer ship), so only the dependency line is cut.
func prunePyprojectMotorDep(merged map[string][]byte) {
	c, ok := merged["python/pyproject.toml"]
	if !ok {
		return
	}
	var keep []string
	changed := false
	for _, line := range strings.Split(string(c), "\n") {
		// Match a top-level `motor = "…"` dependency line (allow leading whitespace
		// for safety, though poetry deps are at column 0).
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "motor ") || strings.HasPrefix(t, "motor=") {
			if strings.Contains(t, "=") {
				changed = true
				continue
			}
		}
		keep = append(keep, line)
	}
	if changed {
		merged["python/pyproject.toml"] = []byte(strings.Join(keep, "\n"))
	}
}

// ── Mongo-stack pruning for Python / Node / Rust SQL deliverables ────────────
// These are the per-language homologues of pruneGoModMongoDriver: when the store
// is SQL (≠ mongo) they drop the demonstrably-unused Mongo dependencies from the
// shipped manifests/locks, mirroring what the Go path already does. Every removal
// is guarded by a source-import check so a dependency the deliverable's code
// actually imports is NEVER removed — making the prune safe by construction (it
// can only remove dead, non-imported, store-mismatched entries).

// pythonModuleImported reports whether any shipped Python source file in merged
// imports the given top-level module (e.g. "motor", "pymongo"). Matches both
// `import <mod>[...]` and `from <mod>[...] import …` with a word boundary so
// `import motorway` does not match `motor`. It is the safety belt for
// prunePoetryLockMongo: a package is dropped from poetry.lock only when no shipped
// .py imports it, so the lock prune can never remove a dependency the code uses.
func pythonModuleImported(merged map[string][]byte, module string) bool {
	impPrefix := "import " + module
	fromPrefix := "from " + module
	boundary := func(rest string) bool {
		return rest == "" || rest[0] == ' ' || rest[0] == '.' || rest[0] == ',' || rest[0] == '\t'
	}
	for p, c := range merged {
		if !strings.HasSuffix(p, ".py") {
			continue
		}
		for _, line := range strings.Split(string(c), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, impPrefix) && boundary(t[len(impPrefix):]) {
				return true
			}
			if strings.HasPrefix(t, fromPrefix) {
				rest := t[len(fromPrefix):]
				if rest != "" && (rest[0] == ' ' || rest[0] == '.' || rest[0] == '\t') {
					return true
				}
			}
		}
	}
	return false
}

// pythonMongoLockPackages are the Mongo-stack packages a Python+SQL deliverable
// never installs: the async/sync drivers and the in-memory mongo test doubles.
// dnspython is handled separately (dropped only when it becomes an orphan once
// pymongo — its sole dependent in the lock — is removed). The poetry package name
// maps 1:1 to its import module except mongomock-motor → mongomock_motor.
var pythonMongoLockPackages = []string{"motor", "pymongo", "mongomock", "mongomock-motor"}

// lockNameToModule maps a poetry package name to its import module name (the only
// non-identity case is the PyPI dash vs import underscore for mongomock-motor).
func lockNameToModule(pkg string) string {
	if pkg == "mongomock-motor" {
		return "mongomock_motor"
	}
	return pkg
}

// prunePoetryLockMongo removes the Mongo-stack [[package]] blocks from
// python/poetry.lock for a Python+SQL deliverable. The lock is parsed into its
// preamble, the ordered list of [[package]] blocks, and the trailing [metadata]
// table; a block is dropped when its name is in pythonMongoLockPackages (and no
// shipped .py imports it). dnspython is dropped as a transitive orphan: only when
// no KEPT package still lists it under [package.dependencies] (its sole lock
// dependent is pymongo, which we remove) and no shipped .py imports `dns`. This is
// closure-correct (exactly like the Go indirect-dep prune): pymongo is required
// only by motor and dnspython only by pymongo, so removing motor orphans both. The
// [metadata] content-hash is intentionally left as-is (it was already stale once
// prunePyprojectMotorDep dropped the motor dep from pyproject.toml; recomputing
// poetry's hash is out of scope — the goal is zero Mongo footprint, not lock
// re-validation). No-op when the lock is absent.
func prunePoetryLockMongo(merged map[string][]byte) {
	const key = "python/poetry.lock"
	content, ok := merged[key]
	if !ok {
		return
	}
	lines := strings.Split(string(content), "\n")

	type block struct {
		name  string
		lines []string
	}
	var preamble []string
	var blocks []block
	var tail []string

	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) != "[[package]]" {
		preamble = append(preamble, lines[i])
		i++
	}
	for i < len(lines) {
		if strings.TrimSpace(lines[i]) == "[metadata]" {
			tail = append(tail, lines[i:]...)
			break
		}
		// lines[i] is a "[[package]]" header.
		b := block{lines: []string{lines[i]}}
		i++
		for i < len(lines) {
			tt := strings.TrimSpace(lines[i])
			if tt == "[[package]]" || tt == "[metadata]" {
				break
			}
			if b.name == "" && strings.HasPrefix(tt, `name = "`) {
				b.name = strings.TrimSuffix(strings.TrimPrefix(tt, `name = "`), `"`)
			}
			b.lines = append(b.lines, lines[i])
			i++
		}
		blocks = append(blocks, b)
	}

	remove := make(map[string]bool)
	for _, pkg := range pythonMongoLockPackages {
		if pythonModuleImported(merged, lockNameToModule(pkg)) {
			continue // shipped source imports it — keep (safety belt)
		}
		remove[pkg] = true
	}
	// dnspython: drop only when it becomes an orphan (no kept package depends on
	// it) and the deliverable does not import the `dns` module directly.
	dnsDependedByKept := false
	for _, b := range blocks {
		if b.name == "dnspython" || remove[b.name] {
			continue
		}
		for _, l := range b.lines {
			if strings.HasPrefix(strings.TrimSpace(l), "dnspython = ") {
				dnsDependedByKept = true
			}
		}
	}
	if !dnsDependedByKept && !pythonModuleImported(merged, "dns") {
		remove["dnspython"] = true
	}

	changed := false
	out := append([]string(nil), preamble...)
	for _, b := range blocks {
		if remove[b.name] {
			changed = true
			continue
		}
		out = append(out, b.lines...)
	}
	out = append(out, tail...)
	if changed {
		merged[key] = []byte(strings.Join(out, "\n"))
	}
}

// pythonMongoMypyTokens are the substrings that mark a mypy `module` override
// entry as a Mongo-stack module a Python+SQL deliverable does not ship (the
// motor/mongomock stubs and the excluded shared.mongo_client package).
var pythonMongoMypyTokens = []string{"motor", "mongomock", "mongo_client", "pymongo"}

// prunePythonMongoMypyOverrides rewrites python/pyproject.toml so no
// `[[tool.mypy.overrides]]` entry references a Mongo module that a Python+SQL
// deliverable does not ship. Each Mongo-token `"…"` module entry is dropped from
// its list; when a list is emptied by that removal (or a single-line
// `module = "<mongo>"` is itself Mongo), the whole override block — plus the
// contiguous leading `#` comment introducing it — is removed so no stale Mongo
// reference (not even in a comment like "# Motor stubs …") survives. Non-Mongo
// entries and non-override config are preserved verbatim. No-op when the file is
// absent or carries no Mongo override.
func prunePythonMongoMypyOverrides(merged map[string][]byte) {
	const key = "python/pyproject.toml"
	content, ok := merged[key]
	if !ok {
		return
	}
	lines := strings.Split(string(content), "\n")

	isMongoEntry := func(t string) bool {
		if !strings.HasPrefix(t, `"`) {
			return false
		}
		low := strings.ToLower(t)
		for _, tok := range pythonMongoMypyTokens {
			if strings.Contains(low, tok) {
				return true
			}
		}
		return false
	}

	var out []string
	changed := false
	i := 0
	for i < len(lines) {
		if strings.TrimSpace(lines[i]) != "[[tool.mypy.overrides]]" {
			out = append(out, lines[i])
			i++
			continue
		}
		// Gather the override block: header until the next table header `[` or EOF.
		blk := []string{lines[i]}
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "[") {
			blk = append(blk, lines[j])
			j++
		}
		// Do not absorb the blank/comment lines that introduce the NEXT block.
		for len(blk) > 1 {
			last := strings.TrimSpace(blk[len(blk)-1])
			if last == "" || strings.HasPrefix(last, "#") {
				blk = blk[:len(blk)-1]
				j--
				continue
			}
			break
		}

		cleaned, removedAny, moduleEmpty := cleanMypyOverrideBlock(blk, isMongoEntry)
		if removedAny {
			changed = true
		}
		if moduleEmpty {
			// Drop the whole block + the contiguous leading comment(s) (and the one
			// blank line above them) already emitted into out.
			for len(out) > 0 && strings.HasPrefix(strings.TrimSpace(out[len(out)-1]), "#") {
				out = out[:len(out)-1]
			}
			if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
				out = out[:len(out)-1]
			}
			changed = true
		} else {
			out = append(out, cleaned...)
		}
		i = j
	}
	if changed {
		merged[key] = []byte(strings.Join(out, "\n"))
	}
}

// cleanMypyOverrideBlock removes Mongo module entries from a single
// `[[tool.mypy.overrides]]` block. It returns the cleaned block, whether any entry
// was removed, and whether the block's `module` list ended up empty (multi-line
// list with zero remaining quoted entries, or a single-line `module = "<mongo>"`)
// — the signal to the caller to drop the block wholesale.
func cleanMypyOverrideBlock(blk []string, isMongoEntry func(string) bool) (out []string, removedAny, moduleEmpty bool) {
	inList := false
	listSeen := false
	entriesAfter := 0
	for _, l := range blk {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "module = ["):
			listSeen = true
			inList = !strings.Contains(t, "]")
			out = append(out, l)
		case strings.HasPrefix(t, `module = "`):
			if isMongoEntry(strings.TrimSpace(strings.TrimPrefix(t, "module = "))) {
				removedAny = true
				moduleEmpty = true
				continue // drop the single-line module line
			}
			out = append(out, l)
		case inList:
			if t == "]" {
				inList = false
				out = append(out, l)
				continue
			}
			entry := strings.TrimSuffix(t, ",")
			if isMongoEntry(entry) {
				removedAny = true
				continue
			}
			if strings.HasPrefix(t, `"`) {
				entriesAfter++
			}
			out = append(out, l)
		default:
			out = append(out, l)
		}
	}
	if listSeen && entriesAfter == 0 {
		moduleEmpty = true
	}
	return out, removedAny, moduleEmpty
}

// nodeMongoDrivers are the native Mongo drivers a Node+SQL (Prisma) deliverable
// never imports; their @types stubs are dropped alongside the dropped driver.
var nodeMongoDrivers = []string{"mongodb", "mongoose"}

// nodeModuleImported reports whether any shipped .ts/.js source in merged imports
// the given npm package (bare specifier or a subpath like "mongodb/lib") via an
// ES `import … "pkg"` / `from "pkg"` or a CommonJS `require("pkg")`. Safety belt
// for pruneNodeMongoDeps: a driver is dropped only when nothing imports it.
func nodeModuleImported(merged map[string][]byte, pkg string) bool {
	needles := []string{
		`"` + pkg + `"`, `'` + pkg + `'`, // bare specifier
		`"` + pkg + `/`, `'` + pkg + `/`, // subpath import
	}
	for p, c := range merged {
		if !(strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".js") ||
			strings.HasSuffix(p, ".mts") || strings.HasSuffix(p, ".cts")) {
			continue
		}
		s := string(c)
		for _, line := range strings.Split(s, "\n") {
			t := strings.TrimSpace(line)
			if !strings.Contains(t, "import") && !strings.Contains(t, "require") &&
				!strings.Contains(t, "from") {
				continue
			}
			for _, n := range needles {
				if strings.Contains(t, n) {
					return true
				}
			}
		}
	}
	return false
}

// pruneNodeMongoDeps removes the Mongo drivers (mongodb/mongoose) and their
// @types stubs from every package.json of a Node+SQL deliverable, ONLY for the
// drivers that no shipped .ts/.js imports. JSON validity is preserved: dependency
// lines are removed by name and any trailing comma left dangling before a closing
// `}` is stripped. No-op when the package.json is already Mongo-free.
func pruneNodeMongoDeps(merged map[string][]byte) {
	// Resolve, once, which drivers are safe to drop (not imported anywhere).
	var drop []string
	for _, d := range nodeMongoDrivers {
		if nodeModuleImported(merged, d) {
			continue
		}
		drop = append(drop, d, "@types/"+d)
	}
	if len(drop) == 0 {
		return
	}
	dropSet := make(map[string]bool, len(drop))
	for _, d := range drop {
		dropSet[d] = true
	}
	for p, c := range merged {
		if !(p == "node/package.json" || (strings.HasPrefix(p, "node/") && strings.HasSuffix(p, "/package.json"))) {
			continue
		}
		if cleaned, changed := dropJSONDepLines(string(c), dropSet); changed {
			merged[p] = []byte(cleaned)
		}
	}
}

// dropJSONDepLines removes any `"name": …` line whose key is in drop from a
// package.json body, then strips a now-dangling trailing comma before a closing
// `}`/`},` so the JSON stays valid. Line-based to preserve the file's exact key
// order and formatting (encoding/json would reorder + reflow). Returns the
// rewritten body and whether anything changed.
func dropJSONDepLines(body string, drop map[string]bool) (string, bool) {
	lines := strings.Split(body, "\n")
	var kept []string
	changed := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		dropped := false
		for name := range drop {
			if strings.HasPrefix(t, `"`+name+`"`) {
				rest := strings.TrimSpace(strings.TrimPrefix(t, `"`+name+`"`))
				if strings.HasPrefix(rest, ":") {
					dropped = true
					break
				}
			}
		}
		if dropped {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	if !changed {
		return body, false
	}
	// Strip a trailing comma left on the last entry of an object whose following
	// non-blank line closes the object.
	for idx := 0; idx < len(kept); idx++ {
		t := strings.TrimRight(kept[idx], " \t")
		if !strings.HasSuffix(t, ",") {
			continue
		}
		k := idx + 1
		for k < len(kept) && strings.TrimSpace(kept[k]) == "" {
			k++
		}
		if k < len(kept) {
			nt := strings.TrimSpace(kept[k])
			if nt == "}" || nt == "}," || strings.HasPrefix(nt, "}") {
				kept[idx] = strings.TrimSuffix(t, ",")
			}
		}
	}
	return strings.Join(kept, "\n"), true
}

// rustCrateImported reports whether any shipped .rs source in merged uses the
// given crate (`use <crate>` / `<crate>::` / `extern crate <crate>`). Safety belt
// for pruneCargoMongoDeps: the crate is dropped from Cargo.toml only when no Rust
// source references it.
func rustCrateImported(merged map[string][]byte, crate string) bool {
	usePrefix := "use " + crate
	externDecl := "extern crate " + crate
	pathRef := crate + "::"
	boundary := func(rest string) bool {
		return rest == "" || rest[0] == ' ' || rest[0] == ';' || rest[0] == ':' || rest[0] == '\t' || rest[0] == '{'
	}
	for p, c := range merged {
		if !strings.HasSuffix(p, ".rs") {
			continue
		}
		s := string(c)
		if strings.Contains(s, pathRef) || strings.Contains(s, externDecl) {
			return true
		}
		for _, line := range strings.Split(s, "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, usePrefix) && boundary(t[len(usePrefix):]) {
				return true
			}
		}
	}
	return false
}

// pruneCargoMongoDeps removes the `mongodb` crate from every Cargo.toml of a
// Rust+SQL (SeaORM) deliverable — both the `[workspace.dependencies]` declaration
// (`mongodb = "3"` / `mongodb = { … }`, including a multi-line inline table) and
// each member's `mongodb.workspace = true` line. It runs only when no shipped .rs
// uses the crate (checked once, workspace-wide: a workspace dep must stay if ANY
// member uses it), so dropping it cannot break compilation. No-op when no
// Cargo.toml declares the crate.
func pruneCargoMongoDeps(merged map[string][]byte) {
	if rustCrateImported(merged, "mongodb") {
		return // a shipped .rs uses the crate — keep it everywhere
	}
	for p, c := range merged {
		if !(p == "rust/Cargo.toml" || (strings.HasPrefix(p, "rust/") && strings.HasSuffix(p, "/Cargo.toml"))) {
			continue
		}
		lines := strings.Split(string(c), "\n")
		var keep []string
		changed := false
		for k := 0; k < len(lines); k++ {
			if cargoDepKey(lines[k]) != "mongodb" {
				keep = append(keep, lines[k])
				continue
			}
			changed = true
			// Consume any continuation lines of a multi-line inline table
			// `mongodb = { … }` spanning multiple lines (balance { } and [ ]).
			bal := bracketBalance(lines[k])
			for bal > 0 && k+1 < len(lines) {
				k++
				bal += bracketBalance(lines[k])
			}
		}
		if changed {
			merged[p] = []byte(strings.Join(keep, "\n"))
		}
	}
}

// cargoDepKey returns the dependency key of a Cargo.toml line — the token before
// the first space, '=' or '.' — or "" for blanks, comments and table headers. So
// `mongodb = "3"`, `mongodb = { … }` and `mongodb.workspace = true` all key to
// "mongodb", while `sea-orm.workspace = true` keys to "sea-orm".
func cargoDepKey(line string) string {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[") {
		return ""
	}
	for idx := 0; idx < len(t); idx++ {
		switch t[idx] {
		case ' ', '=', '.', '\t':
			return t[:idx]
		}
	}
	return t
}

// bracketBalance returns the net count of opening minus closing braces/brackets in
// s, used to walk a multi-line inline `{ … }` (or `[ … ]`) Cargo dependency table.
func bracketBalance(s string) int {
	bal := 0
	for _, r := range s {
		switch r {
		case '{', '[':
			bal++
		case '}', ']':
			bal--
		}
	}
	return bal
}

// goModPlatformDirectDeps are the heavy direct requires the monorepo go.mod
// declares that ONLY the analysis/decomposition/generation workers and the
// platform service repositories import — none of which ship in a deliverable. They
// are dropped from the assembled go.mod.
var goModPlatformDirectDeps = []string{
	"github.com/docker/docker",
	"github.com/go-git/go-git/v5",
	"github.com/go-enry/go-enry/v2",
	"github.com/smacker/go-tree-sitter",
	"github.com/hibiken/asynq",
}

// goModPlatformIndirectDeps are the indirect requires reachable ONLY through the
// goModPlatformDirectDeps (the go-git openpgp/ssh stack, the docker moby/containerd
// stack, the asynq go-redis/cron stack, and the enry oniguruma binding). They are
// confirmed unused by every shipped Go tree (core/shared, core/internal, pkg/*),
// so dropping them keeps the assembled go.mod tidy without removing anything the
// deliverable compiles. Determined by module-graph reachability from the 5 platform
// roots minus reachability from the deliverable's real deps.
var goModPlatformIndirectDeps = []string{
	"github.com/Microsoft/go-winio",
	"github.com/ProtonMail/go-crypto",
	"github.com/cloudflare/circl",
	"github.com/containerd/log",
	"github.com/cyphar/filepath-securejoin",
	"github.com/dgryski/go-rendezvous",
	"github.com/distribution/reference",
	"github.com/docker/go-connections",
	"github.com/docker/go-units",
	"github.com/emirpasic/gods",
	"github.com/go-enry/go-oniguruma",
	"github.com/go-git/gcfg",
	"github.com/go-git/go-billy/v5",
	"github.com/gogo/protobuf",
	"github.com/golang/groupcache",
	"github.com/jbenet/go-context",
	"github.com/kevinburke/ssh_config",
	"github.com/moby/docker-image-spec",
	"github.com/moby/term",
	"github.com/morikuni/aec",
	"github.com/opencontainers/go-digest",
	"github.com/opencontainers/image-spec",
	"github.com/pjbgf/sha1cd",
	"github.com/redis/go-redis/v9",
	"github.com/robfig/cron/v3",
	"github.com/sergi/go-diff",
	"github.com/skeema/knownhosts",
	"github.com/xanzy/ssh-agent",
}

// tidyGoMod rewrites the assembled go.mod (if present) to drop the platform/agent
// direct + indirect requires the deliverable never imports (goModPlatformDirectDeps
// + goModPlatformIndirectDeps). A require line is matched by its module path as the
// first whitespace-separated token after optional leading tabs, so the version and
// any `// indirect` suffix do not affect the match. Only require lines are touched;
// the module/go directives and every other require are preserved verbatim. No-op
// when go.mod is absent (non-Go profiles).
func tidyGoMod(merged map[string][]byte) {
	const key = "go.mod"
	content, ok := merged[key]
	if !ok {
		return
	}
	drop := make(map[string]struct{}, len(goModPlatformDirectDeps)+len(goModPlatformIndirectDeps))
	for _, m := range goModPlatformDirectDeps {
		drop[m] = struct{}{}
	}
	for _, m := range goModPlatformIndirectDeps {
		drop[m] = struct{}{}
	}
	var keep []string
	changed := false
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		// A require line is `<module> <version>[ // indirect]`. The module path is
		// the first token. Lines like `require (`, `)`, `module …`, `go …` have a
		// first token that never matches a module path in the drop set.
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 {
			if _, d := drop[fields[0]]; d {
				changed = true
				continue
			}
		}
		keep = append(keep, line)
	}
	if changed {
		merged[key] = []byte(strings.Join(keep, "\n"))
	}
}

// goModMongoDriverDeps are the go.mongodb.org/mongo-driver require plus the
// indirect requires reachable ONLY through it (its bson/x509 SASL/compression
// stack). A Go+SQL (GORM) deliverable prunes builder_mongo.go and the shared
// mongo_client package, so no shipped Go code imports the mongo driver; these
// requires are then dead weight. They are confirmed mongo-exclusive in this
// monorepo (shared by no other shipped dep), so dropping them keeps the assembled
// go.mod `go mod tidy`-clean for the Go+SQL cell. klauspost/compress is NOT listed:
// it is reachable from other deps too, so it is left intact (a harmless unused
// indirect, never a mongo-leak the cert greps for).
var goModMongoDriverDeps = []string{
	"go.mongodb.org/mongo-driver",
	"github.com/golang/snappy",
	"github.com/montanaflynn/stats",
	"github.com/xdg-go/pbkdf2",
	"github.com/xdg-go/scram",
	"github.com/xdg-go/stringprep",
	"github.com/youmark/pkcs8",
}

// pruneGoModMongoDriver rewrites the assembled go.mod (if present) to drop the
// mongo-driver require and its mongo-only indirect deps (goModMongoDriverDeps). It
// is the Go+SQL homologue of tidyGoMod: same line-matching (the module path is the
// first whitespace-separated token, version + `// indirect` suffix ignored). It runs
// ONLY for a Go+SQL deliverable, where builder_mongo.go + core/shared/mongo_client/
// are pruned so nothing imports the driver. No-op when go.mod is absent.
func pruneGoModMongoDriver(merged map[string][]byte) {
	const key = "go.mod"
	content, ok := merged[key]
	if !ok {
		return
	}
	drop := make(map[string]struct{}, len(goModMongoDriverDeps))
	for _, m := range goModMongoDriverDeps {
		drop[m] = struct{}{}
	}
	var keep []string
	changed := false
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 {
			if _, d := drop[fields[0]]; d {
				changed = true
				continue
			}
		}
		keep = append(keep, line)
	}
	if changed {
		merged[key] = []byte(strings.Join(keep, "\n"))
	}
}

// pythonPlatformServices are the fixed Milton Prism PLATFORM service names. A
// single-service deliverable never ships these (its generated service has a
// user-chosen name like "user"/"bookstack"), so any mypy override / import-linter
// contract / dep that names one of these is platform leakage to prune or rewrite.
var pythonPlatformServices = []string{"identity", "repository", "migration", "analysis", "billing", "articles"}

// dropPyprojectDeps removes the named top-level dependency lines from the
// [tool.poetry.dependencies] section of python/pyproject.toml (e.g. fastapi,
// uvicorn for a non-FastAPI deliverable). A dep line is `<name> = …`; the match is
// on the bare name token so the version/extras spec is irrelevant. Only the
// dependency lines are touched — mypy/ruff config that merely mentions the name is
// inert and left as-is.
func dropPyprojectDeps(merged map[string][]byte, deps []string) {
	c, ok := merged["python/pyproject.toml"]
	if !ok {
		return
	}
	drop := make(map[string]struct{}, len(deps))
	for _, d := range deps {
		drop[d] = struct{}{}
	}
	var keep []string
	changed := false
	for _, line := range strings.Split(string(c), "\n") {
		t := strings.TrimSpace(line)
		if eq := strings.Index(t, "="); eq > 0 {
			name := strings.TrimSpace(t[:eq])
			if _, d := drop[name]; d {
				changed = true
				continue
			}
		}
		keep = append(keep, line)
	}
	if changed {
		merged["python/pyproject.toml"] = []byte(strings.Join(keep, "\n"))
	}
}

// prunePythonMypyOverrides rewrites python/pyproject.toml so the mypy
// `[[tool.mypy.overrides]]` module lists name only modules that exist in the
// deliverable. It drops every `services.<platform>.…` entry (platform services the
// deliverable does not ship) and, when no generated service is present, the
// shared.mongo_client.* block too is left intact only if shared/mongo_client ships.
// For each dropped `services.identity.<suffix>` entry it substitutes the equivalent
// `services.<generated>.<suffix>` entry for every generated service, so the mypy
// relaxations that the platform applied to its identity/repository/migration
// handlers/repos/wire/__main__ also apply to the generated service(s). Entries that
// are not service-scoped (shared.*, grpc.*, google.*, …) are preserved verbatim.
func prunePythonMypyOverrides(merged map[string][]byte, generated []string) {
	c, ok := merged["python/pyproject.toml"]
	if !ok {
		return
	}
	platform := make(map[string]struct{}, len(pythonPlatformServices))
	for _, p := range pythonPlatformServices {
		platform[p] = struct{}{}
	}
	// Map a platform-scoped module entry to its generated-service equivalents.
	// "services.identity.infrastructure.grpc_handlers.identity_handler" with a
	// service-named leaf is remapped structurally: the 2nd dotted segment (the
	// service name) is swapped, and any occurrence of the old service name as a
	// path leaf token (…/identity_handler) is swapped to the generated name.
	expand := func(entry string) []string {
		// entry like `    "services.identity.infrastructure...",`
		trimmed := strings.TrimSpace(entry)
		inner := strings.Trim(trimmed, `",`)
		parts := strings.Split(inner, ".")
		if len(parts) < 2 || parts[0] != "services" {
			return []string{entry} // not a service-scoped module — keep verbatim
		}
		svc := parts[1]
		if _, isPlat := platform[svc]; !isPlat {
			return []string{entry} // already a generated/non-platform service — keep
		}
		var out []string
		for _, g := range generated {
			np := make([]string, len(parts))
			copy(np, parts)
			np[1] = g
			// Swap a service-named leaf token (e.g. identity_handler → user_handler).
			for i := range np {
				if strings.HasPrefix(np[i], svc+"_") {
					np[i] = g + strings.TrimPrefix(np[i], svc)
				}
			}
			out = append(out, `    "`+strings.Join(np, ".")+`",`)
		}
		return out // empty when no generated service → entry simply removed
	}

	var outLines []string
	changed := false
	for _, line := range strings.Split(string(c), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, `"services.`) {
			rep := expand(line)
			if len(rep) != 1 || rep[0] != line {
				changed = true
			}
			outLines = append(outLines, rep...)
			continue
		}
		outLines = append(outLines, line)
	}
	if changed {
		merged["python/pyproject.toml"] = []byte(strings.Join(outLines, "\n"))
	}
}

// rewritePythonImportLinter regenerates python/.importlinter so its contracts cover
// the actually-generated service(s) instead of the platform services
// (identity/repository/migration) the skeleton ships. The platform contracts name
// modules (services.identity.domain, …) that do not exist in a single-service
// deliverable, so import-linter would report them as un-enforceable. This emits the
// same three contract shapes the platform uses (domain-independence,
// application-independence, handlers-not-import-repositories) for each generated
// service, preserving the architectural enforcement. A no-op when the file is
// absent or no generated service was discovered (the platform file is then left as
// shipped rather than emptied).
func rewritePythonImportLinter(merged map[string][]byte, generated []string) {
	const key = "python/.importlinter"
	if _, ok := merged[key]; !ok {
		return
	}
	if len(generated) == 0 {
		return
	}
	var sb strings.Builder
	sb.WriteString("[importlinter]\nroot_packages =\n    shared\n    services\ninclude_external_packages = True\n")
	for _, svc := range generated {
		fmt.Fprintf(&sb, `
[importlinter:contract:%s-domain-independence]
name = %s domain may not import ports, application, or infrastructure
type = forbidden
source_modules =
    services.%s.domain
forbidden_modules =
    services.%s.ports
    services.%s.application
    services.%s.infrastructure
    grpc

[importlinter:contract:%s-application-independence]
name = %s application may not import infrastructure, grpc, or motor
type = forbidden
source_modules =
    services.%s.application
forbidden_modules =
    services.%s.infrastructure
    grpc
    motor

[importlinter:contract:%s-handlers-not-import-repositories]
name = %s gRPC handlers may not import repositories directly
type = forbidden
source_modules =
    services.%s.infrastructure.grpc_handlers
forbidden_modules =
    services.%s.infrastructure.repositories
`, svc, svc, svc, svc, svc, svc, svc, svc, svc, svc, svc, svc, svc, svc)
	}
	merged[key] = []byte(sb.String())
}

// dropMongoConfigForSQL rewrites python/shared/config/loader.py for a Python+SQL
// deliverable: it removes the MongoConfig settings class and the
// BaseServiceConfig.mongo field, so the shipped config models no Mongo env
// (MONGO_URI/MONGO_DATABASE) for a store the SQLAlchemy deliverable never uses. The
// SQLAlchemy session/engine config arrives via the generated artifacts. It also
// prunes the MongoConfig usage from the shared config test so the test suite still
// imports. No-op when loader.py is absent.
func dropMongoConfigForSQL(merged map[string][]byte) {
	const loaderKey = "python/shared/config/loader.py"
	if c, ok := merged[loaderKey]; ok {
		merged[loaderKey] = []byte(removePyMongoConfig(string(c)))
	}
	// Prune any MongoConfig reference from the shared config test.
	const testKey = "python/shared/tests/test_config.py"
	if c, ok := merged[testKey]; ok {
		if cleaned, changed := removePyMongoConfigTest(string(c)); changed {
			merged[testKey] = []byte(cleaned)
		}
	}
	// Also handle the __init__.py re-export of MongoConfig.
	const initKey = "python/shared/config/__init__.py"
	if c, ok := merged[initKey]; ok {
		var keep []string
		changed := false
		for _, line := range strings.Split(string(c), "\n") {
			if strings.Contains(line, "MongoConfig") {
				changed = true
				continue
			}
			keep = append(keep, line)
		}
		if changed {
			merged[initKey] = []byte(strings.Join(keep, "\n"))
		}
	}
}

// removePyMongoConfig removes the `class MongoConfig(...)` block (up to the next
// top-level `class ` / EOF) and the `mongo: MongoConfig = …` field line from a
// loader.py body. Conservative line-walk: a top-level class block is everything
// from its `class X` line to the next line that begins at column 0 with `class `.
func removePyMongoConfig(body string) string {
	lines := strings.Split(body, "\n")
	var out []string
	skipping := false
	for _, line := range lines {
		if strings.HasPrefix(line, "class MongoConfig(") {
			skipping = true
			continue
		}
		if skipping {
			// End the skip when a new top-level class/def starts.
			if strings.HasPrefix(line, "class ") || strings.HasPrefix(line, "def ") {
				skipping = false
			} else {
				continue
			}
		}
		// Drop the BaseServiceConfig.mongo field referencing MongoConfig.
		if strings.Contains(line, "MongoConfig") && strings.Contains(strings.TrimSpace(line), "mongo:") {
			continue
		}
		out = append(out, line)
	}
	// Collapse any run of 3+ blank lines left by the removal to 2.
	joined := strings.Join(out, "\n")
	for strings.Contains(joined, "\n\n\n\n") {
		joined = strings.ReplaceAll(joined, "\n\n\n\n", "\n\n\n")
	}
	return joined
}

// removePyMongoConfigTest drops MongoConfig-referencing lines/blocks from the
// shared config test so it still imports after MongoConfig is removed. Any test
// function whose name contains "mongo" is dropped wholesale; any remaining stray
// MongoConfig reference line is dropped. Returns the rewritten body and whether
// anything changed.
func removePyMongoConfigTest(body string) (string, bool) {
	lines := strings.Split(body, "\n")
	var out []string
	skipping := false
	changed := false
	for _, line := range lines {
		if strings.HasPrefix(line, "def test_") && strings.Contains(strings.ToLower(line), "mongo") {
			skipping = true
			changed = true
			continue
		}
		if skipping {
			if strings.HasPrefix(line, "def ") || (len(line) > 0 && line[0] != ' ' && line[0] != '\t') {
				skipping = false
			} else {
				continue
			}
		}
		if strings.Contains(line, "MongoConfig") {
			changed = true
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), changed
}

// protoImportRe matches a proto `import "<path>";` (incl. `import public "…";`
// and `import weak "…";`), capturing the import path (the protoc include string).
var protoImportRe = regexp.MustCompile(`(?m)^\s*import\s+(?:public\s+|weak\s+)?"([^"]+)"\s*;`)

// resolveProtoImportClosure makes the deliverable's proto set self-contained: for
// every .proto already under protobuf/proto/ in merged, it follows the `import`
// graph and pulls any imported .proto that is missing from merged out of the
// canonical skeleton tree (skeletonRoot/protobuf/proto/<import>). It recurses into
// the freshly added protos so transitive deps (e.g. pagination → openapiv3
// annotations → openapiv3/OpenAPIv3 + google/protobuf/descriptor) are all resolved.
//
// The import path IS the protoc include string, which maps 1:1 to the on-disk path
// under protobuf/proto/, so resolution is a direct file read. Imports already
// present (the google well-known-types the agent vendored, the service's own
// protos) are skipped. An import with no canonical source on disk is left missing
// (it is then either a generated-only proto already shipped, or a genuine gap the
// build will surface) — this only ADDS files, never removes, so it cannot regress a
// working deliverable.
func resolveProtoImportClosure(merged map[string][]byte, skeletonRoot string) {
	const root = "protobuf/proto/"
	// Seed the work queue with the import paths (relative to protobuf/proto/) of
	// every proto currently in the deliverable.
	queue := make([]string, 0)
	enqueueImports := func(content []byte) {
		for _, m := range protoImportRe.FindAllSubmatch(content, -1) {
			queue = append(queue, string(m[1]))
		}
	}
	for p, c := range merged {
		if strings.HasPrefix(p, root) && strings.HasSuffix(p, ".proto") {
			enqueueImports(c)
		}
	}

	seen := make(map[string]struct{})
	for len(queue) > 0 {
		imp := queue[0]
		queue = queue[1:]
		if _, done := seen[imp]; done {
			continue
		}
		seen[imp] = struct{}{}

		canonical := root + imp
		if _, present := merged[canonical]; present {
			continue // already shipped (vendored / generated / earlier pass)
		}
		// Pull from the canonical skeleton tree.
		abs := filepath.Join(skeletonRoot, filepath.FromSlash(canonical))
		data, err := os.ReadFile(abs)
		if err != nil {
			continue // no canonical source — leave missing, do not fabricate
		}
		merged[canonical] = data
		enqueueImports(data) // resolve this proto's own imports too
	}
}

// serviceProtoSeeds returns the canonical-path (protobuf/proto/…) keys of the
// generated SERVICE protos in merged: the *_service.proto files under
// protobuf/proto/milton_prism/services/, or, when the agent named them
// differently, every proto under a /services/ path. These are the roots of the
// import closure — a deliverable ships exactly the protos reachable from its own
// service definitions. When no service proto is present (e.g. a deliverable that
// vendors only types), every proto in merged is treated as a seed so the prune is
// a safe no-op rather than dropping everything.
func serviceProtoSeeds(merged map[string][]byte) []string {
	const root = "protobuf/proto/"
	var seeds []string
	for p := range merged {
		if !strings.HasPrefix(p, root) || !strings.HasSuffix(p, ".proto") {
			continue
		}
		rel := strings.TrimPrefix(p, root)
		if strings.HasPrefix(rel, "milton_prism/services/") {
			seeds = append(seeds, p)
		}
	}
	return seeds
}

// protoImportClosureSet returns the set of canonical-path (protobuf/proto/…) keys
// that are in the transitive import closure of the given seed protos, restricted
// to protos actually present in merged. It is the read-only companion of
// resolveProtoImportClosure (which ADDS missing imports): this only walks what is
// already in merged, so it must run AFTER resolveProtoImportClosure has completed
// the tree. The seeds themselves are always included.
func protoImportClosureSet(merged map[string][]byte, seeds []string) map[string]struct{} {
	const root = "protobuf/proto/"
	keep := make(map[string]struct{})
	queue := append([]string(nil), seeds...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if _, done := keep[cur]; done {
			continue
		}
		keep[cur] = struct{}{}
		content, ok := merged[cur]
		if !ok {
			continue
		}
		for _, m := range protoImportRe.FindAllSubmatch(content, -1) {
			imp := root + string(m[1])
			if _, present := merged[imp]; present {
				if _, done := keep[imp]; !done {
					queue = append(queue, imp)
				}
			}
		}
	}
	return keep
}

// pruneOverVendoredProtos drops every .proto under protobuf/proto/ that is NOT in
// the transitive import closure of a generated service proto. The agent vendors
// well-known-type / google.api bundles wholesale, dragging in protos no service
// imports (the audit measured 14/26 dead protos in a Rust deliverable, e.g.
// cpp_features.proto, go_features.proto, java_features.proto, google/protobuf/{api,
// type,struct,empty,wrappers,field_mask,duration,source_context}.proto,
// google/api/{client,httpbody,launch_stage}.proto). Pruning to the exact closure
// guarantees the proto set is precisely "every transitively-imported proto, nothing
// more". It is a no-op when no service proto seeds the closure (so a types-only
// vendor bundle is never wholesale-deleted). Runs AFTER resolveProtoImportClosure so
// the closure it walks is already complete.
func pruneOverVendoredProtos(merged map[string][]byte) {
	const root = "protobuf/proto/"
	seeds := serviceProtoSeeds(merged)
	if len(seeds) == 0 {
		return // no service proto to anchor the closure — do not prune
	}
	keep := protoImportClosureSet(merged, seeds)
	for p := range merged {
		if !strings.HasPrefix(p, root) || !strings.HasSuffix(p, ".proto") {
			continue
		}
		if _, in := keep[p]; !in {
			delete(merged, p)
		}
	}
}

// shipBufLockAndCleanBufYaml makes the shipped buf module self-consistent and
// resolvable for every deliverable that carries a buf.yaml:
//   - it pulls protobuf/buf.lock from the canonical skeleton root into merged so
//     the module's remote deps (buf.build/googleapis/googleapis,
//     buf.build/bufbuild/protovalidate) resolve — the google/* imports come from
//     those deps, not from disk, and `buf generate`/`buf build` fail without the
//     lock. The skeleton file filters never admit buf.lock, so it is added here.
//   - it strips dangling lint-ignore entries from the shipped buf.yaml: the
//     platform buf.yaml ignores proto/openapi-spec.proto (a platform-only proto
//     that no deliverable ships) and proto/openapiv3 (only relevant when the
//     openapiv3 dir is present). A `buf lint` over the deliverable errors on an
//     ignore path that does not exist, so each ignore entry whose target is absent
//     from the shipped proto tree is removed.
func shipBufLockAndCleanBufYaml(merged map[string][]byte, skeletonRoot string) {
	if _, hasYaml := merged["protobuf/buf.yaml"]; !hasYaml {
		return // no buf module shipped in this deliverable
	}
	// Ship buf.lock so remote deps resolve. Prefer the on-disk copy from the
	// skeleton root (always the current lock), but fall back to the embedded
	// build-time copy when the disk read fails — the bind-mounted lock can be a
	// 0600 file owned by another uid that the distroless container cannot read
	// (EACCES), and without this fallback buf.lock silently never ships, leaving
	// the deliverable's buf module unable to resolve its remote buf.build deps.
	if _, has := merged["protobuf/buf.lock"]; !has {
		abs := filepath.Join(skeletonRoot, "protobuf", "buf.lock")
		if data, err := os.ReadFile(abs); err == nil {
			merged["protobuf/buf.lock"] = data
		} else if len(embeddedBufLock) > 0 {
			applog.Warningf("assembler: buf.lock unreadable at %s (%v) — shipping embedded canonical copy so remote buf deps stay resolvable", abs, err)
			merged["protobuf/buf.lock"] = embeddedBufLock
		}
	}
	// Clean dangling ignore entries from buf.yaml.
	if cleaned, changed := cleanBufYamlIgnores(string(merged["protobuf/buf.yaml"]), merged); changed {
		merged["protobuf/buf.yaml"] = []byte(cleaned)
	}
}

// cleanBufYamlIgnores removes lint `ignore:` list entries from a buf.yaml body
// whose target path does not exist in the shipped proto tree. The buf.yaml lives
// at protobuf/buf.yaml with module path `proto`, so an ignore value `proto/<x>`
// refers to protobuf/proto/<x> in merged. An entry is dropped when neither
// protobuf/proto/<x> (a file) nor any protobuf/proto/<x>/… (a dir) is present.
// This targets the platform's stale ignores (proto/openapi-spec.proto, and
// proto/openapiv3 when the openapiv3 dir was pruned) without touching live ones.
// Returns the rewritten body and whether anything changed.
func cleanBufYamlIgnores(body string, merged map[string][]byte) (string, bool) {
	protoPathPresent := func(modulePath string) bool {
		// modulePath like "proto/openapiv3" → on-disk-in-merged "protobuf/proto/openapiv3".
		full := "protobuf/" + strings.TrimSpace(modulePath)
		if _, ok := merged[full]; ok {
			return true
		}
		prefix := full + "/"
		for p := range merged {
			if strings.HasPrefix(p, prefix) {
				return true
			}
		}
		return false
	}
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// A lint ignore list item: "- proto/<something>".
		if strings.HasPrefix(trimmed, "- proto/") {
			target := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if !protoPathPresent(target) {
				changed = true
				continue // drop the dangling ignore entry
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), changed
}

// sortFiles sorts a File slice by path for deterministic output.
func sortFiles(files []File) {
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].Path < files[j-1].Path; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
}
