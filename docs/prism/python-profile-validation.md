# Milton Prism Python Profile — Validation Report

**Date:** 2026-06-08
**Tasks covered:** P0 (Python Profile doc), P1 (toolchain + proto gen), P2 (shared layer),
P3 (identity service), P4 (Python service generator prompt), P5 (this report)

---

## Gate statuses (P5 final run)

| Gate | Command | Result |
|------|---------|--------|
| **buf lint** | `buf lint` (from `protobuf/`) | **PASS** |
| **ruff** | `poetry run ruff check .` | **PASS** — 0 errors |
| **mypy** | `poetry run mypy --strict .` | **PASS** — 0 issues in 42 source files |
| **lint-imports** | `poetry run lint-imports` | **PASS** — 3 contracts KEPT, 0 broken |
| **pytest** | `poetry run pytest -q` | **PASS** — 50 passed in 0.21 s |

All five gates green on every task from P0 through P5. Gates were run after each task
and iterated to green before proceeding.

---

## Identity service — file manifest

```
services/identity/
├── __init__.py
├── __main__.py
├── wire.py
├── domain/
│   ├── __init__.py
│   ├── domain.py          # proto type aliases
│   └── errors.py          # 16 DomainError sentinels (IDN101–IDN502)
├── ports/
│   ├── __init__.py
│   ├── auth.py            # PasswordHasher, TokenManager, SessionStore
│   ├── repository.py      # UserRepository Protocol
│   └── transaction.py     # TransactionManager Protocol
├── application/
│   ├── __init__.py
│   └── service.py         # IdentityService — 9 use cases + extract_session_id
├── infrastructure/
│   ├── __init__.py
│   ├── grpc_handlers/
│   │   ├── __init__.py
│   │   └── identity_handler.py   # IdentityServicer — all 9 RPCs
│   └── repositories/
│       ├── __init__.py
│       ├── password_hasher.py    # BcryptPasswordHasher
│       ├── session_store.py      # MongoSessionStore (TTL index)
│       ├── token_manager.py      # JwtTokenManager (HS256)
│       ├── transaction_manager.py # MotorTransactionManager
│       └── user_repository.py   # MongoUserRepository (system_counters ID gen)
└── tests/
    ├── __init__.py
    └── test_service.py    # 30 unit tests (AsyncMock ports, no Motor/MongoDB)
```

**Shared layer (shared/):**
```
shared/
├── __init__.py
├── config/
│   ├── __init__.py
│   └── loader.py          # MongoConfig, GrpcServerConfig, BaseServiceConfig
├── errors/
│   ├── __init__.py        # exports DomainError only
│   ├── domain_error.py    # DomainError(code, message) — no grpc
│   └── mapper.py          # map_error() — grpc imported here only
├── grpc_client_sdk/
│   └── builder.py         # GrpcClientBuilder
├── interceptors/
│   └── interceptors.py    # Logging/ContextId/Recovery interceptors
├── logging/
│   └── __init__.py        # infof/warningf/errorf — applog analogue
├── mongo_client/
│   └── client.py          # MongoClientBuilder
└── tests/
    ├── __init__.py
    ├── test_config.py      # 5 tests
    ├── test_domain_error.py # 10 tests
    └── test_logger.py      # 5 tests
```

**Root config files:**
```
python/
├── pyproject.toml
├── .importlinter
├── conftest.py
├── scripts/gen_proto.py   # grpcio-tools protoc wrapper; adds __init__.py
└── gen/                   # NEVER edit by hand — protoc output
    ├── milton_prism/...
    └── openapiv3/...
```

---

## Test coverage

| Suite | Tests | Notes |
|-------|-------|-------|
| `shared/tests/test_logger.py` | 5 | format, level per method |
| `shared/tests/test_domain_error.py` | 10 | DomainError attrs + all map_error code ranges |
| `shared/tests/test_config.py` | 5 | defaults, env override, composition |
| `services/identity/tests/test_service.py` | 30 | every use case: ≥1 success + ≥1 error; all 16 sentinels exercised |
| **Total** | **50** | |

No integration tests (MongoDB not required for the Python profile unit suite).
All 30 service tests use `MagicMock`/`AsyncMock` — no Motor, no real database.

---

## Blocker notes

None. All P0–P5 tasks completed without hitting a stop condition.

---

## Python-to-Go Profile deviations

| # | Deviation | Reason |
|---|-----------|--------|
| 1 | **Sessions in MongoDB** (TTL index on `expires_at`), not Redis | Redis is an explicit profile hole (A.10). `MongoSessionStore` implements the `SessionStore` protocol using the `sessions` collection with `expireAfterSeconds=0` and document-level `expires_at` field. |
| 2 | **`TransactionManager` wired as `None`** in `wire.py` for v1 | Motor transactions require a MongoDB replica set; single-node dev/test instances don't support them. Wired as `None`; `IdentityService` accepts `TransactionManager | None` and skips transaction wrapping when `None`. |
| 3 | **`DomainError` and `map_error` split into two modules** (`domain_error.py` / `mapper.py`) | In Go, `coreerror` contains both `DomainError` and the gRPC status mapper in one package. Importing both from one Python module would cause a transitive `grpc` import in domain and application layers, breaking the import-linter contracts. Split enforces the dependency rule at the module level. |
| 4 | **`extract_session_id_from_access_token()` on `IdentityService`** | In Go the handler can call the token adapter directly. In Python, import-linter would flag any `from repositories.token_manager import ...` in the handler as a contract violation (`handlers-not-import-repositories`). Adding a thin delegation method on the service keeps all repository interactions inside `application/`. |
| 5 | **`explicit_package_bases = true` in mypy** | Python's mypy requires this when `mypy_path` (pointing to `gen/`) causes the same package to be resolved under two roots. Go's toolchain has no equivalent issue. |
| 6 | **Per-module `ignore_errors = true` overrides** for grpc.aio, motor, and generated stubs | grpcio-tools stubs lack generic parameters on `ServerInterceptor`/`ServicerContext`; motor stubs have changed APIs. Go's generated code ships with full type information; Python stubs are incomplete for third-party libs. |
| 7 | **`_configure_stdout_handler()` not called at module load** | Calling it at import time would capture pytest's `caplog` before pytest installs its handler, causing logger tests to fail. Entrypoints call it explicitly; pytest gets clean propagation. Go's `applog` has no equivalent constraint. |

---

## Python Profile document created

`docs/prism/milton-prism-python-profile.md` — canonical peer to the Go Profile.
Sections A.1 (stack), A.2 (layout), A.3 (hexagonal mechanisms), A.4 (error model),
A.5 (transaction boundary), A.6 (ID generation), A.7 (logging), A.8 (testing),
A.9 (gates), A.10 (holes).

## Python Service Generator Prompt created

`docs/prism/milton-prism-service-generator-prompt-python.md` — peer to the Go generator
prompt. Sections: §0 operating context, §1 inputs (proto + boundary spec), §2 preconditions
and stop conditions, §3 build order (16 steps), §4 per-step instructions (all reference
Python Profile section numbers), §5 self-verification loop, §6 generation report template,
§7 hard rejection triggers.

---

## STOP boundary

Generation halted after P5 as instructed. Repository and migration services were **not** generated.
The Go side was **not** touched.
