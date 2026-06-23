// Package ports defines the driven ports of the decomposition pipeline worker.
// All ports obey the Canon's dependency rule: application orchestrates them;
// adapters in the infrastructure layer implement them.
package ports

import (
	"context"

	workerdomain "milton_prism/core/worker/decomposition/domain"
)

// TargetTopologyLoader reads the architectural target topology selected for a
// migration at creation time. The pipeline uses it to decide whether to emit a
// microservices partition (default) or a single collapsed monolith plan.
// When not wired, the pipeline defaults to MICROSERVICES (the existing flow).
type TargetTopologyLoader interface {
	// LoadTopology returns the migration's TargetTopology. Implementations must
	// map an absent/unspecified value to TARGET_TOPOLOGY_MICROSERVICES so the
	// default flow is never broken.
	LoadTopology(ctx context.Context, migrationID uint64) (workerdomain.TargetTopology, error)
	// LoadStore returns the migration's target persistence engine as the short
	// store label ("mongodb"|"postgres"|"mysql") written to the boundary spec's
	// `store:` field. Implementations must map an absent/unspecified value (Auto)
	// to "mongodb" — the resolved engine for Auto is determined later by the
	// generation worker against the analysis database_detection; the boundary spec
	// only needs the explicit override or the safe default.
	LoadStore(ctx context.Context, migrationID uint64) (string, error)
}

// GraphLoader reads the weighted dependency graph from a persisted
// AnalysisSummary. The graph is the primary input to the decomposition pipeline.
type GraphLoader interface {
	Load(ctx context.Context, summaryID uint64) (*workerdomain.Graph, error)
}

// InfraDetector separates shared-infrastructure modules from domain modules.
// It runs before clustering (stage 3); its output is the domain sub-graph that
// the SemanticClusterer receives. Deterministic — no external I/O.
type InfraDetector interface {
	Detect(ctx context.Context, graph *workerdomain.Graph) (*workerdomain.Classification, error)
}

// ClusterInput is the input to SemanticClusterer.Cluster. Bundling fields into
// a struct allows future adapters (LLM cascade, reranker) to add context
// without a breaking port change.
//
// DomainGraph contains only edges between domain modules (DomainSubgraph has
// already been applied by the pipeline).
type ClusterInput struct {
	DomainGraph   *workerdomain.Graph
	DomainModules []workerdomain.Module
	// StructuralFallback mirrors Classification.StructuralFallback. The cascade
	// adapter uses it to predict whether ApplyCoherenceGuardrail would fire and
	// skip the LLM call for star-topology graphs (notiplan pattern).
	StructuralFallback bool
}

// SemanticClusterer partitions domain modules into candidate service boundaries.
//
// Two adapters are defined:
//   - Deterministic (live): community detection biased by blueprint metadata.
//   - LLM (hole): stub that returns "not implemented" in v1; the pipeline falls
//     back to the deterministic adapter with a low-confidence flag.
type SemanticClusterer interface {
	Cluster(ctx context.Context, input ClusterInput) (*workerdomain.ClusteringResult, error)
}

// PrefixAllocator assigns a unique error-code prefix to a proposed service name.
// In v1 the assignment is deterministic (derived from the service name) with
// in-process collision avoidance. The long-term integration point is the
// orchestrator registry that tracks prefixes across all generated services.
type PrefixAllocator interface {
	Allocate(ctx context.Context, serviceName string) (string, error)
}

// SourceAcquirer clones or checks out the repository source for a migration
// into a temporary workspace directory. It mirrors the analysis worker's
// SourceAcquirer but lives in the decomposition worker's port layer.
// The returned cleanup function releases the temporary workspace; callers
// must always invoke it even when err is non-nil.
type SourceAcquirer interface {
	Acquire(ctx context.Context, remoteURL, branch string) (workspacePath string, cleanup func(), err error)
}

// ContractDeriver reads source files from a workspace and derives AIP-compliant
// proto contracts for a single cluster. It is a per-framework port:
//   - Flask/SQLAlchemy adapter: live deterministic implementation.
//   - All other frameworks: stub adapter that reports "not implemented".
//
// tableServiceMap maps SQLAlchemy __tablename__ values to the service names that
// own those tables. It is used to annotate cross-service FK fields with the
// target service name (e.g. "usersprofile" → "user").
//
// The derived .proto file is written to
// {workspacePath}/.milton_prism/contracts/{serviceName}.proto.
type ContractDeriver interface {
	Derive(ctx context.Context, cluster workerdomain.Cluster, workspacePath string, tableServiceMap map[string]string) (*workerdomain.DerivedContract, error)
}

// PlanWriter persists the assembled RestructurePlan for a migration and
// advances the migration state from DESIGNING to AWAITING_APPROVAL.
// It also writes YAML boundary specs to the workspace for the generator stage.
type PlanWriter interface {
	WritePlan(ctx context.Context, migrationID uint64, plan *workerdomain.RestructurePlan, workspacePath string, ownership workerdomain.DataOwnership) error
	// MarkFailed transitions the migration from DESIGNING to FAILED and persists
	// a human-readable failure reason. Called when the decomposition job exhausts
	// all Asynq retries and the failure is definitively permanent.
	MarkFailed(ctx context.Context, migrationID uint64, reason string) error
}

// AutoApprover continues a one-shot, platform-driven roadmap run after the plan
// reaches AWAITING_APPROVAL. The migration service's RunMigration RPC records an
// auto_approve intent; once decomposition writes the plan and flips the state to
// AWAITING_APPROVAL, the worker invokes MaybeAutoApprove to honor that intent
// without a human approval click.
//
// The adapter MUST re-apply the same gates a manual ApproveDesign applies:
//   - skip (do nothing) when auto_approve is not set;
//   - skip and report when the plan has no service boundaries (nothing to
//     generate) or the migrability verdict is NOT_MIGRABLE without an override;
//   - otherwise advance the migration to GENERATING and enqueue generation.
//
// It NEVER advances past GENERATING and NEVER triggers a publish — the final git
// push stays a human-gated step. Returns true when generation was kicked off.
type AutoApprover interface {
	MaybeAutoApprove(ctx context.Context, migrationID uint64) (approved bool, err error)
}

// ArtifactStore persists design-time artifacts (proto text + boundary spec) for
// each proposed service, associated with a migration. Upsert semantics: running
// the decomposition pipeline again overwrites existing artifacts — no duplicates.
// These are stored before the workspace is cleaned up so downstream stages can
// retrieve them without re-running the pipeline.
type ArtifactStore interface {
	UpsertArtifacts(ctx context.Context, migrationID uint64, artifacts []workerdomain.ServiceArtifact) error
}

// SummaryLoader reads the module cards, blueprints, and technologies from a
// persisted AnalysisSummary. These are the inputs to the AnalysisDigest
// distiller (M1). Separate from GraphLoader to keep concerns distinct.
type SummaryLoader interface {
	LoadCards(ctx context.Context, summaryID uint64) (*workerdomain.SummaryCards, error)
	// LoadDeepAnalysisAvailable returns the explicit signal set by the analysis
	// pipeline: whether deep (Tier-2) structural analysis produced any output.
	// False means the assessor must degrade honestly rather than score an empty graph.
	LoadDeepAnalysisAvailable(ctx context.Context, summaryID uint64) (bool, error)
}
