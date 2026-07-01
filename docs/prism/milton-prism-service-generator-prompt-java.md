# Milton Prism — Hexagonal Service Generator (Java / grpc-java Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, build-passing hexagonal **Java + Spring Boot + grpc-java + gRPC** microservice that obeys the Architecture Canon and the Java Language Profile.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice as a complete Maven module under the source root `java/`, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-java-profile.md`** — the Java + Maven + Spring Boot + grpc-java + Spring Data mechanisms.

**Before generating anything, read both documents in full.** They are the source of truth. This prompt does not restate their rules; it orchestrates the workflow and enforces conformance. Where this prompt and a reference document appear to differ, the reference document wins and you flag the discrepancy in your report.

**There is NO `java/` skeleton in the repository to copy.** You build the entire Maven reactor from scratch — the parent `pom.xml`, the service module `pom.xml`, and the service code. The Java Profile (A.2/A.3/A.4) describes every piece; create them all. You DO have the proto contract (below) and a filesystem + shell.

You have a filesystem and a shell. Use them: create the reactor, write the proto, write the service code, run the build gate (`mvn -B package`), iterate until green.

---

## 1. Inputs

You receive:

### 1.1 The service contract (proto)

One or more `.proto` files for this service (provided inline under "Proto Contract"). **The proto is authoritative for the API surface.** You do not invent RPCs, messages, or fields beyond what the contract defines.

**CANONICAL PROTO LOCATION (mandatory).** Write the authoritative service proto to the platform-canonical path — NOT under `java/`:

```
protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto      # shared message types, if any
```

This is the SAME location the Go, Python, Node and Rust profiles use, and the platform's profile-agnostic OpenAPI step reads protos ONLY from `protobuf/proto/milton_prism/services/…` and `protobuf/proto/milton_prism/types/…` to emit `docs/openapi.yaml`. If you place the proto anywhere else (e.g. `java/proto/`), the deliverable ships with NO OpenAPI spec. The Java/grpc-java stubs are GENERATED FROM this canonical proto by `protobuf-maven-plugin` at build time; they live in `target/generated-sources/` and must never be committed.

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
store: mongodb                    # mongodb (Spring Data MongoDB) | postgres | mysql (Spring Data JPA)
needs_transaction: true           # wire a TransactionManager; false → omit it
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
- **Store is supported.** Three stores are generable: `mongodb` (Spring Data MongoDB, Java Profile §A.5/§A.6), `postgres` and `mysql` (both via Spring Data JPA — ONE entity set + repos, the JDBC driver / `DATABASE_URL` scheme chosen by store; see Java Profile §A.13 and the "Persistence: … (JPA)" section). Branch on `store`. Any OTHER store (e.g. Redis as a primary store) → stop and report a **profile hole** (Java Profile A.10). Never improvise an adapter.
- **No messaging requested.** If the spec asks for events/NATS/pub-sub, stop — hole (A.10).
- **No pre-existing service collision.** If `java/services/<service>/` already exists with content, stop and ask.
- **Toolchain available.** JDK 21 + Maven + the warmed `/usr/local/m2` + a `protoc` (the agent image carries them; `protobuf-maven-plugin` also resolves `protoc`/`protoc-gen-grpc-java` from the warmed repo). Run `mvn -v` and `java -version`. If absent or wrong major version, report an environment blocker (§6) — do NOT try to download a JDK/toolchain that may exceed the build budget.

---

## 3. Build order

Create the reactor scaffolding first, then generate strictly inward-to-outward so each layer compiles before the next:

```
REACTOR (create once, idempotent — only if absent):
  R1. java/pom.xml             (PARENT: packaging=pom, Spring Boot BOM import, dependencyManagement,
                                plugin mgmt incl. protobuf-maven-plugin + os-maven-plugin extension, <modules>)
  R2. java/services/<service>/pom.xml   (module pom inheriting parent; the STORE deps: data-mongodb for
                                store: mongodb, OR data-jpa + ONE jdbc driver for postgres|mysql — never both;
                                grpc-* deps; protobuf-maven-plugin bound to generate-sources)

PROTO (canonical location — REQUIRED for the OpenAPI gate):
  P0. Write the authoritative service proto to protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
      (and shared types to protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto) from the inline
      Proto Contract. SAME path Go/Python/Node/Rust use; the platform OpenAPI step reads ONLY from there.
      Do NOT keep the proto under java/proto/.

PROTO CODEGEN (protobuf-maven-plugin, build-time):
  P1. Configure protobuf-maven-plugin in the module pom to point protoSourceRoot at the canonical
      ../../../protobuf/proto include root (so transitive imports resolve: google.api, pagination,
      query_params, openapiv3), with os-maven-plugin as a build extension to resolve protoc/grpc binaries.
      Generated Java lands in target/generated-sources/protobuf/ (message + grpc stubs). NEVER write
      generated code into the tree by hand; never commit it.

SERVICE (inward → outward), under java/services/<service>/src/main/java/com/miltonprism/<service>/:
  1.  domain/model/...            (framework-free POJOs/records mirroring the proto resources; constants)
  2.  domain/error/...            (DomainError + sentinels, codes off error_prefix)
  3.  application/port/...        (Repository interface; TransactionManager interface if needs_transaction;
                                   <Dep>Client interface per inter_service_deps)
  4.  application/usecase/...     (all use cases — throw DomainError; transport-agnostic, framework-light)
  5.  infrastructure/repositories/...  (store: mongodb → @Document + MongoRepository + adapter + system_counters
                                   id gen + MongoTransactionManager. store: postgres|mysql → @Entity + JpaRepository
                                   + adapter + JPA TransactionManager, per §A.13 — NO mongo code) — TxMgr only if needs_transaction
  6.  infrastructure/grpc/...     (gRPC service impl extending the generated <Service>ImplBase; + auth ServerInterceptor)
  7.  infrastructure/config/...   (@Configuration beans wiring ports → adapters → use case → gRPC service)
  8.  Application.java            (main: build & start the grpc-java Server)
  9.  src/main/resources/application.yml   (config, env-overridable)
  10. src/test/java/...           (JUnit 5 application unit tests via Mockito of the ports)
```

All `src/...` paths above are relative to `java/services/<service>/`.

---

## 4. Per-step generation instructions

Read `milton-prism-java-profile.md` §A.2–A.9 carefully — it is your reference (there is no example service to copy). Match its structure, package grouping, and naming precisely.

1. **Domain** (Java Profile A.3). Define framework-free records/POJOs mirroring the proto messages — NO Spring/JPA/grpc/protobuf imports in `domain`. Define `DomainError` + sentinels in `domain/error`, numbering codes off `error_prefix` (`<PREFIX>1xx` validation, `<PREFIX>2xx` domain, `<PREFIX>5xx` internal). Every message follows `Failure_Noun_Descriptor`.

2. **Ports** (Java Profile A.3). Define Java `interface`s in `application/port`. Repository interface covers the standard CRUD shape from the proto, extended only as the spec requires. Include `TransactionManager` if `needs_transaction`. No grpc/Spring/JPA types in the signatures beyond domain types / `DomainError`.

2b. **Inter-service client ports** (skip if `inter_service_deps` empty). For each entry add a `<Dep>Client` interface in `application/port` exposing at minimum `void validate<Dep>Exists(long id) throws DomainError`, plus a `NoOp<Dep>Client` in `infrastructure/repositories`. Wire the NoOp in `infrastructure/config` and inject it; call validation in every use case that writes the FK field.

3. **Application** (Java Profile A.3). Implement every use case as a method that throws `DomainError`. Validate inputs, throw sentinels from `domain/error`, wrap unexpected errors as `DomainError.internal(...)`. No grpc, no Spring Data, no infrastructure imports. Honor FieldMask paths on Update.

4. **Repositories.** Branch on `store`:
   - `store: mongodb` (Java Profile A.5/A.6): `@Document` + `MongoRepository` + a `MongoFooRepository implements ports.FooRepository` adapter mapping domain ⇄ document. Add `system_counters` ID generation (BSON `Long`). Implement `MongoTransactionManager` if `needs_transaction`. Soft-delete `deleteTime` if `soft_delete: true`.
   - `store: postgres` | `store: mysql` (Java Profile §A.13): `@Entity` (autoincrement `Long` `@GeneratedValue(IDENTITY)` PK — NO `system_counters`) + `JpaRepository` + a `JpaFooRepository implements ports.FooRepository` adapter mapping domain ⇄ entity, `@Transactional`-backed `TransactionManager` if `needs_transaction`, `ddl-auto=update` schema, nullable `delete_time` if `soft_delete: true`. NO Mongo code.

5. **Handlers** (Java Profile A.4). Implement the generated gRPC service: `class FooGrpcService extends FooServiceGrpc.FooServiceImplBase`. Each RPC method: read the request → delegate to the use case → on success `responseObserver.onNext(toProto(result)); responseObserver.onCompleted();`; on `DomainError` `responseObserver.onError(mapError(err))`. Extract session_id via the injected auth path (`ServerInterceptor` placing identity in gRPC `Context`), never inline token parsing. Never import from `repositories` directly.

6. **config / wiring** (Java Profile A.3). `@Configuration` beans construct ports → adapters → use case → gRPC service, plus the `ServerInterceptor`. This is the single composition point; no full-graph assembly elsewhere.

7. **Application.java**. Build the grpc-java `Server`: `ServerBuilder.forPort(GRPC_PORT).addService(fooGrpcService).intercept(authInterceptor).build().start()`, then `awaitTermination()`, with graceful shutdown on SIGTERM/SIGINT.

8. **Tests** (Java Profile A.8). JUnit 5 + Mockito mocks of the port interfaces — no Mongo/JPA, no real DB. Every use case: ≥1 success test + ≥1 error scenario. Every sentinel asserted by ≥1 test.

---

## 5. Self-verification loop (THE BUILD GATE)

After generation, run the gates and **iterate until green**. Do not declare success on unverified code. **`mvn -B package` is the hard build gate** — it MUST exit 0 (this includes `protobuf-maven-plugin` codegen against the canonical proto).

```bash
# From protobuf/
buf lint                                          # proto must still pass
# From java/  (use the warmed local repo)
mvn -B -Dmaven.repo.local=/usr/local/m2 package   # MUST exit 0 — THE GATE (codegen + compile + assemble)
mvn -B -Dmaven.repo.local=/usr/local/m2 test      # run the unit tests
```

**The build gate is `mvn -B package`. It MUST exit 0.** Building Spring Boot + grpc-java is heavy: keep deps minimal (Java Profile A.1.1/A.11), rely on the warmed `/usr/local/m2` (prefer `-o` offline when complete), avoid heavy optional starters. If the local repo is missing an artifact and the network is unreachable, report it in §6 as an environment blocker — but never declare success with a failing or skipped build.

On failure: read the compiler/Maven error, fix the offending file, re-run. If the same failure recurs three times without progress, **stop and report** (§6) rather than churning.

Conformance self-audit before finishing:
- Does any `domain` class import Spring, JPA/Jakarta Persistence, grpc, or protobuf? → must be NO (framework-free domain)
- Does any `application` use case import grpc, Spring Data, or infrastructure? → must be no
- Does any handler import from `repositories`? → must be no
- Is `infrastructure/config` the only place constructing the full graph? → must be yes
- Are domain types framework-free records derived from the proto (not JPA entities / not grpc messages)? → must be yes
- Is every error message in `Failure_Noun_Descriptor` format? → must be yes
- Does `mapError` cover every new 2xx code explicitly? → must be yes
- Is the proto written to protobuf/proto/milton_prism/services/<service>/v1/ (NOT under java/)? → must be yes
- Is the generated protobuf/grpc code left in target/generated-sources (never committed)? → must be yes
- Is `target/`, `.class`, `.jar`, and any vendored `.m2` absent from the deliverable? → must be yes
- Does `mvn -B package` exit 0? → must be yes

---

## 6. Output and generation report

Produce the files in place under `java/` (and the proto under `protobuf/`), then emit a concise report:

```
SERVICE: <service>
STATUS: GENERATED | STOPPED
ERROR_PREFIX_USED: <PREFIX>
FILES_CREATED:
  - protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
  - java/pom.xml
  - java/services/<service>/pom.xml
  - java/services/<service>/src/main/java/com/miltonprism/<service>/Application.java
  - ... (full list)
VERIFICATION:
  buf lint:        PASS|FAIL
  mvn -B package:  PASS|FAIL   ← the gate
  mvn -B test:     PASS|SKIP (test count)
ASSUMPTIONS:   <any decision made where the spec was silent>
HOLES_HIT:     <none | nats | redis | ...>   # PostgreSQL/MySQL are NOT holes — generated via Spring Data JPA (§A.13)
DEVIATIONS:    <any place Java forced a different shape, with reason>
DISCREPANCIES: <any place the reference docs were ambiguous or conflicted>
```

If `STATUS: STOPPED`, the report's body is the precise reason and what must be supplied to proceed.

---

## 7. Hard rejection triggers (quick reference)

Any of these means the output is wrong — catch them yourself before the verifier does:

- Business logic in a handler or repository.
- A layer importing something forbidden (domain → Spring/JPA/grpc/protobuf; application → infrastructure).
- Domain modeled as JPA entities or grpc-generated messages instead of framework-free records.
- An error `message` in plain English or snake_case (must be `Failure_Noun_Descriptor`).
- A self-chosen error prefix.
- A private service field accessed directly from a handler (use a service method instead).
- `status`/`phase` instead of `state`; `_at` instead of `_time`; `id` instead of `identifier`.
- A second place (besides `infrastructure/config`) assembling the full dependency graph.
- `System.out.println` / `printStackTrace()` for diagnostics (use SLF4J).
- An improvised Redis or NATS adapter instead of a stop report. (PostgreSQL/MySQL/MariaDB are NOT holes — generated via Spring Data JPA per §A.13; only the wrong tech, e.g. the Mongo starter on a SQL store, is forbidden.)
- The proto written under `java/proto/` instead of the canonical `protobuf/proto/milton_prism/services/...` path (breaks OpenAPI).
- Generated protobuf/grpc code committed into the tree instead of left in `target/generated-sources/`.
- The `uint64 identifier` narrowed to a type that loses precision (use `Long`).
- Shipping `target/`, `.class`, `.jar`, or a vendored `.m2` in the deliverable.
- Declaring success with a failing or skipped `mvn -B package`.
