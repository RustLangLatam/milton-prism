// Package domain contains the internal value types used by the decomposition
// pipeline worker. These are the worker's own types; they are distinct from the
// proto-generated types in the service layer.
package domain

import (
	"fmt"
	"strings"

	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

// Type aliases for migration proto types — proto is the single source of truth.
type (
	RestructurePlan = migrationv1.RestructurePlan
	ProposedService = migrationv1.ProposedService
)

// JobPayload is the Asynq task payload for decompose:run jobs.
type JobPayload struct {
	MigrationID   uint64 `json:"migration_id"`
	SummaryID     uint64 `json:"summary_id"`
	RemoteURL     string `json:"remote_url"`
	DefaultBranch string `json:"default_branch"`
}

// Module is the fully-qualified name of a source module (e.g. "conduit.articles.models").
type Module string

// Edge is a directed dependency between two modules with a coupling weight.
type Edge struct {
	From   Module
	To     Module
	Weight uint32
}

// Graph is the loaded representation of an AnalysisSummary's dependency_graph.
type Graph struct {
	Edges []Edge
}

// AllModules returns the deduplicated set of all module names that appear as
// either a source or target in any edge.
func (g *Graph) AllModules() []Module {
	seen := make(map[Module]struct{}, len(g.Edges)*2)
	for _, e := range g.Edges {
		seen[e.From] = struct{}{}
		seen[e.To] = struct{}{}
	}
	out := make([]Module, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	return out
}

// Classification holds the result of the InfraDetector stage (stage 2).
type Classification struct {
	// Infra are modules classified as shared infrastructure with no own domain
	// identity (e.g. database layer, config, utilities, app factory).
	Infra []Module
	// Domain are modules that belong to a domain blueprint group and will be
	// partitioned into services by the SemanticClusterer in stage 3.
	Domain []Module
	// Tests are test modules excluded from both infra and domain analysis.
	Tests []Module
	// StructuralFallback is true when the infra detector found zero blueprint
	// groups and fell back to the structural fan-in heuristic. The pipeline
	// propagates this flag to ClusteringResult.LowConfidence so the plan is
	// always marked low-confidence when this heuristic was used.
	StructuralFallback bool
}

// Cluster is a group of domain modules proposed as a single service boundary.
type Cluster struct {
	// BlueprintGroup is the dominant blueprint prefix for this cluster
	// (e.g. "conduit.articles"). It drives service naming in stage 4.
	BlueprintGroup string
	// Modules are all domain modules assigned to this cluster.
	Modules []Module
}

// ClusteringResult is the output of the SemanticClusterer (stage 3).
type ClusteringResult struct {
	Clusters   []Cluster
	Modularity float64
	// LowConfidence is set when modularity is below the engine's threshold.
	// The partition is still returned as best-effort — the pipeline continues
	// and flags the result for human review.
	LowConfidence bool
	// CandidateGroupings carries the raw LLM-proposed groups, before the
	// shared-state guardrail runs. Preserved for UI display even when the
	// guardrail blocks the full partition.
	CandidateGroupings []ProposedGroup
	// RestructureRecs are blocking recommendations generated when the LLM
	// shared-state guardrail detects that the proposed partition cannot be
	// approved without decoupling mutable-state hubs first.
	RestructureRecs []RestructureRec
}

// ClusteringProposal is the JSON response expected from the LLM clusterer.
// The anti-hallucination validator checks every module name against the
// known domain module set before accepting this proposal.
type ClusteringProposal struct {
	Groups      []ProposedGroup `json:"groups"`
	Explanation string          `json:"explanation"`
}

// ProposedGroup is one candidate service boundary proposed by the LLM.
type ProposedGroup struct {
	Name             string   `json:"name"`
	Modules          []string `json:"modules"`
	Responsibilities []string `json:"responsibilities"`
	Confidence       string   `json:"confidence"`
}

// RestructureRec is an actionable recommendation produced by the shared-state
// guardrail in the LLM clusterer. A blocking recommendation must be resolved
// before decomposition can produce a valid plan.
type RestructureRec struct {
	Kind     string // "shared_state"
	Subject  string // target module name
	Action   string // concrete recommendation text
	Blocking bool   // true when this blocks decomposition approval
}

// IsIncoherentFallback returns true when the clusters produced by the structural
// fallback are residue-by-exclusion rather than real domain boundaries.
//
// It fires when strictly fewer than half the clusters have at least one internal
// edge in the domain sub-graph. An internal edge connects two modules within
// the same cluster — evidence they belong together for a domain reason, not
// merely because they were left after removing hub modules.
//
// A star-topology codebase (every non-hub module imports only the hub) produces
// isolated spoke nodes. Louvain partitions them into N singleton clusters with
// zero internal cohesion. Repos with real domain but non-standard naming have
// actual cross-module edges; their clusters have internal edges and this
// predicate returns false.
func IsIncoherentFallback(domainGraph *Graph, clusters []Cluster) bool {
	if len(clusters) == 0 {
		return false
	}
	clusterOf := make(map[Module]int, len(clusters)*3)
	for i, c := range clusters {
		for _, m := range c.Modules {
			clusterOf[m] = i
		}
	}
	hasInternal := make([]bool, len(clusters))
	for _, e := range domainGraph.Edges {
		ci, okFrom := clusterOf[e.From]
		cj, okTo := clusterOf[e.To]
		if okFrom && okTo && ci == cj {
			hasInternal[ci] = true
		}
	}
	coherent := 0
	for _, v := range hasInternal {
		if v {
			coherent++
		}
	}
	return coherent*2 < len(clusters)
}

// ServiceCandidate is the output of the characterization stage (stage 4).
// It represents a proposed microservice derived from a Cluster.
type ServiceCandidate struct {
	// Name is the service name derived from the blueprint group (e.g. "articles").
	Name string
	// ErrorPrefix is the unique 3-char uppercase error code (e.g. "ART").
	// Assigned by PrefixAllocator; integration point for the orchestrator registry.
	ErrorPrefix string
	// OwnedResources are the domain-model modules (.models suffix) in this cluster.
	OwnedResources []Module
	// Deps are data-layer inter-service dependencies: edges between .models modules
	// from this service to another, plus FK-derived deps added by augmentDataDeps.
	// These are hard deployment dependencies — the service cannot function without
	// the referenced service's data being accessible.
	Deps []string
	// OperationalCouplings are view/controller-layer cross-service imports. In the
	// monolith these are real Python import statements; in microservices they become
	// gRPC calls or async events. They are NOT hard deployment dependencies and do
	// not belong in Deps — separating them prevents false cycles in the dep graph.
	OperationalCouplings []OperationalCoupling
}

// OperationalCoupling records a view/controller-layer import from one service
// into another. Each coupling is a candidate for an async event or a gRPC call
// after decomposition — it is present in the monolith as a direct import but
// would vanish as a compile-time dependency in a proper microservices topology.
type OperationalCoupling struct {
	FromService  string // service whose view/controller layer imports the other
	ToService    string // service that is referenced
	FromModule   string // the specific module (e.g. "conduit.user.views")
}

// DerivedContract is the output of the ContractDeriver stage (stage 5) for
// a single cluster. It contains the generated .proto file text and the
// structured metadata consumed by stage 6 (data ownership).
type DerivedContract struct {
	ServiceName string
	// ProtoContent is the full text of the derived .proto file.
	ProtoContent string
	// ProtoPath is the path written within the workspace
	// (e.g. ".milton_prism/contracts/articles.proto").
	ProtoPath string
	// Messages are the proto messages derived from SQLAlchemy models.
	Messages []ProtoMessage
	// RPCs are the service RPCs derived from Flask routes.
	RPCs []ServiceRPC
	// HasTODORoutes is true when at least one non-CRUD route was found.
	HasTODORoutes bool
	// Incomplete is true when the deriver could not produce a full contract:
	// models modules were present but zero proto messages were extracted.
	// A flagged artifact must be reviewed before generation proceeds.
	Incomplete       bool
	IncompleteReason string
}

// ProtoMessage represents a message block in the derived proto.
type ProtoMessage struct {
	Name   string
	Fields []ProtoField
	// Relationships holds SQLAlchemy relationship() names and their target classes
	// (e.g. "author → UserProfile"). They are NOT proto fields but are emitted as
	// comments in the generated message block so they are not silently discarded.
	Relationships []string
}

// ProtoField represents one field within a ProtoMessage.
type ProtoField struct {
	Name      string
	Type      string // proto type (e.g. "string", "uint64", "google.protobuf.Timestamp")
	Number    int
	Comment   string // inline comment, e.g. for cross-service FK annotations
	IsCrossFK bool   // true when this field is a foreign key to another service
	RefTable  string // SQLAlchemy __tablename__ of the referenced table
	RefService string // service name that owns RefTable (empty if not resolved)
}

// ServiceRPC represents one entry in the derived service block.
type ServiceRPC struct {
	Name       string // rpc name (e.g. "ListArticles"), empty when IsTODO is true
	Path       string // original Flask route path
	HTTPMethod string
	IsTODO     bool   // true when the route could not be mapped to a standard CRUD method
}

// DataOwnership is the output of stage 6. It records the shared-database flag
// (always true in v1), the list of foreign keys that cross service boundaries,
// and the aggregated operational couplings collected from all service candidates.
type DataOwnership struct {
	// SharedDatabase is always true in v1: all proposed services share one database.
	// Each boundary spec must declare this explicitly so the flag is never silent.
	SharedDatabase      bool
	CrossServiceFKs     []CrossServiceFK
	// OperationalCouplings aggregates the view-layer couplings from all candidates.
	// Used by assemblePlan to populate RestructurePlan.operational_couplings.
	OperationalCouplings []OperationalCoupling
}

// CrossServiceFK describes a single foreign-key reference that crosses a
// service boundary. It is listed as deferred consistency debt in the boundary
// specs and the RestructurePlan rationale.
type CrossServiceFK struct {
	OwnerService string // service that owns the field (e.g. "articles")
	OwnerMessage string // proto message name that contains the field (e.g. "Article", "Comment")
	FieldName    string // AIP proto field name (e.g. "author_identifier")
	RefTable     string // SQLAlchemy __tablename__ of the target (e.g. "userprofile")
	RefService   string // service that owns RefTable (e.g. "profile"), empty if unresolved
}

// DomainSubgraph returns a new Graph containing only edges where both
// endpoints are in the provided domain module set. Tests and infra modules
// are automatically excluded because they are absent from domainModules.
func DomainSubgraph(full *Graph, domainModules []Module) *Graph {
	set := make(map[Module]bool, len(domainModules))
	for _, m := range domainModules {
		set[m] = true
	}
	var edges []Edge
	for _, e := range full.Edges {
		if set[e.From] && set[e.To] {
			edges = append(edges, e)
		}
	}
	return &Graph{Edges: edges}
}

// ServiceArtifact holds the design-time artifacts for a single proposed service.
// These are persisted before the workspace is cleaned up so the generation stage
// can read them without requiring the source workspace to still exist.
type ServiceArtifact struct {
	// ServiceName is the canonical service name (e.g. "articles").
	ServiceName string
	// ProtoContent is the full text of the derived .proto file from stage 5.
	ProtoContent string
	// BoundarySpec is the YAML boundary spec text generated in stage 7.
	BoundarySpec string
	// Incomplete propagates DerivedContract.Incomplete: true when the deriver
	// could not fully extract the service boundary. Generation must not proceed
	// for incomplete artifacts without human review.
	Incomplete       bool
	IncompleteReason string
}

// BuildBoundarySpecYAML generates the YAML boundary spec for a single proposed
// service. It is a pure function of domain types so it can be called from both
// the pipeline (application layer) and the filesystem writer (infrastructure).
func BuildBoundarySpecYAML(
	svc *migrationv1.ProposedService,
	sharedDB bool,
	crossFKs []CrossServiceFK,
	opCouplings []OperationalCoupling,
) string {
	var b strings.Builder

	b.WriteString("# Milton Prism boundary spec — generated by decomposition engine\n")
	b.WriteString("# TODO: per-service data ownership + cross-service consistency\n")
	b.WriteString("# Review and commit to protobuf/proto/... when ready.\n\n")

	b.WriteString("name: " + svc.GetName() + "\n")
	b.WriteString("error_prefix: " + svc.GetErrorPrefix() + "\n")

	b.WriteString("owned_resources:\n")
	for _, r := range svc.GetOwnedResources() {
		b.WriteString("  - " + r + "\n")
	}
	if len(svc.GetOwnedResources()) == 0 {
		b.WriteString("  []\n")
	}

	b.WriteString("inter_service_deps:\n")
	for _, d := range svc.GetInterServiceDeps() {
		b.WriteString("  - " + d + "\n")
	}
	if len(svc.GetInterServiceDeps()) == 0 {
		b.WriteString("  []\n")
	}

	if sharedDB {
		b.WriteString("shared_database: true  # monolith DB — per-service separation deferred\n")
	} else {
		b.WriteString("shared_database: false\n")
	}

	if len(crossFKs) > 0 {
		b.WriteString("cross_service_fks:\n")
		for _, fk := range crossFKs {
			b.WriteString(fmt.Sprintf("  - owner_message: %s\n", fk.OwnerMessage))
			b.WriteString(fmt.Sprintf("    owner: %s\n", fk.OwnerService))
			b.WriteString(fmt.Sprintf("    field: %s\n", fk.FieldName))
			b.WriteString(fmt.Sprintf("    ref_table: %s\n", fk.RefTable))
			if fk.RefService != "" {
				b.WriteString(fmt.Sprintf("    ref_service: %s\n", fk.RefService))
			}
		}
	}

	if len(opCouplings) > 0 {
		b.WriteString("# Operational couplings — view-layer imports that become gRPC calls or events:\n")
		b.WriteString("operational_couplings:\n")
		for _, oc := range opCouplings {
			b.WriteString(fmt.Sprintf("  - from: %s\n", oc.FromService))
			b.WriteString(fmt.Sprintf("    to: %s\n", oc.ToService))
			b.WriteString(fmt.Sprintf("    source_module: %s\n", oc.FromModule))
		}
	}

	return b.String()
}
