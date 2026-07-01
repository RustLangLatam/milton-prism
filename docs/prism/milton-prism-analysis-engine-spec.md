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

---

## 10. Classified enrichment fields (deterministic; LLM anchors)

Two `AnalysisSummary` fields enrich the summary with a *classified* answer, not just raw facts. Both are produced **deterministically** by the worker pipeline before any LLM call. The LLM assessment receives them as anchor facts and may **name/confirm** them in prose, but it never re-derives or contradicts them (Canon §1: facts are deterministic, interpretation is LLM-but-grounded).

### 10.1 `database_detection` (stage 3c) — `DatabaseDetection`

Detects the database engine(s) the analysed code talks to. Signal precedence:

1. **Drivers / ORM packages** (most authoritative — the code links the client): `psycopg2`/`pg`/`asyncpg`→PostgreSQL; `mysqli`/`pdo_mysql`/`mysql2`/`PyMySQL`/`mariadb`→MySQL; `mongo*`/`mongoose`/`pymongo`→MongoDB; `sqlite3`/`pdo_sqlite`/`better-sqlite3`→SQLite; `sqlsrv`/`tedious`/`pymssql`→SQL Server; `cx_Oracle`/`oci8`/`oracledb`→Oracle; `redis`/`predis`/`ioredis`→Redis. Matching is whole-token aware (a `pg` rule never fires on `imagepng`).
2. **Config files**: `.env` `DB_CONNECTION=` and `DATABASE_URL` scheme; Laravel `config/database.php` `'default' => env('DB_CONNECTION', 'mysql')`; CodeIgniter `application/config/database.php` `'dbdriver'` (both `=>` and `[...] =` forms); Django settings `DATABASES['default']['ENGINE'] = 'django.db.backends.*'`.
3. **Framework default tie-break** (last resort, clearly labelled): Laravel ⇒ MySQL, used only when no *primary* (relational/document) engine surfaced.

**Redis is a cache, not the system of record:** it is dropped whenever any primary engine is also detected (including one inferred from a framework default), and survives only when it is the sole signal. **Honest `unknown`** (engines empty, `unknown=true`) when nothing names an engine — never a guess. ORM evidence is preserved even on `unknown` to explain why.

### 10.2 `architectural_pattern` (stage 6f) — `ArchitecturalPattern`

A rules-based classifier maps the structural signals the pipeline **already computed** (no new analysis, no I/O, no LLM) to one canonical pattern with a confidence in `[0,1]` and the evidence used. Signals: `deep_analysis_available`, domain/infra ratio + layers present + `structural_fallback` (from `ModuleClassification`), cluster count + hub severity (from `MigrabilityScore`), framework (from `Technologies`), routing topology (from `ModuleCard.routes`/blueprints).

Decision order (first match wins; confidence reflects how unambiguous the match is):

1. **Spaghetti / Big ball of mud / Acantilado** — `deep_analysis_available=false` (analyzer blind ⇒ reduced confidence) OR no domain layer at all. A present-but-undomained framework lowers confidence.
2. **MVC** — an MVC framework is detected (Laravel, CodeIgniter, Symfony, Rails, Django, Express, Spring…). Checked **before** generic layering because frameworks impose MVC regardless of folder ratios. Extracted HTTP routes are **not required** — the PHP/convention analyzers often cannot emit `RouteInfo` (controllers are router-wired, not decorator-wired), so requiring routes would mis-bucket every Laravel/CodeIgniter monolith. Confidence rises with an explicit application layer and/or extracted routes.
3. **Modular monolith vs. Layered** — ≥3 cohesive clusters with a healthy domain ratio (≥0.30). When a **dominant shared-state hub couples** those clusters, the modules are not truly independent ⇒ **Layered/N-tier** (the hub is the shared layer); otherwise ⇒ **Modular monolith**.
4. **Clean / Hexagonal** — an explicit application layer (`application_modules` non-empty) with a strong domain ratio (≥0.40). Hexagonal when the application layer is the only extra layer; Clean when clustering is also strong (≥3 clusters). v1 cannot prove dependency-rule **direction** statically, so these two are reported at reduced confidence and only on an explicit layer signal (Lesson 11 limit, documented honestly).
5. **Layered / N-tier** — domain and infra layers both present at a moderate ratio, without an explicit application layer or multi-cluster modularity. `structural_fallback` lowers confidence.
6. **Fallback** — domain present but signals weak ⇒ Layered at low confidence.

**Known limits (Lesson 11):** the direction of the dependency rule (inward-pointing for Clean/Hexagonal) is not statically proven in v1, so Clean/Hexagonal are emitted conservatively and at reduced confidence; convention-routed stacks (CI3) expose no `RouteInfo`, so the routing signal is absent for them and the framework prior carries the MVC classification. No pattern is invented — the classifier returns the layered fallback rather than guess.

### 10.3 JSON contract (for the frontend)

`AnalysisSummary` gains two fields (proto field numbers 28/29; persisted as wrapped proto-bytes in Mongo; rendered in `docs/openapi.yaml`):

```json
"databaseDetection": {
  "engines": ["DATABASE_ENGINE_POSTGRESQL"],     // enum; [] when unknown
  "engineNames": ["PostgreSQL"],                  // display names, aligned with engines
  "unknown": false,                               // true ⇒ engines empty (honest)
  "evidence": ["driver: psycopg2 (PostgreSQL)", "orm: SQLAlchemy"]
},
"architecturalPattern": {
  "kind": "ARCHITECTURAL_PATTERN_KIND_MVC",       // enum
  "name": "MVC",                                  // display name
  "confidence": 0.85,                             // float [0,1]
  "evidence": ["framework: Laravel (MVC)", "domain/infra ratio 69%", "93 application-layer module(s)"]
}
```

Both fields are absent (omitted) for Tier-1-only analyses with no structural data.

### 10.4 `intake_assessment` (stage 7-intake) — `IntakeAssessment`

The intake gate is a deterministic, no-I/O, no-LLM verdict on a single honest question: **can the platform migrate this repository today?** It runs on every analysis (independent of deep analysis) so even a Tier-1-only or non-backend repo gets an honest answer. Two guards:

**(5) Codebase kind.** The platform migrates **backends** today. The classifier maps already-computed signals to a `CodebaseKind` with a confidence:

- **BACKEND** — a catalogued web/server framework (Laravel, Symfony, CodeIgniter, Flask, Django, FastAPI, Express, NestJS, Spring, Rails, ASP.NET, Gin…); or HTTP routes/Flask blueprints; or a backend package manager (Composer/PyPI/Maven/NuGet/RubyGems). A backend signal **dominates** a co-present frontend one — fullstack repos are migrated for their backend. A backend package manager with no catalogued framework and no overriding frontend/mobile signal is BACKEND at reduced confidence (likely an uncatalogued framework or a backend library).
- **FRONTEND** — a frontend framework (React, Vue, Angular, Svelte, Next…) **and no backend signal at all**; or npm-only JS/TS with no server framework (lower confidence).
- **MOBILE** — Flutter / React Native / Ionic / Xamarin.
- **UNSPECIFIED** — no decisive signal (Lesson 11: backend/non-backend cannot be proven from static structure alone, so the classifier reports a confidence and refuses to guess rather than mislabel).

**(7) Primary-language support.** The primary backend language is checked against the set of **registered Tier-2 analyzers** (today PHP, Python — derived from the analyzer registry at run time, never hardcoded). A supported backend language present anywhere in the inventory is preferred as the primary language (mirrors the manifest-language boost, so vendored frontend assets cannot mask the real backend language). When the primary language has **no analyzer**, no dependency graph is produced — surfaced honestly here instead of as a silent empty/INCOMPLETE report.

**Decision (honest degradation, Canon):** `migratable = (codebase_kind == BACKEND) && language_supported`. The guards are **non-blocking warnings, not a hard FAILED** — the analysis still completes and all Tier-1 facts (technologies, vulnerabilities) are preserved. This mirrors the existing `deep_analysis_available` → `INCOMPLETE_NO_STRUCTURAL_DATA` precedent: every run produces a valid summary; honest signals propagate from origin. When a guard fails, `migratable=false` plus a specific, human-readable `warnings[]` entry says exactly why (e.g. *"This repository looks like a frontend-only application (SPA), not a backend…"* or *"Primary language Java is not supported yet (supported: PHP, Python); no dependency graph was produced…"*). The two guards do not stack noise: for a frontend/library/CLI/mobile repo the kind warning already explains the outcome, so the unsupported-language warning is suppressed.

A blocking FAILED was rejected on purpose: it would discard the Tier-1 facts and contradict the pipeline contract that **every run yields a valid (possibly partial) summary**. The frontend gates the migrability report on `migratable`; the LLM assessor (when later triggered) reads the assessment to distinguish *"incomplete because unsupported language"* from other INCOMPLETE causes.

**JSON contract (for the frontend):**

```json
"intakeAssessment": {
  "codebaseKind": "CODEBASE_KIND_BACKEND",          // enum (BACKEND|FRONTEND|LIBRARY|CLI|MOBILE|UNSPECIFIED)
  "kindConfidence": 0.95,                            // float [0,1]
  "primaryLanguage": "PHP",                          // go-enry primary backend language
  "languageSupported": true,                         // has a Tier-2 analyzer?
  "supportedLanguages": ["PHP", "Python"],           // analyzers wired at run time
  "migratable": true,                                // headline: backend && supported
  "warnings": [],                                     // specific reasons when not migratable
  "evidence": ["framework: Laravel (web/backend)", "backend package manager present"]
}
```

Non-migratable examples:

```json
// Frontend-only SPA (React, npm-only): guard (5) fails.
{ "codebaseKind": "CODEBASE_KIND_FRONTEND", "kindConfidence": 0.9, "primaryLanguage": "JavaScript",
  "languageSupported": false, "supportedLanguages": ["PHP","Python"], "migratable": false,
  "warnings": ["This repository looks like a frontend-only application (SPA / static site), not a backend service. The platform only migrates backends today, so no migrability verdict is produced for it."],
  "evidence": ["framework: React (frontend SPA)", "no backend framework, package manager, or HTTP routes"] }

// Java/Spring backend: guard (5) passes, guard (7) fails (no Java analyzer).
{ "codebaseKind": "CODEBASE_KIND_BACKEND", "kindConfidence": 0.95, "primaryLanguage": "Java",
  "languageSupported": false, "supportedLanguages": ["PHP","Python"], "migratable": false,
  "warnings": ["Primary language Java is not supported yet (supported: PHP, Python). No dependency graph was produced, so the deep migrability analysis is unavailable for this repository."],
  "evidence": ["framework: Spring (web/backend)", "backend package manager present"] }
```

`intake_assessment` is **always present** on summaries produced after this gate was added (proto field 30; persisted as wrapped proto-bytes in Mongo as `intake_assessment_bytes`; rendered in `docs/openapi.yaml`).

### 10.5 `security_findings` (stage 3d) — `SecurityFinding[]`

Code-level security findings detected **IN** the analysed source — distinct from the dependency CVEs in `vulnerabilities` (stage 5, OSV.dev). The scanner (`SecurityScanner` port, `security_scanner.go` adapter) walks the workspace and reports **hardcoded secrets / credentials in cleartext**. It is a pure deterministic function of file contents: no LLM, no execution of source, no network. Stage 3d, additive — it never affects scores or verdicts.

**What it detects (high signal, file:line, confidence):**

1. **Provider-prefixed secrets** (no benign reading ⇒ HIGH, confidence 0.85–0.97): AWS access key ID (`AKIA…`/`ASIA…`) and secret access key (40-char base64 after `aws_secret_access_key=`), PEM private-key blocks (`-----BEGIN … PRIVATE KEY-----`), OpenAI-style `sk-…`, GitHub `ghp_/gho_/github_pat_…`, Slack `xox[baprs]-…`, Google `AIza…`, Stripe `sk_live_/rk_live_…`, JWTs (`eyJ….eyJ….…`), and **database connection strings carrying credentials** (`scheme://user:pass@host`).
2. **Generic credential assignments** (MEDIUM, confidence 0.6; escalates to HIGH/0.8 when the value is long and high-Shannon-entropy): a key whose name contains `password/passwd/secret/api_key/access_key/auth_token/client_secret/token` assigned a quoted literal, including PHP array-access (`$cfg['password'] = '…'`) and YAML/JSON/`.env` forms.

**Honesty (Lesson 11) — what it does NOT flag (and why):**

- **Placeholders / examples** are suppressed by an exact-value table and a substring table: `changeme`, `your-password-here`, `example`, `password`, `xxxxxxxx`, repeated-char values (`aaaa`, `****`), etc. ⇒ no false positive on `password = "your-password-here"`.
- **Env / config references**, not literals: `env('DB_PASSWORD')`, `process.env.X`, `os.environ[…]`, `${SECRET}`, `{{ vault_key }}`, and bare `UPPER_SNAKE` identifiers (`db_password = "DB_PASSWORD"`) are recognised as references and dropped.
- **Vendored / dependency / build trees** (`vendor/`, `node_modules/`, `.git/`, `venv/`, `dist/`, `build/`, …) are skipped — a secret in third-party code is not this repo's finding and is the dominant false-positive source.
- **Too-short generic values** (`< 8` chars) and **minified/blob lines** (`> 600` chars, data URIs) are skipped.
- The reported `snippet` always has the **secret value redacted** (`AKIA****REDACTED****`); the raw value is never echoed.

**Out of scope in v1 (documented, not faked):** *missing token validation / unauthenticated endpoints.* This was deliberately **not** implemented. Honestly detecting "a route with no auth middleware" requires parsing each framework's routing + middleware flow (which controller, which guard/decorator, whether auth is applied at a group/blueprint level) — the PHP/convention analyzers don't even emit `RouteInfo` for CI3/Laravel (see §10.2), so an auth-guard heuristic would be guesswork producing aggressive false positives, exactly what Lesson 11 forbids. Per the task's stop condition, only the solid, high-signal detector (hardcoded secrets) ships; the enum (`SecurityFindingType`) is left open so a future, flow-aware auth check can be added without a breaking change.

**JSON contract (for the frontend):**

```json
"securityFindings": [
  {
    "type": "SECURITY_FINDING_TYPE_HARDCODED_SECRET",   // enum
    "severity": "SECURITY_SEVERITY_HIGH",               // LOW|MEDIUM|HIGH
    "file": "config/database.php",                       // workspace-relative
    "line": 42,                                          // 1-based
    "description": "Hardcoded AWS secret access key",
    "snippet": "aws_secret_access_key = \"wJal****REDACTED****\"",  // secret redacted
    "rule": "aws-secret-access-key",                     // stable detector id
    "confidence": 0.9                                    // float [0,1]
  }
]
```

`security_findings` is an empty array (honest zero) when the scan found nothing, and absent for analyses run before this stage was added. Proto field **31**; persisted as wrapped proto-bytes in Mongo as `security_findings_bytes`; rendered in `docs/openapi.yaml` (schema `SecurityFinding`).
