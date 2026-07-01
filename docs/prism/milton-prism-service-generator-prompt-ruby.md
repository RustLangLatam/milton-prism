# Milton Prism — Hexagonal Service Generator (Ruby / grpc gem Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, gate-passing hexagonal **Ruby + Bundler + grpc gem + gRPC** microservice that obeys the Architecture Canon and the Ruby Language Profile.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice as a complete app under the source root `ruby/`, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-ruby-profile.md`** — the Ruby + Bundler + grpc gem + Mongoid/ActiveRecord mechanisms.

**Before generating anything, read both documents in full.** They are the source of truth. This prompt does not restate their rules; it orchestrates the workflow and enforces conformance. Where this prompt and a reference document appear to differ, the reference document wins and you flag the discrepancy in your report.

**There is NO `ruby/` skeleton in the repository to copy.** You build the entire app from scratch — the root `Gemfile`, the service code, and the entrypoint. The Ruby Profile (A.2/A.3/A.4) describes every piece; create them all. You DO have the proto contract (below) and a filesystem + shell.

You have a filesystem and a shell. Use them: create the app, write the proto, run the codegen, write the service code, run the gate (`bundle exec rspec` + `ruby -c`), iterate until green.

---

## 1. Inputs

You receive:

### 1.1 The service contract (proto)

One or more `.proto` files for this service (provided inline under "Proto Contract"). **The proto is authoritative for the API surface.** You do not invent RPCs, messages, or fields beyond what the contract defines.

**CANONICAL PROTO LOCATION (mandatory).** Write the authoritative service proto to the platform-canonical path — NOT under `ruby/`:

```
protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto      # shared message types, if any
```

This is the SAME location the Go, Python, Node, Rust and Java profiles use, and the platform's profile-agnostic OpenAPI step reads protos ONLY from `protobuf/proto/milton_prism/services/…` and `protobuf/proto/milton_prism/types/…` to emit `docs/openapi.yaml`. If you place the proto anywhere else (e.g. `ruby/proto/`), the deliverable ships with NO OpenAPI spec. The Ruby gRPC stubs are GENERATED FROM this canonical proto by `grpc_tools_ruby_protoc`; they live under the service `lib/gen/`.

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
store: mongodb                    # mongodb (Mongoid) | postgres | mysql (ActiveRecord)
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
- **Store is supported.** Three stores are generable: `mongodb` (Mongoid, Ruby Profile §A.5/§A.6), `postgres` and `mysql` (both via ActiveRecord — ONE model set + repos, the driver gem / `DATABASE_URL` scheme chosen by store; see Ruby Profile §A.13 and the "Persistence: … (ActiveRecord)" section). Branch on `store`. Any OTHER store (e.g. Redis as a primary store) → stop and report a **profile hole** (Ruby Profile A.10). Never improvise an adapter.
- **No messaging requested.** If the spec asks for events/NATS/pub-sub, stop — hole (A.10).
- **No pre-existing service collision.** If `ruby/services/<service>/` already exists with content, stop and ask.
- **Toolchain available.** Ruby 3 + Bundler + the warmed GEM_HOME + `grpc_tools_ruby_protoc`. Run `ruby -v`, `bundle -v`, and `grpc_tools_ruby_protoc --version`. If absent or wrong major version, report an environment blocker (§6) — do NOT try to download a Ruby/toolchain that may exceed the build budget.

---

## 3. Build order

Create the scaffolding first, then generate strictly inward-to-outward so each layer loads before the next:

```
APP (create once, idempotent — only if absent):
  R1. ruby/Gemfile             (the gem set A.1.1: grpc, grpc-tools, google-protobuf, the STORE gems
                                — mongoid for store: mongodb, OR activerecord + ONE driver for postgres|mysql
                                — never both; jwt; dotenv; rspec)
  R2. ruby/services/<service>/  (the service app skeleton: lib/, spec/, main.rb)

PROTO (canonical location — REQUIRED for the OpenAPI gate):
  P0. Write the authoritative service proto to protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
      (and shared types to protobuf/proto/milton_prism/types/<domain>/v1/<domain>.proto) from the inline
      Proto Contract. SAME path Go/Python/Node/Rust/Java use; the platform OpenAPI step reads ONLY from there.
      Do NOT keep the proto under ruby/proto/.

PROTO CODEGEN (grpc_tools_ruby_protoc):
  P1. Run grpc_tools_ruby_protoc -I protobuf/proto --ruby_out=ruby/services/<service>/lib/gen
      --grpc_out=ruby/services/<service>/lib/gen <the service + type protos>, with the canonical
      protobuf/proto as the include root so transitive imports resolve (google.api, pagination,
      query_params, openapiv3). It emits *_pb.rb (messages) + *_services_pb.rb (service stubs).
      NEVER hand-edit the generated files.

SERVICE (inward → outward), under ruby/services/<service>/lib/<service>/:
  1.  domain/model/...            (framework-free POROs wrapping the proto resources; constants)
  2.  domain/error/...            (DomainError + sentinels, codes off error_prefix)
  3.  application/port/...        (Repository port; TransactionManager port if needs_transaction;
                                   <Dep>Client port per inter_service_deps)
  4.  application/usecase/...     (all use cases — raise DomainError; transport-agnostic)
  5.  infrastructure/repositories/...  (store: mongodb → Mongoid Document + repo adapter + system_counters
                                   id gen + Mongo TransactionManager. store: postgres|mysql → ActiveRecord model
                                   + repo adapter + AR TransactionManager, per §A.13 — NO mongo code) — TxMgr only if needs_transaction
  6.  infrastructure/grpc/...     (GRPC::GenericService impl subclassing the generated <Service>::Service; + auth interceptor)
  7.  infrastructure/config/...   (composition root wiring ports → adapters → use case → gRPC service)
  8.  main.rb                     (build & start the GRPC::RpcServer)
  9.  spec/...                    (RSpec application unit tests via doubles of the ports)
```

All `lib/...` and `spec/...` paths above are relative to `ruby/services/<service>/`.

---

## 4. Per-step generation instructions

Read `milton-prism-ruby-profile.md` §A.2–A.9 carefully — it is your reference (there is no example service to copy). Match its structure, module grouping, and naming precisely.

1. **Domain** (Ruby Profile A.3). Define framework-free POROs wrapping the proto messages — NO Mongoid/ActiveRecord/grpc-server requires in `domain`. Define `DomainError` + sentinels in `domain/error`, numbering codes off `error_prefix` (`<PREFIX>1xx` validation, `<PREFIX>2xx` domain, `<PREFIX>5xx` internal). Every message follows `Failure_Noun_Descriptor`.

2. **Ports** (Ruby Profile A.3). Define duck-typed Ruby port objects in `application/port` (an abstract base raising `NotImplementedError` is fine). Repository port covers the standard CRUD shape from the proto. Include `TransactionManager` if `needs_transaction`. No grpc/Mongoid/ActiveRecord types in the signatures beyond domain types / `DomainError`.

2b. **Inter-service client ports** (skip if `inter_service_deps` empty). For each entry add a `<Dep>Client` port in `application/port` exposing at minimum `validate_<dep>_exists(id)` (raising `DomainError`), plus a `NoOp<Dep>Client` in `infrastructure/repositories`. Wire the NoOp in `infrastructure/config` and inject it; call validation in every use case that writes the FK field.

3. **Application** (Ruby Profile A.3). Implement every use case as a class/method that raises `DomainError`. Validate inputs, raise sentinels from `domain/error`, wrap unexpected errors as `DomainError.internal(...)`. No grpc, no ORM, no infrastructure requires. Honor FieldMask paths on Update.

4. **Repositories.** Branch on `store`:
   - `store: mongodb` (Ruby Profile A.5/A.6): a Mongoid `Document` + a `MongoFooRepository` adapter implementing `ports.FooRepository`, mapping domain ⇄ document. Add `system_counters` ID generation (BSON `Long`). Implement a Mongo `TransactionManager` if `needs_transaction`. Soft-delete `delete_time` if `soft_delete: true`.
   - `store: postgres` | `store: mysql` (Ruby Profile §A.13): an `ActiveRecord::Base` model (autoincrement PK — NO `system_counters`) + an `ArFooRepository` adapter implementing `ports.FooRepository` mapping domain ⇄ model, an `ActiveRecord::Base.transaction`-backed `TransactionManager` if `needs_transaction`, migration-derived schema, nullable `delete_time` if `soft_delete: true`. NO Mongo code.

5. **Handlers** (Ruby Profile A.4). Implement the generated gRPC service: `class FooGrpcService < Foo::V1::FooService::Service`. Each RPC method: read the request → delegate to the use case → on success return the proto response; on `DomainError` raise `map_error(err)` (a `GRPC::BadStatus`). Extract session_id via the injected auth path (`GRPC::ServerInterceptor` placing identity in the call context), never inline token parsing. Never require from `repositories` directly.

6. **config / wiring** (Ruby Profile A.3). The composition root constructs ports → adapters → use case → gRPC service, plus the auth interceptor. This is the single composition point; no full-graph assembly elsewhere.

7. **main.rb**. Build the `GRPC::RpcServer`: `s = GRPC::RpcServer.new; s.add_http2_port("#{GRPC_HOST}:#{GRPC_PORT}", :this_port_is_insecure); s.handle(foo_grpc_service); s.run_till_terminated_or_interrupted([1, 'int', 'SIGTERM'])`, with the auth interceptor registered.

8. **Tests** (Ruby Profile A.8). RSpec + doubles of the port objects — no Mongoid/ActiveRecord, no real DB. Every use case: ≥1 success test + ≥1 error scenario. Every sentinel asserted by ≥1 test.

---

## 5. Self-verification loop (THE BUILD GATE)

After generation, run the gates and **iterate until green**. Do not declare success on unverified code. **`bundle exec rspec` + `ruby -c` is the hard gate** — they MUST pass (this includes the `grpc_tools_ruby_protoc` codegen output loading cleanly).

```bash
# From protobuf/
buf lint                                          # proto must still pass
# From ruby/  (use the warmed GEM_HOME)
bundle install                                    # MUST resolve (offline from the warmed gems)
ruby -c $(find services/<service> -name '*.rb')   # every file parses — exit 0
bundle exec rspec                                 # MUST pass — THE GATE
```

**The build gate is `bundle exec rspec` (plus `ruby -c` on every file). It MUST pass.** Keep the gem set minimal (Ruby Profile A.1.1/A.11), rely on the warmed GEM_HOME (`bundle install` should resolve offline). If a gem is missing from the warmed home and the network is unreachable, report it in §6 as an environment blocker — but never declare success with a failing or skipped gate.

On failure: read the error, fix the offending file, re-run. If the same failure recurs three times without progress, **stop and report** (§6) rather than churning.

Conformance self-audit before finishing:
- Does any `domain` file require Mongoid, ActiveRecord, the grpc server, or Rails? → must be NO (framework-free domain)
- Does any `application` use case require grpc, an ORM, or infrastructure? → must be no
- Does any handler require from `repositories`? → must be no
- Is `infrastructure/config` the only place constructing the full graph? → must be yes
- Are domain types framework-free POROs derived from the proto (not Mongoid docs / not ActiveRecord models)? → must be yes
- Is every error message in `Failure_Noun_Descriptor` format? → must be yes
- Does `map_error` cover every new 2xx code explicitly? → must be yes
- Is the proto written to protobuf/proto/milton_prism/services/<service>/v1/ (NOT under ruby/)? → must be yes
- Are the generated *_pb.rb/*_services_pb.rb present and loading (not hand-edited)? → must be yes
- Is `vendor/bundle/`, `.bundle/`, `tmp/`, `*.gem`, `coverage/` absent from the deliverable? → must be yes
- Does `bundle exec rspec` pass and `ruby -c` exit 0 on every file? → must be yes

---

## 6. Output and generation report

Produce the files in place under `ruby/` (and the proto under `protobuf/`), then emit a concise report:

```
SERVICE: <service>
STATUS: GENERATED | STOPPED
ERROR_PREFIX_USED: <PREFIX>
FILES_CREATED:
  - protobuf/proto/milton_prism/services/<service>/v1/<service>_service.proto
  - ruby/Gemfile
  - ruby/services/<service>/main.rb
  - ruby/services/<service>/lib/<service>/... (full list)
VERIFICATION:
  buf lint:           PASS|FAIL
  ruby -c:            PASS|FAIL
  bundle exec rspec:  PASS|FAIL   ← the gate (test count)
ASSUMPTIONS:   <any decision made where the spec was silent>
HOLES_HIT:     <none | nats | redis | ...>   # PostgreSQL/MySQL are NOT holes — generated via ActiveRecord (§A.13)
DEVIATIONS:    <any place Ruby forced a different shape, with reason>
DISCREPANCIES: <any place the reference docs were ambiguous or conflicted>
```

If `STATUS: STOPPED`, the report's body is the precise reason and what must be supplied to proceed.

---

## 7. Hard rejection triggers (quick reference)

Any of these means the output is wrong — catch them yourself before the verifier does:

- Business logic in a handler or repository.
- A layer requiring something forbidden (domain → Mongoid/ActiveRecord/grpc; application → infrastructure).
- Domain modeled as Mongoid documents or ActiveRecord models instead of framework-free POROs.
- An error `message` in plain English or snake_case (must be `Failure_Noun_Descriptor`).
- A self-chosen error prefix.
- A private service field accessed directly from a handler (use a service method instead).
- `status`/`phase` instead of `state`; `_at` instead of `_time`; `id` instead of `identifier`.
- A second place (besides `infrastructure/config`) assembling the full dependency graph.
- `puts` / `p` / `$stderr.puts` for diagnostics (use `Logger`).
- An improvised Redis or NATS adapter instead of a stop report. (PostgreSQL/MySQL/MariaDB are NOT holes — generated via ActiveRecord per §A.13; only the wrong tech, e.g. Mongoid on a SQL store, is forbidden.)
- The proto written under `ruby/proto/` instead of the canonical `protobuf/proto/milton_prism/services/...` path (breaks OpenAPI).
- The generated *_pb.rb/*_services_pb.rb hand-edited.
- The `uint64 identifier` narrowed to a type that loses precision (use Ruby `Integer`).
- Shipping `vendor/bundle/`, `.bundle/`, `tmp/`, `*.gem`, or `coverage/` in the deliverable.
- Declaring success with a failing or skipped `bundle exec rspec`.
