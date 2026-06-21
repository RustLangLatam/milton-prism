// Package application contains the decomposition pipeline orchestrator.
// It coordinates the stage ports; infrastructure adapters must never be
// imported here (Canon dependency rule).
package application

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
	applog "milton_prism/pkg/log"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

// Pipeline orchestrates the decomposition pipeline stages for a single job.
type Pipeline struct {
	loader        ports.GraphLoader
	detector      ports.InfraDetector
	clusterer     ports.SemanticClusterer
	allocator     ports.PrefixAllocator
	summaryLoader ports.SummaryLoader
	assessor      *Assessor
	// Stage 5 — contract derivation.
	acquirer ports.SourceAcquirer
	deriver  ports.ContractDeriver
	// Stage 7 — plan write + state advance.
	planWriter    ports.PlanWriter
	artifactStore ports.ArtifactStore
}

// NewPipeline constructs a Pipeline wired with the given stage implementations.
// clusterer and allocator are optional; when nil stages 3–4 are logged as
// pending and skipped.
func NewPipeline(loader ports.GraphLoader, detector ports.InfraDetector) *Pipeline {
	return &Pipeline{loader: loader, detector: detector}
}

// WithClusterer wires the SemanticClusterer (stage 3). Returns p for chaining.
func (p *Pipeline) WithClusterer(c ports.SemanticClusterer) *Pipeline {
	p.clusterer = c
	return p
}

// WithAllocator wires the PrefixAllocator (stage 4). Returns p for chaining.
func (p *Pipeline) WithAllocator(a ports.PrefixAllocator) *Pipeline {
	p.allocator = a
	return p
}

// WithAcquirer wires the SourceAcquirer (stage 5 prerequisite). Returns p for chaining.
func (p *Pipeline) WithAcquirer(a ports.SourceAcquirer) *Pipeline {
	p.acquirer = a
	return p
}

// WithContractDeriver wires the ContractDeriver (stage 5). Returns p for chaining.
func (p *Pipeline) WithContractDeriver(d ports.ContractDeriver) *Pipeline {
	p.deriver = d
	return p
}

// WithPlanWriter wires the PlanWriter (stage 7). Returns p for chaining.
func (p *Pipeline) WithPlanWriter(w ports.PlanWriter) *Pipeline {
	p.planWriter = w
	return p
}

// WithArtifactStore wires the ArtifactStore (stage 7 — artifact persistence). Returns p for chaining.
func (p *Pipeline) WithArtifactStore(s ports.ArtifactStore) *Pipeline {
	p.artifactStore = s
	return p
}

// WithSummaryLoader wires the SummaryLoader used by the M1 digest distiller.
// When set, the digest is computed after stage 3 and logged. Returns p for chaining.
func (p *Pipeline) WithSummaryLoader(sl ports.SummaryLoader) *Pipeline {
	p.summaryLoader = sl
	return p
}

// WithAssessor wires the M2 migrability assessor. When set (and SummaryLoader
// is also wired), the assessor is called after the digest is built and its
// verdict is logged. Returns p for chaining.
func (p *Pipeline) WithAssessor(a *Assessor) *Pipeline {
	p.assessor = a
	return p
}

// MarkFailed transitions the migration from DESIGNING to FAILED and persists
// the human-readable reason. Called by the job handler when all Asynq retries
// are exhausted. No-op when no planWriter is wired.
func (p *Pipeline) MarkFailed(ctx context.Context, migrationID uint64, reason string) error {
	if p.planWriter == nil {
		return nil
	}
	return p.planWriter.MarkFailed(ctx, migrationID, reason)
}

// Run executes the decomposition pipeline for the given job.
//
// Stage 1 (graph-load): reads the dependency_graph from the AnalysisSummary.
//
// Stage 2 (infra-detect): classifies each module as INFRA, DOMAIN, or TEST.
// Tests are excluded from the domain sub-graph before clustering.
//
// Stage 3 (cluster): runs the SemanticClusterer on the domain-only sub-graph.
// If not wired, logged as pending.
//
// Stage 4 (characterize): derives service name, error prefix, owned resources,
// and inter-service deps for each cluster. If not wired, logged as pending.
//
// D1–D2 end here. The migration state is not advanced until D4.
func (p *Pipeline) Run(ctx context.Context, job workerdomain.JobPayload) error {
	applog.Infof("decomposition-worker: starting job migration_id=%d summary_id=%d",
		job.MigrationID, job.SummaryID)

	// Stage 1 — load graph.
	graph, err := p.loader.Load(ctx, job.SummaryID)
	if err != nil {
		return fmt.Errorf("stage 1 (graph-load): %w", err)
	}
	applog.Infof("decomposition-worker: graph loaded edges=%d modules=%d",
		len(graph.Edges), len(graph.AllModules()))

	// Stage 2 — infrastructure detection.
	cls, err := p.detector.Detect(ctx, graph)
	if err != nil {
		return fmt.Errorf("stage 2 (infra-detect): %w", err)
	}

	logModules := func(label string, mods []workerdomain.Module) {
		sorted := make([]workerdomain.Module, len(mods))
		copy(sorted, mods)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		for _, m := range sorted {
			applog.Infof("decomposition-worker:   %-6s %s", label, m)
		}
	}

	applog.Infof("decomposition-worker: classification infra=%d domain=%d tests=%d",
		len(cls.Infra), len(cls.Domain), len(cls.Tests))
	logModules("INFRA", cls.Infra)
	logModules("DOMAIN", cls.Domain)

	if p.clusterer == nil {
		applog.Infof("decomposition-worker: D1 complete — stages 3–7 pending (D2–D4)")
		return nil
	}

	// Stage 3 — clustering.
	// Build the domain-only sub-graph: tests.* are excluded from cls.Domain
	// so they never appear in cross-cluster edges or inflate coupling.
	domainGraph := workerdomain.DomainSubgraph(graph, cls.Domain)
	applog.Infof("decomposition-worker: domain sub-graph edges=%d modules=%d",
		len(domainGraph.Edges), len(cls.Domain))

	clusterResult, err := p.clusterer.Cluster(ctx, ports.ClusterInput{
		DomainGraph:        domainGraph,
		DomainModules:      cls.Domain,
		StructuralFallback: cls.StructuralFallback,
	})
	if err != nil {
		return fmt.Errorf("stage 3 (cluster): %w", err)
	}

	// Apply structural-fallback flag + coherence guardrail.
	if ApplyCoherenceGuardrail(cls, clusterResult, domainGraph) {
		applog.Infof("decomposition-worker: coherence guardrail fired — fallback clusters are hub-and-spoke residue, resetting to no-boundaries migration_id=%d", job.MigrationID)
	}

	confidence := "HIGH"
	if clusterResult.LowConfidence {
		confidence = "LOW"
	}
	applog.Infof("decomposition-worker: clustering done clusters=%d modularity=%.4f confidence=%s",
		len(clusterResult.Clusters), clusterResult.Modularity, confidence)

	for _, c := range clusterResult.Clusters {
		ms := make([]string, len(c.Modules))
		for i, m := range c.Modules {
			ms[i] = string(m)
		}
		applog.Infof("decomposition-worker:   CLUSTER %-30s members=%d %v",
			c.BlueprintGroup, len(c.Modules), ms)
	}

	// Stage 3b — AnalysisDigest distillation (M1).
	// Computed on-demand from the in-memory stage 1–3 results and the module
	// cards loaded from MongoDB. Non-fatal: a missing summaryLoader or load
	// failure skips the digest and assessor without blocking the pipeline.
	var digest *workerdomain.AnalysisDigest
	if p.summaryLoader != nil {
		summaryData, loadErr := p.summaryLoader.LoadCards(ctx, job.SummaryID)
		if loadErr != nil {
			applog.Warningf("decomposition-worker: stage 3b load cards failed summary_id=%d: %v",
				job.SummaryID, loadErr)
		} else {
			digest = Distill(graph, cls, clusterResult, summaryData, 0)
			logDigest(digest, job.SummaryID)
		}
	}

	// Stage 3c — Migrability assessment (M2).
	// One LLM call over the AnalysisDigest — costs ~cents per repo.
	// Non-fatal: a missing assessor or API error is logged and skipped.
	// Honest-degrade gate: skip the assessor entirely when deep analysis was
	// unavailable (explicit pipeline signal, same as the persisted-verdict path),
	// so we neither spend a token nor log a confident verdict over an empty digest.
	if p.assessor != nil && digest != nil {
		available := true
		if p.summaryLoader != nil {
			if a, availErr := p.summaryLoader.LoadDeepAnalysisAvailable(ctx, job.SummaryID); availErr != nil {
				applog.Warningf("decomposition-worker: stage 3c availability check failed summary_id=%d: %v",
					job.SummaryID, availErr)
			} else {
				available = a
			}
		}
		if !available {
			applog.Infof("decomposition-worker: stage 3c skipped migration_id=%d verdict=%s (deep analysis unavailable)",
				job.MigrationID, workerdomain.VerdictIncompleteNoStructuralData)
		} else {
			score := Score(digest)
			result, assessErr := p.assessor.Assess(ctx, digest, score, "en")
			if assessErr != nil {
				applog.Warningf("decomposition-worker: stage 3c assessor failed migration_id=%d: %v",
					job.MigrationID, assessErr)
			} else {
				logVerdict(result, job.MigrationID)
			}
		}
	}

	if p.allocator == nil {
		applog.Infof("decomposition-worker: D2 partial — stage 4 pending (PrefixAllocator not wired)")
		return nil
	}

	// Stage 4 — characterization.
	candidates, err := characterize(ctx, domainGraph, clusterResult.Clusters, p.allocator)
	if err != nil {
		return fmt.Errorf("stage 4 (characterize): %w", err)
	}

	applog.Infof("decomposition-worker: characterization done services=%d", len(candidates))
	for _, svc := range candidates {
		applog.Infof("decomposition-worker:   SERVICE %-20s prefix=%s resources=%d deps=%v",
			svc.Name, svc.ErrorPrefix, len(svc.OwnedResources), svc.Deps)
	}

	// When clustering found no service boundaries skip stages 5–6 entirely.
	// Workspace acquire would try to clone the remote (possibly private) repo
	// for contract derivation — which is pointless and error-prone with 0
	// clusters. Write the no-boundaries plan directly and advance state.
	if len(candidates) == 0 {
		if p.planWriter == nil {
			applog.Infof("decomposition-worker: no boundaries found — planWriter not wired, result not persisted")
			return nil
		}
		plan := assemblePlan(candidates, workerdomain.DataOwnership{}, clusterResult)
		if err := p.planWriter.WritePlan(ctx, job.MigrationID, plan, "", workerdomain.DataOwnership{}); err != nil {
			applog.Warningf("decomposition-worker: plan write failed migration_id=%d: %v", job.MigrationID, err)
			return fmt.Errorf("stage 7 (plan-write, no-boundaries): %w", err)
		}
		applog.Infof("decomposition-worker: AWAITING_APPROVAL migration_id=%d no_service_boundaries=true", job.MigrationID)
		return nil
	}

	if p.acquirer == nil || p.deriver == nil {
		applog.Infof("decomposition-worker: D2 complete — stage 5 pending (acquirer/deriver not wired)")
		return nil
	}

	// Stage 5 — contract derivation.
	// Re-acquire the source workspace so the ContractDeriver can read the
	// Python source files. The workspace is held only for this stage and
	// released before the function returns.
	workspacePath, cleanupWS, err := p.acquirer.Acquire(ctx, job.RemoteURL, job.DefaultBranch)
	if err != nil {
		applog.Warningf("decomposition-worker: stage 5 workspace-acquire failed migration_id=%d url=%q branch=%q: %v",
			job.MigrationID, job.RemoteURL, job.DefaultBranch, err)
		return fmt.Errorf("stage 5 (workspace-acquire): %w", err)
	}
	defer cleanupWS()

	applog.Infof("decomposition-worker: stage 5 workspace acquired path=%s", workspacePath)

	// Build the table→service map from all clusters' model files before running
	// the deriver. This allows cross-service FK annotations to carry the target
	// service name (e.g. "usersprofile" → "user").
	tableServiceMap := buildTableServiceMap(workspacePath, clusterResult.Clusters)
	applog.Infof("decomposition-worker: table→service map entries=%d", len(tableServiceMap))

	var contracts []workerdomain.DerivedContract
	for _, c := range clusterResult.Clusters {
		contract, err := p.deriver.Derive(ctx, c, workspacePath, tableServiceMap)
		if err != nil {
			// Non-fatal: framework holes or parse failures skip the cluster.
			applog.Warningf("decomposition-worker: stage 5 deriver skipped cluster=%s: %v",
				c.BlueprintGroup, err)
			continue
		}
		applog.Infof("decomposition-worker:   CONTRACT %-20s messages=%d rpcs=%d todo=%v path=%s",
			contract.ServiceName, len(contract.Messages), len(contract.RPCs),
			contract.HasTODORoutes, contract.ProtoPath)
		for _, msg := range contract.Messages {
			applog.Infof("decomposition-worker:     message %-20s fields=%d", msg.Name, len(msg.Fields))
		}
		contracts = append(contracts, *contract)
	}

	if p.planWriter == nil {
		applog.Infof("decomposition-worker: D3 complete — stages 6–7 pending (planWriter not wired)")
		return nil
	}

	// Stage 6 — data ownership.
	// Assigns resources to services, declares shared_database=true, lists cross-
	// service FKs, and aggregates operational couplings from all candidates.
	ownership := analyzeDataOwnership(candidates, contracts)
	applog.Infof("decomposition-worker: stage 6 ownership shared_db=%v cross_fks=%d op_couplings=%d",
		ownership.SharedDatabase, len(ownership.CrossServiceFKs), len(ownership.OperationalCouplings))
	for _, fk := range ownership.CrossServiceFKs {
		applog.Infof("decomposition-worker:   FK %-20s.%-15s.%-30s → %s (service: %s)",
			fk.OwnerService, fk.OwnerMessage, fk.FieldName, fk.RefTable, fk.RefService)
	}
	for _, oc := range ownership.OperationalCouplings {
		applog.Infof("decomposition-worker:   OP %-20s → %-20s (source: %s)",
			oc.FromService, oc.ToService, oc.FromModule)
	}

	// Augment Deps with FK-derived data dependencies. Some cross-service FKs
	// are expressed as SQLAlchemy table-name strings (not Python imports), so
	// they don't appear as edges in the dependency graph. After stage 6 we have
	// the full FK list and can add the missing data deps deterministically.
	candidates = augmentDataDeps(candidates, ownership.CrossServiceFKs)
	applog.Infof("decomposition-worker: stage 6 deps augmented from FKs")
	for _, c := range candidates {
		applog.Infof("decomposition-worker:   SERVICE %-20s data_deps=%v op_couplings=%d",
			c.Name, c.Deps, len(c.OperationalCouplings))
	}

	// Stage 7 — plan assembly + state advance.
	plan := assemblePlan(candidates, ownership, clusterResult)
	if err := p.planWriter.WritePlan(ctx, job.MigrationID, plan, workspacePath, ownership); err != nil {
		return fmt.Errorf("stage 7 (plan-write): %w", err)
	}
	applog.Infof("decomposition-worker: AWAITING_APPROVAL migration_id=%d services=%d rationale=%q",
		job.MigrationID, len(plan.GetServices()), plan.GetRationale())

	// Stage 7b — persist design artifacts before workspace is cleaned up.
	// The defer cleanupWS() above runs after this function returns, but we
	// call UpsertArtifacts here (before return) to ensure artifacts are read
	// from the in-memory contracts — not from the filesystem.
	if p.artifactStore != nil {
		artifacts := buildArtifacts(plan, contracts, ownership, candidates)
		if err := p.artifactStore.UpsertArtifacts(ctx, job.MigrationID, artifacts); err != nil {
			// Non-fatal: log but do not block the AWAITING_APPROVAL transition.
			applog.Warningf("decomposition-worker: artifact persistence skipped migration_id=%d: %v",
				job.MigrationID, err)
		}
	}

	return nil
}

// characterize derives a ServiceCandidate for each cluster by extracting the
// service name from the blueprint group, identifying owned domain models, and
// tracing inter-service dependencies from the directed domain sub-graph.
func characterize(
	ctx context.Context,
	domainGraph *workerdomain.Graph,
	clusters []workerdomain.Cluster,
	allocator ports.PrefixAllocator,
) ([]workerdomain.ServiceCandidate, error) {
	// Map each module to the service name of its cluster.
	moduleToService := make(map[workerdomain.Module]string, len(domainGraph.Edges)*2)
	for _, c := range clusters {
		svcName := serviceNameFromBlueprint(c.BlueprintGroup)
		for _, m := range c.Modules {
			moduleToService[m] = svcName
		}
	}

	candidates := make([]workerdomain.ServiceCandidate, 0, len(clusters))

	for _, c := range clusters {
		svcName := serviceNameFromBlueprint(c.BlueprintGroup)

		// Owned resources: modules with a ".models" suffix in this cluster.
		var resources []workerdomain.Module
		for _, m := range c.Modules {
			if strings.HasSuffix(string(m), ".models") {
				resources = append(resources, m)
			}
		}
		sort.Slice(resources, func(i, j int) bool { return resources[i] < resources[j] })

		// Classify inter-cluster edges originating from this service:
		//   .models source → data-layer dependency (hard; goes into Deps)
		//   any other source (.views, .serializers, …) → operational coupling
		//     (view-layer import that becomes a gRPC call or async event; does NOT
		//     go into Deps — keeping it separate prevents false cycles).
		depsSet := make(map[string]bool)
		// opKey prevents duplicate operational coupling entries.
		type opKey struct{ from, to, mod string }
		opSet := make(map[opKey]struct{})
		var opCouplings []workerdomain.OperationalCoupling

		for _, e := range domainGraph.Edges {
			fromSvc := moduleToService[e.From]
			toSvc := moduleToService[e.To]
			if fromSvc != svcName || toSvc == "" || toSvc == svcName {
				continue
			}
			if strings.HasSuffix(string(e.From), ".models") {
				depsSet[toSvc] = true
			} else {
				k := opKey{fromSvc, toSvc, string(e.From)}
				if _, seen := opSet[k]; !seen {
					opSet[k] = struct{}{}
					opCouplings = append(opCouplings, workerdomain.OperationalCoupling{
						FromService: fromSvc,
						ToService:   toSvc,
						FromModule:  string(e.From),
					})
				}
			}
		}

		deps := make([]string, 0, len(depsSet))
		for d := range depsSet {
			deps = append(deps, d)
		}
		sort.Strings(deps)
		sort.Slice(opCouplings, func(i, j int) bool {
			ki := opCouplings[i].FromModule + "→" + opCouplings[i].ToService
			kj := opCouplings[j].FromModule + "→" + opCouplings[j].ToService
			return ki < kj
		})

		prefix, err := allocator.Allocate(ctx, svcName)
		if err != nil {
			return nil, fmt.Errorf("prefix allocation for %q: %w", svcName, err)
		}

		candidates = append(candidates, workerdomain.ServiceCandidate{
			Name:                 svcName,
			ErrorPrefix:          prefix,
			OwnedResources:       resources,
			Deps:                 deps,
			OperationalCouplings: opCouplings,
		})
	}

	return candidates, nil
}

// serviceNameFromBlueprint extracts a lowercase service name from the blueprint
// group. Python: last dot-segment ("conduit.articles" → "articles").
// PHP: last backslash-segment ("BookStack\Entities" → "entities").
func serviceNameFromBlueprint(blueprintGroup string) string {
	sep := "."
	if strings.Contains(blueprintGroup, `\`) {
		sep = `\`
	}
	parts := strings.Split(blueprintGroup, sep)
	return strings.ToLower(parts[len(parts)-1])
}

// tableNameRe matches Python __tablename__ assignments in SQLAlchemy model classes.
var tableNameRe = regexp.MustCompile(`__tablename__\s*=\s*['"]([^'"]+)['"]`)

// eloquentTableRe matches a PHP `protected $table = 'name';` declaration.
var eloquentTableRe = regexp.MustCompile(`\$table\s*=\s*['"]([A-Za-z_][A-Za-z0-9_]*)['"]`)

// buildTableServiceMap scans all clusters' model modules for table declarations
// and returns a map from table name to service name. This map is passed to
// ContractDeriver so FK annotations carry the target service. Python clusters
// use __tablename__ in .models files; PHP/Laravel clusters use $table (or the
// pluralised class-name convention) in Eloquent model classes.
func buildTableServiceMap(workspacePath string, clusters []workerdomain.Cluster) map[string]string {
	result := make(map[string]string)
	for _, c := range clusters {
		svcName := serviceNameFromBlueprint(c.BlueprintGroup)
		for _, m := range c.Modules {
			module := string(m)
			if strings.Contains(module, `\`) {
				if table, ok := eloquentTableForModule(workspacePath, module); ok {
					result[table] = svcName
				}
				continue
			}
			if !strings.HasSuffix(module, ".models") {
				continue
			}
			parts := strings.Split(module, ".")
			relPath := filepath.Join(parts...) + ".py"
			data, err := os.ReadFile(filepath.Join(workspacePath, relPath))
			if err != nil {
				continue
			}
			for _, match := range tableNameRe.FindAllStringSubmatch(string(data), -1) {
				result[match[1]] = svcName
			}
		}
	}
	return result
}

// laravelModelModuleRe reports whether a PHP FQN sits under a Models namespace.
var laravelModelModuleRe = regexp.MustCompile(`(^|\\)Models?\\`)

// eloquentTableForModule resolves a PHP Eloquent model FQN to its table name:
// the explicit $table when declared, otherwise the pluralised snake_case of the
// class name (Laravel's convention). Returns false for non-model modules or
// when the file cannot be located.
func eloquentTableForModule(workspacePath, fqn string) (string, bool) {
	if !laravelModelModuleRe.MatchString(fqn) {
		return "", false
	}
	psr4 := loadComposerPSR4(workspacePath)
	path, ok := resolveLaravelClassPath(workspacePath, fqn, psr4)
	if ok {
		if data, err := os.ReadFile(path); err == nil {
			if m := eloquentTableRe.FindSubmatch(data); m != nil {
				return string(m[1]), true
			}
		}
	}
	// Convention fallback: plural snake_case of the class name.
	className := fqn
	if i := strings.LastIndex(fqn, `\`); i >= 0 {
		className = fqn[i+1:]
	}
	return pluralizeSnake(className), true
}

// composerPSR4Re matches a PSR-4 prefix→dir entry in composer.json.
var composerPSR4Re = regexp.MustCompile(`"((?:[A-Za-z0-9_]+\\\\)*[A-Za-z0-9_]+\\\\)"\s*:\s*"([^"]*)"`)

// loadComposerPSR4 returns the PSR-4 prefix→directory map from composer.json.
func loadComposerPSR4(workspacePath string) map[string]string {
	out := make(map[string]string)
	data, err := os.ReadFile(filepath.Join(workspacePath, "composer.json"))
	if err != nil {
		return out
	}
	for _, m := range composerPSR4Re.FindAllStringSubmatch(string(data), -1) {
		prefix := strings.ReplaceAll(m[1], `\\`, `\`)
		out[prefix] = strings.TrimSuffix(m[2], "/")
	}
	return out
}

// resolveLaravelClassPath maps a PHP FQN to a file path via the PSR-4 map.
func resolveLaravelClassPath(workspacePath, fqn string, psr4 map[string]string) (string, bool) {
	bestPrefix, bestDir := "", ""
	for prefix, dir := range psr4 {
		if strings.HasPrefix(fqn, prefix) && len(prefix) > len(bestPrefix) {
			bestPrefix, bestDir = prefix, dir
		}
	}
	if bestPrefix == "" {
		return "", false
	}
	rest := strings.TrimPrefix(fqn, bestPrefix)
	rel := filepath.Join(strings.Split(rest, `\`)...) + ".php"
	full := filepath.Join(workspacePath, bestDir, rel)
	if _, err := os.Stat(full); err == nil {
		return full, true
	}
	return "", false
}

// pluralizeSnake converts a PascalCase class name to Laravel's default plural
// snake_case table name (Book → books, Category → categories).
func pluralizeSnake(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if i > 0 && ch >= 'A' && ch <= 'Z' {
			b.WriteByte('_')
		}
		if ch >= 'A' && ch <= 'Z' {
			b.WriteByte(ch + 32)
		} else {
			b.WriteByte(ch)
		}
	}
	snake := b.String()
	switch {
	case strings.HasSuffix(snake, "y") && len(snake) > 1 && !strings.ContainsRune("aeiou", rune(snake[len(snake)-2])):
		return snake[:len(snake)-1] + "ies"
	case strings.HasSuffix(snake, "fe"):
		return snake[:len(snake)-2] + "ves"
	case strings.HasSuffix(snake, "f"):
		return snake[:len(snake)-1] + "ves"
	case strings.HasSuffix(snake, "s"), strings.HasSuffix(snake, "x"),
		strings.HasSuffix(snake, "ch"), strings.HasSuffix(snake, "sh"):
		return snake + "es"
	default:
		return snake + "s"
	}
}

// analyzeDataOwnership builds the DataOwnership struct from characterised candidates
// and derived contracts. It always sets SharedDatabase=true (v1 monolith DB assumption),
// collects cross-service FK fields with their origin message, and aggregates
// operational couplings from all candidates.
func analyzeDataOwnership(
	candidates []workerdomain.ServiceCandidate,
	contracts []workerdomain.DerivedContract,
) workerdomain.DataOwnership {
	var crossFKs []workerdomain.CrossServiceFK

	for _, contract := range contracts {
		for _, msg := range contract.Messages {
			for _, field := range msg.Fields {
				if !field.IsCrossFK {
					continue
				}
				// Only list FKs that genuinely cross service boundaries.
				if field.RefService == contract.ServiceName {
					continue
				}
				crossFKs = append(crossFKs, workerdomain.CrossServiceFK{
					OwnerService: contract.ServiceName,
					OwnerMessage: msg.Name,
					FieldName:    field.Name,
					RefTable:     field.RefTable,
					RefService:   field.RefService,
				})
			}
		}
	}

	// Sort by (owner_service, owner_message, field_name) for deterministic output.
	// Including owner_message in the key makes Article.author_identifier and
	// Comment.author_identifier distinct rather than collapsed to the same text.
	sort.Slice(crossFKs, func(i, j int) bool {
		ki := crossFKs[i].OwnerService + "." + crossFKs[i].OwnerMessage + "." + crossFKs[i].FieldName
		kj := crossFKs[j].OwnerService + "." + crossFKs[j].OwnerMessage + "." + crossFKs[j].FieldName
		return ki < kj
	})

	// Aggregate operational couplings from all candidates for the plan-level list.
	var opCouplings []workerdomain.OperationalCoupling
	for _, c := range candidates {
		opCouplings = append(opCouplings, c.OperationalCouplings...)
	}
	sort.Slice(opCouplings, func(i, j int) bool {
		ki := opCouplings[i].FromService + "." + opCouplings[i].FromModule + "→" + opCouplings[i].ToService
		kj := opCouplings[j].FromService + "." + opCouplings[j].FromModule + "→" + opCouplings[j].ToService
		return ki < kj
	})

	return workerdomain.DataOwnership{
		SharedDatabase:       true,
		CrossServiceFKs:      crossFKs,
		OperationalCouplings: opCouplings,
	}
}

// augmentDataDeps adds FK-derived data dependencies to each candidate's Deps.
// Some cross-service FKs are expressed via SQLAlchemy table-name strings (not
// Python imports), so they don't appear as edges in the dependency graph.
// After stage 6 produces the full FK list we close that gap deterministically.
func augmentDataDeps(
	candidates []workerdomain.ServiceCandidate,
	crossFKs []workerdomain.CrossServiceFK,
) []workerdomain.ServiceCandidate {
	// Index by name for O(1) lookup.
	idx := make(map[string]int, len(candidates))
	for i, c := range candidates {
		idx[c.Name] = i
	}

	for _, fk := range crossFKs {
		if fk.RefService == "" {
			continue
		}
		i, ok := idx[fk.OwnerService]
		if !ok {
			continue
		}
		alreadyIn := false
		for _, d := range candidates[i].Deps {
			if d == fk.RefService {
				alreadyIn = true
				break
			}
		}
		if !alreadyIn {
			candidates[i].Deps = append(candidates[i].Deps, fk.RefService)
		}
	}

	for i := range candidates {
		sort.Strings(candidates[i].Deps)
	}
	return candidates
}

// noServiceBoundariesExplanation is the plain-language message written into
// RestructurePlan.boundaries_explanation when the decomposition engine finds
// zero service boundaries. It is the UI source of truth for this outcome.
const noServiceBoundariesExplanation = "No service boundaries were found. " +
	"The codebase has no identifiable domain layer — all modules were classified " +
	"as infrastructure or utilities. This is typical of script-style code without " +
	"separation between domain logic and infrastructure. " +
	"Automatic microservices decomposition is not possible; the code would need " +
	"to be restructured before a clean split can be made."

// assemblePlan builds the RestructurePlan proto message from the characterised
// service candidates and data-ownership analysis.
func assemblePlan(
	candidates []workerdomain.ServiceCandidate,
	ownership workerdomain.DataOwnership,
	cr *workerdomain.ClusteringResult,
) *workerdomain.RestructurePlan {
	var lowConfidence bool
	var protoCandidates []*migrationv1.CandidateGrouping
	var protoRecs []*migrationv1.RestructureRecommendation
	if cr != nil {
		lowConfidence = cr.LowConfidence
		for _, g := range cr.CandidateGroupings {
			protoCandidates = append(protoCandidates, &migrationv1.CandidateGrouping{
				Name:             g.Name,
				Modules:          g.Modules,
				Responsibilities: g.Responsibilities,
				Confidence:       g.Confidence,
			})
		}
		for _, r := range cr.RestructureRecs {
			protoRecs = append(protoRecs, &migrationv1.RestructureRecommendation{
				Kind:     r.Kind,
				Subject:  r.Subject,
				Action:   r.Action,
				Blocking: r.Blocking,
			})
		}
	}

	var modularity float64
	if cr != nil {
		modularity = cr.Modularity
	}

	// When clustering found no boundaries, return the signal immediately.
	// Generation is not meaningful with zero services; the plan carries the
	// structured flag so callers can block without parsing the rationale string.
	if len(candidates) == 0 {
		return &workerdomain.RestructurePlan{
			Services:                   nil,
			Rationale:                  "Decomposition produced 0 service boundaries.",
			IsLowConfidence:            lowConfidence,
			NoServiceBoundaries:        true,
			BoundariesExplanation:      noServiceBoundariesExplanation,
			CandidateGroupings:         protoCandidates,
			RestructureRecommendations: protoRecs,
			PartitionModularity:        modularity,
		}
	}

	// Index cross-service FKs by owner service for O(1) per-service lookup.
	fksByOwner := make(map[string][]workerdomain.CrossServiceFK, len(ownership.CrossServiceFKs))
	for _, fk := range ownership.CrossServiceFKs {
		fksByOwner[fk.OwnerService] = append(fksByOwner[fk.OwnerService], fk)
	}

	services := make([]*workerdomain.ProposedService, 0, len(candidates))
	for _, c := range candidates {
		resources := make([]string, len(c.OwnedResources))
		for i, r := range c.OwnedResources {
			resources[i] = string(r)
		}

		// Convert domain CrossServiceFKs to proto CrossServiceFk messages.
		domainFKs := fksByOwner[c.Name]
		protoFKs := make([]*migrationv1.CrossServiceFk, 0, len(domainFKs))
		for _, fk := range domainFKs {
			protoFKs = append(protoFKs, &migrationv1.CrossServiceFk{
				OwnerService: fk.OwnerService,
				OwnerMessage: fk.OwnerMessage,
				Field:        fk.FieldName,
				RefTable:     fk.RefTable,
				RefService:   fk.RefService,
			})
		}

		services = append(services, &workerdomain.ProposedService{
			Name:             c.Name,
			ErrorPrefix:      c.ErrorPrefix,
			OwnedResources:   resources,
			InterServiceDeps: c.Deps,
			CrossServiceFks:  protoFKs,
		})
	}

	// Convert operational couplings to proto messages for the plan.
	protoOps := make([]*migrationv1.OperationalCoupling, 0, len(ownership.OperationalCouplings))
	for _, oc := range ownership.OperationalCouplings {
		protoOps = append(protoOps, &migrationv1.OperationalCoupling{
			FromService:  oc.FromService,
			ToService:    oc.ToService,
			SourceModule: oc.FromModule,
		})
	}

	fkSummary := make([]string, 0, len(ownership.CrossServiceFKs))
	for _, fk := range ownership.CrossServiceFKs {
		ref := fk.RefTable
		if fk.RefService != "" {
			ref = fk.RefTable + " (service: " + fk.RefService + ")"
		}
		fkSummary = append(fkSummary, fk.OwnerService+"."+fk.OwnerMessage+"."+fk.FieldName+" → "+ref)
	}

	rationale := fmt.Sprintf(
		"Blueprint-biased Louvain community detection produced %d service boundaries. "+
			"Shared database declared: all services share one DB in v1 — "+
			"per-service data separation is deferred. "+
			"%d cross-service FK(s) identified as consistency debt: %s.",
		len(candidates),
		len(ownership.CrossServiceFKs),
		strings.Join(fkSummary, "; "),
	)
	if lowConfidence {
		rationale = "[LOW CONFIDENCE — human review recommended] " + rationale
	}

	return &workerdomain.RestructurePlan{
		Services:                   services,
		Rationale:                  rationale,
		OperationalCouplings:       protoOps,
		IsLowConfidence:            lowConfidence,
		CandidateGroupings:         protoCandidates,
		RestructureRecommendations: protoRecs,
		PartitionModularity:        modularity,
	}
}

// buildArtifacts assembles a ServiceArtifact for each service in the plan.
// Proto content comes from the corresponding DerivedContract; the boundary spec
// is generated from domain types so no filesystem access is required.
func buildArtifacts(
	plan *workerdomain.RestructurePlan,
	contracts []workerdomain.DerivedContract,
	ownership workerdomain.DataOwnership,
	candidates []workerdomain.ServiceCandidate,
) []workerdomain.ServiceArtifact {
	byName := make(map[string]workerdomain.DerivedContract, len(contracts))
	for _, c := range contracts {
		byName[c.ServiceName] = c
	}

	fksByOwner := make(map[string][]workerdomain.CrossServiceFK, len(ownership.CrossServiceFKs))
	for _, fk := range ownership.CrossServiceFKs {
		fksByOwner[fk.OwnerService] = append(fksByOwner[fk.OwnerService], fk)
	}

	// Index per-service operational couplings for the boundary spec.
	opByService := make(map[string][]workerdomain.OperationalCoupling, len(candidates))
	for _, c := range candidates {
		opByService[c.Name] = c.OperationalCouplings
	}

	artifacts := make([]workerdomain.ServiceArtifact, 0, len(plan.GetServices()))
	for _, svc := range plan.GetServices() {
		spec := workerdomain.BuildBoundarySpecYAML(
			svc, ownership.SharedDatabase,
			fksByOwner[svc.GetName()],
			opByService[svc.GetName()],
		)
		contract, ok := byName[svc.GetName()]
		incomplete, reason := contract.Incomplete, contract.IncompleteReason
		if !ok {
			incomplete = true
			reason = "no contract derived for this service"
		}
		artifacts = append(artifacts, workerdomain.ServiceArtifact{
			ServiceName:      svc.GetName(),
			ProtoContent:     contract.ProtoContent,
			BoundarySpec:     spec,
			Incomplete:       incomplete,
			IncompleteReason: reason,
		})
	}
	return artifacts
}

// ApplyCoherenceGuardrail applies the structural-fallback low-confidence flag and
// the cluster coherence check. It modifies cls and clusterResult in place and
// returns true when the guardrail fired and no real boundaries remain.
//
// Callers (pipeline.Run and MigrabilityAssessorAdapter.Assess) must call this
// after clustering and before building the AnalysisDigest so that the digest
// correctly reflects DomainEmpty=true / NoServiceBoundaries=true when the
// guardrail fires.
func ApplyCoherenceGuardrail(
	cls *workerdomain.Classification,
	clusterResult *workerdomain.ClusteringResult,
	domainGraph *workerdomain.Graph,
) bool {
	// Structural fallback always implies low confidence regardless of modularity.
	if cls.StructuralFallback {
		clusterResult.LowConfidence = true
	}

	if cls.StructuralFallback && clusterResult.LowConfidence && workerdomain.IsIncoherentFallback(domainGraph, clusterResult.Clusters) {
		clusterResult.Clusters = nil
		cls.Domain = nil
		return true
	}
	return false
}

// logVerdict emits the MigrabilityVerdict returned by the assessor.
func logVerdict(r AssessResult, migrationID uint64) {
	v := r.Verdict
	applog.Infof("decomposition-worker: verdict migration_id=%d verdict=%s confidence=%s cost_usd=%.6f input_tokens=%d output_tokens=%d",
		migrationID, v.Verdict, v.Confidence, r.CostUSD, r.InputTokens, r.OutputTokens)
	applog.Infof("decomposition-worker: verdict summary=%q", v.Summary)
	for i, reason := range v.Reasons {
		applog.Infof("decomposition-worker:   reason[%d]: %s", i, reason)
	}
	for i, blocker := range v.Blockers {
		applog.Infof("decomposition-worker:   blocker[%d]: %s", i, blocker)
	}
}

// logDigest emits a structured log summary of an AnalysisDigest for debugging
// and validation. It is intentionally verbose so Conduit vs. notiplan can be
// compared from log output alone.
func logDigest(d *workerdomain.AnalysisDigest, summaryID uint64) {
	applog.Infof("decomposition-worker: digest summary_id=%d framework=%q techs=%d nodes=%d edges=%d",
		summaryID, d.Framework, len(d.Technologies), len(d.Graph.Nodes), len(d.Graph.Edges))
	applog.Infof("decomposition-worker: digest clusters=%d no_boundaries=%v low_confidence=%v",
		len(d.Clusters), d.NoServiceBoundaries, d.LowConfidence)
	applog.Infof("decomposition-worker: digest modules total=%d sampled=%d blueprints=%d",
		d.TotalModules, d.SampledModules, len(d.Blueprints))
	applog.Infof("decomposition-worker: digest entry_points routes=%d blueprints=%d single_bp=%v",
		d.EntryPoints.TotalRoutes, d.EntryPoints.BlueprintCount, d.EntryPoints.SingleBlueprint)
	applog.Infof("decomposition-worker: digest classification domain=%d infra=%d domain_empty=%v",
		len(d.Classification.DomainModules), len(d.Classification.InfraModules), d.Classification.DomainEmpty)
	applog.Infof("decomposition-worker: digest shared_state_hubs=%d", len(d.SharedStateHubs))
	for _, hub := range d.SharedStateHubs {
		applog.Infof("decomposition-worker:   HUB %-40s fan_in=%d state=%v",
			hub.Module, hub.FanIn, hub.State)
	}
	for _, c := range d.Clusters {
		applog.Infof("decomposition-worker:   DIGEST_CLUSTER %-30s members=%d",
			c.BlueprintGroup, len(c.Modules))
	}
	for _, card := range d.ModuleCards {
		if card.FanIn+card.FanOut > 0 || card.IsSharedStateHub {
			applog.Infof("decomposition-worker:   CARD %-40s loc=%-4d fan_in=%-2d fan_out=%-2d funcs=%-3d state=%v hub=%v",
				card.Module, card.LOC, card.FanIn, card.FanOut, len(card.Functions), card.MutableState, card.IsSharedStateHub)
		}
	}
}
