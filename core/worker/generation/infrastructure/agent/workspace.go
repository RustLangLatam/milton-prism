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

	"milton_prism/core/worker/generation/ports"
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
	case "java":
		if protocol == "http" {
			return "Java (Spring Boot HTTP-native)",
				"docs/prism/milton-prism-java-profile.md",
				"write the authoritative .proto WITH google.api.http annotations on every RPC (the OpenAPI is derived from it), write service code exposing a Spring Boot application (a `@SpringBootApplication` main + `@RestController` handlers) as the ONLY entrypoint (NO io.grpc.Server, NO ServerBuilder, NO BindableService), run mvn -B package (the build gate), run mvn test."
		}
		return "Java (gRPC, grpc-java)",
			"docs/prism/milton-prism-java-profile.md",
			"write protos, run the protobuf/grpc-java codegen, write the service code (grpc-java BindableService + server bootstrap), run mvn -B package (the build gate), run mvn test."
	case "ruby":
		if protocol == "http" {
			return "Ruby (Rails API / Sinatra HTTP-native)",
				"docs/prism/milton-prism-ruby-profile.md",
				"write the authoritative .proto WITH google.api.http annotations on every RPC (the OpenAPI is derived from it), write service code exposing a Rails API (or Sinatra) application as the ONLY entrypoint (NO GRPC::RpcServer, NO GRPC::GenericService), run bundle install, run bundle exec rspec (the build gate), run ruby -c on every source file."
		}
		return "Ruby (gRPC, grpc gem)",
			"docs/prism/milton-prism-ruby-profile.md",
			"write protos, run grpc_tools_ruby_protoc to emit *_pb.rb + *_services_pb.rb, write the service code (a GRPC::GenericService impl + GRPC::RpcServer bootstrap), run bundle install, run bundle exec rspec (the build gate), run ruby -c on every source file."
	case "csharp":
		if protocol == "http" {
			return "C# (.NET, ASP.NET Core HTTP-native)",
				"docs/prism/milton-prism-csharp-profile.md",
				"write the authoritative .proto WITH google.api.http annotations on every RPC (the OpenAPI is derived from it), write service code exposing an ASP.NET Core Minimal API application as the ONLY entrypoint (NO Grpc.AspNetCore, NO MapGrpcService, NO Grpc.Core.Server), run dotnet build (the build gate), run dotnet test."
		}
		return "C# (.NET, gRPC, grpc-dotnet)",
			"docs/prism/milton-prism-csharp-profile.md",
			"write protos (the .csproj <Protobuf> items run Grpc.Tools protoc + grpc_csharp_plugin at build), write the service code (a grpc-dotnet service subclassing the generated *.*Base + MapGrpcService bootstrap), run dotnet build (the build gate), run dotnet test."
	case "cpp":
		if protocol == "http" {
			return "C++ (Drogon HTTP-native)",
				"docs/prism/milton-prism-cpp-profile.md",
				"write the authoritative .proto WITH google.api.http annotations on every RPC (the OpenAPI is derived from it), write the CMakeLists that runs protoc --cpp_out (messages only, NO grpc_cpp_plugin) via find_package against the PREINSTALLED protobuf (NEVER FetchContent), write service code exposing a Drogon app (HttpController + app().run()) as the ONLY entrypoint (NO grpc::ServerBuilder, NO grpc::Server), run cmake --build (the build gate), run ctest."
		}
		return "C++ (gRPC, grpc++)",
			"docs/prism/milton-prism-cpp-profile.md",
			"write protos, write the CMakeLists that runs protoc --cpp_out + --grpc_out (grpc_cpp_plugin) via find_package against the PREINSTALLED grpc++/protobuf/mongocxx (NEVER FetchContent — re-downloading blows the timeout), write the service code (a grpc++ service subclassing the generated *::Service + grpc::ServerBuilder/grpc::Server bootstrap), run cmake --build (the build gate), run ctest."
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

// verifyCommandFor returns the DETERMINISTIC verification command run as a SECOND
// container pass (same image, same workspace) AFTER the agent finishes. Its exit
// code — NOT Claude's — decides GatesPassed: the generated service must actually
// compile and its tests must pass. It is the behavioural-gate twin of buildSteps:
// buildSteps tells the agent what to do; verifyCommandFor is how the platform
// independently proves the agent did it.
//
// The command is scoped to the generated service subtree so the gate measures the
// service under test, not the whole monorepo (whose unrelated suites may need live
// infra). serviceName is the snake_case service (e.g. "articles").
//
// ok is false for profiles whose deliverable layout inside the workspace has not
// been certified for the deterministic gate yet; the invoker then preserves the
// prior behaviour (GatesPassed from the agent's own exit) for that profile so this
// change never regresses an uncertified language.
//
// All eight output profiles are now wired (each command was derived from a real
// certified deliverable layout and the per-language build/test gate stated in the
// profile docs and in transportSection): Go (core/services/<svc>), Python
// (python/, Poetry), Node (node/, npm + tsc + vitest), Rust (rust/, Cargo
// workspace member), Java (java/, Maven reactor module), Ruby (ruby/, Bundler +
// RSpec), C# (csharp/services/<svc>, dotnet test) and C++ (cpp/, CMake + ctest).
// protocol (grpc|http) does NOT change the command: only the generated code
// differs between transports, the toolchain build+test invocation is identical,
// so the same per-profile command verifies both cells.
//
// The toolchain each command needs is present in the generation-agent image: Go
// is in the base; Rust/Java/Ruby/C#/C++ resolve from the image's warmed offline
// caches (CARGO_HOME, /usr/local/m2, /usr/local/bundle, /usr/local/nuget) or the
// preinstalled grpc++/protobuf/mongocxx C++ stack; Node uses npm; Python (the
// only profile with no warmed cache — the image ships python3 but no
// pip/uv/poetry) bootstraps uv + Poetry over the network (the verify container
// has egress on the generation network) and installs from the deliverable's own
// pyproject, then runs the compile gate + pytest.
func verifyCommandFor(outputProfile, protocol, serviceName string) (cmd string, ok bool) {
	_ = protocol // same build+test invocation for the gRPC and HTTP cells.
	svc := strings.TrimSpace(serviceName)
	if svc == "" {
		return "", false
	}
	switch outputProfile {
	case "", "go":
		// The whole service subtree must compile and every service test must pass.
		// -count=1 disables the test cache so a green is always a fresh run.
		pkg := "./core/services/" + svc + "/..."
		return "go build " + pkg + " && go test " + pkg + " -count=1", true
	case "python":
		// Layout: python/ (Poetry root pyproject.toml, pytest config with
		// pythonpath=[".","gen"]), service at python/services/<svc>, tests at
		// python/services/<svc>/tests. The image has no Python package manager, so
		// bootstrap uv (a single static binary) then Poetry, install the
		// deliverable's deps, run the compile gate (python -m compileall) and pytest.
		return "cd python && export PATH=\"$HOME/.local/bin:$PATH\" && " +
			"{ command -v uv >/dev/null 2>&1 || curl -LsSf https://astral.sh/uv/install.sh | sh; } && " +
			"{ command -v poetry >/dev/null 2>&1 || uv tool install poetry; } && " +
			"poetry install --no-interaction --no-root && " +
			"poetry run python -m compileall -q services/" + svc + " && " +
			"poetry run pytest services/" + svc + "/tests", true
	case "node":
		// Layout: node/ (root package.json with build=tsc --noEmit, test=vitest
		// run, prisma:generate), service at node/services/<svc>. tsc --noEmit is the
		// hard build gate (equivalent to go build); vitest is the test gate. Prisma
		// client must be generated before tsc resolves @prisma/client types; it is
		// best-effort (tsc fails anyway if a needed client is missing).
		return "cd node && npm install && " +
			"(npm run prisma:generate >/dev/null 2>&1 || true) && " +
			"npm run build && npm test", true
	case "rust":
		// Layout: rust/ Cargo workspace, service crate at rust/services/<svc>
		// (its own Cargo.toml). cargo build is the hard build gate; cargo test runs
		// the unit tests. --manifest-path scopes the workspace to the service crate.
		// Deps resolve from the image's warmed CARGO_HOME registry.
		mf := "rust/services/" + svc + "/Cargo.toml"
		return "cargo build --manifest-path " + mf + " && cargo test --manifest-path " + mf, true
	case "java":
		// Layout: java/ Maven reactor (parent pom.xml + one module per service at
		// java/services/<svc>), JUnit 5 tests under src/test/java. The test phase
		// compiles main+test (incl. the protobuf-maven-plugin gRPC codegen) and runs
		// the suite; -pl selects the service module, -am builds its upstream deps.
		// Resolves from the image's warmed /usr/local/m2.
		return "cd java && mvn -B -pl services/" + svc + " -am test", true
	case "ruby":
		// Layout: ruby/ (root Gemfile resolving from the warmed /usr/local/bundle),
		// service app at ruby/services/<svc> with lib/ + spec/ + .rspec. bundle
		// exec rspec is the hard gate (loading every source file syntax-checks it,
		// the ruby -c equivalent). rspec runs from the service dir so its .rspec
		// load paths apply.
		return "cd ruby && bundle install && (cd services/" + svc + " && bundle exec rspec)", true
	case "csharp":
		// Layout: csharp/services/<svc> with the service .csproj + a Tests/ project
		// (xUnit). dotnet test on the Tests project builds it AND the referenced
		// service project (the Grpc.Tools codegen + Roslyn compile = the build gate)
		// and runs the suite. Restores from the image's warmed /usr/local/nuget.
		return "cd csharp/services/" + svc + " && dotnet test Tests", true
	case "cpp":
		// Layout: cpp/ (root CMakeLists adds the service subdir at
		// cpp/services/<svc>), GoogleTest/assert tests registered with ctest. The
		// protoc/grpc_cpp_plugin codegen + cmake --build is the build gate; ctest is
		// the test gate. grpc++/protobuf/mongocxx are preinstalled in the image
		// (no FetchContent), so the build is offline.
		return "cd cpp && cmake -S . -B build -G Ninja && cmake --build build && " +
			"ctest --test-dir build --output-on-failure", true
	default:
		// Layout for this profile's deliverable inside the workspace is not yet
		// certified for the deterministic gate — keep the agent-exit behaviour.
		return "", false
	}
}

// sourceToPortSection returns the prompt block (injected AFTER the Proto Contract)
// that carries the ORIGINAL business logic the agent must PORT — the heart of
// "generation anchored to the source". It emits the domain files (the logic to
// translate) and, separately, instructs how to treat the test files (the
// behaviour oracle). Returns "" when no domain source was captured (the
// decomposition pre-dates source capture) so generation degrades to the old
// contract-only behaviour with no regression.
func sourceToPortSection(targetLabel string, files []ports.SourceFile) string {
	var domain, tests []ports.SourceFile
	langs := map[string]bool{}
	for _, f := range files {
		if strings.TrimSpace(f.Content) == "" {
			continue
		}
		if f.Role == "test" {
			tests = append(tests, f)
			continue
		}
		domain = append(domain, f)
		if l := strings.TrimSpace(f.Lang); l != "" {
			langs[l] = true
		}
	}
	if len(domain) == 0 && len(tests) == 0 {
		return ""
	}

	srcLangs := make([]string, 0, len(langs))
	for l := range langs {
		srcLangs = append(srcLangs, l)
	}
	srcLabel := strings.Join(srcLangs, ", ")
	if srcLabel == "" {
		srcLabel = "the original language"
	}

	var b strings.Builder
	b.WriteString("## Source to Port (original business logic — TRANSLATE faithfully, DO NOT copy syntax, DO NOT invent/stub)\n\n")
	b.WriteString("The files below are the ORIGINAL implementation of this service, written in **")
	b.WriteString(srcLabel)
	b.WriteString("**. They are the GROUND TRUTH for behaviour. Your job is to PORT this logic into ")
	b.WriteString(targetLabel)
	b.WriteString(", NOT to invent a generic CRUD service. Mandatory rules:\n")
	b.WriteString("- Translate IDIOMATICALLY to the target language — do not transliterate syntax. Preserve every business rule, validation, default value, derived/computed field, side-effect and the ORDER of operations exactly as the source performs them.\n")
	b.WriteString("- Map each domain function/method below to the corresponding proto RPC. Every RPC handler MUST implement the real logic from the source (compute the derived fields, mutate the correct state, enforce the same invariants) — an empty echo/CRUD handler is a generation DEFECT.\n")
	b.WriteString("- Do NOT silently stub or drop logic. If a piece genuinely cannot be ported (a dependency with no target equivalent), implement the closest faithful behaviour and add an explicit `// PORT-GAP: <what differs and why>` comment at that site. Never leave a silent empty stub.\n\n")

	for _, f := range domain {
		b.WriteString("```")
		b.WriteString(langFence(f.Lang))
		b.WriteString(" path=")
		b.WriteString(f.Path)
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(f.Content, "\n"))
		b.WriteString("\n```\n\n")
	}

	// Behaviour-test oracle: port the input tests, or synthesise characterization
	// tests from the source. These run inside the deterministic verify gate.
	b.WriteString("## Behaviour Tests (the oracle — they run in the deterministic gate and MUST be green)\n\n")
	if len(tests) > 0 {
		b.WriteString("The source ships the test(s) below. PORT them to ")
		b.WriteString(targetLabel)
		b.WriteString(" as characterization tests that exercise your implementation through the same scenarios and assert the same observable behaviour. They are part of the gate — they MUST pass against your generated code.\n")
	} else {
		b.WriteString("The source ships NO tests for this service. GENERATE characterization tests in ")
		b.WriteString(targetLabel)
		b.WriteString(" — at least one per RPC / per domain method below — derived from the source's behaviour (happy path + the key edge cases the source handles). They are part of the gate.\n")
	}
	b.WriteString("- The tests MUST be HERMETIC: no real database, network or external service. Use in-memory fakes / the repository mocks for persistence so the gate runs offline and deterministically.\n")
	b.WriteString("- Cover the LOGIC, not just wiring: assert derived fields are computed (e.g. a slug from a title), state mutations take effect (e.g. favourite/unfavourite changes membership and counts), and invariants/validations reject bad input.\n\n")
	for _, f := range tests {
		b.WriteString("```")
		b.WriteString(langFence(f.Lang))
		b.WriteString(" path=")
		b.WriteString(f.Path)
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(f.Content, "\n"))
		b.WriteString("\n```\n\n")
	}
	return b.String()
}

// deterministicGateSection tells the agent that the platform — not its own exit —
// decides success: a second pass runs verifyCmd in this same container, and
// GatesPassed is its exit. It makes the test+compile contract explicit so the
// agent does not "claim done" with red tests.
func deterministicGateSection(verifyCmd string) string {
	if strings.TrimSpace(verifyCmd) == "" {
		return ""
	}
	return "## Deterministic Gate (how success is actually decided)\n\n" +
		"After you stop, the platform runs this command in THIS same container and workspace:\n\n" +
		"```sh\n" + verifyCmd + "\n```\n\n" +
		"Your generation is accepted ONLY if that command exits 0 — i.e. the service COMPILES and ALL its tests PASS. " +
		"Your own exit code does NOT decide success. So: finish only when the service builds and the behaviour tests above are green; " +
		"do not leave the build broken, do not leave tests failing or skipped, and do not weaken a test to make it pass.\n\n"
}

// previousFailureSection injects the prior attempt's verify stderr on a retry so
// the agent fixes the exact failing build/tests instead of regenerating blind.
// Returns "" on the first attempt (no prior failure).
func previousFailureSection(prevVerifyStderr string) string {
	s := strings.TrimSpace(prevVerifyStderr)
	if s == "" {
		return ""
	}
	const max = 8000
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return "## Previous Attempt FAILED the Deterministic Gate\n\n" +
		"Your previous attempt did NOT pass the verify command (it failed to compile or its tests were red). " +
		"EDIT the existing files to fix exactly these failures — do not start over and do not delete the failing tests. " +
		"The captured output (tail) was:\n\n" +
		"```text\n" + s + "\n```\n\n"
}

// langFence maps a SourceFile.Lang label to a Markdown code-fence info string.
func langFence(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "python":
		return "python"
	case "php":
		return "php"
	case "java":
		return "java"
	case "csharp", "c#":
		return "csharp"
	case "cpp", "c++":
		return "cpp"
	case "go":
		return "go"
	case "ruby":
		return "ruby"
	case "javascript", "node", "typescript":
		return "ts"
	case "rust":
		return "rust"
	default:
		return ""
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
	if outputProfile == "java" {
		return "## Transport: HTTP (native, Spring Boot)\n\n" +
			"This service speaks HTTP, not gRPC. Mandatory constraints:\n" +
			"- The ONLY entrypoint is a Spring Boot application (a `@SpringBootApplication` class with `SpringApplication.run(...)`) exposing `@RestController` handlers. Do NOT create a gRPC server, do NOT use `io.grpc.Server` / `ServerBuilder` / a `BindableService`, do NOT bootstrap grpc-java, and do NOT register any API gateway.\n" +
			"- You MUST still write the authoritative `.proto` at the canonical path `protobuf/proto/milton_prism/services/<svc>/v1/...` with a `google.api.http` annotation on EVERY RPC. The platform derives `docs/openapi.yaml` from those annotations — without them the OpenAPI is empty.\n" +
			"- Model the request/response messages as POJOs / Java records (or Jackson DTOs) equivalent to the proto messages; you do NOT need the grpc-java generated service base classes at runtime when the transport is Spring Boot HTTP.\n" +
			"- Implement `@RestController` handler methods that map 1:1 to the proto RPCs and honour the `google.api.http` routes (method + path via `@GetMapping`/`@PostMapping`/etc.). Map domain errors to HTTP status codes via the service's error module (`@ControllerAdvice`/`ResponseStatusException`).\n" +
			"- The build gate is `mvn -B package` + `mvn test`. There is NO expectation of an `io.grpc.Server`, `ServerBuilder`, or `BindableService`.\n\n"
	}
	if outputProfile == "ruby" {
		return "## Transport: HTTP (native, Rails API / Sinatra)\n\n" +
			"This service speaks HTTP, not gRPC. Mandatory constraints:\n" +
			"- The ONLY entrypoint is a Rack/Rails API application (a Rails API-only app, or a Sinatra app) booted by Puma. Do NOT create a gRPC server, do NOT use `GRPC::RpcServer` / `GRPC::GenericService`, do NOT run `grpc_tools_ruby_protoc` SERVICE codegen, and do NOT register any API gateway.\n" +
			"- You MUST still write the authoritative `.proto` at the canonical path `protobuf/proto/milton_prism/services/<svc>/v1/...` with a `google.api.http` annotation on EVERY RPC. The platform derives `docs/openapi.yaml` from those annotations — without them the OpenAPI is empty.\n" +
			"- Model the request/response messages as plain Ruby objects (Struct/PORO) equivalent to the proto messages; you do NOT need the `*_services_pb.rb` grpc stub at runtime when the transport is Rails/Sinatra (keep the `*_pb.rb` message classes if you use them).\n" +
			"- Implement controller/route handlers that map 1:1 to the proto RPCs and honour the `google.api.http` routes (verb + path). Map domain errors to HTTP status codes via the service's error module (a `rescue_from`/Rack middleware mapper).\n" +
			"- The build gate is `bundle install` + `bundle exec rspec` + `ruby -c` on every source file. There is NO expectation of a `GRPC::RpcServer` or `GRPC::GenericService`.\n\n"
	}
	if outputProfile == "csharp" {
		return "## Transport: HTTP (native, ASP.NET Core)\n\n" +
			"This service speaks HTTP, not gRPC. Mandatory constraints:\n" +
			"- The ONLY entrypoint is an ASP.NET Core application (a Minimal API on `WebApplication`) booted by Kestrel. Do NOT create a gRPC server, do NOT call `AddGrpc()` / `MapGrpcService<...>`, do NOT reference `Grpc.AspNetCore`, and do NOT register any API gateway.\n" +
			"- You MUST still write the authoritative `.proto` at the canonical path `protobuf/proto/milton_prism/services/<svc>/v1/...` with a `google.api.http` annotation on EVERY RPC. The platform derives `docs/openapi.yaml` from those annotations — without them the OpenAPI is empty. The .csproj keeps the `Grpc.Tools` <Protobuf> items as `GrpcServices=\"None\"` (message classes only) — no service stub is generated.\n" +
			"- Implement endpoint handlers (`app.MapGet`/`MapPost`/`MapPut`/`MapDelete`) that map 1:1 to the proto RPCs and honour the `google.api.http` routes (verb + path). Map domain errors to HTTP status codes via the service's error mapper (an exception-handling middleware or `Results.Problem`).\n" +
			"- The build gate is `dotnet build` + `dotnet test`. There is NO expectation of `Grpc.Core.Server`, `AddGrpc()`, or `MapGrpcService`.\n\n"
	}
	if outputProfile == "cpp" {
		return "## Transport: HTTP (native, Drogon)\n\n" +
			"This service speaks HTTP, not gRPC. Mandatory constraints:\n" +
			"- The ONLY entrypoint is a Drogon application (`drogon::HttpController` subclasses registered on `drogon::app()`, started with `app().addListener(host, port).run()`). Do NOT create a gRPC server, do NOT call `grpc::ServerBuilder` / start a `grpc::Server`, do NOT subclass a generated `*::Service`, and do NOT register any API gateway.\n" +
			"- You MUST still write the authoritative `.proto` at the canonical path `protobuf/proto/milton_prism/services/<svc>/v1/...` with a `google.api.http` annotation on EVERY RPC. The platform derives `docs/openapi.yaml` from those annotations — without them the OpenAPI is empty. The CMakeLists runs `protoc --cpp_out` ONLY (message classes) — NO `--grpc_out` / `grpc_cpp_plugin`, so no service base is generated.\n" +
			"- Model the request/response messages with the protoc-generated `*.pb.h` message classes (or plain structs mapping to them); you do NOT need the `*.grpc.pb.*` service base at runtime when the transport is Drogon.\n" +
			"- Implement Drogon handler methods that map 1:1 to the proto RPCs and honour the `google.api.http` routes (verb + path). Map domain errors to HTTP status codes via the service's error mapper.\n" +
			"- The build gate is `cmake --build` + `ctest`. CMake MUST resolve grpc++/protobuf/mongocxx/Drogon via `find_package` against the PREINSTALLED libraries — NEVER `FetchContent`/`ExternalProject` (re-downloading blows the container timeout). There is NO expectation of a `grpc::Server`, `grpc::ServerBuilder`, or a generated `*::Service`.\n\n"
	}
	return "## Transport: HTTP (native)\n\n" +
		"This service speaks HTTP, not gRPC. Mandatory constraints:\n" +
		"- The ONLY entrypoint is an HTTP-native router (idiomatic, lightweight — net/http, chi or gin) wired in a `main` that starts an `http.Server`. Do NOT create a gRPC server, do NOT call any `RegisterXxxServer`, and do NOT emit or register any API gateway.\n" +
		"- You MUST still write the authoritative `.proto` at the canonical path `protobuf/proto/milton_prism/services/<svc>/v1/...` with a `google.api.http` annotation on EVERY RPC. The platform derives `docs/openapi.yaml` from those annotations — without them the OpenAPI is empty.\n" +
		"- Implement REST/AIP handlers that map 1:1 to the proto RPCs and honour the `google.api.http` routes. Reuse `pkg/gateway/common/error` for error mapping.\n" +
		"- The build gate is `go build ./...` + `go test ./...`. There is NO expectation of a gRPC health server or `RegisterXxxServer`.\n\n"
}

// frameworkSection returns the prose block injected into the combined prompt that
// pins the HTTP web framework the generated service's router/handlers are built
// on. It is the framework homologue of transportSection / storeSection: it only
// applies to the HTTP transport (the HTTP-framework sub-axis is meaningless for
// gRPC), and it injects NOTHING for the language's default framework so the
// established HTTP behaviour (Go → net/http, the certified HTTP-native skeleton)
// is unchanged. A non-default, generable framework (Go → Gin) replaces the prose
// constraints so the agent builds the router/handlers on that framework instead.
//
//   - protocol != "http"                  → "" (framework is ignored for gRPC).
//   - framework "" | "net_http" (the Go default) → "" (the net/http HTTP skeleton
//     the Go HTTP prompt already produces is kept verbatim — no regression).
//   - (go, "gin")                         → the Gin router/handlers block.
//   - any other (profile, framework) cell → "" (unreachable while the
//     IsGenerableHttpFramework guard rejects those cells at creation; kept so the
//     prompt is self-consistent if the guard is ever relaxed).
//
// The framework changes ONLY the HTTP layer (router type, handler signature,
// binding, middleware) and the framework dependency in go.mod. It does NOT touch
// the authoritative `.proto` + `google.api.http` annotations — those stay the
// source of the OpenAPI regardless of framework.
func frameworkSection(outputProfile, protocol, framework string) string {
	if protocol != "http" {
		return ""
	}
	fw := strings.ToLower(strings.TrimSpace(framework))
	// The language default injects nothing: net/http is the Go HTTP-native default
	// the skeleton + transportSection already pin, so the default cell is a no-op.
	if fw == "" || fw == "net_http" {
		return ""
	}
	if outputProfile == "go" && fw == "gin" {
		return "## HTTP Framework: Gin\n\n" +
			"This HTTP service MUST be built on the **Gin** web framework (`github.com/gin-gonic/gin`), NOT the standard-library `net/http` router. Gin replaces ONLY the HTTP layer (router, handler signatures, binding, middleware); the rest of the service (domain = proto aliases, ports, repositories, the authoritative `.proto` + `google.api.http`) is unchanged. Mandatory constraints:\n" +
			"- The router is a `*gin.Engine` created with `gin.New()` (add `gin.Recovery()` / a logger) or `gin.Default()`. Mount it in `main` and serve it (either `engine.Run(addr)` or an `http.Server{Handler: engine}`). Do NOT register routes on `http.ServeMux`/`http.HandleFunc` — Gin owns the routing.\n" +
			"- Every handler is a Gin handler: a `func(c *gin.Context)` (or a method with that signature) registered via `engine.GET/POST/PUT/PATCH/DELETE(path, handler)` (or a `*gin.RouterGroup`). Read path params with `c.Param(...)`, query with `c.Query(...)`, and the body with `c.ShouldBindJSON(&req)` (Gin binding); write responses with `c.JSON(status, payload)`. Do NOT hand-roll `json.NewDecoder(r.Body)` / `w.WriteHeader` — use Gin's binding and `c.JSON`.\n" +
			"- The routes MUST map 1:1 to the proto RPCs and honour the `google.api.http` method+path on each RPC (Gin path syntax: `/users/:id` for `{id}`). The authoritative `.proto` WITH `google.api.http` on every RPC is still written (the OpenAPI is derived from it) — Gin does not replace the contract.\n" +
			"- Cross-cutting concerns are Gin middleware (`gin.HandlerFunc` registered with `engine.Use(...)` or per-group): error mapping, auth, request-id/logging. Map domain errors to HTTP status via the service's error module inside a middleware or a shared helper that calls `c.JSON`/`c.AbortWithStatusJSON`.\n" +
			"- `go.mod` MUST `require github.com/gin-gonic/gin` (a recent v1.x). Run `go mod tidy` so the dependency and its transitive set are recorded and the module builds offline. Do NOT leave `gin` imported but absent from `go.mod`.\n" +
			"- The build gate is unchanged: `go build ./...` + `go test ./...` MUST pass with the Gin router wired as the only entrypoint. There is NO gRPC server and NO API gateway (HTTP-native). At least one handler test exercises the Gin router (e.g. `httptest` against `engine`).\n\n"
	}
	return ""
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
	case "java":
		lib = "`io.jsonwebtoken:jjwt` (or `spring-boot-starter-oauth2-resource-server`)"
		if protocol == "http" {
			wire = "a Spring `OncePerRequestFilter` registered in the Security filter chain (`SecurityFilterChain`) covering the protected routes"
		} else {
			wire = "a grpc-java `ServerInterceptor` attached to the server"
		}
		gate = "`mvn -B package` + `mvn test`"
	case "ruby":
		lib = "the `jwt` gem"
		if protocol == "http" {
			wire = "a Rack middleware (or a Rails `before_action`) covering the protected routes"
		} else {
			wire = "a grpc `GRPC::ServerInterceptor` attached to the `GRPC::RpcServer`"
		}
		gate = "`bundle exec rspec` + `ruby -c`"
	case "csharp":
		lib = "`Microsoft.AspNetCore.Authentication.JwtBearer` (or `System.IdentityModel.Tokens.Jwt`)"
		if protocol == "http" {
			wire = "the ASP.NET Core authentication middleware (`AddAuthentication().AddJwtBearer(...)` + `UseAuthentication()`/`UseAuthorization()`) covering the protected endpoints"
		} else {
			wire = "a grpc-dotnet `Interceptor` (a `ServerCallContext` server interceptor) registered on the gRPC service"
		}
		gate = "`dotnet build` + `dotnet test`"
	case "cpp":
		lib = "`jwt-cpp` (header-only, `#include <jwt-cpp/jwt.h>`, backed by OpenSSL)"
		if protocol == "http" {
			wire = "a Drogon `HttpFilter` registered on the protected controllers/paths"
		} else {
			wire = "a `grpc::ServerInterceptor` (a `grpc::experimental::Interceptor` via a `grpc::experimental::ServerInterceptorFactoryInterface`) wired on the `grpc::ServerBuilder`"
		}
		gate = "`cmake --build` + `ctest`"
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
// PostgreSQL AND MySQL/MariaDB) and the pattern is the same one the SQLAlchemy
// (Python), Prisma (Node) and SeaORM (Rust) cells follow: only the (orm, driver,
// dialect) facts change, the surrounding "models in infra, repos implement the
// ports, mapping domain↔model, schema from the models" shape is constant.
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

// nodePrismaStore describes a Node Prisma persistence cell as a (Prisma datasource
// provider, URL scheme) pair. It is the Prisma homologue of sqlStore (Go-GORM) and
// pySQLAlchemyStore (Python): the prompt block is assembled by prismaStoreSection so
// the SAME schema.prisma + @prisma/client + repos serve every wire-compatible engine
// (one schema/client for PostgreSQL AND MySQL/MariaDB) and only the (datasource
// provider, DATABASE_URL example) facts change per store — Prisma handles the
// dialect, exactly as GORM/SQLAlchemy do for Go/Python.
type nodePrismaStore struct {
	engine     string // human label, e.g. "PostgreSQL", "MySQL/MariaDB"
	provider   string // Prisma datasource provider, e.g. "postgresql", "mysql"
	dsnExample string // a placeholder DATABASE_URL example for the .env note
}

// nodePrismaStores maps the worker store token to its (Prisma provider, URL) facts
// for the Node profile. POSTGRES→postgres token, MARIADB→mysql token (see
// databaseStoreToken) — the SAME homologation as goSQLStores/pySQLAlchemyStores.
// Both rows reuse the SAME schema.prisma + @prisma/client + repos; only the
// datasource provider + DATABASE_URL scheme differ, exactly as the Go-GORM cell
// only changes its driver import + DSN.
var nodePrismaStores = map[string]nodePrismaStore{
	"postgres": {
		engine:     "PostgreSQL",
		provider:   "postgresql",
		dsnExample: "postgresql://user:password@host:5432/<svc>_db?schema=public",
	},
	"mysql": {
		engine:     "MySQL/MariaDB",
		provider:   "mysql",
		dsnExample: "mysql://user:password@host:3306/<svc>_db",
	},
}

// rustSeaORMStore describes a Rust SeaORM persistence cell as a (sqlx driver
// feature, runtime URL scheme) pair. It is the SeaORM homologue of sqlStore
// (Go-GORM), pySQLAlchemyStore (Python) and nodePrismaStore (Node): the prompt
// block is assembled by seaORMStoreSection so the SAME SeaORM entities + repos
// serve every wire-compatible engine (one set of entities/repos for PostgreSQL
// AND MySQL/MariaDB) and only the (sqlx driver feature, DATABASE_URL scheme)
// facts change per store — SeaORM abstracts the dialect, exactly as
// GORM/SQLAlchemy/Prisma do for Go/Python/Node.
type rustSeaORMStore struct {
	engine     string // human label, e.g. "PostgreSQL", "MySQL/MariaDB"
	driverFeat string // SeaORM sqlx driver feature, e.g. "sqlx-postgres", "sqlx-mysql"
	dsnExample string // a placeholder DATABASE_URL example for the .env note
}

// rustSeaORMStores maps the worker store token to its (SeaORM sqlx feature, URL)
// facts for the Rust profile. POSTGRES→postgres token, MARIADB→mysql token (see
// databaseStoreToken) — the SAME homologation as goSQLStores/pySQLAlchemyStores/
// nodePrismaStores. Both rows reuse the SAME SeaORM entities + repos; only the
// sqlx driver feature + DATABASE_URL scheme differ, exactly as the Go-GORM cell
// only changes its driver import + DSN. Rust+Mongo is unaffected: it stays on the
// native `mongodb` crate, never SeaORM.
var rustSeaORMStores = map[string]rustSeaORMStore{
	"postgres": {
		engine:     "PostgreSQL",
		driverFeat: "sqlx-postgres",
		dsnExample: "postgres://user:password@host:5432/<svc>_db",
	},
	"mysql": {
		engine:     "MySQL/MariaDB",
		driverFeat: "sqlx-mysql",
		dsnExample: "mysql://user:password@host:3306/<svc>_db",
	},
}

// javaJPAStore describes a Java Spring Data JPA persistence cell as an (engine,
// JDBC driver dependency, JDBC URL scheme) triple. It is the JPA homologue of
// sqlStore (Go-GORM), pySQLAlchemyStore (Python), nodePrismaStore (Node) and
// rustSeaORMStore (Rust): the prompt block is assembled by jpaStoreSection so the
// SAME `@Entity` classes + JpaRepository adapters serve every wire-compatible
// engine (one set of entities/repos for PostgreSQL AND MySQL/MariaDB) and only the
// (JDBC driver dependency, jdbc: URL scheme) facts change per store — Hibernate
// auto-detects the dialect, exactly as GORM/SQLAlchemy/Prisma/SeaORM do.
type javaJPAStore struct {
	engine     string // human label, e.g. "PostgreSQL", "MySQL/MariaDB"
	driverDep  string // Maven JDBC driver coordinate, e.g. "org.postgresql:postgresql"
	jdbcScheme string // JDBC URL scheme, e.g. "jdbc:postgresql"
	dsnExample string // a placeholder JDBC URL example for the .env note
}

// javaJPAStores maps the worker store token to its (JPA engine, JDBC driver) facts
// for the Java profile. POSTGRES→postgres token, MARIADB→mysql token (see
// databaseStoreToken) — the SAME homologation as the other SQL cells. Both rows
// reuse the SAME `@Entity` classes + JpaRepository adapters; only the JDBC driver
// dependency + URL scheme differ, exactly as the Go-GORM cell only changes its
// driver import + DSN (Hibernate auto-detects the dialect). Java+Mongo is
// unaffected: it stays on Spring Data MongoDB, never JPA.
var javaJPAStores = map[string]javaJPAStore{
	"postgres": {
		engine:     "PostgreSQL",
		driverDep:  "org.postgresql:postgresql",
		jdbcScheme: "jdbc:postgresql",
		dsnExample: "jdbc:postgresql://host:5432/<svc>_db",
	},
	"mysql": {
		engine:     "MySQL/MariaDB",
		driverDep:  "org.mariadb.jdbc:mariadb-java-client",
		jdbcScheme: "jdbc:mariadb",
		dsnExample: "jdbc:mariadb://host:3306/<svc>_db",
	},
}

// rubyARStore describes a Ruby ActiveRecord persistence cell as an (engine,
// gem, adapter, URL scheme) tuple. It is the ActiveRecord homologue of sqlStore
// (Go-GORM), pySQLAlchemyStore (Python), nodePrismaStore (Node), rustSeaORMStore
// (Rust) and javaJPAStore (Java): the prompt block is assembled by arStoreSection
// so the SAME `ActiveRecord::Base` models + repository adapters serve every
// wire-compatible engine (one set of models/repos for PostgreSQL AND MySQL/MariaDB)
// and only the (gem, adapter, URL scheme) facts change per store — ActiveRecord
// selects the adapter from the connection URL, exactly as the others do.
type rubyARStore struct {
	engine     string // human label, e.g. "PostgreSQL", "MySQL/MariaDB"
	gem        string // Ruby driver gem, e.g. "pg", "mysql2"
	adapter    string // ActiveRecord adapter name, e.g. "postgresql", "mysql2"
	dsnExample string // a placeholder DATABASE_URL example for the .env note
}

// rubyARStores maps the worker store token to its (ActiveRecord engine, driver gem)
// facts for the Ruby profile. POSTGRES→postgres token, MARIADB→mysql token (see
// databaseStoreToken) — the SAME homologation as the other SQL cells. Both rows
// reuse the SAME `ActiveRecord::Base` models + repository adapters; only the driver
// gem + adapter + URL scheme differ, exactly as the Go-GORM cell only changes its
// driver import + DSN. Ruby+Mongo is unaffected: it stays on Mongoid, never
// ActiveRecord.
var rubyARStores = map[string]rubyARStore{
	"postgres": {
		engine:     "PostgreSQL",
		gem:        "pg",
		adapter:    "postgresql",
		dsnExample: "postgres://user:password@host:5432/<svc>_db",
	},
	"mysql": {
		engine:     "MySQL/MariaDB",
		gem:        "mysql2",
		adapter:    "mysql2",
		dsnExample: "mysql2://user:password@host:3306/<svc>_db",
	},
}

// csharpEFStore describes a C# Entity Framework Core persistence cell as an
// (engine, NuGet provider package, UseXxx call, URL scheme) tuple. It is the EF
// Core homologue of sqlStore (Go-GORM), pySQLAlchemyStore (Python), nodePrismaStore
// (Node), rustSeaORMStore (Rust), javaJPAStore (Java) and rubyARStore (Ruby): the
// prompt block is assembled by efStoreSection so the SAME `DbContext` + entity
// classes + repository adapters serve every wire-compatible engine (one set of
// entities/repos for PostgreSQL AND MySQL/MariaDB) and only the (provider package,
// UseXxx, URL scheme) facts change per store. C#+Mongo is unaffected: it stays on
// MongoDB.Driver, never EF Core.
type csharpEFStore struct {
	engine     string // human label, e.g. "PostgreSQL", "MySQL/MariaDB"
	provider   string // EF Core provider NuGet package
	useCall    string // DbContextOptionsBuilder call, e.g. "UseNpgsql(connectionString)"
	dsnExample string // a placeholder connection string for the .env note
}

// csharpEFStores maps the worker store token to its (EF Core engine, provider)
// facts for the C# profile. POSTGRES→postgres token, MARIADB→mysql token (see
// databaseStoreToken) — the SAME homologation as the other SQL cells. Both rows
// reuse the SAME `DbContext` + entity classes + repository adapters; only the
// provider package + UseXxx call + URL scheme differ, exactly as the Go-GORM cell
// only changes its driver import + DSN. C#+Mongo is unaffected: it stays on
// MongoDB.Driver, never EF Core.
var csharpEFStores = map[string]csharpEFStore{
	"postgres": {
		engine:     "PostgreSQL",
		provider:   "Npgsql.EntityFrameworkCore.PostgreSQL",
		useCall:    "UseNpgsql(connectionString)",
		dsnExample: "Host=host;Port=5432;Database=<svc>_db;Username=user;Password=password",
	},
	"mysql": {
		engine:     "MySQL/MariaDB",
		provider:   "Pomelo.EntityFrameworkCore.MySql",
		useCall:    "UseMySql(connectionString, ServerVersion.AutoDetect(connectionString))",
		dsnExample: "Server=host;Port=3306;Database=<svc>_db;User=user;Password=password",
	},
}

// cppSQLStore describes a C++ artisanal-SQL persistence cell as an (engine, client
// library, connection-string scheme) triple. It is the C++ homologue of the other
// SQL cells (Go-GORM, …, C#-EF Core) — but C++ has NO ORM: the repository implements
// the port BY HAND with parametrised SQL (libpqxx `exec_params` for PostgreSQL,
// mysql-connector-c++ prepared statements for MySQL/MariaDB). The SAME hand-written
// repo shape serves both engines; only the (client lib, connection-string scheme)
// facts change. C++ + Mongo is unaffected: it stays on mongocxx, never SQL.
type cppSQLStore struct {
	engine     string // human label, e.g. "PostgreSQL", "MySQL/MariaDB"
	clientLib  string // C++ client library (apt-preinstalled), e.g. "libpqxx"
	apiHint    string // the artisanal API entrypoint, e.g. "pqxx::connection + exec_params"
	dsnExample string // a placeholder connection string for the .env note
}

// cppSQLStores maps the worker store token to its (engine, client library) facts for
// the C++ profile. POSTGRES→postgres token, MARIADB→mysql token (see databaseStoreToken)
// — the SAME homologation as the other SQL cells. Both rows reuse the SAME hand-written
// parametrised-SQL repository shape; only the client library + connection-string scheme
// differ. C++ + Mongo is unaffected: it stays on mongocxx, never SQL.
var cppSQLStores = map[string]cppSQLStore{
	"postgres": {
		engine:     "PostgreSQL",
		clientLib:  "libpqxx",
		apiHint:    "pqxx::connection + parametrised pqxx::work::exec_params",
		dsnExample: "postgres://user:password@host:5432/<svc>_db",
	},
	"mysql": {
		engine:     "MySQL/MariaDB",
		clientLib:  "mysql-connector-c++ (mysqlcppconn)",
		apiHint:    "sql::mysql::MySQL_Driver + a prepared sql::PreparedStatement",
		dsnExample: "mysql://user:password@host:3306/<svc>_db",
	},
}

// storeSection returns the prose block injected into the combined prompt that
// pins the persistence engine the generated service must target. It is the store
// homologue of transportSection / authSchemeSection: profile- and store-aware.
//
// v1 GENERATES SQL persistence for Go (via GORM), Python (via SQLAlchemy 2.0 async),
// Node (via Prisma) AND Rust (via SeaORM) on + {PostgreSQL, MySQL/MariaDB}; "mongodb"
// (the original path) injects nothing so the established Mongo behaviour is unchanged:
//   - "mongodb"/"" → no block; the profile doc's MongoDB persistence is used as-is
//     (Node+Mongo stays on the native `mongodb` driver, NOT Prisma).
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
//   - (node, "postgres" | "mysql") → a Prisma persistence layer (prismaStoreSection):
//     ONE schema.prisma (datasource provider postgresql|mysql by store) + the
//     @prisma/client in infrastructure, repos implementing the SAME ports mapping
//     Prisma model↔domain, schema applied by Prisma Migrate / db push, nullable
//     soft-delete column, autoincrement IDs, .env with DATABASE_URL/DB_*.
//   - (rust, "postgres" | "mysql") → a SeaORM persistence layer (seaORMStoreSection):
//     SeaORM entities (async, sqlx-backed) in infrastructure/repositories mapping
//     to/from the domain (proto/prost) types, repos implementing the SAME ports,
//     a Database::connect(DATABASE_URL) builder selecting the sqlx driver feature
//     by store, sea-orm-migration schema, nullable soft-delete column, autoincrement
//     IDs, .env with DATABASE_URL/DB_*. (Rust+Mongo stays on the native `mongodb`
//     crate, NOT SeaORM.)
//   - any other (profile, store) SQL cell → an HONEST note that SQL for that cell
//     is a v1 hole and must not be guessed (this path is unreachable while the
//     IsGenerableDatabase guard rejects those cells at creation, but kept so the
//     prompt is self-consistent if the guard is ever relaxed).
func storeSection(outputProfile, store string) string {
	s := strings.ToLower(strings.TrimSpace(store))
	if s == "" || s == "mongodb" {
		return ""
	}
	// Go + a known SQL store → GORM. Python → SQLAlchemy. Node → Prisma. Every other
	// (profile, store) SQL cell is a v1 hole.
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
	if outputProfile == "node" {
		if cell, ok := nodePrismaStores[s]; ok {
			return prismaStoreSection(cell)
		}
	}
	if outputProfile == "rust" {
		if cell, ok := rustSeaORMStores[s]; ok {
			return seaORMStoreSection(cell)
		}
	}
	if outputProfile == "java" {
		if cell, ok := javaJPAStores[s]; ok {
			return jpaStoreSection(cell)
		}
	}
	if outputProfile == "ruby" {
		if cell, ok := rubyARStores[s]; ok {
			return arStoreSection(cell)
		}
	}
	if outputProfile == "csharp" {
		if cell, ok := csharpEFStores[s]; ok {
			return efStoreSection(cell)
		}
	}
	if outputProfile == "cpp" {
		if cell, ok := cppSQLStores[s]; ok {
			return cppSQLStoreSection(cell)
		}
	}
	return "## Persistence: " + s + " (selected; NOT generated in v1)\n\n" +
		"The target database for this migration is **" + s + "** on the **" + outputProfile +
		"** profile, which v1 of the generator does NOT emit (v1 generates SQL persistence " +
		"for Go (GORM), Python (SQLAlchemy), Node (Prisma) and Rust (SeaORM) on PostgreSQL and " +
		"MySQL/MariaDB; every language also supports MongoDB). Do NOT guess a " + s + " " +
		"implementation. Generate the MongoDB persistence layer as the profile doc describes and " +
		"add a single TODO note stating that `" + s + "` was requested but is a v1 generation hole " +
		"and must be wired manually. Be honest about the gap.\n\n"
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

// prismaStoreSection renders the Prisma persistence block for one Node SQL cell.
// It is the TypeScript homologue of ormStoreSection (Go-GORM) and
// sqlAlchemyStoreSection (Python): the text is parametrised by the (Prisma
// datasource provider, DATABASE_URL example) facts so PostgreSQL and MySQL/MariaDB
// share ONE scaffold (one schema.prisma + @prisma/client + repos, only the
// datasource `provider` + DATABASE_URL scheme differ — Prisma handles the dialect),
// keeping the "client+schema in infra, repos implement the ports, mapping
// domain↔model, schema from the schema.prisma" shape identical across languages.
// Node+Mongo is unaffected: it stays on the native `mongodb` driver, never Prisma.
func prismaStoreSection(c nodePrismaStore) string {
	return "## Persistence: " + c.engine + " (Prisma ORM)\n\n" +
		"This service persists to **" + c.engine + "** via the **Prisma ORM** (`prisma` + `@prisma/client`), NOT MongoDB. " +
		"Replace the MongoDB persistence layer (the native `mongodb` driver) the profile doc describes with an idiomatic Prisma layer. Mandatory constraints:\n" +
		"- Use **Prisma** (`prisma` as a devDependency, `@prisma/client` as a runtime dependency). Define ONE `schema.prisma` whose `datasource db { provider = \"" + c.provider + "\"; url = env(\"DATABASE_URL\") }` and a `generator client { provider = \"prisma-client-js\" }`. Do NOT use a raw SQL driver (`pg`/`mysql2`) or another ORM — Prisma is the canon for this cell, and the SAME schema.prisma + generated client + repos serve PostgreSQL and MySQL/MariaDB unchanged (only the datasource `provider` + DATABASE_URL scheme differ; the dialect is Prisma's job).\n" +
		"- **Domain stays proto.** Domain types remain the TypeScript types/interfaces derived from the proto messages (Canon §5.1). The Prisma **models live in `schema.prisma` and the generated client (`@prisma/client`) is infrastructure** — the schema.prisma + a `PrismaClient` wrapper live under `services/<svc>/infrastructure/repositories` (or `infrastructure/prisma`), NEVER in domain. Each repository maps domain↔Prisma-model on read/write — domain is never decorated with Prisma types.\n" +
		"- For EACH owned resource (a proto message in `owned_resources`) declare a Prisma `model` in `schema.prisma` + write a repository `services/<svc>/infrastructure/repositories/prisma-<resource>-repository.ts` that implements the SAME repository port interface the service already defines (`implements <Resource>Repository`; same async method signatures the gRPC/HTTP handlers depend on) — only the implementation changes from `mongodb` to Prisma. The repo uses an injected `PrismaClient`.\n" +
		"- Add a shared client `shared/prisma/client.ts` that builds the `PrismaClient` once (a module singleton — the Mongo-client homologue), reads `DATABASE_URL` from config/env, configures the pool via the connection-string parameters, and `$connect()`s on startup (fail-fast). Wire it where the Mongo client was wired (in `wire.ts`).\n" +
		"- Add a transaction manager behind a `withTransaction<T>(fn)` API over `prisma.$transaction(async (tx) => …)` (a tx-scoped `Prisma.TransactionClient`), null-safe and mirroring the existing Mongo transaction abstraction, so service-layer transaction boundaries are unchanged.\n" +
		"- **Schema via Prisma Migrate / db push.** The schema is derived from `schema.prisma`: run `prisma migrate deploy` (or `prisma db push`) to apply it — do NOT hand-write SQL migrations and do NOT emulate a `system_counters` collection. Model FK columns/relations come from the `cross_service_fks` in the boundary spec (FK columns/indexes only, never a hard cross-service FK constraint, per the data-ownership boundary). `npx prisma generate` produces the typed client (run it as a `postinstall`/build step).\n" +
		"- **IDs** are autoincrement by the database: the model PK is `id BigInt @id @default(autoincrement())` (Canon §5.3) — never an emulated counter. Map the proto `uint64 identifier` to/from Prisma's `BigInt` (never coerce to a JS `number`). Use snake_case table/column names via `@@map`/`@map`.\n" +
		"- **Soft-delete** with a nullable timestamp column (`deleteTime DateTime? @map(\"delete_time\")`); deletes set the column instead of issuing a hard `DELETE`, and reads filter `deleteTime: null`, matching the Mongo path's soft-delete semantics.\n" +
		"- Read the connection config from `.env` / environment: emit a `.env.example` with `DATABASE_URL` (e.g. `" + c.dsnExample + "`) and/or the discrete `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD`/`DB_NAME` variables. NEVER hardcode a password — a hardcoded credential is a generation defect. Do NOT emit any `MONGO_*` variable.\n" +
		"- Ensure `package.json` requires `@prisma/client` (dependencies) and `prisma` (devDependencies), NOT the `mongodb` package. The persistence code MUST pass the build gate (`npm install` + `npx prisma generate` + `tsc --noEmit`): the generated client + repos compile and at least one repository round-trip is exercised (a Prisma-mocked or container-backed test is acceptable).\n\n"
}

// seaORMStoreSection renders the SeaORM persistence block for one Rust SQL cell.
// It is the Rust homologue of ormStoreSection (Go-GORM), sqlAlchemyStoreSection
// (Python) and prismaStoreSection (Node): the text is parametrised by the (sqlx
// driver feature, DATABASE_URL example) facts so PostgreSQL and MySQL/MariaDB
// share ONE scaffold (one set of SeaORM entities + repos, only the sqlx driver
// feature in Cargo.toml + the DATABASE_URL scheme differ — SeaORM handles the
// dialect), keeping the "entities in infra, repos implement the ports, mapping
// domain↔entity, schema from sea-orm-migration" shape identical across languages.
// Rust+Mongo is unaffected: it stays on the native `mongodb` crate, never SeaORM.
func seaORMStoreSection(c rustSeaORMStore) string {
	return "## Persistence: " + c.engine + " (SeaORM)\n\n" +
		"This service persists to **" + c.engine + "** via the **SeaORM** async ORM (`sea-orm`, sqlx-backed, on the tokio runtime), NOT MongoDB. " +
		"Replace the MongoDB persistence layer (the native `mongodb` crate) the profile doc describes with an idiomatic SeaORM layer. Mandatory constraints:\n" +
		"- Use **SeaORM** (`sea-orm` with `runtime-tokio-rustls`) and `sea-orm-migration`, backed by sqlx with the driver feature **`" + c.driverFeat + "`**. Open the connection with `Database::connect(DATABASE_URL)` (async). Do NOT use raw sqlx/SQL, the `mongodb` crate, or another ORM — SeaORM is the canon for this cell, and the SAME entities + repos serve PostgreSQL and MySQL/MariaDB unchanged (only the sqlx driver feature in `Cargo.toml` + the `DATABASE_URL` scheme differ; the dialect is SeaORM's job).\n" +
		"- **Domain stays proto/prost.** Domain types remain aliases/newtypes over the generated prost proto messages (Canon §5.1). The SeaORM **entities are SEPARATE `DeriveEntityModel` structs (a `Model` with `#[sea_orm(...)]` column attrs + the `Entity`/`ActiveModel`) and live in `infrastructure/repositories`** (e.g. `entities/` or alongside the repo), NEVER in domain. Each repository maps domain↔SeaORM-entity on read/write — domain is never decorated with SeaORM derives.\n" +
		"- For EACH owned resource (a proto message in `owned_resources`) write a SeaORM entity + a repository `infrastructure/repositories/seaorm_<resource>_repository.rs` that implements the SAME repository port `trait` the service already defines (`#[async_trait] impl ports::<Resource>Repository for SeaOrm<Resource>Repository`; same method signatures the gRPC/HTTP handlers depend on) — only the implementation changes from `mongodb` to SeaORM. The repo holds an injected `DatabaseConnection` (`Arc<DatabaseConnection>`).\n" +
		"- Add a shared connection builder `shared/seaorm.rs` (or `shared::db`) that builds the `DatabaseConnection` once from config via `Database::connect(ConnectOptions::new(database_url))` (the Mongo-client homologue), configures the pool on `ConnectOptions` (`max_connections`/`min_connections`/`connect_timeout`/`sqlx_logging`), and pings on startup (fail-fast). Wire it where the Mongo client was wired (in `wire.rs`).\n" +
		"- Add a transaction manager implementing the `TransactionManager` trait's async `with_transaction` over `db.transaction::<_, _, DbErr>(|txn| Box::pin(async move { … }))` (a `DatabaseTransaction`-scoped handle), mirroring the existing Mongo transaction abstraction so service-layer transaction boundaries are unchanged.\n" +
		"- **Schema via `sea-orm-migration`.** Provide a `Migrator` (`MigratorTrait`) whose migrations create the tables from the entities (the `SchemaManager`/`create_table` builder, or `Schema::new(backend).create_table_from_entity(Entity)`); run `Migrator::up(&db, None).await` on startup so the schema is derived from the models (homologue of GORM AutoMigrate / Prisma migrate) — do NOT hand-write raw `*.sql` files and do NOT emulate a `system_counters` collection. Model FK columns/indexes come from the `cross_service_fks` in the boundary spec (FK columns/indexes only, never a hard cross-service FK constraint, per the data-ownership boundary).\n" +
		"- **IDs** are autoincrement by the database: the entity PK is a `#[sea_orm(primary_key)]` `i64` column (SeaORM auto-increments an integer PK; `auto_increment = true` is the default for a single integer PK; Canon §5.3) — never an emulated counter. Map the proto `uint64 identifier` to/from the entity's `i64` without losing precision. Use snake_case table/column names (`#[sea_orm(table_name = \"…\")]`).\n" +
		"- **Soft-delete** with a nullable timestamp column (`delete_time: Option<DateTimeUtc>` / a nullable `TimestampWithTimeZone`); deletes set the column via an `ActiveModel` update instead of issuing a hard `DELETE`, and reads filter `delete_time IS NULL` (`.filter(Column::DeleteTime.is_null())`), matching the Mongo path's soft-delete semantics.\n" +
		"- Read the connection config from `.env` / environment (via `dotenvy`/`envy`/`std::env`): emit a `.env.example` with `DATABASE_URL` (e.g. `" + c.dsnExample + "`) and/or the discrete `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD`/`DB_NAME` variables. NEVER hardcode a password — a hardcoded credential is a generation defect. Do NOT emit any `MONGO_*` variable.\n" +
		"- Ensure the service `Cargo.toml` requires `sea-orm` (with `runtime-tokio-rustls` + **`" + c.driverFeat + "`** + the macros/`with-chrono` features it needs) and `sea-orm-migration`, NOT the `mongodb`/`bson` crates. Keep the crate set minimal (the build-cost note still applies). The persistence code MUST be part of the build gate (`cargo build` + `cargo test`): the entities + repos compile and at least one repository round-trip is exercised (a SeaORM `MockDatabase` or container-backed test is acceptable).\n\n"
}

// jpaStoreSection renders the Spring Data JPA persistence block for one Java SQL
// cell. It is the Java homologue of ormStoreSection (Go-GORM), sqlAlchemyStoreSection
// (Python), prismaStoreSection (Node) and seaORMStoreSection (Rust): the text is
// parametrised by the (JDBC driver dependency, JDBC URL scheme) facts so PostgreSQL
// and MySQL/MariaDB share ONE scaffold (one set of `@Entity` classes + JpaRepository
// adapters, only the JDBC driver dependency + jdbc: URL scheme differ — Hibernate
// auto-detects the dialect), keeping the "entities in infra, repos implement the
// ports, mapping domain↔entity, schema from the entities" shape identical across
// languages. Java+Mongo is unaffected: it stays on Spring Data MongoDB, never JPA.
func jpaStoreSection(c javaJPAStore) string {
	return "## Persistence: " + c.engine + " (Spring Data JPA / Hibernate)\n\n" +
		"This service persists to **" + c.engine + "** via **Spring Data JPA** (Hibernate, `spring-boot-starter-data-jpa`), NOT MongoDB. " +
		"Replace the MongoDB persistence layer (Spring Data MongoDB) the profile doc describes with an idiomatic Spring Data JPA layer. Mandatory constraints:\n" +
		"- Use **Spring Data JPA** (`spring-boot-starter-data-jpa`, Hibernate provider) with the JDBC driver dependency **`" + c.driverDep + "`**. The DataSource URL uses the **`" + c.jdbcScheme + "://…`** scheme. Do NOT use raw JDBC, Spring Data MongoDB, or another ORM — JPA is the canon for this cell, and the SAME entities + repositories serve PostgreSQL and MySQL/MariaDB unchanged (only the JDBC driver dependency + URL scheme differ; Hibernate auto-detects the dialect).\n" +
		"- **Domain stays proto.** Domain types remain aliases of the proto messages (Canon §5.1). The JPA **entities are SEPARATE `@Entity` classes (with `@Table`/`@Column` mappings) and live in `infrastructure/repositories`** (NEVER in domain). Each repository adapter maps domain↔JPA-entity on read/write — domain is never decorated with JPA annotations.\n" +
		"- For EACH owned resource (a proto message in `owned_resources`) write a JPA `@Entity` + a Spring Data `JpaRepository<Entity, Long>` and a repository adapter in `infrastructure/repositories` that implements the SAME repository port interface the service already defines (same method signatures the gRPC/HTTP handlers depend on) — only the implementation changes from MongoDB to JPA.\n" +
		"- Add a DataSource configuration (`@Configuration`/`application.yml`) that picks the JDBC driver + URL by store, configures the connection pool (HikariCP — `maximum-pool-size`/`minimum-idle`/`connection-timeout`), and fails fast on startup. Wire it where the Mongo client was wired. Spring's `@Transactional` carries the transaction boundaries (the Mongo transaction homologue), so service-layer boundaries are unchanged.\n" +
		"- **Schema via Hibernate `ddl-auto=update`.** Set `spring.jpa.hibernate.ddl-auto=update` so the schema is derived from the `@Entity` classes (the GORM AutoMigrate homologue) — do NOT hand-write Flyway/Liquibase migrations and do NOT emulate a `system_counters` collection. Entity FK columns/indexes come from the `cross_service_fks` in the boundary spec (FK columns/indexes only, never a hard cross-service FK constraint, per the data-ownership boundary).\n" +
		"- **IDs** are autoincrement by the database: the entity PK is `@Id @GeneratedValue(strategy = GenerationType.IDENTITY)` on a `Long id` (Canon §5.3) — never an emulated counter. Map the proto `uint64 identifier` to/from the entity's `Long` without losing precision. Use snake_case table/column names (`@Table(name = \"…\")`/`@Column(name = \"…\")`).\n" +
		"- **Soft-delete** with a nullable timestamp column (`@Column(name = \"delete_time\") private Instant deleteTime;` nullable); deletes set the column instead of issuing a hard `DELETE`, and reads filter `delete_time IS NULL`, matching the Mongo path's soft-delete semantics.\n" +
		"- Read the connection config from the environment: emit configuration for `DATABASE_URL` and/or the Spring `SPRING_DATASOURCE_URL`/`SPRING_DATASOURCE_USERNAME`/`SPRING_DATASOURCE_PASSWORD` variables (e.g. URL `" + c.dsnExample + "`). NEVER hardcode a password — a hardcoded credential is a generation defect. Do NOT emit any `MONGO_*` variable.\n" +
		"- Ensure `pom.xml` requires `spring-boot-starter-data-jpa` and `" + c.driverDep + "`, NOT `spring-boot-starter-data-mongodb`. The persistence code MUST be part of the build gate (`mvn -B package` + `mvn test`): the entities + repos compile and at least one repository round-trip is exercised (an H2/Testcontainers-backed test is acceptable).\n\n"
}

// arStoreSection renders the ActiveRecord persistence block for one Ruby SQL cell.
// It is the Ruby homologue of ormStoreSection (Go-GORM), sqlAlchemyStoreSection
// (Python), prismaStoreSection (Node), seaORMStoreSection (Rust) and jpaStoreSection
// (Java): the text is parametrised by the (driver gem, adapter, URL scheme) facts so
// PostgreSQL and MySQL/MariaDB share ONE scaffold (one set of `ActiveRecord::Base`
// models + repository adapters, only the driver gem + adapter + URL differ), keeping
// the "models in infra, repos implement the ports, mapping domain↔model, schema from
// the models" shape identical across languages. Ruby+Mongo is unaffected: it stays on
// Mongoid, never ActiveRecord.
func arStoreSection(c rubyARStore) string {
	return "## Persistence: " + c.engine + " (ActiveRecord)\n\n" +
		"This service persists to **" + c.engine + "** via **ActiveRecord** (the `activerecord` gem with the `" + c.gem + "` driver), NOT MongoDB. " +
		"Replace the Mongoid persistence layer the profile doc describes with an idiomatic ActiveRecord layer. Mandatory constraints:\n" +
		"- Use **ActiveRecord** (`activerecord`) with the driver gem **`" + c.gem + "`** and the **`" + c.adapter + "`** adapter. The connection URL uses the **`" + c.dsnExample + "`** form. Do NOT use Mongoid, raw SQL, or another ORM — ActiveRecord is the canon for this cell, and the SAME models + repositories serve PostgreSQL and MySQL/MariaDB unchanged (only the driver gem + adapter + URL scheme differ).\n" +
		"- **Domain stays proto.** Domain types remain wrappers of the proto-generated message classes (Canon §5.1). The ActiveRecord **models are SEPARATE `ActiveRecord::Base` classes and live in `infrastructure/repositories`** (NEVER in domain). Each repository adapter maps domain↔AR-model on read/write — domain is never an `ActiveRecord::Base`.\n" +
		"- For EACH owned resource (a proto message in `owned_resources`) write an `ActiveRecord::Base` model + a repository adapter in `infrastructure/repositories` that implements the SAME repository port the service already defines (same method signatures the gRPC/HTTP handlers depend on) — only the implementation changes from Mongoid to ActiveRecord.\n" +
		"- Add an `establish_connection` configuration (`config/database.yml` or an initializer) that picks the adapter + URL by store from the environment, configures the connection pool, and fails fast on startup. Wire it where the Mongoid client was wired.\n" +
		"- **Schema via a migration / `schema.rb`.** Derive the schema from the models with ActiveRecord migrations (the Mongoid-implicit-schema homologue) — do NOT emulate a `system_counters` collection. FK columns/indexes come from the `cross_service_fks` in the boundary spec (FK columns/indexes only, never a hard cross-service FK constraint, per the data-ownership boundary).\n" +
		"- **IDs** are autoincrement by the database: the model PK is the default AR `bigint` primary key — never an emulated counter. Map the proto `uint64 identifier` to/from the model's integer id without losing precision. Use snake_case table/column names.\n" +
		"- **Soft-delete** with a nullable timestamp column (`delete_time`, nullable); deletes set the column instead of issuing a hard `DELETE`, and reads scope `where(delete_time: nil)`, matching the Mongo path's soft-delete semantics.\n" +
		"- Read the connection config from the environment: emit configuration for `DATABASE_URL` (e.g. `" + c.dsnExample + "`). NEVER hardcode a password — a hardcoded credential is a generation defect. Do NOT emit any `MONGO_*` variable.\n" +
		"- Ensure the `Gemfile` requires `activerecord` and `" + c.gem + "`, NOT `mongoid`. The persistence code MUST be part of the build gate (`bundle exec rspec` + `ruby -c`): the models + repos load and at least one repository round-trip is exercised (a sqlite/Testcontainers-backed spec is acceptable).\n\n"
}

// efStoreSection renders the Entity Framework Core persistence block for one C# SQL
// cell. It is the C# homologue of ormStoreSection (Go-GORM), sqlAlchemyStoreSection
// (Python), prismaStoreSection (Node), seaORMStoreSection (Rust), jpaStoreSection
// (Java) and arStoreSection (Ruby): the text is parametrised by the (provider package,
// UseXxx call, connection-string scheme) facts so PostgreSQL and MySQL/MariaDB share
// ONE scaffold (one `DbContext` + entity set + repository adapters, only the provider
// package + UseXxx + connection string differ), keeping the "entities in infra, repos
// implement the ports, mapping domain↔entity, schema from the entities" shape identical
// across languages. C#+Mongo is unaffected: it stays on MongoDB.Driver, never EF Core.
func efStoreSection(c csharpEFStore) string {
	return "## Persistence: " + c.engine + " (Entity Framework Core)\n\n" +
		"This service persists to **" + c.engine + "** via **Entity Framework Core** (the `Microsoft.EntityFrameworkCore` package with the `" + c.provider + "` provider), NOT MongoDB. " +
		"Replace the MongoDB.Driver persistence layer the profile doc describes with an idiomatic EF Core layer. Mandatory constraints:\n" +
		"- Use **EF Core** (`Microsoft.EntityFrameworkCore`) with the provider package **`" + c.provider + "`** wired via **`" + c.useCall + "`** in `OnConfiguring`/`AddDbContext`. The connection string uses the **`" + c.dsnExample + "`** form. Do NOT use MongoDB.Driver, raw ADO.NET, or another ORM — EF Core is the canon for this cell, and the SAME `DbContext` + entities + repositories serve PostgreSQL and MySQL/MariaDB unchanged (only the provider package + UseXxx call + connection string differ).\n" +
		"- **Domain stays proto.** Domain types remain wrappers of the proto-generated message classes (Canon §5.1). The EF Core **entities are SEPARATE POCO classes (with data annotations / Fluent API mappings) and live in `Infrastructure/Repositories`** (NEVER in domain). Each repository adapter maps domain↔EF-entity on read/write — domain is never an EF entity.\n" +
		"- For EACH owned resource (a proto message in `owned_resources`) write a POCO entity + a `DbSet<>` on the `DbContext` + a repository adapter in `Infrastructure/Repositories` that implements the SAME repository port interface the service already defines (same method signatures the gRPC/HTTP handlers depend on) — only the implementation changes from MongoDB.Driver to EF Core.\n" +
		"- Add a `DbContext` registration (`AddDbContext<>` in the composition root) that picks the provider + connection string by store from configuration, configures the connection, and fails fast on startup. Wire it where the IMongoDatabase was wired. Use `DbContext`-backed transactions (`Database.BeginTransactionAsync`) as the boundary, the Mongo session homologue.\n" +
		"- **Schema via EF Core migrations.** Derive the schema from the entities with `dotnet ef migrations` (or `EnsureCreated` for tests) — do NOT emulate a `system_counters` collection. FK columns/indexes come from the `cross_service_fks` in the boundary spec (FK columns/indexes only, never a hard cross-service FK constraint, per the data-ownership boundary).\n" +
		"- **IDs** are autoincrement by the database: the entity PK is an identity column (`[Key]` + `ValueGeneratedOnAdd()`/`Identity`) on a `long Id` — never an emulated counter. Map the proto `uint64 identifier` to/from the entity's `long` without losing precision. Use snake_case table/column names.\n" +
		"- **Soft-delete** with a nullable timestamp column (`DateTime? DeleteTime`); deletes set the column instead of issuing a hard `DELETE`, and reads filter `DeleteTime == null` (a global query filter is acceptable), matching the Mongo path's soft-delete semantics.\n" +
		"- Read the connection config from the environment: emit configuration for the connection string (e.g. `" + c.dsnExample + "` via `ConnectionStrings__Default` or `DATABASE_URL`). NEVER hardcode a password — a hardcoded credential is a generation defect. Do NOT emit any `MONGO_*` variable.\n" +
		"- Ensure the `.csproj` references `Microsoft.EntityFrameworkCore` and `" + c.provider + "`, NOT `MongoDB.Driver`. The persistence code MUST be part of the build gate (`dotnet build` + `dotnet test`): the DbContext + entities + repos compile and at least one repository round-trip is exercised (a Sqlite/Testcontainers-backed test is acceptable).\n\n"
}

// cppSQLStoreSection renders the artisanal parametrised-SQL persistence block for one
// C++ SQL cell. It is the C++ homologue of ormStoreSection (Go-GORM) … efStoreSection
// (C#-EF Core) — but C++ has NO ORM: the repository implements the port BY HAND with
// parametrised SQL (libpqxx for PostgreSQL, mysql-connector-c++ for MySQL/MariaDB). The
// text is parametrised by the (engine, client library, connection-string scheme) facts
// so PostgreSQL and MySQL/MariaDB share ONE hand-written repo shape; only the client
// library + connection string differ. C++ + Mongo is unaffected: it stays on mongocxx.
func cppSQLStoreSection(c cppSQLStore) string {
	return "## Persistence: " + c.engine + " (artisanal parametrised SQL — " + c.clientLib + ")\n\n" +
		"This service persists to **" + c.engine + "** via **" + c.clientLib + "** with **hand-written parametrised SQL**, NOT mongocxx. " +
		"There is NO ORM in C++ — replace the mongocxx persistence layer the profile doc describes with an artisanal SQL repository. Mandatory constraints:\n" +
		"- Use **" + c.clientLib + "** (apt-preinstalled in the agent image; resolve it via CMake `find_package`/`pkg-config`, NEVER `FetchContent`). Open the connection and run **parametrised** statements via `" + c.apiHint + "`. NEVER concatenate values into SQL strings (injection) — always bind parameters. The SAME hand-written repo shape serves PostgreSQL and MySQL/MariaDB; only the client library + connection-string scheme differ.\n" +
		"- **Domain stays proto.** Domain types remain structs/aliases of the protoc-generated `*.pb.h` message classes (Canon §5.1). The SQL row mapping lives ONLY in the repository in `infrastructure/repositories` — domain is never coupled to the DB.\n" +
		"- For EACH owned resource (a proto message in `owned_resources`) write a repository in `infrastructure/repositories` that implements the SAME abstract port (the service's repository interface; same method signatures the gRPC/HTTP handlers depend on) — only the implementation changes from mongocxx to parametrised SQL, mapping result row ↔ domain struct by hand.\n" +
		"- Add a shared connection factory that builds/holds the connection (or a small pool) once from config, selects the client by store, and fails fast on startup (the mongocxx-client homologue). Wrap a transaction boundary (a `WithTransaction(fn)` helper over the client's transaction/`pqxx::work`) so service-layer boundaries are unchanged.\n" +
		"- **Schema is delivered as a hand-written `schema.sql`** (there is NO ORM migration / no AutoMigrate): the `CREATE TABLE` set is derived from `owned_resources`, the PK an autoincrement/identity column (`BIGSERIAL` / `BIGINT AUTO_INCREMENT`) — never an emulated `system_counters`. FK columns/indexes come from the `cross_service_fks` in the boundary spec (FK columns/indexes only, never a hard cross-service FK constraint, per the data-ownership boundary). Map the proto `uint64 identifier` to/from the column's 64-bit integer without losing precision.\n" +
		"- **Soft-delete** with a nullable timestamp column (`delete_time`, nullable); deletes set the column instead of issuing a hard `DELETE`, and reads filter `delete_time IS NULL`, matching the Mongo path's soft-delete semantics.\n" +
		"- Read the connection config from the environment (`getenv`): emit a `.env.example` with `DATABASE_URL` (e.g. `" + c.dsnExample + "`) and/or the discrete `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD`/`DB_NAME` variables. NEVER hardcode a password — a hardcoded credential is a generation defect. Do NOT emit any `MONGO_*` variable.\n" +
		"- Ensure the `CMakeLists.txt` `find_package`s / links **" + c.clientLib + "** (NOT mongocxx/bsoncxx). The persistence code MUST be part of the build gate (`cmake --build` + `ctest`): the repos compile/link and at least one repository round-trip is exercised (a local/container-backed test is acceptable).\n\n"
}

// writeCombinedPrompt writes the -p prompt content to workspaceDir/_prompt.md.
// The prompt references the generator prompt file and includes boundary spec
// and proto content inline so the agent has everything without a round-trip.
func writeCombinedPrompt(workspaceDir string, generatorPromptRef, serviceName, errorPrefix, outputProfile, protocol, framework, authScheme, authSigAlg, store, boundarySpec, protoContent string, sourceToPort []ports.SourceFile, previousVerifyStderr string) (string, error) {
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
	buf.WriteString(frameworkSection(outputProfile, protocol, framework))
	buf.WriteString(authSchemeSection(outputProfile, protocol, authScheme, authSigAlg))
	buf.WriteString(storeSection(outputProfile, store))
	// The deterministic gate the platform will run after this agent finishes. When
	// it is wired for this profile, tell the agent the real success contract.
	verifyCmd, verifyOK := verifyCommandFor(outputProfile, protocol, serviceName)
	if verifyOK {
		buf.WriteString(deterministicGateSection(verifyCmd))
	}
	buf.WriteString("## Boundary Spec\n\n```yaml\n")
	buf.WriteString(strings.TrimSpace(boundarySpec))
	buf.WriteString("\n```\n\n## Proto Contract\n\n```proto\n")
	buf.WriteString(strings.TrimSpace(protoContent))
	buf.WriteString("\n```\n\n")
	// Source to port: the original business logic + behaviour-test oracle, injected
	// AFTER the proto contract so the agent maps real logic onto the RPCs.
	buf.WriteString(sourceToPortSection(langLabel, sourceToPort))
	// On a retry, the prior attempt's verify failure so the agent fixes it in place.
	buf.WriteString(previousFailureSection(previousVerifyStderr))

	promptPath := filepath.Join(workspaceDir, "_prompt.md")
	if err := os.WriteFile(promptPath, buf.Bytes(), 0644); err != nil {
		return "", err
	}
	return promptPath, nil
}
