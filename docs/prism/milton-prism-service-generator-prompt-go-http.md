# Milton Prism — Hexagonal Service Generator, Go HTTP-native (Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, verifier-passing hexagonal Go microservice whose **only** wire protocol is **HTTP** (a REST/AIP router + handlers), obeying the Architecture Canon and the Go Language Profile.

This is the HTTP homologue of `milton-prism-service-generator-prompt.md` (the Go gRPC prompt). It is identical in spirit — same hexagonal layering, same domain/error rules, same proto-as-contract discipline — and differs **only on the transport edge**: the service exposes HTTP natively instead of registering a gRPC server, and it never wires the gRPC API gateway. Read the gRPC prompt as the baseline; this document records the deltas and the HTTP-specific obligations.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice into an existing Go monorepo, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-go-profile.md`** — the Go + MongoDB mechanisms (domain aliases, ports, application, repositories, mocks, wire, entrypoint, tests).

**Before generating anything, read both documents in full.** Where this prompt and a reference document differ on the **transport**, this prompt wins (it is the HTTP variant); on everything else the reference document wins and you flag the discrepancy in your report.

You have a filesystem and a shell. Use them: read the existing repo to match conventions exactly, write files, run the verifier, iterate.

---

## 1. Inputs

Identical to the Go gRPC prompt §1: you receive the service contract (proto), and the boundary spec (`service`, `module`, `resources`, `rpcs`, `store`, `needs_transaction`, `error_prefix`, `inter_service_deps`, `auth`). The proto is authoritative for the API surface; you do not invent RPCs, messages, or fields. If any field needed to proceed is missing, **stop** (§2).

---

## 2. Preconditions and stop conditions (fail fast)

Same as the gRPC prompt §2 (proto present and AIP-conformant, error prefix supplied, store is `mongodb`, module matches `go.mod`, no pre-existing collision, no messaging/NATS), **plus**:

- **The contract proto MUST carry `google.api.http` annotations.** Every RPC in the service proto must have a `google.api.http` option mapping it to an HTTP method + path. If the supplied proto lacks them, you **add** them (they are part of your job — see §4.0), they are not optional. The platform derives `docs/openapi.yaml` from these annotations; without them the OpenAPI is empty.

---

## 3. Build order

Generate strictly inward-to-outward so each layer compiles against already-written lower layers. This mirrors the gRPC build order with the transport layer swapped from gRPC handlers to HTTP handlers:

```
0.  proto      (write/patch protobuf/proto/milton_prism/services/<svc>/v1/... WITH
                google.api.http on every RPC; run buf generate for the message types)
1.  domain/         (domain.go aliases + errors.go)
2.  ports/          (repository interface + transaction_manager.go if needs_transaction)
2b. cross-service FK client ports  [ONLY if cross_service_fks is non-empty]
3.  application/    (service.go — use cases and business rules)
4.  infrastructure/http_handlers/   (router.go + <resource>_handler.go + mapError)
5.  infrastructure/repositories/    (mongo_<resource>_repository.go, mongo_transaction_manager.go, identifier.go)
6.  mocks/          (mock_<resource>_repository.go, mock_transaction_manager.go)
7.  wire.go         (single composition point — builds the http.Handler/router)
8.  core/cmd/<service>-services/main.go   (entrypoint — starts http.Server)
9.  tests           (application/service_test.go, http_handlers/<resource>_handler_test.go)
10. gateway error messages — pkg/gateway/common/error/<service>_errors.go ONLY
    ⚠️  DO NOT generate or modify pkg/gateway/common/error/message_error.go.
```

---

## 4. Per-step generation instructions

Steps **0** and **4** and **7–8** are the transport deltas. Steps 1, 2, 2b, 3, 5, 6, 9, 10 are **identical** to the Go gRPC prompt §4 — generate them exactly as that prompt instructs (domain aliases, ports over domain types, application use cases with FieldMask discipline, repositories over `*mongo.Database` with the `system_counters` identifier generator, testify mocks, application/handler tests, and the `<service>_errors.go` map).

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

Use AIP-conformant paths (`/v1/<plural>`, `{identifier}` path params, `body:` for create/update). Run `buf generate` so the message type stubs (`.pb.go`) exist for the handlers to (un)marshal against. **You do NOT need the gRPC service stub** (`RegisterFooServiceServer`) — the HTTP handlers implement the RPCs directly — but generating it is harmless; do not register it anywhere.

### 4.4 HTTP handlers (transport delta — replaces the gRPC handlers step)

The transport layer is an **HTTP-native router + handlers**, the service's ONLY entrypoint. Use an idiomatic, lightweight router — `net/http` with the standard `http.ServeMux`, or `chi` (`github.com/go-chi/chi/v5`) if the repo already vendors it; pick one and stay consistent. Do NOT use gRPC, do NOT embed `Unimplemented...Server`, do NOT call any `RegisterXxxServer`.

For each resource, create `infrastructure/http_handlers/<resource>_handler.go`:

- A handler struct holding the application service (the same use-case interface the gRPC handlers would hold).
- One handler method per RPC, mounted on the route declared by that RPC's `google.api.http` annotation (method + path must match the proto). Decode the request from the path params, query string, and JSON body into the proto request message; call the application use case; encode the proto response message to JSON.
- Use `protojson` (`google.golang.org/protobuf/encoding/protojson`) for request/response (un)marshalling so the JSON shape matches the OpenAPI derived from the proto.
- Validate request fields with the same direct `coreerror.New*` checks the gRPC handler would use.
- Route domain errors through a `mapError` method built off this service's error codes, translating each domain `Error.Code` to an HTTP status (validation `<PREFIX>1xx` → 400, not-found → 404, forbidden → 403, conflict → 409, internal `<PREFIX>500` → 500) and a JSON error body. **Reuse `pkg/gateway/common/error`** for the canonical error-message lookup — that subtree (only `common/error/`) is shipped in the deliverable for exactly this reason.
- Inject the auth extractor when `auth: required`: read the bearer token from the `Authorization` header and resolve the user id the same way the gRPC path resolves it from context.

Create `infrastructure/http_handlers/router.go` exposing a constructor that registers every resource's routes on a `*chi.Mux`/`*http.ServeMux` and returns the `http.Handler`. Include a liveness route `GET /health` (or `/health:<service>`) returning 200.

### 4.7 Wire (transport delta)

`wire.go` has a single `Build<Service>Handler(...)` (or `Build<Service>Router`) that builds repo → tx → application → HTTP handlers → router and returns the composed `http.Handler`. It is the only place constructing the full graph. There is **no** gRPC server and **no** `RegisterXxxServer` call.

### 4.8 Entrypoint (transport delta)

`core/cmd/<service>-services/main.go` follows the Go Profile bootstrap (config load, mongo connect, logger) but the serving step starts an **HTTP server**:

- Build the router via `wire.Build<Service>Handler(...)`.
- Construct an `http.Server{ Addr: <addr>, Handler: router }` and call `ListenAndServe` (with graceful shutdown on SIGINT/SIGTERM via `Shutdown(ctx)`).
- Do NOT create a `grpc.NewServer()`, do NOT register a gRPC health server, do NOT register any gateway. The HTTP server is the entire entrypoint.

---

## 5. Self-verification loop

After generation, run the gates and **iterate until green**:

```bash
buf lint                 # contract, incl. that google.api.http resolves
go build ./...
go vet ./...
go test ./core/services/<service>/...
```

The build gate is `go build ./...` + `go test ./core/...`. **There is NO expectation of a gRPC health server, a `RegisterXxxServer`, or a gateway.**

Conformance self-audit (answer each in the report) — same as the gRPC prompt PLUS the transport-specific checks:

- Is the application layer transport-agnostic (no `net/http`, no router import in `application/`)? (must be yes)
- Does the changeset contain **zero** `grpc.NewServer`, `RegisterXxxServer`, or gRPC server import? (must be yes)
- Is there a `main` that constructs and starts an `http.Server`/router as the sole entrypoint? (must be yes)
- Does every RPC in the service proto carry a `google.api.http` annotation? (must be yes — the OpenAPI is derived from them)
- Is `pkg/gateway/` absent from the changeset EXCEPT for reads of `pkg/gateway/common/error`? (must be yes — no gateway runtime/transcoder code is generated)
- (All the non-transport audits from the gRPC prompt §5: domain aliases, no business logic in handlers/repos, wire is the only graph builder, error `Message` in `Failure_Noun_Descriptor`, gateway message entry per code, no `message_error.go` in changeset, no hand-written `.pb.go`.)

---

## 6. Output and generation report

Same as the gRPC prompt §6, with `TRANSPORT: HTTP` noted and the HTTP route table (RPC → method+path) listed under `ASSUMPTIONS`/notes.

---

## 7. Hard rejection triggers (quick reference)

All the gRPC prompt §7 triggers, **plus**:

- A `grpc.NewServer()` or any `RegisterXxxServer` call anywhere in the changeset — this is the HTTP variant; the gRPC server must not exist.
- Any `pkg/gateway/` file other than reads of `pkg/gateway/common/error/` in the changeset — the HTTP service is its own entry point and never ships the gateway.
- An RPC in the service proto with no `google.api.http` annotation — the OpenAPI cannot be derived without it.
- The application or domain layer importing `net/http`, a router package, or `protojson` — transport concerns belong only in `infrastructure/http_handlers/`.
- The entrypoint serving gRPC instead of (or in addition to) HTTP.
