# Milton Prism — Hexagonal Service Generator (C++ / grpc++ Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, gate-passing hexagonal **C++20 + CMake + grpc++ + gRPC** microservice that obeys the Architecture Canon and the C++ Language Profile.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice as a complete app under the source root `cpp/`, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-cpp-profile.md`** — the C++20 + CMake + grpc++ + mongocxx/SQL mechanisms.

**Before generating anything, read both documents in full.** They are the source of truth. This prompt does not restate their rules; it orchestrates the workflow and enforces conformance. Where this prompt and a reference document appear to differ, the reference document wins and you flag the discrepancy in your report.

**There is NO `cpp/` skeleton in the repository to copy.** You build the entire app from scratch — the `CMakeLists.txt` files, the service code, and the entrypoint. The C++ Profile (A.2/A.3/A.4) describes every piece; create them all. You DO have the proto contract (below) and a filesystem + shell.

You have a filesystem and a shell. Use them: create the app, write the proto, wire the CMake `protoc`/`grpc_cpp_plugin` codegen custom command, write the service code, run the gate (`cmake --build` + `ctest`), iterate until green.

---

## 1. Inputs

You receive:

### 1.1 The service contract (proto)

One or more `.proto` files for this service (provided inline under "Proto Contract"). **The proto is authoritative for the API surface.** You do not invent RPCs, messages, or fields beyond what the contract defines.

**CANONICAL PROTO LOCATION (mandatory).** Write the authoritative service proto to the platform-canonical path — NOT under `cpp/`:

```
protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto      # shared message types, if any
```

This is the SAME location the other profiles use, and the platform's profile-agnostic OpenAPI step reads protos ONLY from `protobuf/proto/milton_prism/services/…` and `protobuf/proto/milton_prism/types/…` to emit `docs/openapi.yaml`. If you place the proto anywhere else (e.g. `cpp/proto/`), the deliverable ships with NO OpenAPI spec. The C++ gRPC stubs are GENERATED FROM this canonical proto by `protoc` + `grpc_cpp_plugin` via a CMake custom command at build time (the `CMakeLists.txt` points at the canonical path); they live in the CMake build dir and are NEVER committed/shipped.

### 1.2 The boundary spec

A structured object describing what to build and how it connects (provided inline under "Boundary Spec"):

```yaml
service: foo                      # snake_case service name
resources:
  - name: Foo
    proto_type: <module>/types/foo/v1.Foo
    soft_delete: true
rpcs:                             # must already exist in the proto; listed for traceability
  - GetFoo
  - ListFoos
  - CreateFoo
  - UpdateFoo
  - DeleteFoo
store: mongodb                    # mongodb (mongocxx) | postgres | mysql (artisanal SQL)
needs_transaction: true           # wire an ITransactionManager; false → omit it
error_prefix: "FOO"               # assigned by the orchestrator registry — NEVER choose your own
inter_service_deps:               # synchronous gRPC clients this service consumes
  - identity
auth: required                    # handlers extract session_id via a service method
```

If any field needed to proceed is missing, **stop** (see §2).

---

## 2. Preconditions and stop conditions (fail fast)

Verify before writing any code. If any check fails, **do not generate** — emit a stop report (§6) and halt.

- **Proto present and AIP-conformant.** Run `buf lint` from `protobuf/`. If it fails, stop.
- **Error prefix supplied.** If `error_prefix` is absent, stop. You never allocate a prefix yourself.
- **Store is supported.** Three stores are generable: `mongodb` (mongocxx, C++ Profile §A.5/§A.6), `postgres` and `mysql` (both via artisanal parametrized SQL — hand-written repos + `schema.sql`, the driver / connection string chosen by store; see C++ Profile §A.13 and the "Persistence: … (artisanal parametrized SQL)" section). Branch on `store`. Any OTHER store (e.g. Redis as a primary store) → stop and report a **profile hole** (C++ Profile A.10). Never improvise an adapter.
- **No messaging requested.** If the spec asks for events/NATS/pub-sub, stop — hole (A.10).
- **No pre-existing service collision.** If `cpp/services/<service>/` already exists with content, stop and ask.
- **Toolchain available.** g++ (C++20) + CMake (>=3.20) + Ninja + the preinstalled grpc++/protobuf/mongocxx/libpqxx + `grpc_cpp_plugin`. Run `cmake --version` and `g++ --version`. If absent or wrong version, report an environment blocker (§6) — do NOT try to download/compile a toolchain or FetchContent grpc/protobuf (that exceeds the build budget).

---

## 3. Build order

Create the scaffolding first, then generate strictly inward-to-outward so each layer compiles before the next:

```
APP (create once, idempotent — only if absent):
  C1. cpp/CMakeLists.txt  (top-level: cmake_minimum_required(>=3.20), find_package(PkgConfig REQUIRED) +
                                pkg_check_modules the preinstalled libs — grpc++ + protobuf + libmongocxx/libbsoncxx
                                for store: mongodb OR libpqxx/mysqlcppconn for postgres|mysql — never both;
                                add_subdirectory(services/<service>)). NEVER FetchContent/ExternalProject grpc/protobuf/mongocxx
  C2. cpp/services/<service>/CMakeLists.txt  (the per-service target + the protoc/grpc_cpp_plugin custom command —
                                use the GOLDEN pkg-config template VERBATIM, C++ Profile §A.1.1.1)
  C3. cpp/services/<service>/  (the service app skeleton: domain/, application/, infrastructure/, main.cpp)

PROTO (canonical location — REQUIRED for the OpenAPI gate):
  P0. Write the authoritative service proto to protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
      (and shared types to protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto) from the inline
      Proto Contract. SAME path the other profiles use; the platform OpenAPI step reads ONLY from there.
      Do NOT keep the proto under cpp/proto/.

PROTO CODEGEN (protoc + grpc_cpp_plugin at build):
  P1. Add a CMake custom command that runs protoc with --cpp_out (messages → *.pb.cc/.pb.h) AND --grpc_out with
      --plugin=protoc-gen-grpc=$(which grpc_cpp_plugin) (service → *.grpc.pb.cc/.grpc.pb.h), -I protobuf/proto
      (the canonical include root so transitive imports resolve: google.api, pagination, query_params, openapiv3).
      Emit into the CMake build dir. NEVER hand-edit or ship the generated files.

SERVICE (inward → outward), under cpp/services/<service>/:
  1.  domain/model/...            (framework-free structs/aliases wrapping the proto resources; constants)
  2.  domain/error/...            (DomainError + sentinels, codes off error_prefix)
  3.  application/port/...        (IFooRepository port; ITransactionManager if needs_transaction;
                                   I<Dep>Client port per inter_service_deps — abstract base classes)
  4.  application/usecase/...     (all use cases — throw/return DomainError; transport-agnostic)
  5.  infrastructure/repositories/...  (store: mongodb → mongocxx repo adapter + system_counters id gen +
                                   Mongo ITransactionManager. store: postgres|mysql → hand-written parametrized-SQL
                                   repo adapter + schema.sql + SQL ITransactionManager, per §A.13 — NO mongo code)
                                   — TxMgr only if needs_transaction
  6.  infrastructure/grpc/...     (<Foo>Service : <Foo>::Service impl; + auth grpc::ServerInterceptor)
  7.  infrastructure/config/...   (composition root: construct ports → adapters → use case → gRPC service)
  8.  main.cpp                    (build grpc::ServerBuilder, register the service, start grpc::Server)
  9.  tests / ctest               (application unit tests via doubles of the ports)
```

---

## 4. Per-step generation instructions

Read `milton-prism-cpp-profile.md` §A.2–A.9 carefully — it is your reference (there is no example service to copy). Match its structure, directory grouping, and naming precisely.

1. **Domain** (C++ Profile A.3). Define framework-free structs/aliases wrapping the proto messages — NO mongocxx/libpqxx/grpc++-server/Drogon `#include`s in `domain`. Define `DomainError` + sentinels in `domain/error`, numbering codes off `error_prefix` (`<PREFIX>1xx` validation, `<PREFIX>2xx` domain, `<PREFIX>5xx` internal). Every message follows `Failure_Noun_Descriptor`.

2. **Ports** (C++ Profile A.3). Define abstract base classes in `application/port` (`IFooRepository`, etc., pure-virtual). Repository port covers the standard CRUD shape from the proto. Include `ITransactionManager` if `needs_transaction`. No grpc/mongocxx/libpqxx types in the signatures beyond domain types / `DomainError`.

2b. **Inter-service client ports** (skip if `inter_service_deps` empty). For each entry add an `I<Dep>Client` port in `application/port` exposing at minimum `ValidateExists(id)` (throwing `DomainError`), plus a `NoOp<Dep>Client` in `infrastructure/repositories`. Wire the NoOp in `infrastructure/config` and inject it; call validation in every use case that writes the FK field.

3. **Application** (C++ Profile A.3). Implement every use case as a class/method that throws/returns `DomainError`. Validate inputs, throw sentinels from `domain/error`, wrap unexpected errors as `DomainError::Internal(...)`. No grpc, no SQL/mongo, no infrastructure `#include`s. Honor FieldMask paths on Update.

4. **Repositories.** Branch on `store`:
   - `store: mongodb` (C++ Profile A.5/A.6): a `MongoFooRepository : IFooRepository` adapter mapping domain ⇄ bson document over a `mongocxx::collection` BY HAND (no ORM). Add `system_counters` ID generation (BSON `int64` via `find_one_and_update` `$inc`). Implement a Mongo `ITransactionManager` if `needs_transaction`. Soft-delete `delete_time` if `soft_delete: true`.
   - `store: postgres` | `store: mysql` (C++ Profile §A.13): a `SqlFooRepository : IFooRepository` adapter mapping domain ⇄ row with **parametrized** SQL (autoincrement/identity PK — NO `system_counters`), a hand-written `schema.sql`, a transaction-backed `ITransactionManager` if `needs_transaction`, nullable `delete_time` if `soft_delete: true`. NO Mongo code, NO ORM.

5. **Handlers** (C++ Profile A.4). Implement the generated gRPC service: `class FooService : public <Foo>::Service`. Each RPC method (`grpc::Status GetFoo(grpc::ServerContext*, const FooRequest*, FooResponse*) override`): read the request → delegate to the use case → on success fill the proto response and `return grpc::Status::OK`; on `DomainError` `return MapError(err)` (a `grpc::Status`). Extract session_id via the injected auth path (a `grpc::ServerInterceptor` placing identity in the call context), never inline token parsing. Never include/reference `repositories` directly.

6. **config / wiring** (C++ Profile A.3). The composition root constructs ports → adapters → use case → gRPC service, plus the auth interceptor factory. This is the single composition point; no full-graph assembly elsewhere.

7. **main.cpp**. Build the host: `grpc::ServerBuilder builder; builder.AddListeningPort(addr, grpc::InsecureServerCredentials()); builder.experimental().SetInterceptorCreators(...auth...); builder.RegisterService(&fooService); auto server = builder.BuildAndStart(); server->Wait();` with the address built from `GRPC_HOST`/`GRPC_PORT` over HTTP/2.

8. **Tests** (C++ Profile A.8). GoogleTest (or a minimal assert-based test main) + doubles of the port interfaces — no mongocxx/libpqxx, no real DB. Every use case: ≥1 success test + ≥1 error scenario. Every sentinel asserted by ≥1 test.

---

## 5. Self-verification loop (THE BUILD GATE)

After generation, run the gates and **iterate until green**. Do not declare success on unverified code. **`cmake --build` + `ctest` is the hard gate** — they MUST pass (this includes the protoc/`grpc_cpp_plugin` codegen output compiling cleanly).

```bash
# From protobuf/
buf lint                                          # proto must still pass
# From cpp/  (resolve deps with pkg_check_modules against the PREINSTALLED libs — NEVER FetchContent)
cmake -S . -B build -G Ninja                       # configure: pkg-config the preinstalled grpc++/protobuf/mongocxx
cmake --build build                                # MUST compile (runs the protoc codegen) — THE BUILD GATE
ctest --test-dir build                             # MUST pass — THE GATE
```

**The build gate is `cmake --build` (plus `ctest`). It MUST pass.** The #1 way to avoid a gate TIMEOUT (C++ has the highest gate-failure rate) is to **resolve every dependency with `pkg_check_modules` (pkg-config) against the PREINSTALLED apt libraries — the certified Debian-trixie path, using the GOLDEN CMakeLists template VERBATIM (C++ Profile §A.1.1.1) — and NEVER use `FetchContent`/`ExternalProject` to download grpc/protobuf/mongocxx** — re-downloading and recompiling those blows the container budget. (`find_package(CONFIG)` may also work where a config package exists, but pkg-config is the proven path.) Keep the dependency set minimal; the runtime build should only compile the service's own code + link the preinstalled `.so`s. If a library is missing from the image and the network is unreachable, report it in §6 as an environment blocker — but never declare success with a failing or skipped gate.

On failure: read the error, fix the offending file, re-run. If the same failure recurs three times without progress, **stop and report** (§6) rather than churning.

Conformance self-audit before finishing:
- Does any `domain` file include mongocxx, libpqxx, the grpc++ server, or Drogon? → must be NO (framework-free domain)
- Does any `application` use case include grpc, a SQL/mongo driver, or infrastructure? → must be no
- Does any handler include `repositories`? → must be no
- Is `infrastructure/config` the only place constructing the full graph? → must be yes
- Are domain types framework-free structs derived from the proto (not bson docs / not SQL rows)? → must be yes
- Is every error message in `Failure_Noun_Descriptor` format? → must be yes
- Does `MapError` cover every new 2xx code explicitly? → must be yes
- Is the proto written to protobuf/proto/milton_prism/services/<service>/v1/ (NOT under cpp/)? → must be yes
- Are the generated *.pb.*/*.grpc.pb.* build artifacts (in the build dir, not committed/shipped, not hand-edited)? → must be yes
- Does the CMake resolve every dependency via pkg_check_modules (the golden §A.1.1.1 template) against preinstalled libs (ZERO FetchContent/ExternalProject)? → must be yes
- Is `build/`, `CMakeFiles/`, `*.o`, `*.a`, `*.so`, `CMakeCache.txt` absent from the deliverable? → must be yes
- Does `cmake --build` succeed and `ctest` pass? → must be yes

---

## 6. Output and generation report

Produce the files in place under `cpp/` (and the proto under `protobuf/`), then emit a concise report:

```
SERVICE: <service>
STATUS: GENERATED | STOPPED
ERROR_PREFIX_USED: <PREFIX>
FILES_CREATED:
  - protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
  - cpp/CMakeLists.txt
  - cpp/services/<service>/CMakeLists.txt
  - cpp/services/<service>/main.cpp
  - cpp/services/<service>/... (full list)
VERIFICATION:
  buf lint:        PASS|FAIL
  cmake --build:   PASS|FAIL
  ctest:           PASS|FAIL   ← the gate (test count)
ASSUMPTIONS:   <any decision made where the spec was silent>
HOLES_HIT:     <none | nats | redis | ...>   # PostgreSQL/MySQL are NOT holes — generated via artisanal SQL (§A.13)
DEVIATIONS:    <any place C++ forced a different shape, with reason>
DISCREPANCIES: <any place the reference docs were ambiguous or conflicted>
```

If `STATUS: STOPPED`, the report's body is the precise reason and what must be supplied to proceed.

---

## 7. Hard rejection triggers (quick reference)

Any of these means the output is wrong — catch them yourself before the verifier does:

- Business logic in a handler or repository.
- A layer referencing something forbidden (domain → mongocxx/libpqxx/grpc++; application → infrastructure).
- Domain modeled as bson documents or SQL rows instead of framework-free structs.
- An error `message` in plain English or snake_case (must be `Failure_Noun_Descriptor`).
- A self-chosen error prefix.
- A private service field accessed directly from a handler (use a service method instead).
- `status`/`phase` instead of `state`; `_at` instead of `_time`; `id` instead of `identifier`.
- A second place (besides `infrastructure/config`) assembling the full dependency graph.
- Raw `std::cout`/`printf` for diagnostics (use the injected logger).
- An improvised Redis or NATS adapter instead of a stop report. (PostgreSQL/MySQL/MariaDB are NOT holes — generated via artisanal SQL per §A.13; only the wrong tech, e.g. mongocxx on a SQL store, is forbidden.)
- The proto written under `cpp/proto/` instead of the canonical `protobuf/proto/milton_prism/services/...` path (breaks OpenAPI).
- The generated *.pb.*/*.grpc.pb.* hand-edited or shipped.
- The `uint64 identifier` narrowed to a type that loses precision (use `std::int64_t`/`std::uint64_t`).
- **Using `FetchContent`/`ExternalProject` to download grpc/protobuf/mongocxx instead of `pkg_check_modules` (the golden §A.1.1.1 template) against the preinstalled libs (blows the gate timeout — the #1 C++ failure mode).**
- Shipping `build/`, `CMakeFiles/`, `*.o`, `*.a`, `*.so`, or `CMakeCache.txt` in the deliverable.
- Declaring success with a failing or skipped `cmake --build` / `ctest`.
