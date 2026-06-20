# Task — Bootstrap the Milton Prism foundation (Paso 1)

**Paste this to Claude Code as its first task, with the sample project (`saas_agro_ar`) available as a reference repo.**

---

## Goal

Create a new Go monorepo named `milton_prism` by forking the **proven skeleton and shared foundation** of the reference project, while removing all of its domain services. The result is an empty-of-services-but-compiling foundation, ready to receive Milton Prism's own services. Do **not** invent infrastructure — reuse what the reference already has working.

## Inputs

- Reference repo: the `saas_agro_ar` backend (provided).
- Target module name: `milton_prism`.

## What to KEEP (copy and rename)

Copy these from the reference, preserving structure and behavior:

- `core/internal/svc/` — the `Services` container, server group, token context.
- `core/shared/` — **generic** cross-cutting packages only: `error` (coreerror), `auth_token`, `cache_client`, `mongo_client`, `grpc_client_sdk` (keep the builder + the auth-forwarding credentials; drop the per-service client files, they reference services that no longer exist), `grpc_health`, `http_client`, `interceptors`, `mailer`, `session`, `utils`.
- `pkg/config/`, `pkg/log/` (the `applog` package), `pkg/gateway/` (keep the gateway runtime and the `common/error/` scaffolding; drop the per-service error-message files).
- `protobuf/buf.yaml`, `protobuf/buf.go.gen.yaml`, `protobuf/buf.docs.gen.yaml`.
- `protobuf/proto/milton_prism/types/pagination/v1/` and `.../types/query_params/v1/` — the generic shared types (renamed from the reference's project path).
- `api-gateway/` skeleton (`cmd/` entrypoint and wiring), minus per-service registrations.
- `Makefile`, `go.mod`, `go.sum`, `.gitignore`, and any toolchain/dev scripts.

## What to REMOVE

- `core/services/*` — every domain service (companies, commodities, certification, etc.).
- `core/cmd/*` — every per-service entrypoint.
- `protobuf/proto/.../services/*` — every service proto.
- `protobuf/proto/.../types/*` **except** `pagination` and `query_params` — drop domain types (company, commodity, etc.).
- Domain-specific shared packages with no general purpose — e.g. `arca_client` (an external tax-API client specific to the reference domain). Remove it and any imports of it.
- The per-service error-message maps under `pkg/gateway/common/error/` (keep `common_errors.go` / `message_error.go` and the formatting helpers).
- `backend-old/` and any reference-specific docs, fixtures, or seed data.

## Rename

Rename the module from `saas_agro_ar` to `milton_prism` **everywhere**:

- `go.mod` module path.
- All Go import paths.
- All proto `go_package` options and the proto package project segment (`saas_agro_ar.*` → `milton_prism.*`), and the on-disk `protobuf/proto/saas_agro_ar/` directory → `protobuf/proto/milton_prism/`.
- Be careful: only rename the module/project identifier, not unrelated strings.

## Place the Prism documents

Create `docs/prism/` and place these four files there (provided separately):

- `milton-prism-architecture-canon.md`
- `milton-prism-go-profile.md`
- `milton-prism-service-generator-prompt.md`
- `milton-prism-platform-base-decomposition.md`

Create a root `CLAUDE.md` (provided separately) that points future tasks at the Canon and Go Profile as mandatory reading.

## Acceptance criteria (must all pass before you finish)

```bash
go build ./...        # compiles with zero domain services present
go vet ./...
buf lint              # passes on the remaining generic protos
buf generate          # regenerates pkg/pb/gen for the kept types only
```

- No dangling imports of removed packages or services.
- No remaining occurrence of `saas_agro_ar` anywhere in the tree.
- `core/services/` and `core/cmd/` exist as empty directories (with a `.gitkeep`), ready to receive generated services.

## Report when done

List: directories kept, directories removed, the rename count, and the output of the four acceptance commands. If any reference code is too entangled with a removed service to compile cleanly, stop and report the specific coupling rather than deleting shared behavior to make it build.
