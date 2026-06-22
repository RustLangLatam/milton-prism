// Package ports defines the driven ports of the analysis pipeline worker.
// All ports follow the Canon's dependency rule: application orchestrates them;
// adapters in the infrastructure layer implement them.
package ports

import (
	"context"

	analysisdomain "milton_prism/core/services/analysis/domain"
	workerdomain "milton_prism/core/worker/analysis/domain"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
)

// SourceAcquirer clones or unzips a source repository into a local workspace.
// branch is a hint for the VCS backend (e.g. the default branch from the
// repository record); an empty string means "use the remote's default".
// The returned cleanup function releases any temporary resources created for
// the workspace (e.g. removes a temp directory). Callers must always invoke
// cleanup, even when err is non-nil — if no cleanup is needed, cleanup is a no-op.
// commitSHA is the resolved HEAD commit SHA of the cloned repository; empty when
// the SHA could not be resolved (non-fatal — the caller persists it best-effort).
type SourceAcquirer interface {
	Acquire(ctx context.Context, source, branch string) (workspacePath, commitSHA string, cleanup func(), err error)
}

// LanguageDetector inventories the programming languages present in a workspace.
type LanguageDetector interface {
	Detect(ctx context.Context, workspacePath string) ([]workerdomain.DetectedLanguage, error)
}

// ManifestParser reads dependency manifests for a single package ecosystem.
type ManifestParser interface {
	Parse(ctx context.Context, workspacePath string, ecosystem workerdomain.Ecosystem) ([]workerdomain.Dependency, error)
}

// VersionResolver looks up the latest published version for a package in its
// registry and maps it to a TechnologyStatus.
type VersionResolver interface {
	Latest(ctx context.Context, ecosystem workerdomain.Ecosystem, pkg string) (workerdomain.VersionCurrency, error)
}

// VulnerabilityScanner checks a list of dependencies against vulnerability
// databases (OSV.dev in v1) and returns matching advisories.
type VulnerabilityScanner interface {
	Scan(ctx context.Context, deps []workerdomain.Dependency) ([]*analysisdomain.Vulnerability, error)
}

// DependencyGraphBuilder performs per-language import resolution and produces
// the weighted dependency edges used by the semantic clusterer.
type DependencyGraphBuilder interface {
	Build(ctx context.Context, workspacePath string, lang string) ([]*analysisdomain.DependencyEdge, error)
}

// FrameworkProfile carries static hints about the web framework in use within
// a workspace. Stage 7 (semantic clustering) uses these hints to interpret the
// dependency graph; stage 6 does not consume them.
type FrameworkProfile struct {
	// Framework is the primary web framework detected (e.g. "Flask", "Django").
	Framework string
}

// LanguageAnalyzer provides per-language import resolution, framework metadata,
// and module-card extraction for the pipeline's Tier-2 stages (6–7). Each
// implementation covers exactly one stack; stacks without an implementation are
// holes — the engine skips them and produces a Tier-1-only summary.
type LanguageAnalyzer interface {
	// Language returns the language name as reported by go-enry (e.g. "Python").
	// This value is used to match against DetectedLanguage.Name from stage 2.
	Language() string
	// ResolveImports parses the workspace sources and returns the weighted
	// internal dependency edges. External imports are discarded; only
	// intra-repo module-to-module edges appear in the result.
	ResolveImports(ctx context.Context, workspacePath string) ([]*analysisdomain.DependencyEdge, error)
	// FrameworkProfile returns static metadata about the detected framework.
	// The result is consumed by the semantic clusterer (stage 7) and is not
	// used in stage 6.
	FrameworkProfile() FrameworkProfile
	// ExtractCards performs a structural AST scan of the workspace and returns
	// per-module cards (functions, classes, mutable state, routes, docstring,
	// LOC) plus any web-framework blueprint registrations. Errors are non-fatal
	// in the pipeline — the stage is skipped on failure.
	ExtractCards(ctx context.Context, workspacePath string) ([]*analysisdomain.ModuleCard, []*analysisdomain.BlueprintInfo, error)
}

// ModuleCardProvider routes ExtractCards calls to the LanguageAnalyzer
// registered for each language name. It is implemented by LanguageAnalyzerRegistry
// so the pipeline can wire a single object for both graph building (stage 6)
// and module-card extraction (stage 6b).
type ModuleCardProvider interface {
	ExtractCards(ctx context.Context, workspacePath, lang string) ([]*analysisdomain.ModuleCard, []*analysisdomain.BlueprintInfo, error)
}

// SemanticClusterer proposes candidate bounded contexts from the dependency
// graph and source structure using a language model (stage 7 only).
type SemanticClusterer interface {
	Cluster(ctx context.Context, graph []*analysisdomain.DependencyEdge, sources []string) ([]workerdomain.BoundedContext, error)
}

// SummaryWriter persists the completed AnalysisSummary and advances the
// associated Migration from ANALYZING to DESIGNING when a migration_id is set.
// Implementations must be idempotent: re-writing an already-COMPLETED summary
// is a safe no-op.
type SummaryWriter interface {
	Write(ctx context.Context, summary *analysisdomain.AnalysisSummary) error
	// MarkFailed transitions the migration from ANALYZING to FAILED and persists
	// the human-readable failure reason. Called when the analysis job exhausts
	// all Asynq retries and the failure is definitively permanent.
	MarkFailed(ctx context.Context, migrationID uint64, reason string) error
	// MarkAnalysisFailed transitions the AnalysisSummary from RUNNING to FAILED
	// and records the human-readable failure reason. Called on the final Asynq
	// retry for both standalone and migration-linked analyses.
	MarkAnalysisFailed(ctx context.Context, summaryID uint64, reason string) error
	// MarkAwaitingRootSelection transitions the AnalysisSummary from RUNNING to
	// AWAITING_ROOT_SELECTION and persists the detected candidate roots. Called
	// when a monorepo has multiple distinct project roots and none was chosen:
	// the heavy pipeline is skipped and the user must pick one via SelectRoot.
	// Idempotent (guarded on RUNNING state) and must NOT advance the migration.
	MarkAwaitingRootSelection(ctx context.Context, summaryID uint64, candidates []string) error
	// FindCompletedForBranch returns the most recent COMPLETED AnalysisSummary
	// for the given repository and branch, or nil when none exists.
	// Used by the dedup check before deciding whether to re-analyse.
	FindCompletedForBranch(ctx context.Context, repositoryID uint64, branch string) (*analysisdomain.AnalysisSummary, error)
	// WriteReuse advances the associated Migration from ANALYZING to DESIGNING
	// by linking it to an existing COMPLETED AnalysisSummary (existingSummaryID).
	// Sets analysis_summary_id and analysis_reused=true on the migration record.
	// The AnalysisSummary itself is NOT modified — it remains immutable.
	WriteReuse(ctx context.Context, existingSummaryID, migrationID uint64) error
}

// ModuleClassifier classifies dependency-graph modules into domain, infrastructure,
// and test categories. It is the analysis worker's own classification port —
// independent of the decomposition worker's InfraDetector so each can evolve
// without coupling. Today both use the same blueprint-group + fan-in heuristic
// to guarantee consistent results on identical graphs.
type ModuleClassifier interface {
	Classify(ctx context.Context, edges []*analysisdomain.DependencyEdge) (*analysisdomain.ModuleClassification, error)
}

// DecomposeJobEnqueuer dispatches a decompose:run job after the analysis
// pipeline completes and the migration advances to DESIGNING.
// remoteURL and defaultBranch are forwarded so the decomposition worker
// can re-acquire the source workspace for contract derivation (stage 5).
// rootSubdirectory carries the monorepo scope so stage 5 walks the same
// subdirectory the analysis did; empty means the whole repository root.
type DecomposeJobEnqueuer interface {
	EnqueueDecompose(ctx context.Context, migrationID, summaryID uint64, remoteURL, defaultBranch, rootSubdirectory string) error
}

// RepositoryCredentialReader returns the stored git credential (a PAT or
// similar token) for a given repository. Implementations read from the
// authoritative store (e.g. MongoDB) at job execution time so that the
// credential never has to travel through the task queue.
// Returns an empty string when no credential is registered.
type RepositoryCredentialReader interface {
	GetCredentialRef(ctx context.Context, repositoryID uint64) (string, error)
}

// MigrabilityScorer computes the deterministic structural migrability score from
// the pipeline's own typed data (already in memory from prior stages). It mirrors
// the decomposition worker's Score() pipeline but skips the InfraDetector stage —
// the classification from stage 6c is already correct.
type MigrabilityScorer interface {
	Score(
		ctx context.Context,
		edges []*analysisdomain.DependencyEdge,
		cls *analysisdomain.ModuleClassification,
		cards []*analysisdomain.ModuleCard,
		blueprints []*analysisdomain.BlueprintInfo,
	) (*commonv1.MigrabilityScore, error)
}

// StructuralFrameworkDetector identifies web frameworks by inspecting well-known
// directory and file markers in the workspace, independently of package manager
// manifests. This complements stage-3 manifest detection for frameworks
// distributed as vendored source archives (e.g. CodeIgniter 3 shipped as a
// ZIP) where the framework itself is not a declared dependency.
//
// existing carries all technologies already detected by prior stages so that
// the implementation can avoid adding duplicates when a manifest parser has
// already identified the same framework via a Composer or npm package name.
type StructuralFrameworkDetector interface {
	Detect(ctx context.Context, workspacePath string, existing []*analysisdomain.Technology) ([]*analysisdomain.Technology, error)
}

// DatabaseDetector deterministically identifies the database engine(s) the
// analysed code uses. It draws on three signal sources, in order of authority:
//
//   - drivers/ORM packages from the parsed manifests (psycopg2, pg, mysqli,
//     pdo_mysql, mysql2, mongo*, sqlite3, …);
//   - config files in the workspace (.env DB_CONNECTION, Laravel config/database.php
//     default, DATABASE_URL, Django settings DATABASES engine);
//   - framework defaults (e.g. Laravel default connection MySQL) as a last resort.
//
// It never guesses: when no signal exists the result has Unknown=true and an empty
// engine list. technologies carries the frameworks already detected so the
// framework-default rule can fire without re-deriving them.
type DatabaseDetector interface {
	Detect(
		ctx context.Context,
		workspacePath string,
		deps []workerdomain.Dependency,
		technologies []*analysisdomain.Technology,
	) (*analysisdomain.DatabaseDetection, error)
}

// SecurityScanner deterministically detects code-level security issues IN the
// analysed source — today, hardcoded credentials/secrets in cleartext (API keys,
// passwords, tokens, private keys, JWTs, connection strings with embedded
// credentials). It walks the workspace and matches a fixed table of patterns plus
// an entropy heuristic. It is intentionally conservative (Canon Lesson 11): a
// finding is recorded only when a concrete secret-shaped value surfaces, obvious
// placeholders/examples are suppressed, and every finding carries a confidence.
// It NEVER uses an LLM and NEVER executes the source. Distinct from the dependency
// CVE scan (VulnerabilityScanner); the two never overlap.
type SecurityScanner interface {
	Scan(ctx context.Context, workspacePath string) ([]*analysisdomain.SecurityFinding, error)
}
