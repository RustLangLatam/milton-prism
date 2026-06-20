# Milton Prism — Analysis Engine: Architecture & Implementation Spec

**The first engine service. Composes with the Architecture Canon for its hexagonal skeleton; this document defines its custom internals.**

The analysis engine turns a source codebase into the `AnalysisSummary` contract: technologies (with versions and currency), vulnerabilities, a dependency graph with coupling, and metrics. Its output is what the decomposition engine later consumes to propose service boundaries. Unlike the CRUD services, its application logic is custom and is **not** produced by the service generator — the generator scaffolds the hexagonal skeleton; this spec defines what fills it.

---

## 0. Decisions locked

- **Source stacks (v1):** Java/Spring, PHP (Laravel + Symfony), Node/Express, Python/Django, .NET/C#, Ruby/Rails.
- **Worker language:** Go (single-language v1; `go-tree-sitter` + `go-enry`).
- **Job substrate:** a simple durable queue (**Asynq on the existing Redis**), with the **`Migration` state machine as the workflow**. The orchestrator interface is abstracted so it can be swapped to Temporal later without touching services.
- **Vulnerability source:** **OSV.dev** batch query API, behind a port.

---

## 1. The non-negotiable principle: facts are deterministic, interpretation is LLM

Versions, CVEs, dependency edges, and line counts are **ground truth** produced by tools and external databases — **never** by a language model. A hallucinated CVE or version is a security defect, not a feature. The LLM is used **only** for semantic interpretation (clustering modules into candidate bounded contexts, describing responsibilities), and even that is grounded in the deterministic dependency graph. Most of the pipeline uses no model at all, which also minimizes cost.

---

## 2. Shape: an async pipeline, not request/response

Analysis of a real monolith runs for minutes over thousands of files. It does not fit a synchronous gRPC call. Three pieces:

```
analysis service (Go, hexagonal — already scaffolded, thin)
   • owns the AnalysisSummary record (CRUD)
   • RunAnalysis RPC = enqueue a job; returns immediately with the record in RUNNING
   • writes the final summary when the worker reports back

analysis worker (Go — the pipeline, this spec)
   • pulls jobs from Asynq, runs the stage pipeline against a workspace
   • idempotent and resumable: re-running reads persisted progress and continues

job substrate (Asynq on Redis)
   • durable enqueue/retry/backoff
   • the Migration state machine is the source of truth for "where are we";
     a stage is safe to re-run because it keys off persisted state
```

**Resumability discipline:** every stage must be idempotent and derive "what's done" from persisted state, not from in-memory progress. That is what lets you defer Temporal — "resume" means "read state, run the next stage." Keep the orchestrator behind an interface (`enqueue`, `status`, `cancel`) so a future Temporal adapter is a drop-in.

---

## 3. The pipeline stages

Each stage produces part of `AnalysisSummary`. Marked: **[D]** deterministic / **[X]** external data / **[L]** LLM; **[shared]** works across all 7 stacks / **[per-lang]** needs a language analyzer.

| # | Stage | Output | Type | Scope |
|---|-------|--------|------|-------|
| 1 | Acquire source | workspace on disk (clone/unzip) | [D] | shared |
| 2 | Inventory | total_files, total_lines, detected languages | [D] | shared (`go-enry`) |
| 3 | Manifest parse | dependencies + declared versions per ecosystem | [D] | shared (per-ecosystem parser) |
| 4 | Version currency | latest_version + TechnologyStatus | [X] | shared (per-ecosystem registry) |
| 5 | Vulnerability scan | Vulnerability[] (CVE, severity, component, fixed_in) | [D][X] | shared (OSV.dev) |
| 6 | Dependency graph | DependencyEdge[] (from, to, weight/coupling) | [D] | **per-lang** (import resolution) |
| 7 | Semantic clustering | candidate bounded contexts + responsibilities | [L] | **per-lang** (framework profile) |
| 8 | Assemble & persist | AnalysisSummary written via the service | [D] | shared |

**Tier 1 (broad, all 7 stacks, low effort):** stages 1–5 + 8. This already produces the technologies / vulnerabilities / metrics sections — exactly the "code summary" the product promises and what feeds the Stitch analysis screen.

**Tier 2 (deep, per-language effort):** stages 6–7. These feed decomposition and are built language-by-language. Do **not** promise Tier 2 for all 7 in v1; implement the first stack you will actually migrate, leave the rest as holes.

---

## 4. Hexagonal shape (ports and adapters)

The worker obeys the Canon's layering. Application orchestrates ports; adapters do the I/O. Ports:

```
SourceAcquirer        Acquire(ctx, source) → workspacePath          // git clone / unzip into sandbox workspace
LanguageDetector      Detect(ctx, workspace) → []DetectedLanguage   // go-enry
ManifestParser        Parse(ctx, workspace, ecosystem) → []Dependency
VersionResolver       Latest(ctx, ecosystem, pkg) → version,status  // registry lookups, cached
VulnerabilityScanner  Scan(ctx, []Dependency) → []Vulnerability     // OSV.dev adapter
DependencyGraphBuilder Build(ctx, workspace, lang) → []DependencyEdge
SemanticClusterer     Cluster(ctx, graph, sources) → []BoundedContext // LLM via the model router
SummaryWriter         Write(ctx, migrationID, summary) → error      // calls analysis service
```

The two **per-language** ports (`DependencyGraphBuilder`, `SemanticClusterer`) are realized by a pluggable interface:

```
LanguageAnalyzer
  Ecosystem() Ecosystem
  ResolveImports(ctx, workspace) → []DependencyEdge   // tree-sitter parse + per-language import resolution
  FrameworkProfile() FrameworkProfile                 // hints for semantic clustering (controllers, modules, etc.)
```

One implementation per stack; unimplemented stacks are **holes** — the engine reports "deep analysis not available for <stack>; Tier-1 summary produced" rather than guessing. Same philosophy as the generator's language profiles.

---

## 5. Tooling per stage

- **Acquire (1):** git clone (shallow) or unzip into a workspace inside the sandbox boundary. Treat all source as untrusted (never execute it during analysis).
- **Inventory (2):** `go-enry` (GitHub Linguist port) for language detection and line counting.
- **Manifest parse (3):** per-ecosystem deterministic parsers — `pom.xml`/Gradle (Maven), `package.json`/lockfiles (npm), `composer.json`/`composer.lock` (Composer — covers Laravel and Symfony), `requirements.txt`/`pyproject.toml` (PyPI), `*.csproj`/`packages.config` (NuGet), `Gemfile`/`Gemfile.lock` (RubyGems). Prefer lockfiles when present (resolved versions).
- **Version currency (4):** the matching registry per ecosystem (Maven Central, npm, PyPI, Packagist, NuGet, RubyGems). Cache aggressively; this is the slowest network-bound stage. Map to `TechnologyStatus` (Current / Outdated / End-of-life) using registry latest + known EOL data.
- **Vulnerabilities (5):** **OSV.dev** — given `(ecosystem, package, version)` it returns known advisories (it aggregates GitHub Advisory and ecosystem sources, covering all seven). Batch the dependency list. Cache results; degrade gracefully (mark vulnerabilities as "scan unavailable" rather than failing the whole analysis) if OSV is unreachable.
- **Dependency graph (6):** `go-tree-sitter` with the grammar per language to parse imports, then **per-language resolution** of what each import points to (Java packages, PHP PSR-4 namespaces, Node require/import, Python modules, C# usings, Ruby requires). Compute coupling as edge weight (afferent/efferent counts between modules).
- **Semantic clustering (7):** the LLM, via the multimodel router (§6), grounded in the deterministic graph from stage 6 plus framework hints.

---

## 6. Multimodel usage

Only stage 7 uses a model. The router picks by task weight:

- Stages 1–6, 8: **no model**.
- Stage 7 (semantic clustering / bounded-context proposal): a **capable model**, because the quality of the decomposition depends on it. This is grounded input (the graph), not free generation, which keeps it reliable and bounded in tokens.
- Cheap/free models are appropriate for trivial sub-tasks if any arise (e.g. summarizing a single module's purpose), but the clustering decision itself warrants the capable tier.

Keep the model behind the router interface; never call a provider SDK directly from the application layer (Canon dependency rule).

---

## 7. Output contract

The worker assembles `AnalysisSummary` (the existing proto type) and writes it back through the analysis service (`SummaryWriter` → an internal write RPC or repository call), then advances the `Migration` state from `ANALYZING` toward `DESIGNING`. Partial results are valid: a Tier-1-only run produces technologies/vulnerabilities/metrics with an empty/served-as-unavailable dependency graph and no semantic clusters, and is marked as such.

---

## 8. Failure, caching, and safety

- **Untrusted source:** the codebase is third-party and may be malicious or vulnerable. Acquire and parse inside the sandbox boundary; **never execute** source during analysis (parsing only).
- **Network degradation:** version-currency and OSV stages are network-bound. Cache by `(ecosystem, package, version)`. On outage, produce the summary with those sections marked unavailable rather than failing.
- **Large repos:** stream the file walk; cap per-file parse time; record skipped files in the summary.
- **Idempotency:** re-running a stage overwrites its slice of the summary deterministically; never appends duplicates.
- **Stop conditions:** unsupported stack for Tier 2 → produce Tier 1 + report the hole. Corrupt/unreadable source → fail the migration into `FAILED` with a readable reason, not a panic.

---

## 9. What to build first

1. **Tier 1 across all 7 stacks** — stages 1–5 + 8 and the thin worker + Asynq wiring. This makes the analysis screen real end-to-end and exercises the async pipeline and the `ANALYZING` state transition.
2. **One `LanguageAnalyzer`** (stages 6–7) for the stack you will migrate first — proving the deep path before replicating it.
3. **The remaining `LanguageAnalyzer`s** incrementally, as holes filled one stack at a time.

This sequencing gives you a working, demoable analysis (the summary) quickly, and defers the genuinely hard per-language work to where it is actually needed.
