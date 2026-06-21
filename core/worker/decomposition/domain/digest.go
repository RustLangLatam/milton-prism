package domain

// SummaryCards is the module-level data loaded from an analysis summary for
// digest computation. It is the decomposition worker's view of the analysis
// output — no proto types leak beyond the infrastructure adapter.
type SummaryCards struct {
	ModuleCards  []SummaryModuleCard
	Blueprints   []SummaryBlueprint
	Technologies []string
	Framework    string
}

// SummaryModuleCard is one module's extracted facts from the analysis summary.
type SummaryModuleCard struct {
	Module    string
	File      string
	Functions []string
	Classes   []string
	State     []string // mutable module-level variable names
	Routes    []SummaryRoute
	Docstring string
	LOC       uint32
}

// SummaryRoute is a single HTTP route extracted from the analysis summary.
type SummaryRoute struct {
	Method  string
	Path    string
	Handler string
}

// SummaryBlueprint is a single Flask Blueprint extracted from the analysis summary.
type SummaryBlueprint struct {
	Name      string
	File      string
	URLPrefix string
}

// AnalysisDigest is the deterministic, token-free compact summary of an
// AnalysisSummary. It is consumed by the migrability assessor and the
// LLM-clusterer. All fields are derived from persisted analysis data with no
// LLM calls; re-computing gives the same result.
type AnalysisDigest struct {
	Technologies []string
	Framework    string

	Graph               DigestGraph
	Clusters            []DigestCluster
	NoServiceBoundaries bool
	LowConfidence       bool

	// ModuleCards contains full cards for the top-K modules by weighted degree.
	ModuleCards    []DigestModuleCard
	AggregateCard  *DigestAggregateCard
	TotalModules   int
	SampledModules int

	Blueprints  []DigestBlueprint
	EntryPoints DigestEntryPoints

	Classification  DigestClassification
	SharedStateHubs []DigestSharedStateHub

	// AnchorFacts are deterministic, classifier-produced facts (e.g. detected
	// database engine, classified architectural pattern) that the LLM must anchor
	// its prose to rather than re-deriving. They never affect the score — they are
	// rendered into the prompt only so the model confirms the deterministic
	// classification instead of hallucinating a different one (Canon: deterministic
	// first, LLM anchors). Empty for the decomposition-pipeline path.
	AnchorFacts []string
}

// DigestGraph is the node+edge view of the dependency graph.
type DigestGraph struct {
	Nodes []string
	Edges []DigestEdge
}

// DigestEdge is a directed, weighted dependency between two modules.
type DigestEdge struct {
	From   string
	To     string
	Weight uint32
}

// DigestCluster is a proposed service boundary from clustering.
type DigestCluster struct {
	BlueprintGroup string
	Modules        []string
}

// DigestModuleCard is the per-module summary in the digest, enriched with
// fan-in / fan-out computed from the dependency graph.
type DigestModuleCard struct {
	Module           string
	File             string
	Functions        []string
	Classes          []string
	MutableState     []string
	Routes           []DigestRoute
	DocstringHead    string
	LOC              uint32
	FanIn            uint32
	FanOut           uint32
	IsSharedStateHub bool
}

// DigestRoute is a single HTTP route within a module card.
type DigestRoute struct {
	Method  string
	Path    string
	Handler string
}

// DigestAggregateCard summarises the modules that were cut by the top-K cap.
type DigestAggregateCard struct {
	ModuleCount       int
	TotalLOC          uint32
	TotalFunctions    int
	TotalClasses      int
	MutableStateCount int
	TotalRoutes       int
}

// DigestBlueprint is a single Flask Blueprint in the digest.
type DigestBlueprint struct {
	Name      string
	File      string
	URLPrefix string
}

// DigestEntryPoints summarises HTTP entry-point signals.
type DigestEntryPoints struct {
	HasHTTPRoutes   bool
	TotalRoutes     int
	BlueprintCount  int
	SingleBlueprint bool
}

// DigestClassification records the domain-vs-infra split from stage 2.
type DigestClassification struct {
	DomainModules []string
	InfraModules  []string
	// TestModules are modules the infra detector classified as test — excluded
	// from both domain clustering and infra grouping.
	TestModules []string
	// DomainEmpty is true when the infra detector found zero domain modules —
	// the "acantilado" signal: automatic decomposition is structurally blocked.
	DomainEmpty bool
}

// DigestSharedStateHub is a module with mutable state and fan-in ≥ 2. These
// are anti-forced-decomposition guardrails: extracting them into a separate
// service requires solving the shared-state problem first.
type DigestSharedStateHub struct {
	Module string
	State  []string
	FanIn  uint32
}
