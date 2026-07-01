# Milton Prism — Decomposition Engine: Architecture & Implementation Spec

**The second engine. Runs as the `DESIGNING` stage of the migration. Composes with the Architecture Canon; this document defines its custom internals.**

The decomposition engine consumes the `AnalysisSummary` (technologies, vulnerabilities, the weighted dependency graph, blueprint metadata) and produces the `RestructurePlan`: which microservices to create, what each owns, how they depend on each other, and the `.proto` contracts. The user approves this plan (`AWAITING_APPROVAL`) before any code is generated. Like the analysis engine, its logic is custom — not produced by the service generator.

---

## 0. Decisions locked

- **Mode:** graph-deterministic first. The `SemanticClusterer` is a **port defined from day one** with a live deterministic adapter (community detection) and an **LLM adapter as a marked hole** (stub that reports "not implemented"). Dual mode = filling the hole, not a redesign.
- **Data:** `shared_database = true` at start, **declared explicitly in the plan** with a visible debt marker, surfaced on the approval screen. Per-service data ownership is a marked debt to complete later.
- **Input language v1:** Python/Flask + SQLAlchemy (the resource/route extractor is per-framework, same hole pattern as the analysis `LanguageAnalyzer`).
- **Output language:** decided by the migration's `TargetConfig` (Go or Python profile).

---

## 1. Where it fits

```
ANALYZING  → analysis engine writes AnalysisSummary, advances to DESIGNING
DESIGNING  → THIS ENGINE runs (in the worker, async), produces RestructurePlan
             + .proto contracts + boundary specs, advances to AWAITING_APPROVAL
AWAITING_APPROVAL → user reviews the plan (frontend screen 16); ApproveDesign → GENERATING
GENERATING → the service generator produces the services from the plan
```

The engine runs in the analysis worker (or a sibling worker) as the `DESIGNING` stage, idempotent and resumable off the persisted migration state, same discipline as analysis.

---

## 2. Input / output contract

**Input:** the `AnalysisSummary` (graph + blueprints + technologies), the source workspace, and the migration's `TargetConfig`.

**Output, written during `DESIGNING`:**
- `RestructurePlan` (the existing proto type) set on `Migration.plan`: `repeated ProposedService services` + `rationale`. Each `ProposedService` = name, error_prefix (from the orchestrator registry), owned_resources, inter_service_deps.
- The **`.proto` contracts** for each proposed service, written to the migration workspace (the plan references them; the user approves real contracts, not just names).
- The **boundary specs** (the YAML the generator consumes) per service.
- A **shared-database declaration** and debt markers (see §6).

The user approves **the contracts and the partition**, not a vague list of service names — the `.proto` is the design.

---

## 3. Two modes and the confidence signal

The clustering stage returns candidate clusters **plus a quality signal** (graph modularity / how cleanly the graph partitions).

- **Graph-sufficient (Conduit's case):** modularity above threshold → the deterministic clustering is trusted. The LLM is needed only to *name and describe* services, and even that can be deterministic for v1 (derive names from blueprints/module paths).
- **LLM-needed (spaghetti):** modularity below threshold, or one giant cluster (the "ball of mud") → the partition is unreliable from the graph alone. Here the `SemanticClusterer` LLM adapter would read code at the function/class level to propose boundaries.

**v1 behavior when the LLM adapter is a hole:** if the graph is insufficient, do **not** fail and do **not** guess silently. Produce the best-effort deterministic partition, set a low-confidence flag on the plan, and surface a clear warning on the approval screen ("graph-based decomposition has low confidence for this codebase; human review strongly advised"). This keeps the human gate meaningful exactly when the machine is unsure.

---

## 4. Pipeline stages

| # | Stage | What it does | Type |
|---|-------|--------------|------|
| 1 | Load graph | Read `dependency_graph` + blueprints from the summary | [D] |
| 2 | Detect infrastructure | Separate shared-infra hubs from domain modules (high fan-in, no domain identity) | [D] |
| 3 | Cluster | Partition domain modules into candidate services + a modularity score | [D] live / [LLM] hole |
| 4 | Characterize | Name each cluster, identify owned resources, derive **data-layer** inter-service deps from `.models` cross-cluster edges, collect operational couplings from non-models edges, request an error prefix | [D] live / [LLM] for naming/desc |
| 5 | Derive contracts | From the cluster's models + routes, derive AIP `.proto` (resources + RPCs) | [D] resources / [LLM] route semantics (hole) |
| 6 | Data ownership | Assign resources to services; declare `shared_database=true` + debts; flag cross-service FKs | [D] |
| 7 | Assemble & persist | Fill `RestructurePlan`, write contracts/specs to workspace, set `Migration.plan`, advance to `AWAITING_APPROVAL` | [D] |

**[D]** deterministic / **[LLM]** language-model (hole where marked).

### Stage 2 — infrastructure detection (important, grounded in Conduit)
A module is **shared infrastructure**, not a service, when it has high fan-in from multiple domain clusters and exports no domain identity of its own. In Conduit: `conduit.database` (3 blueprint models import it), `conduit.extensions`, `conduit.settings`, `conduit.utils`, `conduit.app`, `autoapp`, `conduit.exceptions`, `conduit.commands`. These become **replicated shared code / shared infrastructure**, never a "database service." Detection heuristic: fan-in across ≥2 clusters AND low internal domain coupling.

### Stage 3 — clustering (the `SemanticClusterer` port)
- **Deterministic adapter (live):** community detection (e.g. Louvain / label propagation) on the weighted undirected projection of the domain graph, **biased by blueprint metadata** (modules in the same blueprint strongly prefer the same cluster). Returns clusters + modularity.
- **LLM adapter (hole):** reads code semantics when the graph is insufficient. Stub raises "not implemented"; the engine falls back to deterministic + low-confidence flag (§3).

### Stage 4 — characterize: data deps vs. operational couplings

This distinction is **fundamental and will reappear in every real monolith decomposition**.

A cross-cluster import edge can mean two very different things depending on where it originates:

| Edge source layer | Meaning | Goes into |
|---|---|---|
| `*.models` | Data-layer hard dependency — entity A holds a reference to entity B's table; without B's schema, A cannot be deployed | `inter_service_deps` |
| `*.views`, `*.serializers`, `*.api`, `*.tasks`, … | Operational coupling — at runtime the view reads or writes data from B's domain; in microservices this becomes a synchronous gRPC call or an async event | `operational_couplings` |

**Why this matters:** In real Django/Flask codebases the views layer is often bidirectionally coupled (service A's view imports B's model, and B's view imports A's model) because both services expose endpoints that aggregate data. Treating these view-layer edges as hard dependencies produces false dep cycles that make the graph unsolvable. Treating them correctly as operational couplings — future gRPC or event calls — keeps the data-layer graph acyclic and deployable.

**Conduit example:** `conduit/user/views.py` imports `conduit.profile.models.UserProfile` (to create a profile on registration) and `conduit/profile/views.py` imports `conduit.user.models.User` (to look up a user by username). If these edges fed `inter_service_deps` there would be a user↔profile cycle. Classified as operational couplings they drop out of the dep graph: `profile.data_deps=[user]` (FK-derived, see §6), `user.data_deps=[]`.

**FK augmentation (after Stage 6):** Some FK relationships are expressed as SQLAlchemy `reference_col('tablename', ...)` strings, not Python import statements. They don't appear as graph edges at all, so Stage 4 alone can't see them. After Stage 6 resolves FK ownership, a second pass (`augmentDataDeps`) adds any FK-derived deps that aren't already in `inter_service_deps`. This catches cross-service foreign keys that live entirely in the DB schema rather than in the import graph.

### Stage 5 — contract derivation (per-framework, hole pattern)
From each cluster's **SQLAlchemy models** derive the resource messages (model fields → proto fields, AIP-compliant: `identifier`, `state`, `_time` suffixes, soft-delete). From the **Flask routes/blueprints** derive the RPCs (CRUD maps deterministically; custom routes → custom methods need interpretation). The model→message mapping is deterministic; the **route→RPC semantic mapping is where the LLM helps** and is a marked hole for non-CRUD routes (v1: map the clean CRUD cases, flag the rest for human/LLM). The extractor is **per-framework** (Flask/SQLAlchemy first), mirroring the analysis `LanguageAnalyzer` holes.

---

## 5. Hexagonal shape (ports)

The engine obeys the Canon's dependency rule. Application orchestrates ports:

```
GraphLoader          Load(ctx, summaryID) → Graph                       // from AnalysisSummary
InfraDetector        Detect(ctx, graph) → (infra[], domainGraph)
SemanticClusterer    Cluster(ctx, domainGraph, blueprints) → (clusters[], modularity)
                       det adapter = community detection (LIVE)
                       llm adapter = code-semantic clustering (HOLE)
ContractDeriver      Derive(ctx, cluster, workspace) → (protoFiles, ownedResources, rpcs)
                       per-framework adapter (Flask/SQLAlchemy LIVE; others HOLE)
PrefixAllocator      Allocate(ctx, serviceName) → prefix                 // orchestrator registry
PlanWriter           Write(ctx, migrationID, plan, contracts, specs)     // sets Migration.plan, advances state
ModelClient          (router port for the LLM; simple adapter later, HOLE now)
```

No provider SDK or framework parser in domain/application — all behind adapters (Canon dependency rule).

---

## 6. Data ownership and the shared-database debt

Per the locked decision, v1 keeps a **shared database** to avoid distributed-transaction work up front. This is a deliberate, **declared** shortcut:

- Each `ProposedService` boundary spec carries `shared_database: true`.
- The `RestructurePlan` declares it and lists the **cross-service foreign keys** as deferred consistency concerns — in Conduit: `Article.author_identifier → userprofile (profile service)`, `Comment.author_identifier → userprofile (profile service)`, `UserProfile.user_identifier → users (user service)`. Each FK carries its `owner_message` (the proto message name) so `Article.author` and `Comment.author` are distinguishable. These are the future Saga/Outbox boundaries.
- After Stage 6, `augmentDataDeps` adds FK-derived service dependencies to `inter_service_deps` for any FK whose target table is owned by a different service. This catches references expressed as SQLAlchemy table-name strings that don't appear as Python import edges (see Stage 4, §4).
- The approval screen shows a visible banner: these are **not yet fully independent microservices** (a shared DB makes this a "distributed monolith" until data is split); the plan says so honestly.
- Debt marker `// TODO: per-service data ownership + cross-service consistency` travels in the generated specs.

This makes the shortcut conscious and reclaimable, not a silent violation of the Canon's "each service owns its data."

---

## 7. Conduit worked example (the validation target)

Running the engine on `flask-realworld-example-app` (28 nodes, 50 edges) should produce approximately:

**Shared infrastructure (not services):** `conduit.database`, `conduit.extensions`, `conduit.settings`, `conduit.utils`, `conduit.app`, `autoapp`, `conduit.exceptions`, `conduit.commands`.

**Proposed services (live output, migration 10005):**
| Service | Owns (resources) | Data deps | Operational couplings |
|---------|------------------|-----------|-----------------------|
| user | `conduit.user.*` | — | → profile (user/views.py imports profile.models) |
| profile | `conduit.profile.*` | user (FK: UserProfile.user_identifier → users) | → user (profile/views.py imports user.models) |
| articles | `conduit.articles.*` | profile (FK: Article/Comment.author_identifier → userprofile) | → profile (articles/serializers.py), → user (articles/views.py) |

**Cross-service FKs (3, each with owner_message):**
- `articles.Article.author_identifier → userprofile (service: profile)`
- `articles.Comment.author_identifier → userprofile (service: profile)`
- `profile.UserProfile.user_identifier → users (service: user)`

**Operational couplings (4) — not in data deps, future gRPC/event calls:**
- `articles → profile` (source: `conduit.articles.serializers`)
- `articles → user` (source: `conduit.articles.views`)
- `profile → user` (source: `conduit.profile.views`)
- `user → profile` (source: `conduit.user.views`)

Note that `user → profile` and `profile → user` appear in `operational_couplings` but do **not** form a cycle in `inter_service_deps` — the data-layer graph is `articles → profile → user`, strictly acyclic.

**Declared:** `shared_database: true`; `ModularityScore`: high → graph-sufficient mode, LLM not required.

This is the concrete acceptance test: if the engine produces ~these three services + shared infra with those dependencies on Conduit, it works. Conduit is the easy case on purpose — validate the engine here before pointing it at spaghetti.

---

## 8. Holes and debt (explicit, to complete later)

1. **`SemanticClusterer` LLM adapter** — port defined, deterministic adapter live; LLM adapter stubbed. Needed for low-modularity (spaghetti) codebases. The engine already flags low confidence and falls back; filling this hole makes those cases reliable.
2. **`ContractDeriver` route→RPC semantics** — model→message is deterministic and live; non-CRUD route mapping is flagged for LLM/human. Same hole owner as #1.
3. **Per-service data ownership** — `shared_database: true` declared now; splitting data + Saga/Outbox for cross-service FKs is deferred and marked in the plan (§6).
4. **`ModelClient` / multimodel router** — the LLM port has a simple-adapter/hole now; the cost-optimizing router comes later.
5. **Per-framework extractors beyond Flask/SQLAlchemy** — same hole pattern as the analysis `LanguageAnalyzer`.

A generator/engine encountering an unfilled hole **reports it**, never guesses.

---

## 9. What to build first

1. **Stages 1–4 deterministic + the `SemanticClusterer` port with the community-detection adapter.** Validate against Conduit: it must produce the ~3-service partition + shared infra from §7. This proves the core without any LLM.
2. **Stage 5 contract derivation, deterministic part:** SQLAlchemy models → AIP `.proto` resources, CRUD routes → standard methods. Flag non-CRUD routes.
3. **Stages 6–7:** data-ownership declaration (shared DB + debt markers) + `RestructurePlan` assembly + `PlanWriter` advancing to `AWAITING_APPROVAL`, and wire the frontend approval screen to show the real plan and the warning banners.
4. **Then the holes:** the LLM clustering adapter (for spaghetti), the route-semantics LLM, and per-service data split — each a marked hole, filled when needed.

Build the deterministic core first and validate on Conduit, where you already know the right answer. Only reach for the LLM where the graph genuinely falls short.
