# Milton Prism — Architecture Canon

**Language-agnostic constitution for generated microservices.**

This document defines the rules every microservice produced by Milton Prism MUST obey, regardless of target language (Go, Rust, Python). It is the most stable and most-referenced artifact in the system. It is deliberately language-neutral: it specifies *principles and contracts*, never *mechanisms*.

---

## 0. How to read this document

### Normative language

- **MUST / MUST NOT** — hard rule. A violation is grounds for rejection by the verifier.
- **SHOULD / SHOULD NOT** — strong default. Deviation requires a written, technical justification.
- **MAY** — genuinely optional.

### The composition model

The full instruction set handed to a code-generation agent is **never just this Canon**. It is composed at generation time from two layers:

```
Generator Constitution  =  Architecture Canon  (this document — portable)
                        +  Language Profile     (Go | Rust | Python — mechanism-specific)
```

- **The Canon** owns: hexagonal layering and the dependency rule, the proto/AIP contract, the error taxonomy, the port/adapter philosophy, inter-service communication, the contract-first frontend chain, testing philosophy, and engineering values.
- **A Language Profile** owns: dependency-injection mechanism, logger, mocking framework, error-handling mechanics, documentation style, file/package naming, concrete database drivers, transaction implementation, and ID-generation implementation.

When the Canon states a *principle* and a Profile states a *mechanism*, both apply. If they ever conflict, the Canon wins and the conflict is a bug in the Profile.

> **v1 scope.** The Go Profile is authored from working reference code. Rust and Python Profiles, and the Postgres and NATS adapters, are **profile holes**: their *shape* is fixed by this Canon, but their concrete mechanism is filled in later. A generator MUST NOT improvise a mechanism for an unfilled profile hole; it MUST stop and report the gap.

---

## 1. Hexagonal architecture

Every service is structured in four conceptual layers. The names below are conceptual; a Profile maps them to concrete directories.

| Layer | Responsibility |
|-------|----------------|
| **Domain** | The contract types, typed errors, and constants. No behavior that touches the outside world. |
| **Ports** | Interfaces the application depends on: repositories and the transaction boundary. Pure abstractions. |
| **Application** | The **only** layer that contains business logic and use cases. Orchestrates ports; knows nothing about concrete infrastructure. |
| **Infrastructure** | Adapters that implement ports (data stores) and adapters that expose the application (transport handlers). |

### 1.1 The Dependency Rule (the portable core)

Dependencies point **inward only**: Infrastructure → Application → Ports → Domain. The Domain depends on nothing but the language standard library and the generated contract types.

| Layer | MAY depend on | MUST NOT depend on |
|-------|---------------|--------------------|
| Domain | standard library, generated contract types | ports, application, infrastructure |
| Ports | domain, generated contract types | application, infrastructure |
| Application | domain, ports, generated contract types | any infrastructure (data-store drivers, transport, handlers) |
| Transport handlers | application, domain, error-mapping, generated contract types, transport runtime | data-store adapters |
| Data-store adapters | ports, domain, generated contract types, the data-store driver | application, transport handlers |

These are absolute. A transport handler reaching into a data-store adapter, or the application importing a database driver, is a rejection.

### 1.2 Single composition point

Each service MUST have exactly **one** location where its dependency graph is constructed (concrete repositories built, injected into the application, handler registered on the transport). No other file may construct the full graph. The *mechanism* of injection is a Profile concern; the *single-point* rule is Canon.

### 1.3 Business-logic placement

All business logic lives in the Application layer. Transport handlers only deserialize, delegate, and map errors. Data-store adapters only translate between domain types and storage. A handler or repository containing a business rule is a rejection.

---

## 2. The contract: protobuf + Google AIP

Protobuf is the **single source of truth** for the schema of every service, **even when inter-service transport is not gRPC**. The contract is the neutral waist of the entire system: backends in any language and the frontend client are all generated from it. This section is fully portable and is the most carefully guarded part of the Canon.

Proto files live under `protobuf/proto/<project>/`:
- `services/<service>/v1/` — service contracts (RPCs and their request/response messages)
- `types/<domain>/v1/` — shared resource types, never defined inline in a service proto

### 2.1 Resources and identifiers

- The canonical identifier field is named **`identifier`** (unsigned 64-bit integer). Never `id`, `foo_id`, or any abbreviation.
- Lifecycle state uses a field named **`state`** with an enum named **`<Resource>State`**. Never `status`, `phase`, or `condition` (AIP-216).

### 2.2 Message and method naming (AIP-122 / AIP-132)

The verb comes **first** in both RPCs and messages.

| Element | Correct | Wrong |
|---------|---------|-------|
| Service | `<Resource>Service` (singular) | `FoosService`, `FooSvc` |
| RPC | `GetFoo`, `ListFoos`, `CreateFoo` | `FooGet`, `FooList` |
| Request | `<Verb><Resource>Request` | `Foo<Verb>Request` |
| Response | `<Verb><Resource>Response` | `Foo<Verb>Response` |

### 2.3 Standard methods

| Method | HTTP mapping | Notes |
|--------|--------------|-------|
| Get (AIP-131) | `GET /v1/foos/{identifier}` | `method_signature = "identifier"` |
| List (AIP-132) | `GET /v1/foos` | response is `repeated Foo foos = 1` |
| Create (AIP-133) | `POST /v1/foos`, body `foo` | request carries the full resource: `Foo foo = 1` |
| Update (AIP-134) | `PATCH /v1/foos/{foo.identifier}`, body `foo` | see 2.4 |
| Delete (AIP-135) | `DELETE /v1/foos/{identifier}` | soft delete by default (see 2.6) |

### 2.4 Update with FieldMask (AIP-134) — strict

- `UpdateFooRequest` MUST contain the **full nested resource** (`Foo foo = 1`, REQUIRED) plus `update_mask` (`google.protobuf.FieldMask`, REQUIRED).
- MUST NOT use loose scalar fields in the update request.
- MUST NOT introduce auxiliary update structs (`FooUpdateData`, `FooUpdatePayload`, `FooChanges`) in **any** layer.
- The Application implementation MUST honor the mask field by field. When the mask is nil/empty, it updates all mutable fields (`"*"` semantics) — this case MUST be handled explicitly, never by accident.

### 2.5 Custom methods (AIP-136)

- Separator is a colon `:`, never a slash. Verb after the colon is **camelCase**.
- Side-effecting custom methods use **POST**; read-only use **GET**. Never PATCH.
- Examples: `POST /v1/foos:sendEmail`, `POST /v1/foos/{identifier}:activate`, `GET /v1/foos:listSummaries`.

### 2.6 Time, dates, and soft delete

- Instants: `google.protobuf.Timestamp` with a **`_time`** suffix (`create_time`, `update_time`). Never `string`/`int64`, never an `_at` suffix, never a `json_name` alias.
- Date-only: `google.type.Date` with a **`_date`** suffix.
- Standard behaviors: `create_time` is OUTPUT_ONLY + IMMUTABLE; `update_time` is OUTPUT_ONLY.
- Every **primary domain resource** MUST include `optional delete_time` and `optional purge_time`, both OUTPUT_ONLY (AIP-164). `delete_time` null → active; set → soft-deleted. Expose an `UndeleteFoo` RPC when the resource is recoverable. Support entities (filters, pagination, sub-messages) do not carry these fields.

### 2.7 Enums (AIP-126)

- Every value carries the **complete type name** as prefix in SCREAMING_SNAKE_CASE.
- The zero value is always `<ENUM_NAME>_UNSPECIFIED = 0`.

### 2.8 URL rules

| Rule | Correct | Wrong |
|------|---------|-------|
| Collection segments | plural snake_case (`/consignment_notes`) | singular or camelCase |
| Identifier variable | `{identifier}` | `{id}`, `{foo_id}` |
| Custom-method separator | `:` | `/` |
| `method_signature` | exact request field name (`"identifier"`, `"foo,update_mask"`) | abbreviations |
| Path variables | match request-message field names | mismatched names |

### 2.9 Shared types and linting

- Reusable types live in `types/<domain>/v1/` and are imported — never duplicated inline in service protos.
- Import order: Google standard APIs → OpenAPI annotations → project-owned types.
- The required per-file proto options and their exact order are fixed by the build configuration shipped with the Canon. Generated code is never edited by hand and never appears in a diff.

---

## 3. Domain modeling

- The Domain layer **reuses the generated contract types** as its model. It MUST NOT define parallel structs that duplicate a contract message. *(How a language re-exports or wraps the generated type is a Profile mechanism; the no-duplication principle is Canon.)*
- Domain constants and typed errors live in the Domain layer.

---

## 4. Error taxonomy

The *taxonomy* is portable; the *error-handling mechanics* (wrapping, propagation, pattern-matching) are a Profile concern.

### 4.1 Typed errors

Each service defines a typed error carrying a **code** and a **message**, and a set of named error sentinels in one domain errors location.

- **Code**: a string with the service's unique prefix and a number (e.g. `COMP101`).
- **Message**: MUST follow `Failure_Noun_Descriptor` — PascalCase segments joined by underscores (`Failure_Foo_Not_Found`, `Failure_Internal`). Never plain English, never lowercase snake_case. This value travels in the transport status and is the gateway's readable fallback.

### 4.2 Per-service prefixes — dynamically allocated

Each service owns a **unique** error-code prefix; prefixes are never reused across services. In Milton Prism this allocation is **not static**: the orchestrator maintains a prefix registry and assigns a unique prefix per generated service. The generator MUST request a prefix from the registry, never hardcode one.

### 4.3 Mapping to transport

Transport handlers MUST NOT construct transport-level status errors directly for domain errors. They map each domain error code to the appropriate transport status through a shared error-mapping facility. Unmapped errors fall back to an internal error and are logged. Direct status errors are permitted **only** for request-field validation *before* delegating to the application.

### 4.4 Gateway friendly messages

Every domain error code SHOULD have a human-friendly message entry in the gateway's per-service message map. Absent an entry, the gateway degrades by formatting the `Failure_Noun_Descriptor` value into prose. The map is preferred.

---

## 5. Ports and adapters

### 5.1 Repository ports

A repository is an **interface over domain types**, expressed in the Ports layer, with the standard resource shape: create, get-by-identifier, list-by-filter, update, delete. Adapters in Infrastructure implement these against a concrete store. The interface knows nothing about the store.

### 5.2 The transaction boundary is a port

Transactional boundaries are expressed as a **port**, not as a leaked database concept. The application asks the port to run a unit of work atomically; the adapter decides how (a database session, a SQL transaction, etc.). A nil/absent transaction manager MUST degrade to running the work without a transactional wrapper, so services that need no transactional boundary can omit the dependency. *(The exact signature is a Profile mechanism.)*

### 5.3 ID generation is an adapter concern

Generating the sequential `identifier` is an Infrastructure responsibility and is store-specific (a counter document, a database sequence, an identity column). The Canon fixes only that the result is a conflict-free, monotonically increasing unsigned 64-bit value consistent across the service.

---

## 6. Inter-service communication and data ownership

### 6.1 v1 baseline — synchronous gRPC

- Inter-service calls are **synchronous gRPC**. Each service exposes a gRPC server and consumes other services through generated gRPC clients.
- **Each service owns its data.** A service MUST NOT read or write another service's store directly; it goes through that service's contract.
- Consistency within a single service uses its local transaction boundary (section 5.2). Operations that span services compose synchronous calls and tolerate partial-failure handling at the application layer.

### 6.2 Out of scope for v1 (future profile)

Asynchronous messaging (NATS, events), the Outbox pattern, and Saga orchestration for cross-service consistency are **deliberately excluded from the v1 Canon**. They are a future extension with the same status as the Rust and Python profiles. When introduced, eventing adapters MUST be expressed as ports (publish/subscribe) following section 5, and proto remains the schema source of truth for event payloads.

---

## 7. Contract-first frontend

The frontend client of every generated application is derived from the contract, never hand-written against it.

```
proto (with google.api.http annotations)
   → OpenAPI document (emitted at gateway build time)
   → openapi-generator-cli
   → typed client (consumed by the migrated app's frontend)
```

- The HTTP→gRPC gateway MUST emit an OpenAPI document derived from the `google.api.http` annotations on the protos. This is why service protos import the OpenAPI annotations.
- The generated OpenAPI is the single input to `openapi-generator-cli`. The frontend client MUST be regenerated from it whenever the contract changes, so client and contract never drift.

---

## 8. Testing philosophy

The *philosophy* is portable; the test runner, assertion library, and mock framework are Profile mechanisms.

- The application is tested **against its ports, using mocks** of those ports. The concrete data store is **not** mocked directly; repositories are exercised by integration tests against a real instance of the store.
- Coverage floor: every exported application use case has at least one success test and at least one error-scenario test. Every error sentinel is asserted by at least one test that checks the typed error (not a string comparison).
- Test names follow a `<Operation>_<Scenario>` shape, with conventional scenario suffixes (`OK`, `NotFound`, `AlreadyExists`, `ValidationFailures`, `DuplicateKey`, `InternalError`).
- Tests run in parallel where the language supports it.

---

## 9. Engineering values

- Comments explain **why**, never **what**. A comment that restates the symbol name is removed.
- No AI-documentation tics: phrases like "This function is responsible for", "This method handles", "The purpose of this", "This struct represents", and section headers like "Overview:", "Note:", "Summary:" are rejected.
- No premature abstraction: three similar lines beat a questionable helper.
- No defensive code for impossible internal cases.
- All code, comments, and contract documentation are written in **English**.

---

## 10. Verification gates

The verifier (Prism's "critic") enforces this Canon automatically. Gates split by ownership:

| Gate | Owner | Scope |
|------|-------|-------|
| Proto/AIP conformance (`buf lint` + AIP rules from section 2) | **Canon** | language-independent; runs on the contract |
| OpenAPI emission present and current | **Canon** | the contract-first chain (section 7) |
| Error message format, prefix uniqueness, mapping completeness | **Canon** | taxonomy from section 4 |
| Layer/dependency-import enforcement | **Profile** | one linter per language |
| Test coverage floor and naming | **Profile** | one test runner per language |

A generated service is accepted only when **all applicable gates pass**. A Canon-level failure is never waivable by a Profile.

---

## 11. Hand-off to Language Profiles

For clarity, the following are **explicitly not** Canon and MUST be specified by each Language Profile:

- Dependency-injection mechanism and the single-composition-point file.
- Logger and logging format conventions.
- Mocking framework and mock location.
- Error-handling mechanics (wrapping, propagation, pattern-matching, error-as-value vs exceptions).
- Documentation style (godoc / rustdoc / docstrings) and the file/package naming scheme.
- Concrete database drivers and the data-store adapter implementations.
- The transaction-boundary implementation and signature.
- The ID-generation implementation per store.

A Profile fills these while preserving every principle above. Unfilled holes (Rust, Python, Postgres, NATS as of v1) MUST cause the generator to stop and report, never to improvise.
