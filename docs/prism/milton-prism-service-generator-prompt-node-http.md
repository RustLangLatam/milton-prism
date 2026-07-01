# Milton Prism — Hexagonal Service Generator, Node HTTP-native / Fastify (Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, build-passing hexagonal **TypeScript** microservice whose **only** wire protocol is **HTTP** — a **Fastify** application (registered routes + REST handlers) — obeying the Architecture Canon and the Node Language Profile.

This is the HTTP/Fastify homologue of `milton-prism-service-generator-prompt-node.md` (the Node gRPC prompt). It is identical in spirit — same hexagonal layering (domain / application / ports / infrastructure), same domain/error rules, same proto-as-contract discipline — and differs **only on the transport edge**: the service exposes HTTP natively through Fastify instead of registering a `@grpc/grpc-js` server, and it never wires the gRPC API gateway. Read the Node gRPC prompt as the baseline; this document records the deltas and the HTTP-specific obligations.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice as a complete TypeScript workspace under the source root `node/`, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-node-profile.md`** — the TypeScript + `mongodb` (MongoDB) mechanisms (domain aliases, ports, application, repositories, wire, tests). Read the `@grpc/grpc-js` sections for the hexagonal layering only; the transport edge is replaced by Fastify here.

**Before generating anything, read both documents in full.** Where this prompt and a reference document differ on the **transport**, this prompt wins (it is the HTTP/Fastify variant); on everything else the reference document wins and you flag the discrepancy in your report.

**There is NO `node/` skeleton in the repository to copy.** Unlike the Go/Python profiles, you build the entire TypeScript workspace from scratch — the shared scaffolding (`node/shared/...`), the workspace root (`node/package.json`, `node/tsconfig.json`), and the service itself. The Node Profile describes every piece; create them all. You DO have the proto contract and a filesystem + shell.

You have a filesystem and a shell. Use them: create the workspace, write the service code, run the build gate (`tsc`), iterate until green.

### Fixed stack (non-negotiable)

- **Web framework:** Fastify. The app is a `Fastify()` instance with registered routes (plugins / `fastify.route(...)` / `fastify.get|post|patch|delete(...)`).
- **Server:** the entrypoint builds the Fastify app and calls `app.listen({ host, port })`. There is **no** gRPC server.
- **Persistence:** branch on `store`. `store: mongodb` → MongoDB via the native `mongodb` driver (`MongoClient` / `Db` / `Collection`), same `system_counters` identifier generation as the gRPC profile. `store: postgres` / `store: mysql` → **Prisma** (ONE `schema.prisma` with datasource `provider` postgresql|mysql + `@prisma/client` + repos; autoincrement `BigInt` PK; see Node Profile §A.12 and the prompt's "Persistence: … (Prisma ORM)" section). The persistence layer is identical for HTTP and gRPC — only the transport edge differs.
- **Models / validation:** TypeScript interfaces/types equivalent to the proto messages (optionally with a JSON-schema or a runtime validator on the Fastify route for body/params). You do **not** need `@grpc/proto-loader` runtime stubs or a `*_grpc_pb` server stub when the request/response bodies are plain TS types and the transport is Fastify. The `.proto` is still written (it is the authoritative API contract that drives the OpenAPI); the TS types are only the in-process representation.
- **Config:** a per-service `.env` (the platform appends a per-service `.env.example`); load it with the Node Profile's config loader (e.g. `dotenv` + a typed config module).
- **Language gate:** `tsc --noEmit` (the TypeScript compiler) must exit 0. This is the build gate — there is no `go build` / `compileall` homologue beyond `tsc`.

---

## 1. Inputs

Identical to the Node gRPC prompt §1: you receive the service contract (proto), and the boundary spec (`service`, `resources`, `rpcs`, `store`, `needs_transaction`, `error_prefix`, `inter_service_deps`, `auth`, optionally `cross_service_fks`). The proto is authoritative for the API surface; you do not invent RPCs, messages, or fields.

**CANONICAL PROTO LOCATION (mandatory).** Write the authoritative service proto to the platform-canonical path — NOT under `node/`:

```
protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto      # shared message types, if any
```

This is the SAME location the Go and Python profiles use, and the platform's profile-agnostic OpenAPI step reads protos ONLY from `protobuf/proto/milton_prism/services/…` and `protobuf/proto/milton_prism/types/…` to emit `docs/openapi.yaml`. If you place the proto anywhere else, the deliverable ships with NO OpenAPI spec.

---

## 2. Preconditions and stop conditions (fail fast)

Same as the Node gRPC prompt §2 (proto present and AIP-conformant via `buf lint`, error prefix supplied, store is generable — `mongodb` (native driver) or `postgres`/`mysql` (Prisma, §A.12); any other store is a hole — no messaging/NATS, no pre-existing collision), **plus**:

- **The contract proto MUST carry `google.api.http` annotations.** Every RPC in the service proto must have a `google.api.http` option mapping it to an HTTP method + path. If the supplied proto lacks them, you **add** them (this is part of your job — see §4.0); they are not optional. The platform derives `docs/openapi.yaml` from these annotations; without them the OpenAPI is empty.

---

## 3. Build order

Generate strictly inward-to-outward so each layer compiles before the next. This mirrors the gRPC build order with the transport layer swapped from gRPC handlers to Fastify routes:

```
0.  proto      (write/patch protobuf/proto/milton_prism/services/<svc>/v1/<svc>_service.proto
                WITH google.api.http on every RPC; the OpenAPI is derived from it)
1.  node/services/<service>/domain/domain.ts    (domain types — TS interfaces mirroring the proto messages)
2.  node/services/<service>/domain/errors.ts    (DomainError sentinels, codes off error_prefix)
3.  node/services/<service>/ports/repository.ts (Repository interface)
4.  node/services/<service>/ports/transaction.ts (TransactionManager interface if needs_transaction)
5.  cross-service FK client ports  [ONLY if cross_service_fks is non-empty]
6.  node/services/<service>/application/service.ts  (all use cases — transport-agnostic)
7.  node/services/<service>/infrastructure/repositories/<resource>_repository.ts
8.  node/services/<service>/infrastructure/repositories/transaction_manager.ts   (if needed)
9.  node/services/<service>/infrastructure/http/<resource>_routes.ts   (Fastify route registration + handlers)
10. node/services/<service>/infrastructure/http/app.ts                 (Fastify app factory; registers routes; /health)
11. node/services/<service>/infrastructure/http/errors.ts              (DomainError → HTTP status mapping)
12. node/services/<service>/wire.ts             (single composition point — builds the Fastify app)
13. node/services/<service>/main.ts             (Fastify listen entrypoint — NOT a gRPC server)
14. node/services/<service>/tests/service.test.ts   (application unit tests)
15. node/services/<service>/tests/http.test.ts      (route tests via app.inject())
```

Steps 1–8 and the tests are **identical** to the Node gRPC prompt — generate them exactly as that prompt instructs (domain, error sentinels, ports as interfaces, application use cases with FieldMask discipline, `mongodb` repositories with the `system_counters` identifier generator, application unit tests). Steps **0, 9–13** are the transport deltas below.

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

Use AIP-conformant paths (`/v1/<plural>`, `{identifier}` path params, `body:` for create/update). The `.proto` is the **authoritative contract** that drives `docs/openapi.yaml` — the platform regenerates the OpenAPI from these annotations (the pipeline is reused unchanged; you do not edit `docs/openapi.yaml` by hand). You do **not** need to generate `@grpc/proto-loader` runtime stubs or a `*_grpc_pb` server stub: the request/response bodies are plain TypeScript types.

### 4.9–4.11 Fastify transport edge (replaces the gRPC handlers step)

The transport layer is a **Fastify app**, the service's ONLY entrypoint. Do NOT create a gRPC server, do NOT call `new Server()` (`@grpc/grpc-js`), do NOT call `server.addService(...)`, do NOT emit a `*_grpc_pb` server stub, and do NOT register any API gateway.

For each resource, create `infrastructure/http/<resource>_routes.ts`:

- A function `register<Resource>Routes(app: FastifyInstance, service: <ServiceName>)` that registers one **route** per RPC, mounted on the method + path declared by that RPC's `google.api.http` annotation (method + path must match the proto). Decode the request from path params (`request.params`), query string (`request.query`), and a typed body (`request.body`); call the application use case; reply with the response model (`reply.send(...)`; Fastify serialises it to JSON matching the OpenAPI).
- Validate request fields the same way the gRPC handler would (throw the service's `DomainError` sentinels from `domain/errors.ts`).
- Map domain errors to HTTP status codes via `infrastructure/http/errors.ts`: a `mapError(err: DomainError): { status: number; body: { code: string; message: string } }` that translates each domain `code` to an HTTP status (validation `<PREFIX>1xx` → 400, not-found → 404, forbidden → 403, conflict → 409, internal `<PREFIX>500` → 500). Wire it as a Fastify `setErrorHandler` on the app.
- Inject the auth check when `auth: required`: read the bearer token from the `Authorization` header (a Fastify `preHandler` hook or decorator) and resolve the session/user id the same way the gRPC path resolves it from metadata.

Create `infrastructure/http/app.ts` exposing a `createApp(...) : FastifyInstance` factory that builds the `Fastify()` instance, registers the `DomainError` error handler (`setErrorHandler`), and registers every resource route module. Add a liveness route `GET /health` returning `200 { "status": "ok" }`.

### 4.12 Wire (transport delta)

`wire.ts` has a single `buildApp(...) : FastifyInstance` (the Fastify homologue of the gRPC `wire.ts`) that builds db → repo → tx → application service → routes → `createApp(...)` and returns the composed Fastify app. It is the only place constructing the full graph. There is **no** gRPC `Server` and **no** `addService` call.

### 4.13 Entrypoint (transport delta)

`node/services/<service>/main.ts` follows the Node Profile bootstrap (config load, mongo connect, logging) but the serving step starts **Fastify**:

- Build the app via `wire.buildApp(...)`.
- Run it: `await app.listen({ host, port })` with graceful SIGTERM/SIGINT shutdown (`app.close()`).
- Do NOT create `new Server()` (`@grpc/grpc-js`), do NOT register a gRPC health service, do NOT call any `addService`, do NOT register any gateway. The Fastify server is the entire entrypoint.

---

## 5. Self-verification loop

After generation, run the gates and **iterate until green**. The build gate is **`tsc --noEmit`** (every generated module type-checks) **plus importing the Fastify app** (no import-time error) **plus `npm test`**:

```bash
# From node/
npm install                     # confirm no new conflicts (fastify, mongodb, dotenv present; NO @grpc/grpc-js needed)
buf lint                        # from protobuf/ — proto + google.api.http must resolve
npx tsc --noEmit                # THE BUILD GATE — must exit 0
node -e "require('./services/<service>/wire')"   # app graph must import (or an equivalent ts-node import)
npm test                        # service + http (app.inject) tests
```

On failure: read the error, fix the offending file, re-run. If the same failure recurs three times without progress, **stop and report** (§6) rather than churning.

Conformance self-audit (answer each in the report) — the non-transport audits from the Node gRPC prompt PLUS the transport-specific checks:

- Is the application layer transport-agnostic (no `fastify` import, no route module import in `application/`)? (must be yes)
- Does the changeset contain **zero** `new Server()` (`@grpc/grpc-js`), `addService`, or `*_grpc_pb` server stub? (must be yes)
- Is there a `main.ts` that builds and `listen`s a Fastify server as the sole entrypoint (no gRPC server)? (must be yes)
- Does every RPC in the service proto carry a `google.api.http` annotation? (must be yes — the OpenAPI is derived from them)
- Are the request/response bodies plain TS types (not hand-marshalled `*_grpc_pb` objects)? (must be yes)
- Does `npx tsc --noEmit` exit 0? (must be yes — the build gate)

---

## 6. Output and generation report

Same as the Node gRPC prompt §6, with `TRANSPORT: HTTP (Fastify)` noted and the HTTP route table (RPC → method+path) listed under `ASSUMPTIONS`/notes.

---

## 7. Hard rejection triggers (quick reference)

All the Node gRPC prompt §7 triggers, **plus**:

- A `new Server()` (`@grpc/grpc-js`) or any `server.addService(...)` call anywhere in the changeset — this is the HTTP/Fastify variant; the gRPC server must not exist.
- A `*_grpc_pb` server stub or a `@grpc/proto-loader` bootstrap used at runtime.
- A `main.ts` that starts a gRPC server instead of (or in addition to) Fastify.
- An RPC in the service proto with no `google.api.http` annotation — the OpenAPI cannot be derived without it.
- The application or domain layer importing `fastify` or a route module — transport concerns belong only in `infrastructure/http/`.
- Hand-editing `docs/openapi.yaml` instead of letting the platform derive it from the proto.
