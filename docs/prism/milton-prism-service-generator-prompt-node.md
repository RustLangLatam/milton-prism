# Milton Prism — Hexagonal Service Generator (Node / TypeScript Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, build-passing hexagonal **TypeScript + gRPC** microservice that obeys the Architecture Canon and the Node Language Profile.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice as a complete TypeScript workspace under the source root `node/`, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-node-profile.md`** — the TypeScript + `mongodb` + `@grpc/grpc-js` mechanisms.

**Before generating anything, read both documents in full.** They are the source of truth. This prompt does not restate their rules; it orchestrates the workflow and enforces conformance. Where this prompt and a reference document appear to differ, the reference document wins and you flag the discrepancy in your report.

**There is NO `node/` skeleton in the repository to copy.** Unlike the Go/Python profiles, you build the entire TypeScript workspace from scratch — the shared scaffolding (`node/shared/...`), the workspace root (`node/package.json`, `node/tsconfig.json`), and the service itself. The Node Profile (A.2/A.3/A.4) describes every piece; create them all. You DO have the proto contract (below) and a filesystem + shell.

You have a filesystem and a shell. Use them: create the workspace, generate the proto stubs, write the service code, run the build gate, iterate until green.

---

## 1. Inputs

You receive:

### 1.1 The service contract (proto)

One or more `.proto` files for this service (provided inline in this prompt under "Proto Contract"). **The proto is authoritative for the API surface.** You do not invent RPCs, messages, or fields beyond what the contract defines.

**CANONICAL PROTO LOCATION (mandatory).** Write the authoritative service proto to the platform-canonical path — NOT under `node/`:

```
protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto      # shared message types, if any
```

This is the SAME location the Go and Python profiles use, and the platform's profile-agnostic OpenAPI step reads protos ONLY from `protobuf/proto/milton_prism/services/…` and `protobuf/proto/milton_prism/types/…` to emit `docs/openapi.yaml`. If you place the proto anywhere else (e.g. `node/proto/`), the deliverable ships with NO OpenAPI spec. The `node/gen/` TypeScript stubs are GENERATED FROM this canonical proto; they are not the proto's home.

### 1.2 The boundary spec

A structured object describing what to build and how it connects (provided inline under "Boundary Spec"):

```yaml
service: foo                      # snake_case service name
resources:                        # primary domain resources owned by this service
  - name: Foo
    proto_type: <module>/types/foo/v1.Foo
    soft_delete: true             # adds delete_time/purge_time handling (Canon §2.6)
rpcs:                             # must already exist in the proto; listed for traceability
  - GetFoo
  - ListFoos
  - CreateFoo
  - UpdateFoo
  - DeleteFoo
store: mongodb                    # v1: mongodb (native driver) | postgres | mysql (Prisma)
needs_transaction: true           # wire a MongoTransactionManager; false → omit it
error_prefix: "FOO"               # assigned by the orchestrator registry — NEVER choose your own
inter_service_deps:               # synchronous gRPC clients this service consumes
  - identity
auth: required                    # handlers extract session_id via a service method
```

If any field needed to proceed is missing, **stop** (see §2).

---

## 2. Preconditions and stop conditions (fail fast)

Verify before writing any code. If any check fails, **do not generate** — emit a stop report (§6) explaining precisely what is missing or wrong, and halt.

- **Proto present and AIP-conformant.** Run `buf lint` from `protobuf/`. If it fails, stop.
- **Error prefix supplied.** If `error_prefix` is absent, stop. You never allocate a prefix yourself.
- **Store is supported.** Three stores are generable: `mongodb` (the native `mongodb` driver, §A.5/§A.6), `postgres` and `mysql` (both via Prisma — ONE `schema.prisma` + `@prisma/client` + repos, the datasource `provider`/`DATABASE_URL` chosen by store; see Node Profile §A.12 and the generation prompt's "Persistence: … (Prisma ORM)" section). Branch on `store`: `mongodb` → the `mongodb`-driver adapters; `postgres`/`mysql` → the Prisma adapters. Any OTHER store (e.g. Redis as a primary store) → stop and report a **profile hole** (Node Profile A.10). Never improvise an adapter for an unsupported store.
- **No messaging requested.** If the spec asks for events/NATS/pub-sub, stop — hole (A.10).
- **No pre-existing service collision.** If `node/services/<service>/` already exists with content, stop and ask.

---

## 3. Build order

Create the workspace scaffolding first, then generate strictly inward-to-outward so each layer compiles before the next:

```
WORKSPACE (create once, idempotent — only if absent):
  W1. node/package.json        (deps: @grpc/grpc-js, mongodb, ts-proto, typescript, vitest; scripts: build/test/lint)
  W2. node/tsconfig.json       (strict: true, target es2022, module commonjs, rootDir ., outDir dist)
  W3. node/shared/logging/index.ts        (the only logger)
  W4. node/shared/errors/domain-error.ts  (DomainError class) + node/shared/errors/mapper.ts (mapError → gRPC status)
  W5. node/shared/config/index.ts         (typed env config: MONGO_URI, MONGO_DATABASE, GRPC_HOST, GRPC_PORT, JWT_SECRET)
  W6. node/shared/mongo/index.ts          (MongoClient builder + connect/close lifecycle)
  W7. node/shared/ids/system-counters.ts  (system_counters id generator)
  W8. node/shared/grpc-clients/index.ts   (inter-service gRPC client builder — only if inter_service_deps non-empty)

PROTO (canonical location — REQUIRED for the OpenAPI gate G4):
  P0. Write the authoritative service proto to protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
      (and shared types to protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto) from the inline
      Proto Contract. This is the SAME path Go/Python use; the platform OpenAPI step reads ONLY from there.
      Do NOT keep the proto under node/proto/ — that yields a deliverable with no docs/openapi.yaml.

PROTO STUBS:
  P1. Generate TypeScript proto type declarations into node/gen/ FROM the canonical proto written in P0.
      PRIMARY (npm-only, no protoc): `npx proto-loader-gen-types --grpcLib=@grpc/grpc-js --outDir=gen/ \
        --proto_path ../protobuf/proto ../protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto`
      (proto-loader-gen-types ships with @grpc/proto-loader). The service is loaded at RUNTIME via
      protoLoader.loadSync(...) + grpc.loadPackageDefinition(...); the .d.ts give compile-time types.
      ALTERNATIVE (only if `command -v protoc` succeeds): ts-proto via protoc/buf, outputServices=grpc-js.
      NEVER hand-edit gen/. Do NOT fail the build trying to install a protoc binary — use the primary path.

SERVICE (inward → outward):
  1.  services/<service>/domain/domain.ts        (proto type aliases / re-exports)
  2.  services/<service>/domain/errors.ts        (DomainError sentinels, codes off error_prefix)
  3.  services/<service>/domain/index.ts         (re-exports)
  4.  services/<service>/ports/repository.ts     (Repository interface)
  5.  services/<service>/ports/transaction.ts    (TransactionManager interface, if needs_transaction)
  5b. services/<service>/ports/<dep>-client.ts   (one per inter_service_deps entry)
  6.  services/<service>/ports/index.ts
  7.  services/<service>/application/service.ts   (all use cases)
  8.  services/<service>/application/index.ts
  9.  services/<service>/infrastructure/repositories/<resource>-repository.ts
  10. services/<service>/infrastructure/repositories/transaction-manager.ts   (if needs_transaction)
  11. services/<service>/infrastructure/grpc/<service>-handler.ts   (servicer impl)
  12. services/<service>/wire.ts                  (single composition point)
  13. services/<service>/index.ts                 (gRPC server entrypoint)
  14. services/<service>/tests/service.test.ts    (application unit tests)
```

All paths above are relative to the source root `node/`.

---

## 4. Per-step generation instructions

Read `milton-prism-node-profile.md` §A.2–A.9 carefully — it is your reference (there is no example service to copy). Match its structure, import grouping, and naming precisely.

1. **Domain** (Node Profile A.3). Import proto-generated types from `gen/`; re-export through `domain.ts`. Define `DomainError` sentinels in `errors.ts`, numbering codes off `error_prefix` (`<PREFIX>1xx` validation, `<PREFIX>2xx` domain, `<PREFIX>5xx` internal). Every message follows `Failure_Noun_Descriptor`.

2. **Ports** (Node Profile A.3). Define `interface`s. Repository interface covers the standard CRUD shape from the proto, extended only as the spec requires. Include `TransactionManager` interface if `needs_transaction`. No grpc/mongodb imports here.

2b. **Inter-service client ports** (skip entirely if `inter_service_deps` is absent/empty). For each entry, create `ports/<dep>-client.ts` with a `<Dep>Client` interface exposing at minimum `validate<Dep>Exists(id: Long): Promise<void>` (throws `DomainError` if absent), plus a `NoOp<Dep>Client` concrete implementation in `infrastructure/repositories/noop-<dep>-client.ts`. Instantiate the NoOp client in `wire.ts` and inject it into the service. Call the validation in every use case that writes the FK field.

3. **Application** (Node Profile A.3). Implement every use case as an `async` method. Validate inputs, throw sentinels from `domain/errors.ts`, wrap unexpected errors as `new DomainError(ERR_INTERNAL.code, ...)`. No grpc, no mongodb, no infrastructure imports. Honor FieldMask paths on Update.

4. **Repositories** (Node Profile A.6). Implement ports against the `mongodb` `Db`. Add `system_counters` ID generation (BSON `Long`) at the seed from the decomposition. Implement `MongoTransactionManager` if `needs_transaction`. Add soft-delete timestamp if `soft_delete: true`.

5. **Handlers** (Node Profile A.4). Implement the gRPC service handlers. With the primary `@grpc/proto-loader` path, the service is a `ServiceDefinition` from `loadPackageDefinition`, and handlers are an object of `(call, callback) => ...` functions typed via the `gen/` `.d.ts` (`*Handlers` interface from proto-loader-gen-types). Each handler: read `call.request` → delegate to the application service → on success call `callback(null, response)`; on `DomainError` call `callback(mapError(err))`. Extract session_id via a service method, never inline token parsing. Never import from `repositories` directly.

6. **wire.ts**. The ONLY place that imports from both application and infrastructure. Build: config → MongoClient → db → repo → (transaction manager) → service → handler. Export an async factory the entrypoint awaits.

7. **index.ts**. Standard `@grpc/grpc-js` bootstrap: `new Server()`, `server.addService(<Service>Definition, handlers)`, `server.bindAsync(host:port, ServerCredentials.createInsecure(), cb)`, SIGTERM/SIGINT graceful `tryShutdown`.

8. **Tests** (Node Profile A.8). Application tests mock ports with plain objects / `vi.fn()` — no mongodb, no real DB. Every use case: ≥1 success test + ≥1 error scenario. Every sentinel asserted by ≥1 test.

---

## 5. Self-verification loop (THE BUILD GATE)

After generation, run the gates and **iterate until green**. Do not declare success on unverified code. The `tsc` build is the hard gate — it is the TypeScript equivalent of `go build` and MUST pass.

```bash
# From node/
npm install                 # install deps (offline if registry unreachable — pin and vendor if needed)
buf lint                    # from protobuf/ — proto must still pass
npm run build               # === tsc --noEmit (or tsc): MUST exit 0 with strict:true — THE GATE
npx eslint . || true        # best-effort lint; never blocks
npm test --silent || true   # vitest; run if tests were emitted
```

**The build gate is `npm run build` (tsc). It MUST exit 0.** If `npm install` cannot reach the registry, pin exact versions and retry; if it still fails, report it in §6 as an environment blocker — but never declare success with a failing or skipped build.

On failure: read the compiler error, fix the offending file, re-run. If the same failure recurs three times without progress, **stop and report** (§6) rather than churning.

Conformance self-audit before finishing:
- Does any `application` file import grpc, mongodb, or infrastructure? → must be no
- Does any handler import from `repositories`? → must be no
- Is `wire.ts` the only place importing from both application and infrastructure? → must be yes
- Are domain types proto aliases, not parallel interfaces? → must be yes
- Is every error message in `Failure_Noun_Descriptor` format? → must be yes
- Does `mapError` cover every new 2xx code explicitly? → must be yes
- Are there any hand-edited files under `gen/`? → must be no
- Does `npm run build` (tsc, strict) exit 0? → must be yes

---

## 6. Output and generation report

Produce the files in place under `node/`, then emit a concise report:

```
SERVICE: <service>
STATUS: GENERATED | STOPPED
ERROR_PREFIX_USED: <PREFIX>
FILES_CREATED:
  - node/package.json
  - node/services/<service>/domain/domain.ts
  - ... (full list)
VERIFICATION:
  buf lint:      PASS|FAIL
  npm install:   PASS|FAIL
  tsc (build):   PASS|FAIL   ← the gate
  eslint:        PASS|SKIP
  vitest:        PASS|SKIP (test count)
ASSUMPTIONS:   <any decision made where the spec was silent>
HOLES_HIT:     <none | postgresql | nats | redis | ...>
DEVIATIONS:    <any place TypeScript forced a different shape than Go, with reason>
DISCREPANCIES: <any place the reference docs were ambiguous or conflicted>
```

If `STATUS: STOPPED`, the report's body is the precise reason and what must be supplied to proceed.

---

## 7. Hard rejection triggers (quick reference)

Any of these means the output is wrong — catch them yourself before the verifier does:

- Business logic in a handler or repository.
- A layer importing something forbidden (domain → grpc; application → infrastructure).
- Domain modeled as parallel interfaces instead of proto aliases.
- An error `message` in plain English or snake_case (must be `Failure_Noun_Descriptor`).
- A self-chosen error prefix.
- A private service field accessed directly from a handler (use a service method instead).
- `status`/`phase` instead of `state`; `_at` instead of `_time`; `id` instead of `identifier`.
- A second place (besides `wire.ts`) assembling the full dependency graph.
- `console.log` / `console.error` for diagnostics (use `shared/logging`).
- An improvised Redis or NATS adapter instead of a stop report. (PostgreSQL/MariaDB are NOT holes for Node — `store: postgres`/`mysql` is generated via Prisma per §A.12; only the wrong ORM, e.g. raw `pg`/`mysql2` or a non-Prisma ORM, is forbidden.)
- Hand-edited files under `gen/` (or under the Prisma-generated client).
- The `uint64 identifier` coerced to a JS `number` (use BSON `Long` for Mongo, Prisma `BigInt` for SQL).
- Declaring success with a failing or skipped `tsc` build.
```
