# Milton Prism — Python Profile + Implementation Tasks (autonomous run)

This document has two parts. **Part A** is the Python Language Profile — the architectural decisions, already fixed. **Part B** is the sequenced implementation tasks. Claude Code must treat Part A as frozen: implement exactly what it says, do **not** invent or change architecture. If something in Part A is ambiguous or impossible, **stop and write a STOP note in `docs/prism/python-profile-blockers.md`** rather than improvising — a wrong architectural guess made unsupervised compounds across every later task.

The Python Profile composes with the **Architecture Canon** (`docs/prism/milton-prism-architecture-canon.md`) exactly like the Go Profile does: the Canon's principles, the proto/AIP contract, the error taxonomy, the dependency rule, and the testing philosophy all still apply, unchanged. This document only fills the Python *mechanisms*.

> **Scope note:** this Profile defines the **output** language for generated microservices. It is independent of the analysis engine's input languages. v1 supported store is **MongoDB**; transport is **gRPC**. Postgres/MariaDB and NATS remain holes — if requested, stop, do not improvise.

---

# PART A — Python Language Profile (FROZEN DECISIONS)

## A.1 Stack (fixed)

- **Framework:** FastAPI (HTTP surface) + **grpcio** / grpcio-tools (gRPC servers and inter-service clients). gRPC is the primary transport per the Canon; FastAPI fronts the HTTP/gateway surface.
- **Models:** Pydantic v2 for domain/validation models; the generated protobuf messages remain the source of truth (Pydantic models wrap/adapt them, they do not replace them).
- **Mongo driver:** **Motor** (async MongoDB driver).
- **Async:** the service is async end to end (`async def`); never block the event loop with sync I/O.
- **Dependency management:** `pyproject.toml` with Poetry; pinned versions; lockfile committed.
- **Python:** 3.12+.

## A.2 Repository layout (mirrors the Go monorepo conceptually)

```
<module>/                                  # python monorepo root
├── pyproject.toml
├── proto/<module>/...                     # SAME proto contracts as Go — protobuf is language-neutral
├── gen/<module>/...                       # buf/grpcio-tools generated Python stubs — NEVER edited by hand
├── shared/                                # cross-cutting (mirrors core/shared in Go)
│   ├── errors/                            # typed error model + transport mapping
│   ├── mongo_client/                      # Motor client builder + lifecycle
│   ├── grpc_client_sdk/                   # inter-service gRPC client builders
│   ├── interceptors/                      # logging, ctx-id, panic-recovery (gRPC interceptors)
│   ├── logging/                           # the only logger (mirrors applog)
│   └── config/                            # typed config loading
└── services/
    └── <service>/
        ├── domain/                        # domain types (wrap proto), typed errors, constants
        ├── ports/                         # repository + transaction-boundary Protocols (abstract)
        ├── application/                   # use cases — the only place with business logic
        ├── infrastructure/
        │   ├── grpc_handlers/             # servicer: deserialize → delegate → map errors
        │   └── repositories/              # Motor implementations of ports
        ├── wire.py                        # single composition point
        └── tests/                         # pytest, mirrors the layers
```

Entrypoint per service: `services/<service>/__main__.py` (or `cmd/<service>/main.py`) bootstrapping the gRPC server.

## A.3 Hexagonal mechanisms (the Python equivalents)

- **Layering & dependency rule:** identical to the Canon. Domain depends on nothing but stdlib + generated proto. Application depends only on domain + ports. Infrastructure depends inward. **Enforced** (see A.9).
- **Ports = `typing.Protocol`** (structural interfaces), not ABCs, so adapters need no explicit inheritance. Repository protocols and the transaction-boundary protocol live in `ports/`.
- **Dependency injection = constructor injection**, assembled only in `wire.py`. No global singletons, no service locator, no framework magic DI in the domain/application layers. This is the Python analogue of the Go `wire.go` single-composition-point rule.
- **Domain types** wrap the generated proto messages (e.g. a thin dataclass/Pydantic alias) — **no parallel models** that duplicate a proto message. The proto stays the single source of truth.

## A.4 Error model (Python mechanism for the Canon's taxonomy)

- A typed `DomainError` exception carrying `code: str` and `message: str`, with the same `Failure_Noun_Descriptor` message format and the orchestrator-assigned prefix (e.g. `IDN201`). Sentinels defined in `domain/errors.py`.
- Application raises `DomainError`. Handlers catch and **map** to gRPC status via a shared `map_error` helper in `shared/errors` (the analogue of `coreerror` + `mapError`). Direct `grpc` status is allowed **only** for request-field validation before delegating.
- Every new code gets a friendly-message entry in the gateway's per-service message map.

## A.5 Transaction boundary (port + Motor adapter)

- A `TransactionManager` Protocol with `async def with_transaction(self, fn)` — `fn` is an async callable receiving the session-bound context. Mirrors the Go closure mechanism, async-flavored.
- Motor adapter implements it over `AsyncIOMotorClient.start_session()` + `session.with_transaction(...)`. Nil/None manager degrades to running `fn` without a transaction, same as Go.

## A.6 ID generation (Mongo mechanism)

- Same `system_counters` pattern as the Go Profile: a dedicated collection, `find_one_and_update` with `$inc`, `return_document=AFTER`, seeded at a reserved start value, retry on duplicate-key. Produces conflict-free monotonic `int` identifiers (mapped to the proto `uint64 identifier`).

## A.7 Logging

- One logger module in `shared/logging` (the `applog` analogue). Structured format `"<context>: key=value"`. **Forbidden:** bare `print`, the root `logging` calls scattered in layers, or any ad-hoc logger. Everything goes through the shared logger.

## A.8 Testing (Python mechanism for the Canon's philosophy)

- **pytest** + **pytest-asyncio**. Application tested against its ports using mocks (`unittest.mock`/`pytest` fixtures); Mongo is **not** mocked — repositories get integration tests against a real MongoDB (testcontainers or a provided instance).
- Coverage floor (Canon §8): every exported application use case has a success test + ≥1 error scenario; every error sentinel asserted by ≥1 test. Test names `test_<operation>_<scenario>`.
- Type checking: **mypy strict** on the whole tree is part of the gates.

## A.9 Python-specific verifier gates

In addition to the Canon-level gates (`buf lint`, AIP, OpenAPI emission):

- **Layer-import linter** — enforce the dependency rule. Use `import-linter` with contracts encoding: domain may not import ports/application/infrastructure; application may not import infrastructure/grpc/motor; handlers may not import repositories. A violation fails the build.
- **mypy --strict** passes.
- **ruff** (lint + format) passes.
- **pytest** passes with the coverage floor.
- No hand-edited files under `gen/`.

## A.10 What stays a hole

PostgreSQL, MariaDB, NATS, and any non-gRPC transport. If a task or boundary spec requires them, **stop and write a blocker note** — do not improvise an adapter.

---

# PART B — Implementation tasks (run in order; gates between each)

**Standing rules for every task below:**
- Part A is frozen. Implement it; do not redesign it. Ambiguity/impossibility → STOP note in `docs/prism/python-profile-blockers.md`, then continue with the next independent task if possible.
- Obey the Canon dependency rule. No framework magic or third-party SDK in domain/application.
- After each task, run the gate block. Do not proceed past a red gate; if you cannot make it green after 3 attempts, write a STOP note and move on.
- Mirror the existing Go monorepo's conventions where they are language-neutral (naming, structure, error format).

**Gate block (run after each task):**
```
poetry install
buf lint
ruff check .
mypy --strict .
lint-imports            # import-linter contracts
pytest -q
```

### Task P0 — Write the Profile document
Create `docs/prism/milton-prism-python-profile.md` containing Part A of this document verbatim, formatted as the canonical Python Profile (peer to the Go Profile). This is the reference the rest of the tasks cite.

### Task P1 — Toolchain & generated stubs
Set up the Python monorepo: `pyproject.toml` (Poetry, Python 3.12, deps: fastapi, grpcio, grpcio-tools, protobuf, motor, pydantic v2, pytest, pytest-asyncio, mypy, ruff, import-linter). Configure proto → Python stub generation from the SAME `proto/` contracts the Go side uses (grpcio-tools or buf with the python plugin) into `gen/`. Generate stubs for the existing `identity` proto. Gate must pass on an otherwise empty tree.

### Task P2 — Shared foundation
Implement `shared/`: the structured logger (A.7), the typed config loader (A.1/A.2), the `DomainError` + `map_error` to gRPC status (A.4), the Motor `mongo_client` builder with lifecycle (A.1), the gRPC interceptors (logging, ctx-id, recovery) (A.3), and the inter-service gRPC client builder skeleton (A.3). Unit tests for the logger, error mapping, and config. Define the `import-linter` contracts (A.9) now so later tasks are checked against them.

### Task P3 — Generate the `identity` service (the validation service)
Using the Architecture Canon + the Python Profile (Part A) + the existing `identity` boundary spec and proto, build the full `identity` service in Python following the hexagonal layout (A.2): domain (wrap proto + errors), ports (Protocols), application (use cases — password hashing, token issuance, sessions; if no shared auth package exists in Python yet, implement minimal, clearly-bounded adapters and note them), infrastructure (grpc_handlers servicer + Motor repository + transaction manager + id generation), `wire.py`, entrypoint, and tests meeting the coverage floor. This service is the **proof** the Profile works; treat it as the reference implementation.

### Task P4 — Adapt the service generator prompt for Python
Create `docs/prism/milton-prism-service-generator-prompt-python.md`: a copy of the existing service generator prompt, adjusted so its mechanism references point to the Python Profile instead of the Go Profile (layout, DI = wire.py, errors = DomainError/map_error, mocks = pytest, store = Motor, gates = A.9). The Canon references stay identical. Do not change the workflow, stop conditions, or the boundary-spec input format.

### Task P5 — Validation report
Run the full gate block on the whole tree and write `docs/prism/python-profile-validation.md` summarizing: gates status, the `identity` service file list, coverage numbers, any STOP/blocker notes, and any place where Python forced a deviation from the Go Profile's shape (with the reason). Do **not** generate the other services (repository, migration) autonomously — they wait for human review of the Profile first.

---

## IMPORTANT — autonomous-run guardrails

- **Stop, don't guess.** This is new architecture with no reference implementation to diff against. Every time a decision is not covered by Part A, the correct action is a STOP note, not a guess. A green gate does not mean the design is right when there is nothing to compare against.
- **P3 is the real test.** If `identity` in Python does not come out clean, the Profile is wrong — say so in the validation report rather than papering over it.
- **Do not generate repository/migration.** Stop after P5 so the Profile can be reviewed before replicating it across services.
- Leave the Go side completely untouched.
