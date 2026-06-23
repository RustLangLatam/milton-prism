package repositories

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/ports"
	analysisadapters "milton_prism/core/worker/analysis/infrastructure/adapters"
	analysisports "milton_prism/core/worker/analysis/ports"
	workerapp "milton_prism/core/worker/decomposition/application"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	workeradapters "milton_prism/core/worker/decomposition/infrastructure/adapters"
	workerports "milton_prism/core/worker/decomposition/ports"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ ports.BlueprintGenerator = (*BlueprintGeneratorAdapter)(nil)

// BlueprintGeneratorAdapter implements ports.BlueprintGenerator by running the
// full Distill pipeline (graph load → infra detect → cluster → cards) and
// calling the LLM with the resulting AnalysisDigest. No raw source code is
// ever included in the prompt — only module names, coupling weights, and
// function/class names already present in the digest.
type BlueprintGeneratorAdapter struct {
	graphLoader *workeradapters.MongoGraphLoader
	detector    workerports.InfraDetector
	clusterer   *workeradapters.LouvainClusterer
	client      analysisports.ModelClient
	// recorder accounts LLM token spend in billing (best-effort). May be nil when
	// billing is not wired — recording is then skipped.
	recorder ports.UsageRecorder
}

// NewBlueprintGeneratorAdapter constructs the adapter.
// analysisDB must be the analysis database (milton_prism_analysis).
// recorder accounts LLM token spend in billing best-effort; pass nil to disable
// recording (e.g. billing not configured).
// Returns an error only when ANTHROPIC_API_KEY is absent from the environment.
func NewBlueprintGeneratorAdapter(analysisDB *mongo.Database, recorder ports.UsageRecorder) (*BlueprintGeneratorAdapter, error) {
	client, err := analysisadapters.NewAnthropicModelClient(nil)
	if err != nil {
		return nil, fmt.Errorf("blueprint generator: model client: %w", err)
	}
	return &BlueprintGeneratorAdapter{
		graphLoader: workeradapters.NewMongoGraphLoader(analysisDB),
		detector:    workeradapters.NewPHPAwareInfraDetector(),
		clusterer:   workeradapters.NewLouvainClusterer(),
		client:      client,
		recorder:    recorder,
	}, nil
}

// Generate runs the Distill pipeline for analysisSummaryID and asks the LLM to
// propose microservice groupings anchored to the measured coupling in the graph.
// The roadmap is used only to supply blocking step orders for precondition_note
// and required_steps — it is not re-scored here.
func (a *BlueprintGeneratorAdapter) Generate(ctx context.Context, userID, migrationID, analysisSummaryID uint64, roadmap *domain.RestructuringRoadmap) (*domain.ServiceBlueprint, error) {
	graph, err := a.graphLoader.Load(ctx, analysisSummaryID)
	if err != nil {
		return nil, fmt.Errorf("blueprint generator: load graph: %w", err)
	}

	cls, err := a.detector.Detect(ctx, graph)
	if err != nil {
		return nil, fmt.Errorf("blueprint generator: detect: %w", err)
	}

	domainGraph := workerdomain.DomainSubgraph(graph, cls.Domain)
	clusterResult, err := a.clusterer.Cluster(ctx, workerports.ClusterInput{
		DomainGraph:        domainGraph,
		DomainModules:      cls.Domain,
		StructuralFallback: cls.StructuralFallback,
	})
	if err != nil {
		return nil, fmt.Errorf("blueprint generator: cluster: %w", err)
	}

	preDomain := cls.Domain
	if workerapp.ApplyCoherenceGuardrail(cls, clusterResult, domainGraph) {
		// Mirror the louvain scorer: move domain modules to infra so that the full
		// module population is preserved in the classification (none silently dropped).
		cls.Infra = append(cls.Infra, preDomain...)
	}

	cards, err := a.graphLoader.LoadCards(ctx, analysisSummaryID)
	if err != nil {
		return nil, fmt.Errorf("blueprint generator: load cards: %w", err)
	}

	digest := workerapp.Distill(graph, cls, clusterResult, cards, 0)

	return a.GenerateFromDigest(ctx, userID, migrationID, digest, roadmap)
}

// GenerateFromDigest accepts a pre-built AnalysisDigest, skipping the MongoDB
// graph-load pipeline. Used in tests that inject fixtures directly.
func (a *BlueprintGeneratorAdapter) GenerateFromDigest(ctx context.Context, userID, migrationID uint64, digest *workerdomain.AnalysisDigest, roadmap *domain.RestructuringRoadmap) (*domain.ServiceBlueprint, error) {
	prompt := buildBlueprintPrompt(digest, roadmap)
	req := analysisports.ModelRequest{
		System:    blueprintSystemPrompt,
		Prompt:    prompt,
		MaxTokens: 3000,
		Purpose:   "blueprint-generation",
	}

	type blueprintServiceJSON struct {
		Name      string   `json:"name"`
		Modules   []string `json:"modules"`
		Rationale string   `json:"rationale"`
	}
	type blueprintResponseJSON struct {
		Services         []blueprintServiceJSON `json:"services"`
		IsHypothetical   bool                   `json:"is_hypothetical"`
		PreconditionNote string                 `json:"precondition_note"`
		RequiredSteps    []int32                `json:"required_steps"`
		ConfidenceNote   string                 `json:"confidence_note"`
	}

	result, resp, err := enricherComplete[blueprintResponseJSON](ctx, a.client, req)
	if err != nil {
		return nil, fmt.Errorf("blueprint generator: llm: %w", err)
	}

	// Record LLM token spend in billing (best-effort). A failure is logged and
	// swallowed — it must never break the generation.
	recordMigrationSpend(ctx, a.recorder, ports.UsageSpend{
		UserID:      userID,
		MigrationID: migrationID,
		Operation:   billingv1.UsageOperation_USAGE_OPERATION_MIGRATION,
		TokensIn:    int64(resp.InputTokens),
		TokensOut:   int64(resp.OutputTokens),
		CostUSD:     resp.CostUSD,
	})

	services := make([]*migrationv1.BlueprintService, len(result.Services))
	for i, s := range result.Services {
		services[i] = &migrationv1.BlueprintService{
			Name:      s.Name,
			Modules:   s.Modules,
			Rationale: s.Rationale,
		}
	}
	return &migrationv1.ServiceBlueprint{
		Services:         services,
		IsHypothetical:   result.IsHypothetical,
		PreconditionNote: result.PreconditionNote,
		RequiredSteps:    result.RequiredSteps,
		CostUsd:          resp.CostUSD,
		GeneratedTime:    timestamppb.New(time.Now().UTC()),
		ConfidenceNote:   result.ConfidenceNote,
	}, nil
}

// blueprintSystemPrompt instructs the model on its role and the honesty contract.
const blueprintSystemPrompt = `You are a software architect proposing microservice boundaries for a real codebase.

You receive a structural digest from static analysis: module names, dependency edges with categorical coupling levels, per-module function/class/state details, infra-vs-domain classification, and Louvain clustering results (when available).

HONESTY CONTRACT — these rules are mandatory:
1. Group modules by MEASURED COUPLING (coupling levels in the graph), not by name similarity.
2. If Louvain clusters exist, use them as the primary grouping signal. You may refine but NOT invent a different partition.
3. If no domain modules exist (DomainEmpty=true) AND no clusters exist, return an EMPTY services list with is_hypothetical=true.
   - precondition_note: 1-2 sentences ONLY. State that no blueprint is possible until the blocking action plan steps are completed, referencing them by kind/order. DO NOT re-describe what those steps do. Reference, do not narrate.
   - confidence_note: 1 sentence ONLY. State the confidence level and the structural reason (no domain layer / no clusters). Do NOT invent numbers or metrics not present in the digest.
4. If there are clusters but the domain is sparse, propose services anchored to the clusters and set is_hypothetical appropriately.
5. Never reference source code — only module names, function names, coupling levels, and structural facts from the digest.
6. The rationale for each service MUST cite coupling level labels from the module data above (e.g. "very-high coupling", "codebase-wide hub"). "tightly coupled (very-high)" is good; "related functionality" is not. Do NOT invent numeric values.
7. Respond ONLY with valid JSON matching the requested schema. No preamble, no markdown, no extra text.`

// buildBlueprintPrompt serialises the full digest into a prompt string.
// Never includes raw source code — only structural metadata already in the digest.
func buildBlueprintPrompt(digest *workerdomain.AnalysisDigest, roadmap *domain.RestructuringRoadmap) string {
	var b strings.Builder

	// --- Technologies ---
	if len(digest.Technologies) > 0 {
		b.WriteString(fmt.Sprintf("## Technologies\n%s\n", strings.Join(digest.Technologies, ", ")))
		if digest.Framework != "" {
			b.WriteString(fmt.Sprintf("Framework: %s\n", digest.Framework))
		}
	}

	// --- Graph summary ---
	b.WriteString("\n## Dependency Graph\n")
	b.WriteString(fmt.Sprintf("No-service-boundaries: %v  Low-confidence: %v\n",
		digest.NoServiceBoundaries, digest.LowConfidence))

	// --- Classification ---
	b.WriteString(fmt.Sprintf("\n## Domain/Infra Classification\nDomain modules: %s\nInfra modules: %s\nTest modules: %s\nDomainEmpty: %v\n",
		strings.Join(digest.Classification.DomainModules, ", "),
		strings.Join(digest.Classification.InfraModules, ", "),
		strings.Join(digest.Classification.TestModules, ", "),
		digest.Classification.DomainEmpty,
	))

	// --- Clusters (Louvain result) ---
	if len(digest.Clusters) > 0 {
		b.WriteString("\n## Louvain Clusters (use these as primary grouping signal)\n")
		for _, c := range digest.Clusters {
			b.WriteString(fmt.Sprintf("Cluster %q: %s\n", c.BlueprintGroup, strings.Join(c.Modules, ", ")))
		}
	} else {
		b.WriteString("\n## Louvain Clusters\nNone — no clustering result available.\n")
	}

	// --- Shared-state hubs ---
	if len(digest.SharedStateHubs) > 0 {
		b.WriteString("\n## Shared-State Hubs (high coupling — splitting these requires solving shared state first)\n")
		for _, h := range digest.SharedStateHubs {
			b.WriteString(fmt.Sprintf("- %s: coupling=%s state_vars=%v\n", h.Module, hubCouplingLevel(h.FanIn), h.State))
		}
	}

	// --- Top edges by weight (most important coupling signals) ---
	b.WriteString("\n## Top Dependency Edges (from→to, weight)\n")
	type edge struct {
		from, to string
		weight   uint32
	}
	edges := make([]edge, len(digest.Graph.Edges))
	for i, e := range digest.Graph.Edges {
		edges[i] = edge{e.From, e.To, e.Weight}
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].weight > edges[j].weight })
	limit := 30
	if len(edges) < limit {
		limit = len(edges)
	}
	for _, e := range edges[:limit] {
		b.WriteString(fmt.Sprintf("  %s → %s (coupling=%s)\n", e.from, e.to, edgeWeightLevel(e.weight)))
	}
	if len(edges) > 30 {
		b.WriteString("  ... (additional edges omitted)\n")
	}

	// --- Module cards (top by degree) ---
	b.WriteString("\n## Module Cards (top by coupling degree)\n")
	cardLimit := 20
	if len(digest.ModuleCards) < cardLimit {
		cardLimit = len(digest.ModuleCards)
	}
	for _, c := range digest.ModuleCards[:cardLimit] {
		funcs := strings.Join(c.Functions, ", ")
		if len(funcs) > 120 {
			funcs = funcs[:120] + "..."
		}
		b.WriteString(fmt.Sprintf("- %s (coupling=%s loc=%s hub=%v)\n  functions: %s\n",
			c.Module, couplingLevel(c.FanIn, c.FanOut), locLevel(c.LOC), c.IsSharedStateHub, funcs))
		if len(c.Routes) > 0 {
			b.WriteString(fmt.Sprintf("  routes: %s\n", routeLevel(len(c.Routes))))
		}
	}

	// --- Roadmap blocking steps ---
	var blockingOrders []int32
	var blockingDescriptions []string
	if roadmap != nil {
		for _, item := range roadmap.GetActionPlan() {
			if item.GetBlocking() {
				blockingOrders = append(blockingOrders, item.GetOrder())
				blockingDescriptions = append(blockingDescriptions, fmt.Sprintf("  Step %d (%s): %s", item.GetOrder(), item.GetKind(), item.GetAction()))
			}
		}
	}
	if len(blockingOrders) > 0 {
		b.WriteString("\n## Blocking Restructuring Steps (from action_plan)\n")
		for _, d := range blockingDescriptions {
			b.WriteString(d + "\n")
		}
	}

	// --- Honesty instruction ---
	b.WriteString(`
---

Based ONLY on the coupling data above, propose a microservice blueprint.

CRITICAL RULES:
- If DomainEmpty=true AND no Louvain clusters exist: return "services": [] with is_hypothetical=true.
  precondition_note must be SHORT (1-2 sentences): say a blueprint requires completing the blocking steps, name them by kind (e.g. EXTRACT_DOMAIN, DEFINE_BOUNDARIES), and say what becomes possible after. NO re-explaining the steps, NO re-stating the diagnosis.
  confidence_note must be ONE sentence: low confidence + the structural reason (no domain layer, no clusters). Do NOT invent numbers or metrics.
- If Louvain clusters exist: use them as service boundaries, refine if needed, but do NOT invent a completely different partition.
- Each service rationale MUST cite coupling level labels from the module data above (e.g. "very-high coupling", "codebase-wide hub").
- required_steps must list the blocking step order numbers that must be completed before this blueprint is valid.

Respond with exactly this JSON:
{
  "services": [
    { "name": "...", "modules": ["...", "..."], "rationale": "..." }
  ],
  "is_hypothetical": true/false,
  "precondition_note": "...",
  "required_steps": [1, 2],
  "confidence_note": "..."
}`)

	_ = blockingOrders // used in LLM context via blockingDescriptions
	return b.String()
}

// hubCouplingLevel maps weighted fan-in to a coupling label. Mirrors the
// assessor helper so that both prompts use consistent category thresholds.
func hubCouplingLevel(fanIn uint32) string {
	switch {
	case fanIn >= 15:
		return "codebase-wide"
	case fanIn >= 8:
		return "very-high"
	case fanIn >= 4:
		return "high"
	default:
		return "moderate"
	}
}

// edgeWeightLevel maps an edge weight to a coupling-strength label.
func edgeWeightLevel(w uint32) string {
	switch {
	case w >= 10:
		return "very-high"
	case w >= 5:
		return "high"
	case w >= 2:
		return "moderate"
	default:
		return "low"
	}
}

// locLevel maps a line count to a size label.
func locLevel(loc uint32) string {
	switch {
	case loc >= 800:
		return "very-large"
	case loc >= 300:
		return "large"
	case loc >= 100:
		return "medium"
	default:
		return "small"
	}
}

// couplingLevel maps combined fan-in+fan-out to a degree label.
func couplingLevel(fanIn, fanOut uint32) string {
	switch {
	case fanIn+fanOut >= 25:
		return "very-high"
	case fanIn+fanOut >= 12:
		return "high"
	case fanIn+fanOut >= 5:
		return "moderate"
	case fanIn+fanOut > 0:
		return "low"
	default:
		return "isolated"
	}
}

// routeLevel maps a route count to a descriptor.
func routeLevel(n int) string {
	switch {
	case n >= 50:
		return "extensive"
	case n >= 20:
		return "many"
	case n >= 5:
		return "moderate"
	default:
		return "few"
	}
}

// penaltyLevel maps a structural-problem score penalty to a severity label.
// Used by the enricher prompt in the same package.
func penaltyLevel(p int32) string {
	switch {
	case p >= 20:
		return "critical"
	case p >= 10:
		return "major"
	default:
		return "moderate"
	}
}

// impactLevel maps an action-step score improvement estimate to a label.
// Used by the enricher prompt in the same package.
func impactLevel(p int32) string {
	switch {
	case p >= 20:
		return "high"
	case p >= 10:
		return "significant"
	default:
		return "moderate"
	}
}
