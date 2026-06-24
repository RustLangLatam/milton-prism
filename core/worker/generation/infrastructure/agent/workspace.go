package agent

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	applog "milton_prism/pkg/log"
)

// maxWorkspaceFileBytes is the file size threshold above which a file is not
// copied into the generation workspace. No legitimate .go or .proto file
// approaches this limit; compiled binaries and data blobs regularly exceed it.
// This acts as a universal backstop so future large files are silently excluded
// without needing to update any exclusion list.
const maxWorkspaceFileBytes = 512 * 1024 // 512 KiB

// workspaceExcludes are top-level directory names skipped when copying the
// monorepo into an ephemeral generation workspace. They contain no Go source
// needed for compilation, or are large enough to hurt copy performance.
var workspaceExcludes = []string{
	".git",
	".frontend", // stale frontend copy with ~200 MB of node_modules
	"frontend",
	"infra",
	"bin", // compiled worker binaries (~35 MB each)
}

// serviceArtifactDirs returns workspace-relative directory paths that a
// successful generation creates for the given service. These are removed
// before the agent runs so it starts with a clean slate.
func serviceArtifactDirs(serviceName string) []string {
	return []string{
		filepath.Join("core", "services", serviceName),
		filepath.Join("core", "cmd", serviceName+"-services"),
		filepath.Join("protobuf", "proto", "milton_prism", "types", serviceName),
		filepath.Join("protobuf", "proto", "milton_prism", "services", serviceName),
		filepath.Join("pkg", "pb", "gen", "milton_prism", "types", serviceName),
		filepath.Join("pkg", "pb", "gen", "milton_prism", "services", serviceName),
	}
}

// serviceArtifactFiles returns workspace-relative individual files that a
// successful generation creates for the given service.
func serviceArtifactFiles(serviceName string) []string {
	return []string{
		filepath.Join("pkg", "gateway", "common", "error", serviceName+"_errors.go"),
	}
}

// PrepareWorkspace copies the monorepo at baseDir to a fresh temp directory,
// removes pre-existing artifacts for serviceName (so the agent starts clean),
// and returns the workspace path plus a cleanup function that must be deferred.
// tempBaseDir controls where the temp dir is created; pass "" to use the OS
// default (/tmp). When running inside Docker (DooD), pass the host-mapped
// shared workspace path so the Docker daemon can resolve the bind mount.
func PrepareWorkspace(baseDir, serviceName, tempBaseDir string) (workspaceDir string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp(tempBaseDir, "prism-gen-"+serviceName+"-*")
	if err != nil {
		return "", nil, fmt.Errorf("workspace: mktemp: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	if err := copyMonorepo(baseDir, tmpDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("workspace: copy: %w", err)
	}

	// The generation-worker runs as root (uid=0); os.MkdirTemp and copyMonorepo
	// create directories owned by root. The agent container runs as prism
	// (uid=1000), which is "other" relative to root-owned dirs. Without explicit
	// write permission for "other", the agent cannot create new service files.
	// chmod 0777 grants write access; the workspace is ephemeral (cleaned up
	// immediately after the job), so the wide permission is safe here.
	if err := chmodWorkspaceDirs(tmpDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("workspace: chmod dirs: %w", err)
	}

	// Remove service-specific artifacts so the agent generates them fresh.
	for _, rel := range serviceArtifactDirs(serviceName) {
		if err := os.RemoveAll(filepath.Join(tmpDir, rel)); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("workspace: remove %s: %w", rel, err)
		}
	}
	for _, rel := range serviceArtifactFiles(serviceName) {
		path := filepath.Join(tmpDir, rel)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanup()
			return "", nil, fmt.Errorf("workspace: remove %s: %w", rel, err)
		}
	}

	// Patch the gateway error lookup to remove any reference to the service
	// being regenerated — the agent re-adds it as part of its generation.
	if err := removeServiceFromErrorLookup(tmpDir, serviceName); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("workspace: patch message_error.go: %w", err)
	}

	return tmpDir, cleanup, nil
}

// removeServiceFromErrorLookup removes the "<service>ErrorMessages," line from
// the lookupErrorMessage function in message_error.go so the workspace compiles
// cleanly before the agent regenerates the gateway error file.
func removeServiceFromErrorLookup(workspaceDir, serviceName string) error {
	path := filepath.Join(workspaceDir, "pkg", "gateway", "common", "error", "message_error.go")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	target := serviceName + "ErrorMessages,"
	lines := strings.Split(string(data), "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if !strings.Contains(line, target) {
			filtered = append(filtered, line)
		}
	}
	if len(filtered) == len(lines) {
		return nil // nothing to patch
	}
	return os.WriteFile(path, []byte(strings.Join(filtered, "\n")), 0644)
}

// fileSnapshot records mtime for every file under dir.
type fileSnapshot map[string]time.Time

// snapshotFiles walks dir and records the mtime of each regular file.
// Paths in the returned map are relative to dir.
func snapshotFiles(dir string) (fileSnapshot, error) {
	snap := make(fileSnapshot)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		snap[rel] = info.ModTime()
		return nil
	})
	return snap, err
}

// diffFiles returns paths that appear in after but not in before, or whose
// mtime is strictly after every mtime in before (new or modified files).
func diffFiles(before, after fileSnapshot) []string {
	var out []string
	for rel, mt := range after {
		if _, existed := before[rel]; !existed {
			out = append(out, rel)
			continue
		}
		if mt.After(before[rel]) {
			out = append(out, rel)
		}
	}
	return out
}

// chmodWorkspaceDirs walks dir and sets every directory to 0777 so the agent
// container (uid=1000, prism) can create files in directories that were copied
// from the monorepo and are owned by root (uid=0, the generation-worker user).
func chmodWorkspaceDirs(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		return os.Chmod(path, 0777)
	})
}

// copyMonorepo copies baseDir to dstDir, skipping workspaceExcludes and
// root-level binary/archive files that serve no purpose in a code-generation
// workspace (compiled Go binaries, zip archives, etc.).
func copyMonorepo(baseDir, dstDir string) error {
	return filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(baseDir, path)
		if shouldExclude(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip symlinks — the workspace needs no references outside the monorepo.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		// Skip root-level executables (compiled Go binaries) and archives: they
		// are never needed for generation and can be tens of MB each.
		if !d.IsDir() && isRootLevelBinary(rel, d) {
			return nil
		}
		// Universal size cap: no legitimate .go/.proto file exceeds 512 KiB;
		// any file that does is a binary or data blob and must not enter the
		// workspace regardless of its name, location, or extension.
		if !d.IsDir() {
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			if info.Size() > maxWorkspaceFileBytes {
				applog.Warningf("workspace: skip large file rel=%s size=%d bytes (max=%d)",
					rel, info.Size(), maxWorkspaceFileBytes)
				return nil
			}
		}
		dst := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		return copyFile(path, dst)
	})
}

// isRootLevelBinary reports whether rel is a root-level non-directory file
// that should not be copied into a generation workspace. It matches:
//   - known archive extensions (.zip, .tar, .tar.gz, .tar.bz, .tar.bz2)
//   - files with any execute bit set (ELF binaries built with go build)
//
// Only root-level entries (no path separator) are considered so that, e.g.,
// script files inside subdirectories are not accidentally excluded.
func isRootLevelBinary(rel string, d fs.DirEntry) bool {
	if strings.ContainsRune(rel, os.PathSeparator) || d.IsDir() {
		return false
	}
	lower := strings.ToLower(rel)
	for _, ext := range []string{".zip", ".tar", ".tar.gz", ".tar.bz", ".tar.bz2"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	info, err := d.Info()
	if err != nil {
		return false
	}
	return info.Mode()&0111 != 0
}

func shouldExclude(rel string) bool {
	top := strings.SplitN(rel, string(os.PathSeparator), 2)[0]
	for _, ex := range workspaceExcludes {
		if top == ex {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, 32*1024)
	_, err = io.CopyBuffer(out, in, buf)
	return err
}

// promptProfileBindings resolves the language-specific fragments of the
// combined prompt from the output profile label. The generation worker holds
// no per-language templates; this is the only place where the worker is aware
// of the target language, and it derives everything from the profile string so
// adding a language is a profile-doc + mapping change, not a worker rewrite.
//
// Go, Python and Node are certified profiles. Rust (Tonic + gRPC) has a real
// profile doc and generator prompt and is certified by a real containerised run.
// Unknown profiles fall back to Go.
//
// protocol selects the transport variant of a profile. "http" is supported for
// Go, Python, Node and Rust (HTTP-native service: router/Fastify/axum + REST
// handlers, no gRPC server, no gateway); every other (profile, protocol) cell uses the gRPC
// build steps. Empty protocol is treated as "grpc". MUST stay in lockstep with
// the migration service's generatorPromptRef and the worker's
// profileAndPromptForLanguage.
func promptProfileBindings(outputProfile, protocol string) (langLabel, profileDoc, buildSteps string) {
	switch outputProfile {
	case "python":
		if protocol == "http" {
			return "Python (FastAPI HTTP-native)",
				"docs/prism/milton-prism-python-profile.md",
				"write the authoritative .proto WITH google.api.http annotations on every RPC (the OpenAPI is derived from it), write service code exposing a FastAPI app (APIRouter + REST handlers) as the ONLY entrypoint with uvicorn (NO grpc.server, NO add_*Servicer_to_server), run python -m compileall (the build gate) and import the FastAPI app, run pytest."
		}
		return "Python",
			"docs/prism/milton-prism-python-profile.md",
			"write protos, run buf generate, write service code, run ruff/mypy, run pytest."
	case "node":
		if protocol == "http" {
			return "TypeScript (Fastify HTTP-native)",
				"docs/prism/milton-prism-node-profile.md",
				"write the authoritative .proto WITH google.api.http annotations on every RPC (the OpenAPI is derived from it), write service code exposing a Fastify app (registered routes + REST handlers) as the ONLY entrypoint (NO @grpc/grpc-js Server, NO new Server()/addService), run npm install, run tsc --noEmit (the build gate) and import the app, run npm test."
		}
		return "TypeScript (Node)",
			"docs/prism/milton-prism-node-profile.md",
			"write protos, generate TS proto stubs, write service code, run npm install, run tsc (the build gate), run npm test."
	case "rust":
		if protocol == "http" {
			return "Rust (axum HTTP-native)",
				"docs/prism/milton-prism-rust-profile.md",
				"write the authoritative .proto WITH google.api.http annotations on every RPC (the OpenAPI is derived from it), write service code exposing an axum app (Router + REST handlers) on tokio as the ONLY entrypoint (NO tonic::transport::Server, NO add_service, NO build.rs tonic-build server codegen), run cargo build (the build gate), run cargo test."
		}
		return "Rust (Tonic)",
			"docs/prism/milton-prism-rust-profile.md",
			"write protos, write the service code and build.rs (tonic-build codegen), run cargo build (the build gate), run cargo test."
	default:
		if protocol == "http" {
			return "Go (HTTP-native)",
				"docs/prism/milton-prism-go-profile.md",
				"write the authoritative .proto WITH google.api.http annotations on every RPC (the OpenAPI is derived from it), write service code exposing an HTTP-native router + REST/AIP handlers as the ONLY entrypoint (NO gRPC server, NO gateway registration), run go build, run go test."
		}
		return "Go",
			"docs/prism/milton-prism-go-profile.md",
			"write protos, run buf generate, write service code, run go build, run go test."
	}
}

// transportSection returns the prose block injected into the combined prompt that
// pins the wire protocol the generated service must speak. For HTTP it makes the
// HTTP-native contract explicit (router + handlers, no gRPC server, no gateway,
// google.api.http on every RPC); for gRPC it returns the empty string so the gRPC
// prompts (the established behaviour) are unchanged. The block is profile-aware:
// the Python profile gets the FastAPI/uvicorn homologue, the Node profile gets
// the Fastify/tsc homologue and the Rust profile gets the axum/cargo homologue
// (no gRPC server, the language build as the gate) instead of the Go net/http prose.
func transportSection(outputProfile, protocol string) string {
	if protocol != "http" {
		return ""
	}
	if outputProfile == "python" {
		return "## Transport: HTTP (native, FastAPI)\n\n" +
			"This service speaks HTTP, not gRPC. Mandatory constraints:\n" +
			"- The ONLY entrypoint is a FastAPI app (an `app = FastAPI()` plus `APIRouter`) served by uvicorn. Do NOT create a gRPC server, do NOT call `grpc.server(...)` or any `add_*Servicer_to_server`, and do NOT emit any gRPC server bootstrap (`__main__` with `grpc.aio.server()`).\n" +
			"- You MUST still write the authoritative `.proto` at the canonical path `protobuf/proto/milton_prism/services/<svc>/v1/...` with a `google.api.http` annotation on EVERY RPC. The platform derives `docs/openapi.yaml` from those annotations — without them the OpenAPI is empty.\n" +
			"- Model the request/response messages as pydantic models equivalent to the proto messages; you do NOT need `*_pb2.py`/`*_pb2_grpc.py` at runtime when using pydantic.\n" +
			"- Implement REST handlers (path operations) that map 1:1 to the proto RPCs and honour the `google.api.http` routes. Map domain errors to HTTP status codes via the service's error module.\n" +
			"- The build gate is `python -m compileall <source_root>/` + importing the FastAPI `app` + `pytest`. There is NO expectation of a gRPC server, `grpc.server(...)`, or `add_*Servicer_to_server`.\n\n"
	}
	if outputProfile == "node" {
		return "## Transport: HTTP (native, Fastify)\n\n" +
			"This service speaks HTTP, not gRPC. Mandatory constraints:\n" +
			"- The ONLY entrypoint is a Fastify app (a `Fastify()` instance with registered routes) that `listen`s on host:port. Do NOT create a gRPC server, do NOT call `new Server()` (`@grpc/grpc-js`) / `server.addService(...)`, do NOT emit a `*_grpc_pb` server stub, and do NOT register any API gateway.\n" +
			"- You MUST still write the authoritative `.proto` at the canonical path `protobuf/proto/milton_prism/services/<svc>/v1/...` with a `google.api.http` annotation on EVERY RPC. The platform derives `docs/openapi.yaml` from those annotations — without them the OpenAPI is empty.\n" +
			"- Model the request/response messages as TypeScript interfaces/types (derived from the proto or equivalent); you do NOT need the `@grpc/proto-loader` runtime stub or a `*_grpc_pb` server stub when the transport is Fastify.\n" +
			"- Implement Fastify route handlers that map 1:1 to the proto RPCs and honour the `google.api.http` routes (method + path). Map domain errors to HTTP status codes via the service's error module/mapper.\n" +
			"- The build gate is `npm install` + `tsc --noEmit` (strict) + importing the app. There is NO expectation of a gRPC server, `new Server()`, or `addService(...)`.\n\n"
	}
	if outputProfile == "rust" {
		return "## Transport: HTTP (native, axum)\n\n" +
			"This service speaks HTTP, not gRPC. Mandatory constraints:\n" +
			"- The ONLY entrypoint is an axum app (an `axum::Router` with registered routes) served by tokio (`axum::serve` / a `TcpListener`). Do NOT create a tonic gRPC server, do NOT call `tonic::transport::Server::builder()` / `.add_service(...)`, do NOT run tonic-build SERVER codegen in `build.rs`, and do NOT register any API gateway.\n" +
			"- You MUST still write the authoritative `.proto` at the canonical path `protobuf/proto/milton_prism/services/<svc>/v1/...` with a `google.api.http` annotation on EVERY RPC. The platform derives `docs/openapi.yaml` from those annotations — without them the OpenAPI is empty.\n" +
			"- Model the request/response messages as Rust structs (serde `Serialize`/`Deserialize`) equivalent to the proto messages; you do NOT need the tonic-generated server trait at runtime when the transport is axum.\n" +
			"- Implement axum handlers that map 1:1 to the proto RPCs and honour the `google.api.http` routes (method + path). Map domain errors to HTTP status codes via the service's error module (`shared::errors` / a `mapError`-style `IntoResponse`).\n" +
			"- The build gate is `cargo build` (the whole workspace compiles) + `cargo test`. There is NO expectation of a tonic server, `transport::Server`, or `add_service`.\n\n"
	}
	return "## Transport: HTTP (native)\n\n" +
		"This service speaks HTTP, not gRPC. Mandatory constraints:\n" +
		"- The ONLY entrypoint is an HTTP-native router (idiomatic, lightweight — net/http, chi or gin) wired in a `main` that starts an `http.Server`. Do NOT create a gRPC server, do NOT call any `RegisterXxxServer`, and do NOT emit or register any API gateway.\n" +
		"- You MUST still write the authoritative `.proto` at the canonical path `protobuf/proto/milton_prism/services/<svc>/v1/...` with a `google.api.http` annotation on EVERY RPC. The platform derives `docs/openapi.yaml` from those annotations — without them the OpenAPI is empty.\n" +
		"- Implement REST/AIP handlers that map 1:1 to the proto RPCs and honour the `google.api.http` routes. Reuse `pkg/gateway/common/error` for error mapping.\n" +
		"- The build gate is `go build ./...` + `go test ./...`. There is NO expectation of a gRPC health server or `RegisterXxxServer`.\n\n"
}

// authSchemeSection returns the prose block injected into the combined prompt that
// pins the request-authentication scheme the generated service must implement. It is
// the auth homologue of transportSection: profile- and protocol-aware.
//
// v1 GENERATES only JWT and none:
//   - "none"/""  → no auth code; an explicit note that the source authenticated
//     nothing, so endpoints are intentionally unauthenticated.
//   - "jwt"      → idiomatic JWT validation per stack (golang-jwt / PyJWT /
//     jose|jsonwebtoken / jsonwebtoken crate). Common rules: validate the bearer
//     token; read the secret/public key/issuer/audience/expected claims from `.env`
//     (NEVER hardcode a secret/key); fail with a TYPED error mapped to 401; wire it
//     as a gRPC interceptor (gRPC transport) or an HTTP middleware/guard (HTTP).
//   - any other detected scheme (oauth2/session_cookie/api_key/basic) → an HONEST
//     note that the scheme was detected but v1 does not generate it; the agent must
//     NOT guess an implementation. It stubs the boundary and documents the gap.
func authSchemeSection(outputProfile, protocol, authScheme, authSigAlg string) string {
	scheme := strings.ToLower(strings.TrimSpace(authScheme))
	if scheme == "" {
		scheme = "none"
	}
	if scheme == "none" {
		return "## Auth / Validation: none\n\n" +
			"The analysed source performs NO request authentication (honest detection result). " +
			"Do NOT invent an auth layer: generate the endpoints WITHOUT any token/session validation " +
			"middleware or interceptor. (A future migration can opt into JWT via the auth-scheme override.)\n\n"
	}
	if scheme != "jwt" {
		return "## Auth / Validation: " + scheme + " (detected; NOT generated in v1)\n\n" +
			"The analysed source uses **" + scheme + "** authentication. v1 of the generator only emits " +
			"JWT and none — it does NOT generate a " + scheme + " implementation, and you MUST NOT guess one " +
			"(no fabricated OAuth2 flow, session store, API-key table, or Basic realm). Generate the service " +
			"WITHOUT an auth layer and add a single TODO note at the entrypoint stating that `" + scheme + "` " +
			"validation was detected in the source and must be wired manually (or re-run the migration with " +
			"`target_auth_scheme = AUTH_SCHEME_JWT` to generate JWT instead). Be honest about the gap.\n\n"
	}

	// scheme == "jwt": idiomatic, .env-driven, typed-error validation per stack.
	alg := strings.ToUpper(strings.TrimSpace(authSigAlg))
	algLine := "- Accept the signature algorithm family the source used"
	switch {
	case strings.HasPrefix(alg, "HS"):
		algLine = "- The token is signed with a SYMMETRIC secret (" + alg + "): validate with the shared secret read from `.env` (e.g. `JWT_SECRET`). Reject any token whose `alg` header is not in the expected HMAC family (no `alg=none`, no algorithm confusion)."
	case strings.HasPrefix(alg, "RS"), strings.HasPrefix(alg, "ES"), alg == "EDDSA":
		algLine = "- The token is signed with an ASYMMETRIC key (" + alg + "): validate with the PUBLIC key / JWKS read from `.env` (e.g. `JWT_PUBLIC_KEY` path or `JWT_JWKS_URL`). NEVER embed the private key in the service. Reject `alg=none` and any algorithm outside the expected family (no algorithm-confusion downgrade to HMAC)."
	default:
		algLine = "- Validate the token signature using the key material read from `.env` (symmetric `JWT_SECRET` or asymmetric `JWT_PUBLIC_KEY`/JWKS, whichever the config provides). Reject `alg=none` and unexpected algorithms."
	}

	var lib, wire, gate string
	switch outputProfile {
	case "python":
		lib = "PyJWT (`jwt.decode`)"
		if protocol == "http" {
			wire = "a FastAPI dependency / middleware applied to every protected path operation (e.g. `Depends(verify_jwt)`)"
		} else {
			wire = "a gRPC `ServerInterceptor` that runs before every RPC handler"
		}
		gate = "`python -m compileall` + importing the app + `pytest`"
	case "node":
		lib = "`jsonwebtoken` (or `jose`)"
		if protocol == "http" {
			wire = "a Fastify `preHandler` hook / plugin registered on the protected routes"
		} else {
			wire = "a gRPC server interceptor invoked before every handler"
		}
		gate = "`npm install` + `tsc --noEmit`"
	case "rust":
		lib = "the `jsonwebtoken` crate"
		if protocol == "http" {
			wire = "an axum middleware (`tower`/`axum::middleware::from_fn`) or an extractor applied to the protected `Router`"
		} else {
			wire = "a tonic `Interceptor` attached to the service"
		}
		gate = "`cargo build` + `cargo test`"
	default: // go
		lib = "`github.com/golang-jwt/jwt/v5`"
		if protocol == "http" {
			wire = "an `http.Handler` middleware wrapping the protected routes"
		} else {
			wire = "a `grpc.UnaryServerInterceptor` (and stream interceptor) on the server"
		}
		gate = "`go build ./...` + `go test ./...`"
	}

	return "## Auth / Validation: JWT\n\n" +
		"The analysed source authenticates requests with **JWT bearer tokens**. Generate JWT validation " +
		"for this service using " + lib + ". Mandatory constraints:\n" +
		"- Read the bearer token from the `Authorization: Bearer <token>` header.\n" +
		algLine + "\n" +
		"- Read ALL secrets/keys, the issuer (`iss`), audience (`aud`), and any required claims from `.env` / environment variables. NEVER hardcode a secret, key, issuer, or audience in source — a hardcoded credential is a generation defect.\n" +
		"- Verify the standard claims (`exp` not expired, `nbf`/`iat` sane, and `iss`/`aud` when configured).\n" +
		"- On any validation failure return a TYPED error from the service's error module (a dedicated `Failure_Unauthenticated`-style code) mapped to HTTP 401 / gRPC `UNAUTHENTICATED`. Do NOT leak the reason or the token.\n" +
		"- Wire the validation as " + wire + " so every protected endpoint is covered uniformly; expose the authenticated identity (e.g. the `sub` claim) to the handlers via the request context.\n" +
		"- Add a `.env.example` entry for every auth variable you read (e.g. `JWT_SECRET=`, `JWT_PUBLIC_KEY=`, `JWT_ISSUER=`, `JWT_AUDIENCE=`) so the service documents its own configuration.\n" +
		"- The validation code MUST be part of the build gate (" + gate + "): it compiles and is exercised by at least one unit test (valid token passes, missing/expired/wrong-signature token is rejected).\n\n"
}

// sqlStore describes a SQL persistence cell as an (ORM, driver) pair. The prompt
// block is assembled from these parts by ormStoreSection, so the same ORM-SQL
// scaffold serves every wire-compatible engine (one set of GORM models/repos for
// PostgreSQL AND MySQL/MariaDB) and the pattern is reusable when SQLAlchemy /
// Prisma / SeaORM cells land for Python / Node / Rust: only the (orm, driver,
// dialect) facts change, the surrounding "models in infra, repos implement the
// ports, mapping domain↔model, AutoMigrate from the models" shape is constant.
type sqlStore struct {
	engine     string // human label, e.g. "PostgreSQL", "MySQL/MariaDB"
	driverPkg  string // GORM driver import path
	driverCtor string // GORM dialector constructor, e.g. "postgres.Open(dsn)"
	dsnExample string // a placeholder DSN example for the .env note
}

// goSQLStores maps the worker store token to its (ORM, driver) facts for the Go
// profile. POSTGRES→postgres token, MARIADB→mysql token (see databaseStoreToken).
// Both rows reuse the SAME GORM models/repos — only the driver row differs.
var goSQLStores = map[string]sqlStore{
	"postgres": {
		engine:     "PostgreSQL",
		driverPkg:  "gorm.io/driver/postgres",
		driverCtor: "postgres.Open(dsn)",
		dsnExample: "postgres://user:password@host:5432/<svc>_db?sslmode=disable",
	},
	"mysql": {
		engine:     "MySQL/MariaDB",
		driverPkg:  "gorm.io/driver/mysql",
		driverCtor: "mysql.Open(dsn)",
		dsnExample: "user:password@tcp(host:3306)/<svc>_db?charset=utf8mb4&parseTime=True&loc=Local",
	},
}

// pySQLAlchemyStore describes a Python SQLAlchemy persistence cell as an
// (engine, async driver, URL scheme) triple. It is the SQLAlchemy homologue of
// sqlStore (Go-GORM): the prompt block is assembled by sqlAlchemyStoreSection so
// the same SQLAlchemy DeclarativeBase models/repos serve every wire-compatible
// engine (one set of models/repos for PostgreSQL AND MySQL/MariaDB) and only the
// (driver pip dependency, async URL scheme, DSN example) facts change per store.
type pySQLAlchemyStore struct {
	engine     string // human label, e.g. "PostgreSQL", "MySQL/MariaDB"
	driverPkg  string // pip dependency for the async driver, e.g. "asyncpg"
	urlScheme  string // SQLAlchemy async URL scheme, e.g. "postgresql+asyncpg"
	dsnExample string // a placeholder DATABASE_URL example for the .env note
}

// pySQLAlchemyStores maps the worker store token to its (SQLAlchemy engine, async
// driver) facts for the Python profile. POSTGRES→postgres token, MARIADB→mysql
// token (see databaseStoreToken) — the SAME homologation as goSQLStores. Both
// rows reuse the SAME SQLAlchemy models/repos; only the async driver + URL scheme
// differ, exactly as the Go-GORM cell only changes its driver import + DSN.
var pySQLAlchemyStores = map[string]pySQLAlchemyStore{
	"postgres": {
		engine:     "PostgreSQL",
		driverPkg:  "asyncpg",
		urlScheme:  "postgresql+asyncpg",
		dsnExample: "postgresql+asyncpg://user:password@host:5432/<svc>_db",
	},
	"mysql": {
		engine:     "MySQL/MariaDB",
		driverPkg:  "aiomysql",
		urlScheme:  "mysql+aiomysql",
		dsnExample: "mysql+aiomysql://user:password@host:3306/<svc>_db?charset=utf8mb4",
	},
}

// storeSection returns the prose block injected into the combined prompt that
// pins the persistence engine the generated service must target. It is the store
// homologue of transportSection / authSchemeSection: profile- and store-aware.
//
// v1 GENERATES SQL persistence for Go (via GORM) AND Python (via SQLAlchemy 2.0
// async) on + {PostgreSQL, MySQL/MariaDB}; "mongodb" (the original path) injects
// nothing so the established Mongo behaviour is unchanged:
//   - "mongodb"/"" → no block; the profile doc's MongoDB persistence is used as-is.
//   - (go, "postgres" | "mysql") → a GORM persistence layer (ormStoreSection):
//     GORM models in infrastructure/repositories mapping to/from the domain types,
//     repos implementing the SAME ports, a gorm_client builder that opens the
//     connection with the driver chosen by store, AutoMigrate, gorm.DeletedAt
//     soft-delete, autoincrement IDs, .env with DATABASE_URL/DB_*.
//   - (python, "postgres" | "mysql") → a SQLAlchemy 2.0 async persistence layer
//     (sqlAlchemyStoreSection): DeclarativeBase models in infrastructure/repositories
//     mapping to/from the domain types, repos implementing the SAME ports, an async
//     engine builder selecting the driver/URL by store, create_all schema, nullable
//     soft-delete column, autoincrement IDs, .env with DATABASE_URL/DB_*.
//   - any other (profile, store) SQL cell → an HONEST note that SQL for that cell
//     is a v1 hole and must not be guessed (this path is unreachable while the
//     IsGenerableDatabase guard rejects those cells at creation, but kept so the
//     prompt is self-consistent if the guard is ever relaxed).
func storeSection(outputProfile, store string) string {
	s := strings.ToLower(strings.TrimSpace(store))
	if s == "" || s == "mongodb" {
		return ""
	}
	// Go + a known SQL store → GORM. Python + a known SQL store → SQLAlchemy. Every
	// other (profile, store) SQL cell is a v1 hole.
	if outputProfile == "go" {
		if cell, ok := goSQLStores[s]; ok {
			return ormStoreSection(cell)
		}
	}
	if outputProfile == "python" {
		if cell, ok := pySQLAlchemyStores[s]; ok {
			return sqlAlchemyStoreSection(cell)
		}
	}
	return "## Persistence: " + s + " (selected; NOT generated in v1)\n\n" +
		"The target database for this migration is **" + s + "** on the **" + outputProfile +
		"** profile, which v1 of the generator does NOT emit (v1 generates SQL persistence " +
		"for Go (GORM) and Python (SQLAlchemy) on PostgreSQL and MySQL/MariaDB; every other " +
		"language uses MongoDB). Do NOT guess a " + s + " implementation. Generate the MongoDB " +
		"persistence layer as the profile doc describes and add a single TODO note stating that `" +
		s + "` was requested but is a v1 generation hole and must be wired manually. Be honest " +
		"about the gap.\n\n"
}

// ormStoreSection renders the GORM persistence block for one SQL cell. The text
// is parametrised by the (ORM, driver) facts so PostgreSQL and MySQL/MariaDB share
// one scaffold (one set of GORM models/repos, only the driver import + DSN differ),
// keeping the pattern reusable for future ORM cells in other languages.
func ormStoreSection(c sqlStore) string {
	return "## Persistence: " + c.engine + " (GORM ORM)\n\n" +
		"This service persists to **" + c.engine + "** via the **GORM** ORM (`gorm.io/gorm`), NOT MongoDB. " +
		"Replace the MongoDB persistence layer the profile doc describes with an idiomatic GORM layer. Mandatory constraints:\n" +
		"- Use **GORM** (`gorm.io/gorm`) with the driver **`" + c.driverPkg + "`**. Open the connection with `gorm.Open(" + c.driverCtor + ", &gorm.Config{})`. Do NOT use raw SQL/pgx or another ORM — GORM is the canon for this cell, and the same models/repos serve PostgreSQL and MySQL/MariaDB unchanged (only the driver import + DSN differ).\n" +
		"- **Domain stays proto.** Domain types remain aliases of the proto messages (Canon §5.1). The GORM **models are SEPARATE structs with `gorm` tags and live in `core/services/<svc>/infrastructure/repositories`** (NEVER in domain). Each repository maps domain↔GORM-model on read/write — domain is never decorated with ORM tags.\n" +
		"- For EACH owned resource (a proto message in `owned_resources`) write a GORM model + a repository `core/services/<svc>/infrastructure/repositories/gorm_<resource>_repository.go` that implements the SAME repository ports the service already defines (assert `var _ ports.<Resource>Repository = (*Gorm<Resource>Repository)(nil)`; same method signatures the gRPC/HTTP handlers depend on) — only the implementation changes from Mongo to GORM.\n" +
		"- Add a shared client `core/shared/gorm_client/builder.go` that builds the `*gorm.DB` once (sync.Once) from config, selects the driver by config/store, configures the connection pool via `db.DB()` (`SetMaxOpenConns`/`SetMaxIdleConns`/`SetConnMaxLifetime`), and pings on startup (the Mongo-client homologue). Wire it where the Mongo client was wired.\n" +
		"- Add a transaction manager behind a `WithTransaction(ctx, fn)` API over `db.Transaction(...)` (GORM transactions, ctx-scoped `*gorm.DB`), nil-safe and mirroring the existing Mongo transaction abstraction, so service-layer transaction boundaries are unchanged.\n" +
		"- **Schema via `AutoMigrate`.** On startup the client runs `db.AutoMigrate(&Model{}, ...)` over every GORM model so the schema is derived from the models — do NOT hand-write golang-migrate `*.sql` files and do NOT emulate a `system_counters` collection. Model FK columns/indexes come from the `cross_service_fks` in the boundary spec (FK columns/indexes only, never a hard cross-service FK constraint, per the data-ownership boundary).\n" +
		"- **IDs** are autoincrement by the ORM: model PK is `ID uint64 \\`gorm:\"primaryKey;autoIncrement\"\\`` (Canon §5.3) — never an emulated counter. Use snake_case table/column names (GORM's default naming).\n" +
		"- **Soft-delete** with `gorm.DeletedAt` (embed `gorm.DeletedAt \\`gorm:\"index\"\\`` or `gorm.Model`) so deletes are logical, matching the Mongo path's soft-delete semantics.\n" +
		"- Read the connection config from `.env` / environment: emit a `.env.example` with `DATABASE_URL` (e.g. `" + c.dsnExample + "`) and/or the discrete `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD`/`DB_NAME` variables. NEVER hardcode a password — a hardcoded credential is a generation defect. Do NOT emit any `MONGO_*` variable.\n" +
		"- Ensure `go.mod` requires `gorm.io/gorm` and `" + c.driverPkg + "`. The persistence code MUST be part of the build gate (`go build ./...` + `go test ./...`): the repos compile and at least one repository round-trip is exercised (a `sqlmock`/in-memory or container-backed test is acceptable).\n\n"
}

// sqlAlchemyStoreSection renders the SQLAlchemy 2.0 (async) persistence block for
// one Python SQL cell. It is the Python homologue of ormStoreSection (Go-GORM):
// the text is parametrised by the (engine, async driver, URL scheme) facts so
// PostgreSQL and MySQL/MariaDB share one scaffold (one set of DeclarativeBase
// models/repos, only the driver dependency + async URL scheme differ), keeping the
// "models in infra, repos implement the ports, mapping domain↔model, schema from
// the models" shape identical across languages.
func sqlAlchemyStoreSection(c pySQLAlchemyStore) string {
	return "## Persistence: " + c.engine + " (SQLAlchemy 2.0 async)\n\n" +
		"This service persists to **" + c.engine + "** via the **SQLAlchemy 2.0 async ORM** (`sqlalchemy[asyncio]`), NOT MongoDB. " +
		"Replace the MongoDB persistence layer (Motor/pymongo) the profile doc describes with an idiomatic async SQLAlchemy layer. Mandatory constraints:\n" +
		"- Use **SQLAlchemy 2.0 in async mode** (`sqlalchemy.ext.asyncio`: `create_async_engine`, `AsyncSession`, `async_sessionmaker`) with the async driver **`" + c.driverPkg + "`**. Build the engine from the URL scheme **`" + c.urlScheme + "://…`**. Do NOT use raw SQL/psycopg2 sync, Motor, or another ORM — SQLAlchemy is the canon for this cell, and the SAME models/repos serve PostgreSQL and MySQL/MariaDB unchanged (only the driver dependency + URL scheme differ; the dialect is SQLAlchemy's job).\n" +
		"- **Domain stays proto.** Domain types remain aliases of the proto messages / dataclasses (Canon §5.1). The SQLAlchemy **models are SEPARATE `DeclarativeBase` mapped classes (`Mapped[...]` / `mapped_column(...)`) and live in `services/<svc>/infrastructure/repositories`** (e.g. `models.py`), NEVER in domain. Each repository maps domain↔ORM-model on read/write — domain is never decorated with ORM mappings.\n" +
		"- For EACH owned resource (a proto message in `owned_resources`) write a SQLAlchemy model + a repository `services/<svc>/infrastructure/repositories/sqlalchemy_<resource>_repository.py` that implements the SAME repository `Protocol` ports the service already defines (same async method signatures the gRPC/HTTP handlers depend on) — only the implementation changes from Motor to SQLAlchemy. The async repo uses an injected `AsyncSession` / `async_sessionmaker`.\n" +
		"- Add a shared async engine builder `shared/sqlalchemy_client/engine.py` that builds the `AsyncEngine` once from config (the Motor-client homologue), selects the driver/URL by config/store, configures the pool (`pool_size`/`max_overflow`/`pool_pre_ping=True`), exposes an `async_sessionmaker[AsyncSession]`, and pings on startup (`SELECT 1`). Wire it where the Motor client was wired (in `wire.py`).\n" +
		"- Add an async transaction manager implementing the `TransactionManager` Protocol's `async def with_transaction(self, fn)` over `async with session.begin(): …` (a session-scoped `AsyncSession`), None-safe and mirroring the existing Motor transaction abstraction, so service-layer transaction boundaries are unchanged.\n" +
		"- **Schema via `create_all`.** On startup the engine builder runs `async with engine.begin() as conn: await conn.run_sync(Base.metadata.create_all)` over the DeclarativeBase metadata so the schema is derived from the models (homologue of GORM AutoMigrate) — do NOT hand-write Alembic `versions/*.py` migrations and do NOT emulate a `system_counters` collection. Model FK columns/indexes come from the `cross_service_fks` in the boundary spec (FK columns/indexes only, never a hard cross-service FK constraint, per the data-ownership boundary).\n" +
		"- **IDs** are autoincrement by the ORM: the model PK is `id: Mapped[int] = mapped_column(primary_key=True)` (SQLAlchemy autoincrements an integer PK; Canon §5.3) — never an emulated counter. Use snake_case `__tablename__`/column names.\n" +
		"- **Soft-delete** with a nullable timestamp column (`delete_time: Mapped[datetime | None] = mapped_column(nullable=True)`); deletes set the column instead of issuing a hard `DELETE`, and reads filter `delete_time IS NULL`, matching the Mongo path's soft-delete semantics.\n" +
		"- Read the connection config from `.env` / environment: emit a `.env.example` with `DATABASE_URL` (e.g. `" + c.dsnExample + "`) and/or the discrete `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD`/`DB_NAME` variables. NEVER hardcode a password — a hardcoded credential is a generation defect. Do NOT emit any `MONGO_*` variable.\n" +
		"- Ensure `pyproject.toml` requires `sqlalchemy[asyncio]` and `" + c.driverPkg + "` (NOT motor/pymongo). The persistence code MUST pass the build gate (`python -m compileall` + importing the app/repos): the repos compile/import and at least one repository round-trip is exercised (an aiosqlite/in-memory or container-backed async test is acceptable).\n\n"
}

// writeCombinedPrompt writes the -p prompt content to workspaceDir/_prompt.md.
// The prompt references the generator prompt file and includes boundary spec
// and proto content inline so the agent has everything without a round-trip.
func writeCombinedPrompt(workspaceDir string, generatorPromptRef, serviceName, errorPrefix, outputProfile, protocol, authScheme, authSigAlg, store, boundarySpec, protoContent string) (string, error) {
	// The combined prompt is profile-parametrised: the worker carries no
	// language-specific templates, so the per-language coupling lives only in
	// the profile doc and the language label resolved here. Defaults to Go.
	// protocol selects the transport variant (gRPC default, HTTP for Go).
	langLabel, profileDoc, buildSteps := promptProfileBindings(outputProfile, protocol)

	var buf bytes.Buffer
	buf.WriteString("You are a code-generation agent. Your task is to materialise a complete ")
	buf.WriteString(langLabel)
	buf.WriteString(" microservice into this workspace by WRITING FILES using the Write and Edit tools. ")
	buf.WriteString("Do NOT output code as text blocks in your response — every file must be created on disk via tool calls.\n\n")
	buf.WriteString("Step 1: Read ")
	buf.WriteString(generatorPromptRef)
	buf.WriteString(" for the complete step-by-step generation workflow.\n")
	buf.WriteString("Step 2: Read docs/prism/milton-prism-architecture-canon.md and ")
	buf.WriteString(profileDoc)
	buf.WriteString(" in full before writing any code.\n")
	buf.WriteString("Step 3: Follow the workflow exactly — ")
	buf.WriteString(buildSteps)
	buf.WriteString("\n\n")
	buf.WriteString("Generate a new service with the following inputs:\n\n")
	buf.WriteString("Service Name: ")
	buf.WriteString(serviceName)
	buf.WriteString("\nError Prefix: ")
	buf.WriteString(errorPrefix)
	buf.WriteString("\nOutput Profile: ")
	buf.WriteString(outputProfile)
	buf.WriteString("\n\n")
	buf.WriteString(transportSection(outputProfile, protocol))
	buf.WriteString(authSchemeSection(outputProfile, protocol, authScheme, authSigAlg))
	buf.WriteString(storeSection(outputProfile, store))
	buf.WriteString("## Boundary Spec\n\n```yaml\n")
	buf.WriteString(strings.TrimSpace(boundarySpec))
	buf.WriteString("\n```\n\n## Proto Contract\n\n```proto\n")
	buf.WriteString(strings.TrimSpace(protoContent))
	buf.WriteString("\n```\n")

	promptPath := filepath.Join(workspaceDir, "_prompt.md")
	if err := os.WriteFile(promptPath, buf.Bytes(), 0644); err != nil {
		return "", err
	}
	return promptPath, nil
}
