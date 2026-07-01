# Milton Prism — Hexagonal Service Generator (C++ / Drogon HTTP Agent Prompt)

**Operational instruction set for the code-generation agent (Claude Code).**
Given a service contract and a boundary spec, produce a complete, gate-passing hexagonal **C++20 + CMake + Drogon + HTTP** microservice that obeys the Architecture Canon and the C++ Language Profile.

This is the **HTTP-native** sibling of `milton-prism-service-generator-prompt-cpp.md` (gRPC). Read that prompt AND `milton-prism-cpp-profile.md` (especially **A.4-HTTP**) and `milton-prism-architecture-canon.md` in full first. Everything in the gRPC prompt applies EXCEPT the transport: §§0–4, 6, 7 carry over unchanged; this document overrides the transport-specific steps below.

---

## Transport override (CRITICAL — no gRPC server)

The **Drogon app is the ONLY runtime entrypoint.** Per C++ Profile A.4-HTTP:

- **There MUST be no `grpc::ServerBuilder`, no `grpc::Server`, no `*.grpc.pb.*` service base wired, and no `infrastructure/grpc/` directory.** Do NOT bootstrap a gRPC server. The CMake protoc custom command emits ONLY message classes (`--cpp_out`, **no** `--grpc_out` and **no** `grpc_cpp_plugin`).
- You MUST still write the authoritative `.proto` to the canonical `protobuf/proto/milton_prism/services/<service>/v1/...` path WITH a `google.api.http` annotation on EVERY RPC — the platform derives `docs/openapi.yaml` from those annotations. Without them the OpenAPI is empty.
- Model request/response messages as the proto-generated message classes (or DTOs mapping to them), serialized as JSON. You do NOT need the `*.grpc.pb.*` service base at runtime.
- Implement **Drogon controllers in `infrastructure/http/`** (`drogon::HttpController` methods registered with `METHOD_ADD`/path), **1:1 with the proto RPCs**, mounted on the verb + path declared by each RPC's `google.api.http` annotation. Map `DomainError` → HTTP status via a shared mapper (`1xx`→400, not-found→404, forbidden→403, conflict→409, `5xx`→500), returning `{ code, message }`.

## Build-order override

Replace the gRPC steps 5–7 of the base prompt with:

- **5. Handlers** → `infrastructure/http/`: Drogon controllers 1:1 with the RPCs, honoring the `google.api.http` routes; delegate to the use case; map errors via the shared mapper. Extract the authenticated identity via a Drogon filter (C++ Profile A.12), never inline token parsing.
- **6. config / wiring** → the composition root constructs ports → adapters → use case → Drogon controllers, plus the auth filter.
- **7. main.cpp** → register the controllers/filters, then `drogon::app().addListener(GRPC_HOST, GRPC_PORT).run();` (here an HTTP port). NO `grpc::ServerBuilder`/`grpc::Server`.

## Auth override

JWT (C++ Profile A.12, HTTP variant): a **Drogon filter** (`drogon::HttpFilter`) covering the protected endpoints; expose the authenticated `sub` to handlers via the request attributes. jwt-cpp (header-only); secret/issuer/audience from `ENV`; reject `alg=none` and algorithm confusion; failures → HTTP 401.

## Gate (unchanged shape, HTTP entrypoint)

```bash
buf lint                                          # from protobuf/ — proto + google.api.http must pass
cmake -S . -B build -G Ninja                       # from cpp/ — pkg_check_modules the PREINSTALLED libs (NEVER FetchContent)
cmake --build build                                # MUST compile (protoc --cpp_out, message classes only) — THE BUILD GATE
ctest --test-dir build                             # MUST pass — THE GATE (endpoint/integration tests welcome)
```

As in the gRPC prompt, the #1 way to avoid a gate TIMEOUT is to resolve every dependency (protobuf, Drogon, mongocxx/SQL) with `pkg_check_modules` (pkg-config — the certified Debian-trixie path; `find_package(CONFIG)` may also work) against the **preinstalled** apt libraries and **NEVER** `FetchContent`/`ExternalProject` grpc/protobuf/mongocxx/Drogon. Start from the golden CMakeLists template (C++ Profile §A.1.1.1) but **drop the `--grpc_out` half of the protoc command and the `.grpc.pb.cc` source** — message classes only (`--cpp_out`) — and link Drogon instead of the grpc++ service base.

## HTTP-specific self-audit additions

- Zero `grpc::ServerBuilder` / `grpc::Server` / `infrastructure/grpc/` in the deliverable? → must be yes
- The CMake protoc command uses ONLY `--cpp_out` (no `--grpc_out`, no `grpc_cpp_plugin`)? → must be yes
- Every RPC carries a `google.api.http` annotation in the proto? → must be yes
- Drogon controllers are 1:1 with the RPCs and honor the annotated verb+path? → must be yes
- The Drogon app is the only entrypoint (no gRPC bootstrap)? → must be yes
- Every dependency resolved via pkg_check_modules (the golden §A.1.1.1 template) against preinstalled libs (ZERO FetchContent)? → must be yes

All other rules, the report format (§6), and the rejection triggers (§7) of the base gRPC prompt apply unchanged — except gRPC bootstrap is REQUIRED to be ABSENT here.
