# Milton Prism — Hexagonal Service Generator, Python HTTP-native / FastAPI (Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, verifier-passing hexagonal **Python** microservice whose **only** wire protocol is **HTTP** — a **FastAPI** application (router + REST path operations) served by **uvicorn** — obeying the Architecture Canon and the Python Language Profile.

This is the HTTP/FastAPI homologue of `milton-prism-service-generator-prompt-python.md` (the Python gRPC prompt). It is identical in spirit — same hexagonal layering (domain / application / ports / infrastructure), same domain/error rules, same proto-as-contract discipline — and differs **only on the transport edge**: the service exposes HTTP natively through FastAPI instead of registering a gRPC server, and it never wires the gRPC API gateway. Read the Python gRPC prompt as the baseline; this document records the deltas and the HTTP-specific obligations.

---

## 0. Operating context and mandatory references

You are a code-generation agent operating inside Milton Prism. Your single job in this run is to materialize **one** microservice into the existing Python monorepo at `python/`, following two authoritative documents that ship with this task:

1. **`milton-prism-architecture-canon.md`** — portable principles, the proto/AIP contract, error taxonomy, testing philosophy, verification gates.
2. **`milton-prism-python-profile.md`** — the Python + Motor/PyMongo (MongoDB) mechanisms (domain aliases, ports, application, repositories, wire, tests).

**Before generating anything, read both documents in full.** Where this prompt and a reference document differ on the **transport**, this prompt wins (it is the HTTP/FastAPI variant); on everything else the reference document wins and you flag the discrepancy in your report.

You have a filesystem and a shell. Use them: read the existing `python/` tree and `services/identity/` as the hexagonal reference (everything except the transport edge applies verbatim), match conventions exactly, write files, run the verifier, iterate.

### Fixed stack (non-negotiable)

- **Web framework:** FastAPI. The app is `app = FastAPI(...)` plus one or more `APIRouter`s.
- **Server:** uvicorn (the `__main__` runs `uvicorn.run(...)` or builds a `uvicorn.Server`).
- **Persistence:** branch on `store`. `store: mongodb` → MongoDB via **motor** (`AsyncIOMotorDatabase`) with `system_counters` identifier generation; `store: postgres` / `store: mysql` → **SQLAlchemy 2.0 async** (`DeclarativeBase` models in `infrastructure/repositories`, async engine builder, `create_all`, autoincrement PK) per Python Profile §A.12. Identical to the gRPC profile's persistence layer.
- **Models / validation:** **pydantic** models equivalent to the proto messages. You do **not** need `*_pb2.py` / `*_pb2_grpc.py` runtime stubs when the request/response bodies are pydantic models — decide per service and state the decision in your report. The `.proto` is still written (it is the authoritative API contract that drives the OpenAPI); pydantic is only the in-process representation.
- **Config:** a per-service `.env` (the platform appends a per-service `.env.example`); load it with the Python Profile's config loader / pydantic-settings.

---

## 1. Inputs

Identical to the Python gRPC prompt §1: you receive the service contract (proto), and the boundary spec (`service`, `resources`, `rpcs`, `store`, `needs_transaction`, `error_prefix`, `inter_service_deps`, `auth`, optionally `cross_service_fks`). The proto is authoritative for the API surface; you do not invent RPCs, messages, or fields. If any field needed to proceed is missing, **stop** (§2).

---

## 2. Preconditions and stop conditions (fail fast)

Same as the Python gRPC prompt §2 (proto present and AIP-conformant, error prefix supplied, store is one of `mongodb`/`postgres`/`mysql` — anything else is a hole, no messaging/NATS, no pre-existing collision), **plus**:

- **The contract proto MUST carry `google.api.http` annotations.** Every RPC in the service proto must have a `google.api.http` option mapping it to an HTTP method + path. If the supplied proto lacks them, you **add** them (this is part of your job — see §4.0); they are not optional. The platform derives `docs/openapi.yaml` from these annotations; without them the OpenAPI is empty.

---

## 3. Build order

Generate strictly inward-to-outward so each layer is importable before the next. This mirrors the gRPC build order with the transport layer swapped from gRPC servicer handlers to FastAPI routers:

```
0.  proto      (write/patch protobuf/proto/milton_prism/services/<svc>/v1/<svc>_service.proto
                WITH google.api.http on every RPC; the OpenAPI is derived from it)
1.  services/<service>/domain/domain.py    (domain types — pydantic models mirroring the proto messages)
2.  services/<service>/domain/errors.py    (DomainError sentinels, codes off error_prefix)
3.  services/<service>/domain/__init__.py
4.  services/<service>/ports/repository.py (Repository Protocol)
5.  services/<service>/ports/transaction.py (TransactionManager Protocol if needs_transaction)
6.  services/<service>/ports/__init__.py
6b. cross-service FK client ports  [ONLY if cross_service_fks is non-empty]
7.  services/<service>/application/service.py  (all use cases — transport-agnostic)
8.  services/<service>/application/__init__.py
9.  services/<service>/infrastructure/repositories/<resource>_repository.py
10. services/<service>/infrastructure/repositories/transaction_manager.py   (if needed)
11. services/<service>/infrastructure/http/<resource>_router.py   (FastAPI APIRouter + path operations)
12. services/<service>/infrastructure/http/app.py                 (FastAPI app factory; mounts routers; /health)
13. services/<service>/infrastructure/http/errors.py              (DomainError → HTTP status mapping)
14. services/<service>/wire.py             (single composition point — builds the FastAPI app)
15. services/<service>/__main__.py         (uvicorn entrypoint — NOT a gRPC server)
16. services/<service>/tests/test_service.py   (application unit tests)
17. services/<service>/tests/test_http.py      (router tests via FastAPI TestClient)
```

Steps 1–10 and the tests are **identical** to the Python gRPC prompt §4 — generate them exactly as that prompt instructs (domain, error sentinels, ports as `Protocol`s, application use cases with FieldMask discipline, the store-branched repositories — Motor + `system_counters` for `mongodb`, SQLAlchemy 2.0 async + `create_all` for `postgres`/`mysql` per §A.12 — application unit tests). Steps **0, 11–15** are the transport deltas below.

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

Use AIP-conformant paths (`/v1/<plural>`, `{identifier}` path params, `body:` for create/update). The `.proto` is the **authoritative contract** that drives `docs/openapi.yaml` — the platform regenerates the OpenAPI from these annotations (the pipeline is reused unchanged; you do not edit `docs/openapi.yaml` by hand). You do **not** need to generate `*_pb2.py` / `*_pb2_grpc.py` runtime stubs: the request/response bodies are pydantic models. (If you choose to generate `*_pb2.py` for any reason, the assembler keeps the message stubs but drops `*_pb2_grpc.py`.)

### 4.11–4.13 FastAPI transport edge (replaces the gRPC handlers step)

The transport layer is a **FastAPI app**, the service's ONLY entrypoint. Do NOT create a gRPC server, do NOT call `grpc.server(...)` / `grpc.aio.server(...)`, do NOT call any `add_*Servicer_to_server`, and do NOT register any API gateway.

For each resource, create `infrastructure/http/<resource>_router.py`:

- A module-level `APIRouter()` (or a factory returning one) holding a reference to the application service (the same use-case object the gRPC handler would hold).
- One **path operation** per RPC (`@router.get`, `@router.post`, `@router.patch`, `@router.delete`), mounted on the route declared by that RPC's `google.api.http` annotation (method + path must match the proto). Decode the request from path params, query string, and a pydantic request body; call the application use case; return the pydantic response model (FastAPI serialises it to JSON matching the OpenAPI).
- Validate request fields the same way the gRPC handler would (raise the service's `DomainError` sentinels from `domain/errors.py`).
- Map domain errors to HTTP status codes via `infrastructure/http/errors.py`: a small `map_error(exc: DomainError) -> HTTPException` that translates each domain `code` to an HTTP status (validation `<PREFIX>1xx` → 400, not-found → 404, forbidden → 403, conflict → 409, internal `<PREFIX>500` → 500) and a JSON error body `{ "code": ..., "message": ... }`. Wire it as a FastAPI exception handler on the app.
- Inject the auth dependency when `auth: required`: read the bearer token from the `Authorization` header (a FastAPI dependency) and resolve the session/user id the same way the gRPC path resolves it from context.

Create `infrastructure/http/app.py` exposing a `create_app(...) -> FastAPI` factory that builds the `FastAPI()` instance, registers the `DomainError` exception handler, and `include_router`s every resource router. Add a liveness route `GET /health` returning `200 {"status": "ok"}`.

### 4.14 Wire (transport delta)

`wire.py` has a single `build_app(...) -> FastAPI` (the FastAPI homologue of the gRPC `wire.py`) that builds db → repo → tx → application service → routers → `create_app(...)` and returns the composed FastAPI app. It is the only place constructing the full graph. There is **no** gRPC server and **no** `add_*Servicer_to_server` call.

### 4.15 Entrypoint (transport delta)

`services/<service>/__main__.py` follows the Python Profile bootstrap (config load, mongo connect, logging via `shared.logging`) but the serving step starts **uvicorn**:

- Build the app via `wire.build_app(...)`.
- Run it with uvicorn: `uvicorn.run(app, host=..., port=...)` or construct a `uvicorn.Server(uvicorn.Config(app, ...))` and `await server.serve()` for graceful SIGTERM/SIGINT shutdown.
- Do NOT create `grpc.aio.server()`, do NOT register a gRPC health servicer, do NOT call any `add_*Servicer_to_server`, do NOT register any gateway. The uvicorn server is the entire entrypoint.

---

## 5. Self-verification loop

After generation, run the gates and **iterate until green**. The build gate is **`python -m compileall`** (every generated module byte-compiles) **plus importing the FastAPI app** (no import-time error) **plus `pytest`**:

```bash
# From python/
poetry install                 # confirm no new conflicts (fastapi, uvicorn, motor, pydantic present)
buf lint                        # from protobuf/ — proto + google.api.http must resolve
ruff check . --fix && ruff check .
mypy --strict services/<service>
python -m compileall services/<service>   # THE BUILD GATE — must exit 0
python -c "from services.<service>.wire import build_app; build_app"   # app must import
pytest -q services/<service>
```

On failure: read the error, fix the offending file, re-run. If the same failure recurs three times without progress, **stop and report** (§6) rather than churning.

Conformance self-audit (answer each in the report) — same as the Python gRPC prompt §5 PLUS the transport-specific checks:

- Is the application layer transport-agnostic (no `fastapi`, no `uvicorn`, no router import in `application/`)? (must be yes)
- Does the changeset contain **zero** `grpc.server(`, `grpc.aio.server(`, or `add_*Servicer_to_server` call? (must be yes)
- Is there a `__main__` that builds and starts a uvicorn server as the sole entrypoint (no gRPC server)? (must be yes)
- Does every RPC in the service proto carry a `google.api.http` annotation? (must be yes — the OpenAPI is derived from them)
- Are the request/response bodies pydantic models (not hand-marshalled `*_pb2` objects)? (must be yes)
- (All the non-transport audits from the Python gRPC prompt §5: domain rules, no business logic in routers/repos, `wire.py` is the only graph builder, error `message` in `Failure_Noun_Descriptor`, `shared.logging` for diagnostics, no improvised store outside mongodb/postgres/mysql.)

---

## 6. Output and generation report

Same as the Python gRPC prompt §6, with `TRANSPORT: HTTP (FastAPI)` noted and the HTTP route table (RPC → method+path) listed under `ASSUMPTIONS`/notes. Record the pydantic-vs-`*_pb2` decision under `DEVIATIONS`.

---

## 7. Hard rejection triggers (quick reference)

All the Python gRPC prompt §7 triggers, **plus**:

- A `grpc.server(`/`grpc.aio.server(` or any `add_*Servicer_to_server` call anywhere in the changeset — this is the HTTP/FastAPI variant; the gRPC server must not exist.
- A `__main__` that starts a gRPC server instead of (or in addition to) uvicorn.
- An RPC in the service proto with no `google.api.http` annotation — the OpenAPI cannot be derived without it.
- The application or domain layer importing `fastapi`, `uvicorn`, or a router module — transport concerns belong only in `infrastructure/http/`.
- Hand-editing `docs/openapi.yaml` instead of letting the platform derive it from the proto.
