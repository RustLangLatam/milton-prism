# Milton Prism — Hexagonal Service Generator, Rust HTTP-native / axum (Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, build-passing hexagonal **Rust** microservice whose **only** wire protocol is **HTTP** — an **axum** application (a `Router` with REST handlers, served on tokio) — obeying the Architecture Canon and the Rust Language Profile.

This is the HTTP/axum homologue of `milton-prism-service-generator-prompt-rust.md` (the Rust + Tonic + gRPC prompt). It is identical in spirit — same hexagonal layering (domain / application / ports / infrastructure), same domain/error rules, same proto-as-contract discipline, same persistence branching on `store` (mongodb via the `mongodb` crate + `system_counters`; postgres|mysql via SeaORM per Rust Profile §A.13) — and differs **only on the transport edge**: the service exposes HTTP natively through axum instead of building a `tonic::transport::Server`, and it never wires the gRPC API gateway. Read the Rust gRPC prompt and the Rust Profile as the baseline; this document records the deltas and the HTTP-specific obligations.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice as a complete Cargo workspace member under the source root `rust/`, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-rust-profile.md`** — the Rust + `mongodb` mechanisms (domain aliases/newtypes, ports, application, repositories, wire, tests). Read the Tonic sections for the hexagonal layering only; the transport edge is replaced by axum here.

**Before generating anything, read both documents in full.** Where this prompt and a reference document differ on the **transport**, this prompt wins (it is the HTTP/axum variant); on everything else the reference document wins and you flag the discrepancy in your report.

**There is NO `rust/` skeleton in the repository to copy.** Unlike the Go/Python profiles, you build the entire Cargo workspace from scratch — the workspace root (`rust/Cargo.toml`), the shared scaffolding member crate (`rust/shared/...`), and the service crate itself. The Rust Profile describes every piece; create them all. You DO have the proto contract (below) and a filesystem + shell.

You have a filesystem and a shell. Use them: create the workspace, write the service code, run the build gate (`cargo build`), iterate until green.

### Fixed stack (non-negotiable)

- **Web framework:** **axum** (on top of tokio + hyper/tower). The app is an `axum::Router` with registered routes (`Router::new().route("/v1/foos", get(...).post(...))`, nested routers, layered middleware).
- **Server:** the entrypoint (`#[tokio::main]`) builds the `Router` and serves it with `axum::serve(TcpListener::bind(addr).await?, app).await?`. There is **no** tonic gRPC server.
- **Persistence:** branch on `store` (same as the gRPC profile). `store: mongodb` → the `mongodb` crate (`Client` / `Database` / `Collection`) with `system_counters` identifier generation (BSON `Int64`). `store: postgres` | `store: mysql` → **SeaORM** (Rust Profile §A.13): entities + repos in `infrastructure/repositories`, `Database::connect(DATABASE_URL)` selecting the sqlx driver feature (`sqlx-postgres`/`sqlx-mysql`) by store, `sea-orm-migration` schema, autoincrement `i64` PK (NO `system_counters`), nullable `delete_time` soft-delete.
- **Models / validation:** Rust structs deriving `serde::Serialize` / `serde::Deserialize` equivalent to the proto messages (with `axum::extract::{Path, Query, Json}` for decoding). You do **not** need the tonic-generated server trait, and you do **not** run `tonic-build` *server* codegen. The `.proto` is still written (it is the authoritative API contract that drives the OpenAPI); the Rust structs are only the in-process representation. (If you choose to keep `prost`/`tonic-build` purely for message types you may, but it is not required and adds build cost — plain serde structs are the recommended, lightest path.)
- **Config:** a per-service `.env` (the platform appends a per-service `.env.example`); load it with the Rust Profile's typed config loader (`dotenvy` + `envy`), using `HTTP_HOST` / `HTTP_PORT` in place of `GRPC_HOST`/`GRPC_PORT`.
- **Language gate:** `cargo build` (the whole workspace compiles, exit 0). This is the build gate — there is no `go build` / `tsc` homologue beyond `cargo build`.

---

## 1. Inputs

Identical to the Rust gRPC prompt §1: you receive the service contract (proto), and the boundary spec (`service`, `resources`, `rpcs`, `store`, `needs_transaction`, `error_prefix`, `inter_service_deps`, `auth`). The proto is authoritative for the API surface; you do not invent RPCs, messages, or fields.

**CANONICAL PROTO LOCATION (mandatory).** Write the authoritative service proto to the platform-canonical path — NOT under `rust/`:

```
protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto      # shared message types, if any
```

This is the SAME location the Go, Python and Node profiles use, and the platform's profile-agnostic OpenAPI step reads protos ONLY from `protobuf/proto/milton_prism/services/…` and `protobuf/proto/milton_prism/types/…` to emit `docs/openapi.yaml`. If you place the proto anywhere else (e.g. `rust/proto/`), the deliverable ships with NO OpenAPI spec.

---

## 2. Preconditions and stop conditions (fail fast)

Same as the Rust gRPC prompt §2 (proto present and AIP-conformant via `buf lint`, error prefix supplied, store is generable — `mongodb`/`postgres`/`mysql`, branch per the gRPC prompt §2 and Rust Profile §A.13, any other store stops as a hole — no messaging/NATS, no pre-existing collision), **plus**:

- **The contract proto MUST carry `google.api.http` annotations.** Every RPC in the service proto must have a `google.api.http` option mapping it to an HTTP method + path. If the supplied proto lacks them, you **add** them (this is part of your job — see §4.0); they are not optional. The platform derives `docs/openapi.yaml` from these annotations; without them the OpenAPI is empty.

---

## 3. Build order

Create the workspace scaffolding first, then generate strictly inward-to-outward. This mirrors the Rust gRPC build order with the transport layer swapped from tonic handlers to axum routes:

```
WORKSPACE (create once, idempotent — only if absent):
  W1. rust/Cargo.toml          ([workspace], members = ["shared", "services/*"], resolver = "2")
  W2. rust/shared/Cargo.toml + rust/shared/src/lib.rs   (the shared member crate)
  W3. rust/shared/src/logging.rs        (the only logger — tracing)
  W4. rust/shared/src/errors.rs         (DomainError enum + an axum IntoResponse / status mapper)
  W5. rust/shared/src/config.rs         (typed env config via dotenvy/envy: HTTP_HOST, HTTP_PORT, JWT_SECRET + the store keys — MONGO_URI/MONGO_DATABASE for mongodb, OR DATABASE_URL/DB_* for postgres|mysql)
  W6. rust/shared/src/{mongo.rs | db.rs}  (mongodb: Client builder + connect lifecycle. postgres|mysql: SeaORM Database::connect builder per §A.13 — NO mongo.rs)
  W7. rust/shared/src/ids.rs            (system_counters id generator — BSON Int64; mongodb ONLY. For postgres|mysql IDs are a SeaORM autoincrement PK, so OMIT this file)
  W8. rust/shared/src/http_clients.rs   (inter-service HTTP/reqwest client builder — only if inter_service_deps non-empty)

PROTO (canonical location — REQUIRED for the OpenAPI gate; WITH google.api.http):
  P0. Write the authoritative service proto to protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
      (and shared types to protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto) from the inline
      Proto Contract, adding a google.api.http option to EVERY RPC. This is the SAME path Go/Python/Node use;
      the platform OpenAPI step reads ONLY from there. Do NOT keep the proto under rust/proto/.

SERVICE (inward → outward), under rust/services/<service>/src/:
  1.  domain/mod.rs               (domain structs — serde Serialize/Deserialize mirroring the proto messages; newtypes; constants)
  2.  domain/errors.rs            (DomainError sentinels, codes off error_prefix)
  3.  ports/mod.rs                (Repository trait; TransactionManager trait if needs_transaction; <dep> client trait per inter_service_deps)
  4.  application/mod.rs          (all use cases — async, Result<T, DomainError>; transport-agnostic)
  5.  infrastructure/repositories/mod.rs   (store: mongodb → mongodb impls of ports + MongoTransactionManager. store: postgres|mysql → SeaORM entities + seaorm_<resource>_repository.rs impls + SeaORM TransactionManager + Migrator, per §A.13 — NO mongodb code) — TransactionManager only if needs_transaction
  6.  infrastructure/http/routes.rs        (axum Router builder + per-RPC handler functions)
  7.  infrastructure/http/errors.rs        (DomainError → HTTP status mapping / IntoResponse)
  8.  infrastructure/mod.rs       (re-exports)
  9.  lib.rs                      (module tree)
  10. wire.rs                     (single composition point — builds the axum Router; Arc<dyn ...> graph)
  11. main.rs                     (#[tokio::main] axum::serve bootstrap — NOT a tonic server)
  12. tests (#[cfg(test)] in application/mod.rs, or services/<service>/tests/)  (application unit tests + axum route tests via tower::ServiceExt::oneshot)
  13. rust/services/<service>/Cargo.toml  (deps: axum, tokio, tower, serde, async-trait, dotenvy, shared + the STORE deps: mongodb+bson for store: mongodb, OR sea-orm [runtime-tokio-rustls + sqlx-postgres|sqlx-mysql] + sea-orm-migration for store: postgres|mysql — never both; NO tonic server, NO build.rs/tonic-build server codegen. List ONLY crates the code actually uses — include `envy` only if config is parsed with it; if config uses `std::env`/`dotenvy`, omit `envy`. No unused dependency.)
```

All `src/...` paths above are relative to `rust/services/<service>/`. Steps 1–5 and the application tests are **the same hexagonal shape** as the Rust gRPC prompt — generate them as the Rust Profile instructs (domain newtypes/aliases, error sentinels numbered off `error_prefix`, ports as traits, application use cases with FieldMask discipline, and the persistence layer chosen by `store`: `mongodb` repositories with the `system_counters` id generator, OR SeaORM entities + repos per §A.13 for `postgres`/`mysql`). Steps **P0, 6, 7, 10, 11** are the transport deltas below. There is **no** `build.rs` and **no** `pb.rs`/`tonic::include_proto!` (no tonic-build server codegen).

The **contract↔implementation** and **no-dead-code/no-unused-dep** rules apply here identically (Rust gRPC prompt §4.2/§7, Rust Profile A.3/A.11): the repository port + adapter expose EXACTLY the persistence operations the proto's RPC set requires — no speculative `create`/`get`/`soft_delete`/`update`/`delete` that no route/RPC reaches — and the axum-native handlers (the `@RestController`-equivalent route functions) mirror the proto's RPCs one-for-one, so the persistence operations behind them mirror the RPCs too. List in `Cargo.toml` only crates the code uses (e.g. include `envy` only if config is actually parsed with it; otherwise `std::env`/`dotenvy` and omit `envy`); emit no orphan consts/macros (`IDENTIFIER_SEED`, unused log macros). `cargo build` must be warning-clean.

---

## 4. Per-step generation instructions (transport deltas)

### 4.0 Proto — authoritative contract WITH HTTP annotations

Write (or patch) the service proto at the canonical path
`protobuf/proto/milton_prism/services/<svc>/v1/<svc>_service.proto` and the shared types under `.../types/<domain>/v1/`. Every RPC MUST carry a `google.api.http` option, e.g.:

```proto
import "google/api/annotations.proto";

service FooService {
  rpc GetFoo(GetFooRequest) returns (Foo) {
    option (google.api.http) = { get: "/v1/foos/{identifier}" };
  }
  rpc ListFoos(ListFoosRequest) returns (ListFoosResponse) {
    option (google.api.http) = { get: "/v1/foos" };
  }
  rpc CreateFoo(CreateFooRequest) returns (Foo) {
    option (google.api.http) = { post: "/v1/foos" body: "foo" };
  }
  rpc UpdateFoo(UpdateFooRequest) returns (Foo) {
    option (google.api.http) = { patch: "/v1/foos/{foo.identifier}" body: "foo" };
  }
  rpc DeleteFoo(DeleteFooRequest) returns (google.protobuf.Empty) {
    option (google.api.http) = { delete: "/v1/foos/{identifier}" };
  }
}
```

Use AIP-conformant paths (`/v1/<plural>`, `{identifier}` path params, `body:` for create/update). The `.proto` is the **authoritative contract** that drives `docs/openapi.yaml` — the platform regenerates the OpenAPI from these annotations (the pipeline is reused unchanged; you do not edit `docs/openapi.yaml` by hand).

### 4.6–4.7 axum transport edge (replaces the tonic handlers step)

The transport layer is an **axum app**, the service's ONLY entrypoint. Do NOT create a tonic gRPC server, do NOT call `tonic::transport::Server::builder()`, do NOT call `.add_service(...)`, do NOT run tonic-build server codegen, and do NOT register any API gateway.

Create `infrastructure/http/routes.rs`:

- A function `fn router(service: Arc<<ServiceName>>) -> axum::Router` that registers one **route** per RPC, mounted on the method + path declared by that RPC's `google.api.http` annotation (method + path must match the proto). axum path syntax uses `:param` (or `{param}` on axum ≥ 0.8) for path params; map `{identifier}` accordingly. Each handler is an `async fn` that decodes the request from path params (`Path<...>`), query string (`Query<...>`), and a typed JSON body (`Json<...>`), calls the application use case, and returns `Json(response)` (or `impl IntoResponse`).
- Validate request fields the same way the tonic handler would (return the service's `DomainError` sentinels from `domain/errors.rs`).
- Map domain errors to HTTP status codes via `infrastructure/http/errors.rs`: an `IntoResponse` for `DomainError` (or a `fn map_error(err: &DomainError) -> (StatusCode, Json<ErrorBody>)`) that translates each domain `code` to an HTTP status (validation `<PREFIX>1xx` → 400, not-found → 404, forbidden → 403, conflict → 409, internal `<PREFIX>500` → 500), with a body `{ code, message }`.
- Inject the auth check when `auth: required`: read the bearer token from the `Authorization` header (an axum extractor / `middleware::from_fn` layer) and resolve the session/user id the same way the gRPC path resolves it from metadata.
- Add a liveness route `GET /health` returning `200` with `{ "status": "ok" }`.

### 4.10 Wire (transport delta)

`wire.rs` has a single async factory `pub async fn build_router(config) -> Result<axum::Router, ...>` (the axum homologue of the tonic `wire.rs`) that builds config → the store handle (mongodb `Client` → `Database`, OR a SeaORM `DatabaseConnection` for postgres|mysql) → repo → (transaction manager) → application service (wrapped `Arc`) → `routes::router(service)` and returns the composed `Router`. It is the only place constructing the full graph. There is **no** tonic `Server` and **no** `add_service` call.

### 4.11 Entrypoint (transport delta)

`rust/services/<service>/src/main.rs` follows the Rust Profile bootstrap (`#[tokio::main]`, config load, mongo connect, `tracing` logging) but the serving step starts **axum**:

- Build the router via `wire::build_router(config).await?`.
- Bind and serve: `let listener = tokio::net::TcpListener::bind((host, port)).await?; axum::serve(listener, app).await?;` with graceful SIGTERM/SIGINT shutdown (`.with_graceful_shutdown(...)`).
- Do NOT build a `tonic::transport::Server`, do NOT register a gRPC health service, do NOT call any `.add_service(...)`, do NOT register any gateway. The axum server is the entire entrypoint.

---

## 5. Self-verification loop (THE BUILD GATE)

After generation, run the gates and **iterate until green**. The build gate is **`cargo build`** (the whole workspace compiles, exit 0):

```bash
buf lint                        # from protobuf/ — proto + google.api.http must resolve
# From rust/
cargo build                     # THE BUILD GATE — must exit 0 (axum app + shared + all crates)
cargo clippy || true            # best-effort lint; never blocks
cargo test || true              # application unit tests + axum route tests (oneshot)
```

Building axum + tokio + the `mongodb` driver is lighter than tonic/prost (no proto codegen) — keep deps minimal (Rust Profile A.1/A.11) and rely on the agent image's pre-warmed Cargo cache. If the crate registry is unreachable, report it in §6 as an environment blocker — but never declare success with a failing or skipped build.

On failure: read the compiler error, fix the offending file, re-run. If the same failure recurs three times without progress, **stop and report** (§6) rather than churning.

Conformance self-audit (answer each in the report) — the non-transport audits from the Rust gRPC prompt PLUS the transport-specific checks. (The non-transport audits include: the repository port/adapter exposes exactly the operations the proto's RPCs require with NO speculative `create`/`get`/`soft_delete` that no route reaches; and `cargo build` is warning-clean with no unused crate (`envy` if config uses only `std::env`/`dotenvy`) and no orphan consts/macros.)

- Do the axum route handlers mirror the proto's RPCs one-for-one, and does the repository port expose only the persistence operations those RPCs require (no dead, unrouted CRUD method)? (must be yes)
- Is the application layer transport-agnostic (no `axum` import, no route module import in `application/`)? (must be yes)
- Does the changeset contain **zero** `tonic::transport::Server`, `.add_service(`, `build.rs` tonic-build server codegen, or `infrastructure/grpc/`? (must be yes)
- Is there a `main.rs` that builds and serves an axum `Router` as the sole entrypoint (`axum::serve` — no tonic server)? (must be yes)
- Does every RPC in the service proto carry a `google.api.http` annotation? (must be yes — the OpenAPI is derived from them)
- Are the request/response bodies plain Rust/serde structs (not tonic-generated message structs hand-marshalled through a gRPC server)? (must be yes)
- Does `cargo build` exit 0? (must be yes — the build gate)

---

## 6. Output and generation report

Same as the Rust gRPC prompt §6, with `TRANSPORT: HTTP (axum)` noted and the HTTP route table (RPC → method+path) listed under `ASSUMPTIONS`/notes. The `VERIFICATION` block reports `cargo build` as the gate (there is no `build.rs`/tonic-build line).

---

## 7. Hard rejection triggers (quick reference)

All the Rust gRPC prompt §7 triggers, **plus**:

- A `tonic::transport::Server::builder()` or any `.add_service(...)` call anywhere in the changeset — this is the HTTP/axum variant; the tonic server must not exist.
- A `build.rs` running tonic-build SERVER codegen, or an `infrastructure/grpc/` module — the transport is axum.
- A `main.rs` that starts a tonic server instead of (or in addition to) axum.
- An RPC in the service proto with no `google.api.http` annotation — the OpenAPI cannot be derived without it.
- The application or domain layer importing `axum` or a route module — transport concerns belong only in `infrastructure/http/`.
- Hand-editing `docs/openapi.yaml` instead of letting the platform derive it from the proto.
