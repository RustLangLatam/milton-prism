# Milton Prism — Migrability Engine (spec + tasks)

The first LLM use on the **analysis** side. Optional, opt-in, costs tokens. It produces (a) a human-readable summary of *what the code is*, and (b) a **migrability verdict** with nuance (migrable / partial / not-migrable) and reasons — so the user understands their code and whether it's worth migrating, before approving anything.

Composes with the Architecture Canon and sits on top of the deterministic analysis (Tier 1 + graph). It does **not** replace the deterministic facts — it interprets them.

Two parts: **Part A** = frozen decisions. **Part B** = tasks. One task at a time, validate against the two reference repos. Ambiguity/impossibility → STOP note in `docs/prism/migrability-blockers.md`.

---

# PART A — Frozen decisions

## A.1 What it does
After deterministic analysis completes, the user can optionally run a **migrability assessment**: an LLM reads the *distilled facts* of the analysis (not raw code) and returns a structured verdict — what the code is, whether it's migrable to microservices, and why.

## A.2 Trigger — opt-in, after deterministic analysis (it costs tokens)
Deterministic analysis (Tier 1 + graph) always runs and is free. The migrability assessment is a **separate action the user requests** (a button on the analysis result screen), because it costs tokens. The user sees the free facts first, then decides to deepen. It is NOT run automatically on every analysis.

## A.3 What the LLM judges on — distilled facts, NEVER raw code
Per the hybrid principle (deterministic extracts facts, LLM interprets), the LLM never receives the whole codebase. It receives the **outputs of the deterministic pipeline**:
- Technologies + versions + framework (from manifests).
- The dependency graph + coupling + detected clusters (or the absence of clusters).
- Directory structure and entry-point signals (HTTP routes/handlers, `main`, blueprints, apps, `wsgi.py`, etc.).
- Whether a domain layer was identified (models/entities) vs only infrastructure.
- Optionally, a small sample of the highest fan-in / most central modules (capped — controls cost).

This keeps the call cheap, scalable to large repos, and accurate. Defining the input precisely is the core of M1.

## A.4 Verdict — nuanced, structured
The verdict is structured (not free text), with an enum and supporting fields:
- `verdict`: `MIGRABLE` | `PARTIAL` | `NOT_MIGRABLE`.
- `summary`: human-readable "what this code is" (e.g. "Flask REST API for a blogging platform, organized in blueprints").
- `reasons`: why this verdict.
- `blockers` (for PARTIAL/NOT): what prevents clean migration and what would need to change.
- `confidence`: the LLM's own confidence (it's a judgment, not a fact).

Grounded in the two reference cases:
- **Conduit → MIGRABLE.** Clear API service, Flask blueprints, domain models, decomposes into 3 services. Summary: "Flask REST API, blueprint-organized, clear domain separation."
- **notiplan → PARTIAL or NOT_MIGRABLE.** Flask but script-style, no domain-layer separation (everything classifies as infrastructure, 0 clusters). Verdict explains: "no clear domain/service boundaries; functions and shared state without a domain model; would need restructuring before a clean microservices split." This is the honest answer that today's silent "0 services" fails to give.
- **Non-service repos (library, CLI, frontend, config) → NOT_MIGRABLE** with a clear reason ("this is a library, not a service-oriented backend").

## A.5 Blocking — block-by-default with override (refinement of "blocking")
The verdict gates progression, but is not an absolute wall (the LLM can err — it's a judgment):
- `MIGRABLE` → green light, proceed normally.
- `PARTIAL` → strong warning shown, but the user may proceed.
- `NOT_MIGRABLE` → progression to generation is **blocked by default**; the user can read the reasons and **explicitly override** ("I understand the risk, migrate anyway"). Block with a valve, not a wall.
- If the assessment was never run → no block (the user proceeds without it, at their own risk); the UI nudges them to run it before approving.

## A.6 ModelClient port — first LLM call in analysis
This introduces a `ModelClient` port (the same abstraction the SemanticClusterer LLM hole will use later). v1 adapter: a **direct API call** with structured JSON output — NOT Claude Code headless (this is a single structured assessment, not an agentic code-writing loop). Uses the same `ANTHROPIC_API_KEY` (runtime env, never logged) already set up for Camino B. The multimodel cost-optimizing router is a future hole.

## A.7 Cost
A single LLM call (or a few) over distilled facts — cheap (cents, not the dollars of generation; no agentic loop, no code execution). Still, record the estimated cost per assessment and show it.

## A.8 Where it fits — assessment on the analysis, not a new mandatory state
It is NOT a new state in the machine (it's optional). The verdict is stored on the migration (a field/sub-document), runnable on-demand after `ANALYZING` completes (when the summary exists). It gates the Approve/Generate action in `AWAITING_APPROVAL`: if a `NOT_MIGRABLE` verdict exists without override, Approve is disabled until override. A check layered on the existing flow, not a new step.

## A.9 Holes
- Multimodel router (simple direct adapter now).
- Sampling strategy for very large repos (start with a simple central-modules cap; refine if needed).
- The LLM verdict is advisory+gating, not infallible — the override (A.5) is the safeguard.

### A.9.1 CodeIgniter 3 (convention-routed) signal coverage — Lesson 11 honesty

The CI3 path (`php_ci3_resolver.go` + `php_language_analyzer.go`) resolves the
dependency graph by convention (no PSR-4, no namespaces). The deterministic scorer
feeds on `SummaryModuleCard` facts; CI3 cards populate only what is *actually
extractable* from convention-routed PHP. What each scorer signal gets, and why:

- **god_modules — fed.** Method count per CI3 class is extracted directly
  (`phpExtractClassMembers`), so `Users_model` (51 methods) crosses the
  ≥20-function threshold honestly. The remaining gate (`IsSharedStateHub`) is now
  satisfied via the structural-hub rule below.
- **hub_severity / shared-state hubs — fed by *structural* fan-in, not mutable
  state.** CI3 application classes declare **no module-level mutable state** — they
  hold constants, methods, and per-request `$this->` instance state, none of which
  is module-scoped mutable. The PSR-4/Python hub rule (`len(State)>0 && fanIn≥2`)
  therefore never fires for CI3. The honest, extractable coupling signal that *does*
  exist is the dependency graph's fan-in: a base class everyone `extends`
  (`MY_Controller` fan-in 22, `MY_Model` fan-in 18) or a model everyone loads
  (`Users_model` fan-in 12) concentrates incoming coupling, which is exactly the
  "must decouple before extracting" semantic of a shared-state hub. So for the
  convention-routed regime, fan-in ≥ 2 alone qualifies a module as a hub. The
  hub's `State` list is reported **empty (honest)** — we surface the fan-in, never
  a fabricated variable name. Regime detection is intrinsic: a CI3 card's module
  identity is its `.php` file path (`Module == File`), which Python dotted names and
  PSR-4 backslash FQNs never match. See `isSharedStateHub` / `isConventionRoutedCard`
  in `distiller.go` and the mirror `isSharedStateHubCard` in `pipeline.go`.
- **routing_layout — NOT fed (documented gap).** CI3 routes by convention
  (controller class = route), and the analyzer does not emit `Routes` for CI3 cards
  (`extractCI3Cards` sets none; `application/config/routes.php` custom maps are out of
  v1 scope). Rather than fabricate a route count, routing_layout stays at penalty 0
  for CI3. This is an honest omission, not a signal we invented. Filling it would
  require a `routes.php` parser plus the controller-as-route convention expansion —
  tractable later, deliberately not faked now.

---

# PART B — Tasks (one at a time)

**Gate block (CGO_ENABLED=1):** `buf lint` · `go build ./...` · `go vet ./...` · `go test ./core/...` (+ `vite build` for frontend tasks).

### Task M0 — ModelClient port + direct API adapter (no real LLM call needed to build)
Define a `ModelClient` port: takes a prompt/structured request, returns the model's text/JSON response + usage/cost. Implement a direct-API adapter (single call, structured JSON output, `ANTHROPIC_API_KEY` from env, never logged). Validate the adapter's plumbing with a mock/stubbed response (no real tokens). Gate block. STOP for review — this is the first model-call abstraction on the backend; confirm it's clean before using it.

### Task M1 — Fact distillation (deterministic, no LLM)
Build the distiller that assembles the LLM input from the analysis outputs (A.3): technologies, the graph + clusters (or absence), directory structure, entry-point signals, domain-vs-infra classification, and a capped sample of the most central modules. Output a compact structured "analysis digest". Validate the digest content against Conduit (rich: blueprints, domain models, 3 clusters) and notiplan (sparse: no domain, all infra, 0 clusters) — the digest must capture the difference. No tokens spent. Gate block.

### Task M2 — Migrability assessor (the LLM call — spends tokens)
Implement the assessor: feeds the M1 digest to the `ModelClient` with a prompt that asks for the structured verdict (A.4), parses the JSON response into a typed `MigrabilityVerdict`, records cost. Validate against BOTH reference repos with real calls (cheap — single call each): Conduit must return `MIGRABLE` with a sensible summary; notiplan must return `PARTIAL` or `NOT_MIGRABLE` with reasons about the lack of domain separation. Report both verdicts verbatim + cost. STOP for review — confirm the verdicts are sensible before wiring the gate.

### Task M3 — Wire into the flow + the gate (block-with-override)
Persist the verdict on the migration. Expose it (extend `GetMigration` or a dedicated read). Implement the gate (A.5/A.8): a `NOT_MIGRABLE` verdict without override blocks the Approve/Generate action; an override flag (set by explicit user action) lifts the block; `PARTIAL` warns but allows; absent verdict = no block. Idempotent re-run. Gate block. No tokens (use a stored verdict or mock).

### Task M4 — Frontend: verdict display + run button + override
On the analysis result / transformation screen: a "Assess migrability" button (opt-in, notes it costs tokens) that triggers M2; display the verdict prominently (the reserved space at the top of the transformation view) with verdict badge, summary, reasons, blockers, confidence, and cost. For `NOT_MIGRABLE`: disable Approve and show the explicit override control ("I understand, migrate anyway"). Validate against stored verdicts for Conduit (migrable) and notiplan (not/partial). `vite build` clean.

---

## Standing rules
- Part A frozen. The LLM judges distilled facts (A.3), never raw code.
- The verdict gates with override (A.5) — never a hard wall.
- Validate verdicts against the two real cases: Conduit (migrable) and notiplan (partial/not).
- Token-spending steps are only M2 and the M4 trigger — everything else uses mocks/stored data.
- `ANTHROPIC_API_KEY` from runtime env, never logged.
- Holes (A.9) get a marked stop/report, never a guess.
- STOP for review after M0 and M2.
