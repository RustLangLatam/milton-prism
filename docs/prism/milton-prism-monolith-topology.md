# Milton Prism — Monolith vs. Microservices Target Topology (G12)

When a migration is created the user picks an **architectural target**:

- **Microservices** (default / actual): the decomposition engine partitions the
  monolith into N services that talk over the inter-service transport and are
  fronted by `grpc-api-gateway` (the single HTTP entry point).
- **Monolith → monolith**: the plan is **not** partitioned. Every domain module
  is grouped into **one service** that exposes **HTTP natively** and is itself
  the entry point — **no `grpc-api-gateway`**. A same-shape modernisation onto
  the target stack.

This document records what was modelled, what runs today, and the one honest
sub-pending (the HTTP-native generator profile).

---

## 1. The target field (proto + openapi + JSON contract)

`TargetConfig` gained a `topology` field plus a `TargetTopology` enum
(`protobuf/proto/milton_prism/types/migration/v1/migration.proto`):

```proto
enum TargetTopology {
  TARGET_TOPOLOGY_UNSPECIFIED   = 0; // treated as MICROSERVICES
  TARGET_TOPOLOGY_MICROSERVICES = 1; // decompose + gateway (current/default)
  TARGET_TOPOLOGY_MONOLITH      = 2; // single HTTP-native service, no gateway
}

message TargetConfig {
  TargetLanguage language               = 1;
  TargetDatabase database               = 2;
  Transport      inter_service_transport = 3;
  bool           use_api_gateway        = 4; // ignored when topology = MONOLITH
  TargetTopology topology               = 5; // NEW — selected at creation
}
```

**Default = MICROSERVICES.** `UNSPECIFIED` is normalised to MICROSERVICES at
two points so nothing breaks:

- `CreateMigration` (`core/services/migration/application/service.go`) sets
  `topology = MICROSERVICES` when the request leaves it unspecified, so the
  persisted `TargetConfig` is always explicit.
- The decomposition pipeline's `resolveTopology` treats absent / errored /
  unspecified as MICROSERVICES (a missing topology must never error the flow).

### JSON contract for the frontend (NewMigration `topology` selector)

The frontend `NewMigrationPage` already has the mono/micro tiles (the
"Monolithic" tile was a disabled `SOON` placeholder). It now wires the
`topology` field in the `target` object of the CreateMigration request:

```jsonc
// POST /v1/migrations  — body.migration.target
{
  "language": "TARGET_LANGUAGE_GO",
  "database": "TARGET_DATABASE_MONGODB",
  "interServiceTransport": "TRANSPORT_GRPC",
  "useApiGateway": true,
  "topology": "TARGET_TOPOLOGY_MICROSERVICES"   // or "TARGET_TOPOLOGY_MONOLITH"
}
```

- Field name (REST/JSON, camelCase): **`topology`**.
- Allowed values: `TARGET_TOPOLOGY_MICROSERVICES`, `TARGET_TOPOLOGY_MONOLITH`
  (and `TARGET_TOPOLOGY_UNSPECIFIED`, which the backend coerces to
  MICROSERVICES). The enum is in the generated openapi `TargetConfig.topology`.
- When the user picks the Monolithic tile, send `TARGET_TOPOLOGY_MONOLITH`.
  `useApiGateway` is then irrelevant (the backend ignores it for monolith).
- The plan returned at `AWAITING_APPROVAL` for a monolith migration has exactly
  **one** `ProposedService` (name `app`), empty `interServiceDeps`, empty
  `crossServiceFks`, and a `rationale` that says "single HTTP-native service
  (no API gateway)". The approval screen can branch on
  `plan.services.length === 1` + the rationale to show a monolith summary.

Exposed in: `docs/openapi.yaml` (and the panel's regenerated
`milton-prism-panel/openapi.yaml`) under `TargetConfig.topology`.

---

## 2. The monolith plan (1-service decomposition)

Where the service count is decided: the decomposition pipeline
(`core/worker/decomposition/application/pipeline.go`) clusters the domain graph
(Louvain) in stage 3, characterises N candidates in stage 4, then assembles the
`RestructurePlan` in stage 7.

The monolith path is a **collapse applied after stage 4**:

1. `resolveTopology(migrationID)` reads the migration's `TargetTopology` via the
   `TargetTopologyLoader` port (`MongoTopologyLoader` reads `target_bytes`).
2. If MONOLITH and there is at least one candidate,
   `workerdomain.CollapseToMonolith(candidates, prefix)` folds the N candidates
   into **one** `ServiceCandidate` named `app` that owns every domain resource.
   Because there is exactly one service, all inter-service data deps and
   operational couplings become in-process calls and are dropped.
3. Stage 6 ownership: `analyzeDataOwnership(_, _, monolith=true)` emits **no**
   cross-service FKs and **no** operational couplings (nothing crosses a
   boundary), keeping `shared_database = true` (one service, one DB).
4. Stage 7 plan: `assemblePlan(..., monolith=true)` writes a single-service plan
   with a monolith-specific rationale; `buildArtifacts(..., monolith=true)`
   merges every per-cluster derived `.proto` under the one `app` service and
   emits a **monolith boundary spec** (`BuildMonolithBoundarySpecYAML`) that
   declares `topology: monolith`, `api_gateway: false`, `transport: http`.

The graph is still *analysed* (Louvain modularity is recorded, informative
only) — it is simply **not applied**. The no-boundaries path (empty domain
layer) is unchanged: `CollapseToMonolith(nil)` returns nil, so a monolith with
no domain layer falls through to the existing `no_service_boundaries` plan.

The default microservices path is **untouched** — every monolith branch is
guarded by `monolith == true`; `false` runs the exact prior code.

### Deliverable / publish

`download_deliverable.go` already had a `useApiGateway` flag that omits
`api-gateway/cmd/...` from the assembled deliverable. It now also forces no
gateway when `topology == MONOLITH`, regardless of `use_api_gateway`.

---

## 3. Sub-pending (honest): the HTTP-native generator profile

**What does NOT run yet:** the actual Camino B generation of an HTTP-native
single service.

The service generator
(`docs/prism/milton-prism-service-generator-prompt.md` +
`milton-prism-go-profile.md`) produces **gRPC services fronted by the gateway**:
it writes `infrastructure/grpc_handlers/`, per-service
`pkg/gateway/common/error/<service>_errors.go`, and assumes the
`grpc-api-gateway` provides the HTTP edge (see prompt §10, go-profile §9–§10:
"only `mongodb`/`gRPC` — anything else is a hole"). There is **no HTTP-native Go
profile** that emits an `net/http`/router service exposing HTTP directly with no
gateway.

Therefore, for a MONOLITH migration today:

- The **target modelling**, the **1-service plan**, the **merged contract**, and
  the **monolith boundary spec** (`topology: monolith`, `api_gateway: false`)
  are produced and persisted correctly, and the flow reaches
  `AWAITING_APPROVAL` with one service.
- The **generation** stage, if run, would invoke the existing gRPC generator
  prompt against the single `app` boundary spec. That produces a (single) gRPC
  service, **not** an HTTP-native one. The boundary spec tells the generator
  `api_gateway: false` / `transport: http`, but the generator prompt + go
  profile do not yet know how to honour that — it is a **profile hole**, exactly
  the Lesson-11 / Stop-condition case: do not fabricate generation that does not
  run.

**To close the hole** (a new, sizeable generator profile — out of scope here):

1. Add an **HTTP-native Go profile** (`milton-prism-go-profile-http.md` or a
   `transport: http` branch in the existing profile) that emits an
   `net/http`/chi/grpc-gateway-in-process edge inside the single service instead
   of relying on `grpc-api-gateway`, and skips the per-service
   `<service>_errors.go` gateway aggregation (or folds it in-process).
2. Branch the generator prompt / `GenerationPackage.output_profile` on the
   boundary spec's `topology`/`transport` so the agent loads the HTTP-native
   profile for monolith migrations.
3. Validate on Conduit-as-monolith: one `app` service, HTTP endpoints, no
   `api-gateway/` directory in the deliverable, gates green.

Until then, MONOLITH is **plan-complete and contract-complete** but
**generation-pending** for the HTTP-native edge. The microservices flow is the
fully-supported default.

---

## 4. Files touched

- `protobuf/proto/milton_prism/types/migration/v1/migration.proto` — `TargetTopology` enum + `TargetConfig.topology`.
- `pkg/pb/gen/.../migration.pb.go`, `docs/openapi.yaml` — regenerated.
- `core/services/migration/domain/domain.go` — topology aliases.
- `core/services/migration/application/service.go` — default topology = MICROSERVICES.
- `core/services/migration/application/download_deliverable.go` — no gateway for MONOLITH.
- `core/worker/decomposition/domain/domain.go` — `CollapseToMonolith`, `BuildMonolithBoundarySpecYAML`, topology aliases.
- `core/worker/decomposition/ports/ports.go` — `TargetTopologyLoader` port.
- `core/worker/decomposition/application/pipeline.go` — topology resolution + collapse + monolith plan/ownership/artifacts.
- `core/worker/decomposition/infrastructure/adapters/mongo_topology_loader.go` — reads `target_bytes`.
- `core/cmd/analysis-worker/main.go` — wires `WithTopologyLoader`.
- Tests: `core/worker/decomposition/application/monolith_test.go`.
