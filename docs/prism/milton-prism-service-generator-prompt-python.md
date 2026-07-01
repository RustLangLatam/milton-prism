# Milton Prism — Hexagonal Service Generator (Python Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, verifier-passing hexagonal Python microservice that obeys the Architecture Canon and the Python Language Profile.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice into the existing Python monorepo at `python/`, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-python-profile.md`** — the Python + Motor + gRPC mechanisms.

**Before generating anything, read both documents in full.** They are the source of truth. This prompt does not restate their rules; it orchestrates the workflow and enforces conformance. Where this prompt and a reference document appear to differ, the reference document wins and you flag the discrepancy in your report.

You have a filesystem and a shell. Use them: read the existing `python/` tree and `services/identity/` as the reference implementation, match conventions exactly, write files, run the verifier, iterate.

---

## 1. Inputs

You receive:

### 1.1 The service contract (proto)

One or more `.proto` files for this service under `protobuf/proto/<module>/services/<service>/v1/` and any shared types under `.../types/<domain>/v1/`. **The proto is authoritative for the API surface.** You do not invent RPCs, messages, or fields beyond what the contract defines.

### 1.2 The boundary spec

A structured object describing what to build and how it connects:

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
store: mongodb                    # mongodb (Motor) | postgres | mysql (SQLAlchemy 2.0 async)
needs_transaction: true           # wire a MotorTransactionManager; false → omit it
error_prefix: "FOO"               # assigned by the orchestrator registry — NEVER choose your own
inter_service_deps:               # synchronous gRPC clients this service consumes
  - identity
auth: required                    # handlers extract session_id via service method
```

If any field needed to proceed is missing, **stop** (see §2).

---

## 2. Preconditions and stop conditions (fail fast)

Verify before writing any code. If any check fails, **do not generate** — emit a stop report (§6) explaining precisely what is missing or wrong, and halt.

- **Proto present and AIP-conformant.** Run `buf lint` from `protobuf/`. If it fails, stop.
- **Proto stubs generated.** Run `python scripts/gen_proto.py` to ensure the new service's stubs are in `gen/`. If generation fails, stop.
- **Error prefix supplied.** If `error_prefix` is absent, stop. You never allocate a prefix yourself.
- **Store is supported.** `store: mongodb` (Motor, §A.5/§A.6) and `store: postgres` / `store: mysql` (SQLAlchemy 2.0 async, §A.12) are ALL generated. Branch the repository layer on `store` (see step 4). Any OTHER store (e.g. `redis`) is a hole — stop and report a **profile hole** (Python Profile A.10). Never improvise a Redis or unrecognised adapter.
- **No messaging requested.** If the spec asks for events/NATS/pub-sub, stop — hole (A.10).
- **No pre-existing service collision.** If `python/services/<service>/` already exists with content, stop and ask.

---

## 3. Build order

Generate strictly inward-to-outward so each layer is importable before the next:

```
1.  services/<service>/domain/domain.py    (proto type aliases, re-exports)
2.  services/<service>/domain/errors.py    (DomainError sentinels, codes off error_prefix)
3.  services/<service>/domain/__init__.py  (re-exports)
4.  services/<service>/ports/repository.py (UserRepository-style Protocol)
5.  services/<service>/ports/auth.py       (PasswordHasher/TokenManager/SessionStore if needed)
6.  services/<service>/ports/transaction.py (TransactionManager if needs_transaction)
7.  services/<service>/ports/__init__.py
7b. cross-service FK client ports  [ONLY if cross_service_fks is non-empty in the spec]
      For each distinct ref_service in cross_service_fks:
        - services/<service>/ports/{ref_service}_client.py   → {RefService}Client Protocol
        - services/<service>/infrastructure/repositories/noop_{ref_service}_client.py  → NoOp{RefService}Client
8.  services/<service>/application/service.py  (all use cases)
9.  services/<service>/application/__init__.py
10. services/<service>/infrastructure/repositories/<resource>_repository.py
11. services/<service>/infrastructure/repositories/transaction_manager.py   (if needed)
12. services/<service>/infrastructure/grpc_handlers/<service>_handler.py
13. services/<service>/wire.py             (single composition point)
14. services/<service>/__main__.py         (gRPC server entrypoint)
15. services/<service>/tests/test_service.py   (application unit tests)
16. Update .importlinter with new service contracts
```

Stubs for the new service must already exist in `gen/` (step P1 equivalent). Add them by updating `scripts/gen_proto.py` and re-running it.

---

## 4. Per-step generation instructions

Read `services/identity/` in full before writing anything. Match its structure, import grouping, typing patterns, and module names precisely.

1. **Domain** (Python Profile A.3). Import proto-generated types from `gen/`; re-export through `domain.py`. Define `DomainError` sentinels in `errors.py`, numbering codes off `error_prefix` (`<PREFIX>1xx` validation, `<PREFIX>2xx` domain, `<PREFIX>5xx` internal). Every message follows `Failure_Noun_Descriptor`.

2. **Ports** (Python Profile A.3). Define `Protocol` classes (not ABCs). Repository protocol covers the standard CRUD shape from the proto, extended only as the spec requires. Include `TransactionManager` protocol if `needs_transaction`. No grpc imports here.

2b. **Cross-service FK client ports** (skip entirely if `cross_service_fks` is absent or empty). For each **distinct** `ref_service` in `cross_service_fks`:
    - Create `ports/{ref_service}_client.py` with a `{RefService}Client` `Protocol`. It must expose at minimum `async def validate_{ref_service}_exists(self, ctx: ServicerContext, id: int) -> None` (raises `DomainError` if the referenced entity is absent); add further methods only if the FK fields explicitly require them.
    - Create `infrastructure/repositories/noop_{ref_service}_client.py` with a `NoOp{RefService}Client` concrete class that satisfies the Protocol (runtime `isinstance` check is optional but the class must structurally match). All methods return `None` / do nothing.
    - In `wire.py`, instantiate the NoOp client (`{ref_service}_client = NoOp{RefService}Client()`) and pass it to the `Service` constructor.
    - In `application/service.py`, accept the client as a constructor argument (typed as `{RefService}Client`) and call `validate_{ref_service}_exists` in every method that sets or mutates the FK field — this is a hard precondition, not optional. Raise the service's appropriate `DomainError` sentinel on failure.
    - Export the Protocol from `ports/__init__.py`.

3. **Application** (Python Profile A.3). Implement every use case as an `async def` method. Validate inputs, raise sentinels from `domain/errors.py`, wrap unexpected exceptions as `DomainError(ERR_INTERNAL.code, ...) from exc`. No grpc, no motor, no infrastructure imports. Honor FieldMask paths on Update.

4. **Repositories** — branch on `store`:
   - `store: mongodb` (Python Profile A.6): implement ports against `AsyncIOMotorDatabase`. Add `system_counters` ID generation at the seed from the decomposition doc. Implement `MongoTransactionManager` if `needs_transaction`. Add soft-delete timestamp if `soft_delete: true`.
   - `store: postgres` / `store: mysql` (Python Profile A.12, and the prompt's "Persistence: … (SQLAlchemy 2.0 async)" section): implement ports against SQLAlchemy 2.0 async. Define `DeclarativeBase` models in `infrastructure/repositories/models.py` (autoincrement integer PK, nullable `delete_time` for `soft_delete`), one `sqlalchemy_<resource>_repository.py` per owned resource mapping domain↔model, the `shared/sqlalchemy_client/engine.py` async engine builder (driver/URL by store, pool, `create_all` on startup), and a `transaction_manager.py` over `async with session.begin()` if `needs_transaction`. NO `system_counters`, NO Motor, NO Alembic. `pyproject.toml` requires `sqlalchemy[asyncio]` + `asyncpg`/`aiomysql` (NOT motor/pymongo).

5. **Handlers** (Python Profile A.4). Subclass the generated `*Servicer`. Deserialize → delegate → map errors through `shared.errors.mapper.map_error`. Extract session_id via `self._svc.extract_session_id_from_access_token(token)`. Never import from `repositories` directly.

6. **wire.py**. The ONLY place that imports from both application and infrastructure. Build: builder → db → repo → hasher/tokens/sessions → service → servicer.

7. **`__main__.py`**. Standard async gRPC server bootstrap using `grpc.aio.server()`, `_configure_stdout_handler()`, `BaseServiceConfig`, `SIGTERM/SIGINT` handlers.

8. **Tests** (Python Profile A.8). All application tests use `MagicMock`/`AsyncMock` for ports — no Motor/SQLAlchemy session, no real database. Every use case: `≥1` success test + `≥1` error scenario. Every sentinel asserted by `≥1` test. Names: `test_<operation>_<scenario>`. (A repository round-trip test against aiosqlite/in-memory is acceptable for the SQLAlchemy cell.)

9. **import-linter**. Add three contracts for the new service mirroring the identity contracts in `.importlinter`.

---

## 5. Self-verification loop

After generation, run the gates and **iterate until green**. Do not declare success on unverified code.

```bash
# From python/
poetry install          # confirm no new conflicts
buf lint                # from protobuf/ — proto must still pass
ruff check .            # auto-fix with --fix then re-check
mypy --strict .         # add per-module overrides only for external-lib interfaces
lint-imports            # all contracts must be KEPT
pytest -q               # all tests must pass
```

On failure: read the error, fix the offending file, re-run. If the same failure recurs three times without progress, **stop and report** (§6) rather than churning.

Conformance self-audit before finishing:
- Does any `application` file import grpc, motor, or infrastructure? → must be no
- Does any handler import from `repositories`? → must be no
- Is `wire.py` the only place importing from both application and infrastructure? → must be yes
- Are domain types proto aliases, not parallel dataclasses? → must be yes
- Is every error message in `Failure_Noun_Descriptor` format? → must be yes
- Does `map_error` in `shared.errors.mapper` cover every new 2xx code explicitly? → must be yes
- Are there any hand-edited files under `gen/`? → must be no
- For each `ref_service` in `cross_service_fks`: does `ports/{ref_service}_client.py` exist with the `{RefService}Client` Protocol? Is `NoOp{RefService}Client` instantiated and injected in `wire.py`? Does `application/service.py` call the FK validation wherever the referenced field is written? (must be yes for every FK; if any answer is no, generate the missing files before finishing)

---

## 6. Output and generation report

Produce the files in place, then emit a concise report:

```
SERVICE: <service>
STATUS: GENERATED | STOPPED
ERROR_PREFIX_USED: <PREFIX>
FILES_CREATED:
  - services/<service>/domain/domain.py
  - ... (full list)
VERIFICATION:
  buf lint:      PASS|FAIL
  ruff check:    PASS|FAIL
  mypy --strict: PASS|FAIL
  lint-imports:  PASS|FAIL (contracts KEPT count)
  pytest:        PASS|FAIL (test count)
ASSUMPTIONS:   <any decision made where the spec was silent>
HOLES_HIT:     <none | postgresql | nats | redis | ...>
DEVIATIONS:    <any place Python forced a different shape than Go, with reason>
DISCREPANCIES: <any place the reference docs were ambiguous or conflicted>
```

If `STATUS: STOPPED`, the report's body is the precise reason and what must be supplied to proceed.

---

## 7. Hard rejection triggers (quick reference)

Any of these means the output is wrong — catch them yourself before the verifier does:

- Business logic in a handler or repository.
- A layer importing something forbidden (domain → grpc; application → infrastructure).
- Domain modeled as Pydantic models instead of proto aliases.
- An error `message` in plain English or snake_case (must be `Failure_Noun_Descriptor`).
- A self-chosen error prefix.
- `_tokens`, `_repo`, or any private service attribute accessed directly from a handler (use a service method instead).
- `_svc.some_private` accessed outside `wire.py` or the service itself.
- `status`/`phase` instead of `state`; `_at` instead of `_time`; `id` instead of `identifier`.
- A second place (besides `wire.py`) assembling the full dependency graph.
- `print()`, bare `logging.*` calls, or `sys.stdout.write()` for diagnostics (use `shared.logging`).
- An improvised PostgreSQL, MariaDB, Redis, or NATS adapter instead of a stop report.
- Hand-edited files under `gen/`.
- A `cross_service_fks` entry in the spec with no corresponding `ports/{ref_service}_client.py` Protocol, no `NoOp{RefService}Client` injected in `wire.py`, or no FK validation call in `application/service.py`.
