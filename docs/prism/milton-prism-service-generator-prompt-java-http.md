# Milton Prism — Hexagonal Service Generator, Java HTTP-native / Spring Boot (Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, build-passing hexagonal **Java** microservice whose **only** wire protocol is **HTTP** — a **Spring Boot** application (`@SpringBootApplication` with `@RestController` handlers) — obeying the Architecture Canon and the Java Language Profile.

This is the HTTP/Spring-MVC homologue of `milton-prism-service-generator-prompt-java.md` (the Java + grpc-java + gRPC prompt). It is identical in spirit — same hexagonal layering (domain / application / infrastructure), same domain/error rules, same proto-as-contract discipline, same persistence branching on `store` (mongodb via Spring Data MongoDB + `system_counters`; postgres|mysql via Spring Data JPA per Java Profile §A.13) — and differs **only on the transport edge**: the service exposes HTTP natively through Spring `@RestController`s and **never** starts a gRPC server. Read the Java gRPC prompt and the Java Profile as the baseline; this document records the deltas and the HTTP-specific obligations.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice as a complete Maven module under the source root `java/`, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-java-profile.md`** — the Java + Spring Boot + Spring Data mechanisms (framework-free domain records, ports, use cases, repositories, wiring, tests). Read the gRPC sections for the hexagonal layering only; the transport edge is replaced by Spring MVC here (see Profile §A.4-HTTP).

**Before generating anything, read both documents in full.** Where this prompt and a reference document differ on the **transport**, this prompt wins (it is the HTTP variant); on everything else the reference document wins and you flag the discrepancy in your report.

**There is NO `java/` skeleton in the repository to copy.** You build the entire Maven reactor from scratch — the parent `pom.xml`, the service module `pom.xml`, and the service code. The Java Profile describes every piece; create them all. You DO have the proto contract (below) and a filesystem + shell.

You have a filesystem and a shell. Use them: create the reactor, write the service code, run the build gate (`mvn -B package`), iterate until green.

### Fixed stack (non-negotiable)

- **Web framework:** **Spring Boot** (`spring-boot-starter-web`, Spring MVC). The app is a `@SpringBootApplication` with `@RestController` handlers; routes declared by `@GetMapping`/`@PostMapping`/`@PatchMapping`/`@DeleteMapping`.
- **Server / entrypoint:** the **Spring Boot application is the ONLY entrypoint** — `Application.java` with `public static void main` calling `SpringApplication.run(Application.class, args)`. There is **NO** grpc-java server.
- **NO gRPC AT RUNTIME (CRITICAL, Java Profile §A.4-HTTP):** there MUST be no `io.grpc.Server`, no `ServerBuilder`, no `BindableService`, no `*ImplBase`, and no `infrastructure/grpc/` package. The `protobuf-maven-plugin` does NOT generate or wire a gRPC server.
- **Persistence:** branch on `store` (same as the gRPC profile). `store: mongodb` → Spring Data MongoDB (`@Document`, `MongoRepository`) with `system_counters` identifier generation (BSON `Long`). `store: postgres` | `store: mysql` → **Spring Data JPA** (Java Profile §A.13): `@Entity` + `JpaRepository`, the JDBC driver (`org.postgresql:postgresql` / `org.mariadb.jdbc:mariadb-java-client`) + `DATABASE_URL` scheme (`jdbc:postgresql://…` / `jdbc:mariadb://…`) chosen by store, `ddl-auto=update` schema, autoincrement `Long` `@GeneratedValue(IDENTITY)` PK (NO `system_counters`), nullable `delete_time` soft-delete.
- **Models / validation:** request/response messages are **POJOs/records or Jackson DTOs** (`@RequestBody`, `@PathVariable`, `@RequestParam` → `ResponseEntity`/`@ResponseBody`), equivalent to the proto messages. You do **not** need the grpc-generated server stub and do **not** run grpc-java server codegen. The `.proto` is still written (it is the authoritative API contract that drives the OpenAPI); the DTOs are only the in-process HTTP representation. (Keeping `protobuf-maven-plugin` purely for message types is optional and adds build cost — plain Jackson DTOs are the recommended, lightest path.)
- **Config:** `application.yml` + environment, using `SERVER_PORT` (Spring's `server.port`) in place of `GRPC_PORT`.
- **Language gate:** `mvn -B package` (the whole reactor compiles + assembles, exit 0). This is the build gate; `mvn -B test` runs the tests.

---

## 1. Inputs

Identical to the Java gRPC prompt §1: you receive the service contract (proto), and the boundary spec (`service`, `resources`, `rpcs`, `store`, `needs_transaction`, `error_prefix`, `inter_service_deps`, `auth`). The proto is authoritative for the API surface; you do not invent RPCs, messages, or fields.

**CANONICAL PROTO LOCATION (mandatory).** Write the authoritative service proto to the platform-canonical path — NOT under `java/`:

```
protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto      # shared message types, if any
```

This is the SAME location the Go, Python, Node and Rust profiles use, and the platform's profile-agnostic OpenAPI step reads protos ONLY from `protobuf/proto/milton_prism/services/…` and `protobuf/proto/milton_prism/types/…` to emit `docs/openapi.yaml`. If you place the proto anywhere else (e.g. `java/proto/`), the deliverable ships with NO OpenAPI spec.

---

## 2. Preconditions and stop conditions (fail fast)

Same as the Java gRPC prompt §2 (proto present and AIP-conformant via `buf lint`, error prefix supplied, store is generable — `mongodb`/`postgres`/`mysql`, branch per the gRPC prompt §2 and Java Profile §A.13, any other store stops as a hole — no messaging/NATS, no pre-existing collision, JDK 21 + Maven + warmed `/usr/local/m2` available), **plus**:

- **The contract proto MUST carry `google.api.http` annotations.** Every RPC in the service proto must have a `google.api.http` option mapping it to an HTTP method + path. If the supplied proto lacks them, you **add** them (this is part of your job — see §4.0); they are not optional. The platform derives `docs/openapi.yaml` from these annotations; without them the OpenAPI is empty.

---

## 3. Build order

Create the reactor scaffolding first, then generate strictly inward-to-outward. This mirrors the Java gRPC build order with the transport layer swapped from grpc-java service impls to Spring `@RestController`s:

```
REACTOR (create once, idempotent — only if absent):
  R1. java/pom.xml             (PARENT: packaging=pom, Spring Boot BOM import, dependencyManagement, plugin mgmt, <modules>)
  R2. java/services/<service>/pom.xml   (module pom inheriting parent: spring-boot-starter-web + the STORE deps:
                                data-mongodb for store: mongodb, OR data-jpa + ONE jdbc driver for postgres|mysql —
                                never both; NO grpc server deps, NO grpc-java server codegen)

PROTO (canonical location — REQUIRED for the OpenAPI gate; WITH google.api.http):
  P0. Write the authoritative service proto to protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
      (and shared types to .../types/<domain>/v1/<domain>.proto) from the inline Proto Contract, adding a
      google.api.http option to EVERY RPC. SAME path Go/Python/Node/Rust use; the platform OpenAPI step reads ONLY
      from there. Do NOT keep the proto under java/proto/.

SERVICE (inward → outward), under java/services/<service>/src/main/java/com/miltonprism/<service>/:
  1.  domain/model/...            (framework-free POJOs/records mirroring the proto messages; newtypes; constants)
  2.  domain/error/...            (DomainError + sentinels, codes off error_prefix)
  3.  application/port/...        (Repository interface; TransactionManager interface if needs_transaction; <Dep>Client interface)
  4.  application/usecase/...     (all use cases — throw DomainError; transport-agnostic)
  5.  infrastructure/repositories/...  (store: mongodb → @Document + MongoRepository + adapter + system_counters
                                   + MongoTransactionManager. store: postgres|mysql → @Entity + JpaRepository + adapter
                                   + JPA TransactionManager, per §A.13 — NO mongo code) — TxMgr only if needs_transaction
  6.  infrastructure/http/...     (@RestController per resource — one handler method per RPC; Jackson DTOs)
  7.  infrastructure/http/...     (@RestControllerAdvice / @ExceptionHandler: DomainError → HTTP status + {code,message})
  8.  infrastructure/config/...   (@Configuration beans wiring ports → adapters → use case → controllers; Security filter chain if auth)
  9.  Application.java            (@SpringBootApplication main — SpringApplication.run; NO grpc server)
  10. src/main/resources/application.yml   (config, env-overridable; server.port)
  11. src/test/java/...           (JUnit 5 application unit tests via Mockito + @WebMvcTest / MockMvc controller tests)
```

All `src/...` paths above are relative to `java/services/<service>/`. Steps 1–5 and the application tests are **the same hexagonal shape** as the Java gRPC prompt — generate them as the Java Profile instructs. Steps **P0, 6, 7, 8, 9** are the transport deltas below. There is **no** `infrastructure/grpc/`, **no** grpc-java server, and **no** grpc-java server codegen.

---

## 4. Per-step generation instructions (transport deltas)

### 4.0 Proto — authoritative contract WITH HTTP annotations

Write (or patch) the service proto at the canonical path and the shared types under `.../types/<domain>/v1/`. Every RPC MUST carry a `google.api.http` option, e.g.:

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

### 4.6–4.8 Spring MVC transport edge (replaces the grpc-java service-impl step)

The transport layer is a **Spring `@RestController` app**, the service's ONLY entrypoint. Do NOT create a gRPC server, do NOT use `io.grpc.ServerBuilder`, do NOT implement a `BindableService`/`*ImplBase`, do NOT run grpc-java server codegen, and do NOT register any API gateway.

Create the controllers under `infrastructure/http/`:

- A `@RestController` (one per resource, or per service) with **one handler method per RPC**, each mounted on the method + path declared by that RPC's `google.api.http` annotation (method + path must match the proto). Spring path syntax uses `{param}`; map `{identifier}` to `@PathVariable`. Each handler decodes the request from path (`@PathVariable`), query (`@RequestParam`), and a typed JSON body (`@RequestBody DTO`), calls the application use case, and returns `ResponseEntity<DTO>` (or `@ResponseBody DTO`).
- Validate request fields the same way the gRPC handler would (throw the service's `DomainError` sentinels from `domain/error`).
- Map domain errors to HTTP status codes via a `@RestControllerAdvice` with `@ExceptionHandler(DomainError.class)`: translate each domain `code` to an HTTP status (validation `<PREFIX>1xx` → 400, not-found → 404, forbidden → 403, conflict → 409, internal `<PREFIX>500` → 500), with a body `{ code, message }`.
- Inject the auth check when `auth: required`: read the bearer token from the `Authorization` header via a Spring Security **filter chain** (`SecurityFilterChain` bean) or an `OncePerRequestFilter`, and resolve the session/user id the same way the gRPC path resolves it from metadata (Java Profile §A.12). Expose the identity via a request attribute / `SecurityContext`.
- Add a liveness route `GET /health` returning `200` with `{ "status": "ok" }`.

### 4.8 Wiring (transport delta)

`infrastructure/config` `@Configuration` beans build config → the store handle (Mongo template/repos OR a JPA `DataSource`/repos) → repo adapter → (transaction manager) → application use case → the `@RestController`(s) + the Security filter chain. It is the only place constructing the full graph. There is **no** grpc-java `Server` and **no** `addService` call.

### 4.9 Entrypoint (transport delta)

`Application.java` is a `@SpringBootApplication` whose `main` calls `SpringApplication.run(Application.class, args)`. Spring Boot's embedded servlet container serves the controllers on `server.port`.

- Do NOT build an `io.grpc.Server`/`ServerBuilder`, do NOT register a gRPC health service, do NOT call any `.addService(...)`, do NOT register any gateway. The Spring Boot application is the entire entrypoint.

---

## 5. Self-verification loop (THE BUILD GATE)

After generation, run the gates and **iterate until green**. The build gate is **`mvn -B package`** (the whole reactor compiles + assembles, exit 0):

```bash
# From protobuf/
buf lint                                          # proto + google.api.http must resolve
# From java/  (use the warmed local repo)
mvn -B -Dmaven.repo.local=/usr/local/m2 package   # THE BUILD GATE — must exit 0 (Spring Boot app + all modules)
mvn -B -Dmaven.repo.local=/usr/local/m2 test      # application unit tests + MockMvc controller tests
```

Building Spring Boot MVC is lighter than grpc-java (no proto/grpc server codegen) — keep deps minimal (Java Profile A.1.1/A.11) and rely on the warmed `/usr/local/m2` (prefer `-o` offline when complete). If the local repo is missing an artifact and the network is unreachable, report it in §6 as an environment blocker — but never declare success with a failing or skipped build.

On failure: read the compiler/Maven error, fix the offending file, re-run. If the same failure recurs three times without progress, **stop and report** (§6) rather than churning.

Conformance self-audit (answer each in the report) — the non-transport audits from the Java gRPC prompt PLUS the transport-specific checks:

- Is the application layer transport-agnostic (no Spring MVC import, no controller import in `application/`)? (must be yes)
- Is the **domain** layer framework-free (no Spring/JPA/grpc/protobuf)? (must be yes)
- Does the changeset contain **zero** `io.grpc.Server`/`ServerBuilder`, `.addService(`, `BindableService`/`*ImplBase`, grpc-java server codegen, or `infrastructure/grpc/`? (must be yes)
- Is there an `Application.java` that runs Spring Boot as the sole entrypoint (`SpringApplication.run` — no grpc server)? (must be yes)
- Does every RPC in the service proto carry a `google.api.http` annotation? (must be yes — the OpenAPI is derived from them)
- Are the request/response bodies plain Java/Jackson DTOs (not grpc-generated message types hand-marshalled through a gRPC server)? (must be yes)
- Is `target/`, `.class`, `.jar`, and any vendored `.m2` absent from the deliverable? (must be yes)
- Does `mvn -B package` exit 0? (must be yes — the build gate)

---

## 6. Output and generation report

Same as the Java gRPC prompt §6, with `TRANSPORT: HTTP (Spring Boot)` noted and the HTTP route table (RPC → method+path) listed under `ASSUMPTIONS`/notes. The `VERIFICATION` block reports `mvn -B package` as the gate (there is no grpc-java server-codegen line).

---

## 7. Hard rejection triggers (quick reference)

All the Java gRPC prompt §7 triggers, **plus**:

- An `io.grpc.Server`/`ServerBuilder` or any `.addService(...)` call anywhere in the changeset — this is the HTTP variant; the gRPC server must not exist.
- A `BindableService`/`*ImplBase` implementation, grpc-java SERVER codegen, or an `infrastructure/grpc/` module — the transport is Spring MVC.
- An `Application.java` that starts a gRPC server instead of (or in addition to) Spring Boot.
- An RPC in the service proto with no `google.api.http` annotation — the OpenAPI cannot be derived without it.
- The application or domain layer importing Spring MVC / a controller class — transport concerns belong only in `infrastructure/http/`.
- The **domain** layer importing Spring/JPA/grpc/protobuf — the domain must be framework-free.
- Hand-editing `docs/openapi.yaml` instead of letting the platform derive it from the proto.
