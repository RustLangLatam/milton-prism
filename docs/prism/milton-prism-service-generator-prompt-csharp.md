# Milton Prism — Hexagonal Service Generator (C# / grpc-dotnet Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, gate-passing hexagonal **C# + .NET + grpc-dotnet + gRPC** microservice that obeys the Architecture Canon and the C# Language Profile.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice as a complete app under the source root `csharp/`, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-csharp-profile.md`** — the C# + .NET + grpc-dotnet + MongoDB.Driver/EF Core mechanisms.

**Before generating anything, read both documents in full.** They are the source of truth. This prompt does not restate their rules; it orchestrates the workflow and enforces conformance. Where this prompt and a reference document appear to differ, the reference document wins and you flag the discrepancy in your report.

**There is NO `csharp/` skeleton in the repository to copy.** You build the entire app from scratch — the `.csproj`, the service code, and the entrypoint. The C# Profile (A.2/A.3/A.4) describes every piece; create them all. You DO have the proto contract (below) and a filesystem + shell.

You have a filesystem and a shell. Use them: create the app, write the proto, wire the `.csproj` `<Protobuf>` codegen items, write the service code, run the gate (`dotnet build` + `dotnet test`), iterate until green.

---

## 1. Inputs

You receive:

### 1.1 The service contract (proto)

One or more `.proto` files for this service (provided inline under "Proto Contract"). **The proto is authoritative for the API surface.** You do not invent RPCs, messages, or fields beyond what the contract defines.

**CANONICAL PROTO LOCATION (mandatory).** Write the authoritative service proto to the platform-canonical path — NOT under `csharp/`:

```
protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto      # shared message types, if any
```

This is the SAME location the other profiles use, and the platform's profile-agnostic OpenAPI step reads protos ONLY from `protobuf/proto/milton_prism/services/…` and `protobuf/proto/milton_prism/types/…` to emit `docs/openapi.yaml`. If you place the proto anywhere else (e.g. `csharp/proto/`), the deliverable ships with NO OpenAPI spec. The C# gRPC stubs are GENERATED FROM this canonical proto by `Grpc.Tools` at build time (the `.csproj` `<Protobuf>` items point at the canonical path); they live in `obj/` and are NEVER committed/shipped.

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
store: mongodb                    # mongodb (MongoDB.Driver) | postgres | mysql (EF Core)
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
- **Store is supported.** Three stores are generable: `mongodb` (MongoDB.Driver, C# Profile §A.5/§A.6), `postgres` and `mysql` (both via EF Core — ONE DbContext + entity set + repos, the provider package / connection string chosen by store; see C# Profile §A.13 and the "Persistence: … (Entity Framework Core)" section). Branch on `store`. Any OTHER store (e.g. Redis as a primary store) → stop and report a **profile hole** (C# Profile A.10). Never improvise an adapter.
- **No messaging requested.** If the spec asks for events/NATS/pub-sub, stop — hole (A.10).
- **No pre-existing service collision.** If `csharp/services/<service>/` already exists with content, stop and ask.
- **Toolchain available.** .NET SDK 9 + the warmed NuGet cache + `Grpc.Tools`. Run `dotnet --version`. If absent or wrong major version, report an environment blocker (§6) — do NOT try to download a .NET SDK that may exceed the build budget.

---

## 3. Build order

Create the scaffolding first, then generate strictly inward-to-outward so each layer compiles before the next:

```
APP (create once, idempotent — only if absent):
  C1. csharp/services/<service>/<Service>.csproj  (the package set A.1.1: Grpc.AspNetCore, Grpc.Tools,
                                Google.Protobuf, the STORE packages — MongoDB.Driver for store: mongodb, OR
                                Microsoft.EntityFrameworkCore + ONE provider for postgres|mysql — never both;
                                JwtBearer; xunit + Microsoft.NET.Test.Sdk) PLUS the <Protobuf> codegen items
  C2. csharp/services/<service>/  (the service app skeleton: Domain/, Application/, Infrastructure/, Program.cs)

PROTO (canonical location — REQUIRED for the OpenAPI gate):
  P0. Write the authoritative service proto to protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
      (and shared types to protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto) from the inline
      Proto Contract. SAME path the other profiles use; the platform OpenAPI step reads ONLY from there.
      Do NOT keep the proto under csharp/proto/.

PROTO CODEGEN (Grpc.Tools at build):
  P1. Add <Protobuf Include="...protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto"
      GrpcServices="Server" ProtoRoot="protobuf/proto" /> items so Grpc.Tools runs protoc + grpc_csharp_plugin
      at `dotnet build` against the canonical protobuf/proto include root (so transitive imports resolve:
      google.api, pagination, query_params, openapiv3). It emits *.cs (messages) + *Grpc.cs (service base)
      into obj/. NEVER hand-edit or ship the generated files.

SERVICE (inward → outward), under csharp/services/<service>/:
  1.  Domain/Model/...            (framework-free records wrapping the proto resources; constants)
  2.  Domain/Error/...            (DomainError + sentinels, codes off error_prefix)
  3.  Application/Port/...        (IFooRepository port; ITransactionManager if needs_transaction;
                                   I<Dep>Client port per inter_service_deps)
  4.  Application/UseCase/...     (all use cases — throw DomainError; transport-agnostic)
  5.  Infrastructure/Repositories/...  (store: mongodb → Mongo document + repo adapter + system_counters
                                   id gen + Mongo ITransactionManager. store: postgres|mysql → EF Core DbContext
                                   + POCO entity + repo adapter + EF ITransactionManager, per §A.13 — NO mongo code)
                                   — TxMgr only if needs_transaction
  6.  Infrastructure/Grpc/...     (<Foo>Service : Foo.FooBase impl; + auth Interceptor)
  7.  Infrastructure/Config/...   (composition root: register ports → adapters → use case → gRPC service on DI)
  8.  Program.cs                  (build the WebApplication, MapGrpcService<>, run on Kestrel)
  9.  tests project / xUnit       (application unit tests via doubles of the ports)
```

---

## 4. Per-step generation instructions

Read `milton-prism-csharp-profile.md` §A.2–A.9 carefully — it is your reference (there is no example service to copy). Match its structure, namespace grouping, and naming precisely.

1. **Domain** (C# Profile A.3). Define framework-free records wrapping the proto messages — NO MongoDB.Driver/EF Core/gRPC-server `using`s in `Domain`. Define `DomainError` + sentinels in `Domain/Error`, numbering codes off `error_prefix` (`<PREFIX>1xx` validation, `<PREFIX>2xx` domain, `<PREFIX>5xx` internal). Every message follows `Failure_Noun_Descriptor`.

2. **Ports** (C# Profile A.3). Define C# interfaces in `Application/Port` (`IFooRepository`, etc.). Repository port covers the standard CRUD shape from the proto. Include `ITransactionManager` if `needs_transaction`. No gRPC/MongoDB.Driver/EF Core types in the signatures beyond domain types / `DomainError`.

2b. **Inter-service client ports** (skip if `inter_service_deps` empty). For each entry add an `I<Dep>Client` port in `Application/Port` exposing at minimum `ValidateExistsAsync(id)` (throwing `DomainError`), plus a `NoOp<Dep>Client` in `Infrastructure/Repositories`. Wire the NoOp in `Infrastructure/Config` and inject it; call validation in every use case that writes the FK field.

3. **Application** (C# Profile A.3). Implement every use case as a class/method that throws `DomainError`. Validate inputs, throw sentinels from `Domain/Error`, wrap unexpected errors as `DomainError.Internal(...)`. No gRPC, no ORM, no infrastructure `using`s. Honor FieldMask paths on Update.

4. **Repositories.** Branch on `store`:
   - `store: mongodb` (C# Profile A.5/A.6): a Mongo document + a `MongoFooRepository : IFooRepository` adapter mapping domain ⇄ document over `IMongoCollection<>`. Add `system_counters` ID generation (BSON `Int64`). Implement a Mongo `ITransactionManager` if `needs_transaction`. Soft-delete `DeleteTime` if `soft_delete: true`.
   - `store: postgres` | `store: mysql` (C# Profile §A.13): an EF Core `DbContext` + POCO entity (identity PK — NO `system_counters`) + an `EfFooRepository : IFooRepository` adapter mapping domain ⇄ entity, a `BeginTransactionAsync`-backed `ITransactionManager` if `needs_transaction`, migration-derived schema, nullable `DeleteTime` if `soft_delete: true`. NO Mongo code.

5. **Handlers** (C# Profile A.4). Implement the generated gRPC service: `class FooService : Foo.FooBase`. Each RPC method (`override async Task<FooResponse> GetFoo(...)`): read the request → delegate to the use case → on success return the proto response; on `DomainError` throw `MapError(err)` (an `RpcException`). Extract session_id via the injected auth path (a grpc-dotnet `Interceptor` placing identity in the call context), never inline token parsing. Never reference `Repositories` directly.

6. **config / wiring** (C# Profile A.3). The composition root registers ports → adapters → use case → gRPC service on `IServiceCollection`, plus the auth interceptor. This is the single composition point; no full-graph assembly elsewhere.

7. **Program.cs**. Build the host: `var builder = WebApplication.CreateBuilder(args); builder.Services.AddGrpc(o => o.Interceptors.Add<AuthInterceptor>()); /* + DI registrations */ var app = builder.Build(); app.MapGrpcService<FooService>(); app.Run();` with Kestrel bound to `GRPC_HOST`/`GRPC_PORT` over HTTP/2.

8. **Tests** (C# Profile A.8). xUnit + doubles of the port interfaces — no MongoDB.Driver/EF Core, no real DB. Every use case: ≥1 success test + ≥1 error scenario. Every sentinel asserted by ≥1 test.

---

## 5. Self-verification loop (THE BUILD GATE)

After generation, run the gates and **iterate until green**. Do not declare success on unverified code. **`dotnet build` + `dotnet test` is the hard gate** — they MUST pass (this includes the `Grpc.Tools` codegen output compiling cleanly).

```bash
# From protobuf/
buf lint                                          # proto must still pass
# From csharp/  (use the warmed NUGET_PACKAGES cache)
dotnet build                                      # MUST restore (offline from the warmed cache) + compile
dotnet test                                       # MUST pass — THE GATE
```

**The build gate is `dotnet build` (plus `dotnet test`). It MUST pass.** Keep the package set minimal (C# Profile A.1.1/A.11), rely on the warmed NuGet cache (`dotnet build` should restore offline). If a package is missing from the warmed cache and the network is unreachable, report it in §6 as an environment blocker — but never declare success with a failing or skipped gate.

On failure: read the error, fix the offending file, re-run. If the same failure recurs three times without progress, **stop and report** (§6) rather than churning.

Conformance self-audit before finishing:
- Does any `Domain` file reference MongoDB.Driver, EF Core, the gRPC server, or ASP.NET Core? → must be NO (framework-free domain)
- Does any `Application` use case reference gRPC, an ORM, or infrastructure? → must be no
- Does any handler reference `Repositories`? → must be no
- Is `Infrastructure/Config` the only place constructing the full graph? → must be yes
- Are domain types framework-free records derived from the proto (not Mongo docs / not EF entities)? → must be yes
- Is every error message in `Failure_Noun_Descriptor` format? → must be yes
- Does `MapError` cover every new 2xx code explicitly? → must be yes
- Is the proto written to protobuf/proto/milton_prism/services/<service>/v1/ (NOT under csharp/)? → must be yes
- Are the generated *.cs/*Grpc.cs build artifacts (in obj/, not committed/shipped, not hand-edited)? → must be yes
- Is `bin/`, `obj/`, `*.dll`, `*.pdb`, `*.nupkg` absent from the deliverable? → must be yes
- Does `dotnet build` succeed and `dotnet test` pass? → must be yes

---

## 6. Output and generation report

Produce the files in place under `csharp/` (and the proto under `protobuf/`), then emit a concise report:

```
SERVICE: <service>
STATUS: GENERATED | STOPPED
ERROR_PREFIX_USED: <PREFIX>
FILES_CREATED:
  - protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
  - csharp/services/<service>/<Service>.csproj
  - csharp/services/<service>/Program.cs
  - csharp/services/<service>/... (full list)
VERIFICATION:
  buf lint:        PASS|FAIL
  dotnet build:    PASS|FAIL
  dotnet test:     PASS|FAIL   ← the gate (test count)
ASSUMPTIONS:   <any decision made where the spec was silent>
HOLES_HIT:     <none | nats | redis | ...>   # PostgreSQL/MySQL are NOT holes — generated via EF Core (§A.13)
DEVIATIONS:    <any place C# forced a different shape, with reason>
DISCREPANCIES: <any place the reference docs were ambiguous or conflicted>
```

If `STATUS: STOPPED`, the report's body is the precise reason and what must be supplied to proceed.

---

## 7. Hard rejection triggers (quick reference)

Any of these means the output is wrong — catch them yourself before the verifier does:

- Business logic in a handler or repository.
- A layer referencing something forbidden (domain → MongoDB.Driver/EF Core/gRPC; application → infrastructure).
- Domain modeled as MongoDB documents or EF Core entities instead of framework-free records.
- An error `message` in plain English or snake_case (must be `Failure_Noun_Descriptor`).
- A self-chosen error prefix.
- A private service field accessed directly from a handler (use a service method instead).
- `status`/`phase` instead of `state`; `_at` instead of `_time`; `id` instead of `identifier`.
- A second place (besides `Infrastructure/Config`) assembling the full dependency graph.
- `Console.WriteLine` for diagnostics (use `ILogger`).
- An improvised Redis or NATS adapter instead of a stop report. (PostgreSQL/MySQL/MariaDB are NOT holes — generated via EF Core per §A.13; only the wrong tech, e.g. MongoDB.Driver on a SQL store, is forbidden.)
- The proto written under `csharp/proto/` instead of the canonical `protobuf/proto/milton_prism/services/...` path (breaks OpenAPI).
- The generated *.cs/*Grpc.cs hand-edited or shipped.
- The `uint64 identifier` narrowed to a type that loses precision (use C# `long`).
- Shipping `bin/`, `obj/`, `*.dll`, `*.pdb`, or `*.nupkg` in the deliverable.
- Declaring success with a failing or skipped `dotnet build` / `dotnet test`.
