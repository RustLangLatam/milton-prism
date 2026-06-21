package application

import (
	"sort"
	"strings"

	workerdomain "milton_prism/core/worker/decomposition/domain"
)

// defaultTopK is the maximum number of module cards included in full detail.
// Modules beyond this threshold are collapsed into AggregateCard.
const defaultTopK = 250

// sharedStateHubFanIn is the weighted fan-in floor at which an importable module
// counts as a shared-state hub. Two or more importers means extracting the module
// into its own service forces solving the shared-state problem first.
const sharedStateHubFanIn = 2

// isSharedStateHub reports whether a module card is a shared-state hub at the
// given weighted fan-in.
//
// Two regimes:
//   - PSR-4 / Python (the default): a hub MUST expose extracted module-level
//     mutable state (c.State) AND meet the fan-in floor. This is unchanged.
//   - Convention-routed (CodeIgniter 3): the analyzer cannot extract module-level
//     mutable state — CI3 application classes hold no module-scoped mutable vars,
//     only constants, methods, and per-request $this-> instance state. There the
//     honest, extractable coupling signal is structural: a base class everything
//     `extends` (MY_Controller / MY_Model) or a model everything loads concentrates
//     incoming coupling, so a high fan-in alone qualifies it as a hub. See
//     isConventionRoutedCard for how the regime is recognised.
func isSharedStateHub(c workerdomain.SummaryModuleCard, fanIn uint32) bool {
	if fanIn < sharedStateHubFanIn {
		return false
	}
	if len(c.State) > 0 {
		return true
	}
	return isConventionRoutedCard(c)
}

// isConventionRoutedCard reports whether a card was produced by the CodeIgniter 3
// convention resolver. CI3 cards are the only ones whose module identity is the
// workspace-relative .php file path itself (Module == File): the PHP PSR-4 path
// uses backslash FQNs (NS\Class) and Python uses dotted names, both distinct from
// the file path. This intrinsic, deterministic marker (set by extractCI3Cards,
// which assigns Module = File = relPath) survives proto round-trips, so it is the
// regime signal without threading a new flag through the ports, the proto, and
// both card converters.
func isConventionRoutedCard(c workerdomain.SummaryModuleCard) bool {
	return c.File != "" && c.Module == c.File && strings.HasSuffix(strings.ToLower(c.File), ".php")
}

// Distill computes an AnalysisDigest from the outputs of pipeline stages 1–3
// and the module-level data loaded from the analysis summary. It is a pure,
// deterministic function with no I/O — re-running with the same inputs gives
// the same result.
//
// data may be nil (e.g. for non-Python repos without module cards); the digest
// is still valid but ModuleCards, Blueprints, and EntryPoints will be empty.
func Distill(
	graph *workerdomain.Graph,
	cls *workerdomain.Classification,
	clusterResult *workerdomain.ClusteringResult,
	data *workerdomain.SummaryCards,
	topK int,
) *workerdomain.AnalysisDigest {
	if topK <= 0 {
		topK = defaultTopK
	}

	// --- Fan-in / fan-out from graph edges ---
	nodeSet := make(map[string]struct{})
	fanIn := make(map[string]uint32)
	fanOut := make(map[string]uint32)
	digestEdges := make([]workerdomain.DigestEdge, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		from, to := string(e.From), string(e.To)
		nodeSet[from] = struct{}{}
		nodeSet[to] = struct{}{}
		fanOut[from] += e.Weight
		fanIn[to] += e.Weight
		digestEdges = append(digestEdges, workerdomain.DigestEdge{
			From: from, To: to, Weight: e.Weight,
		})
	}

	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	sort.Slice(digestEdges, func(i, j int) bool {
		if digestEdges[i].From != digestEdges[j].From {
			return digestEdges[i].From < digestEdges[j].From
		}
		return digestEdges[i].To < digestEdges[j].To
	})

	// --- Clusters ---
	var digestClusters []workerdomain.DigestCluster
	noServiceBoundaries := true
	var lowConf bool
	if clusterResult != nil {
		noServiceBoundaries = len(clusterResult.Clusters) == 0
		lowConf = clusterResult.LowConfidence
		for _, c := range clusterResult.Clusters {
			modules := make([]string, len(c.Modules))
			for i, m := range c.Modules {
				modules[i] = string(m)
			}
			sort.Strings(modules)
			digestClusters = append(digestClusters, workerdomain.DigestCluster{
				BlueprintGroup: c.BlueprintGroup,
				Modules:        modules,
			})
		}
	}

	// --- Module cards: copy, sort by weighted degree, apply top-K cap ---
	var allCards []workerdomain.SummaryModuleCard
	if data != nil {
		allCards = make([]workerdomain.SummaryModuleCard, len(data.ModuleCards))
		copy(allCards, data.ModuleCards)
	}

	totalModules := len(allCards)

	// Sort by weighted degree (fan-in + fan-out) descending; tie-break by module name.
	sort.Slice(allCards, func(i, j int) bool {
		di := fanIn[allCards[i].Module] + fanOut[allCards[i].Module]
		dj := fanIn[allCards[j].Module] + fanOut[allCards[j].Module]
		if di != dj {
			return di > dj
		}
		return allCards[i].Module < allCards[j].Module
	})

	sampled := allCards
	var overflow []workerdomain.SummaryModuleCard
	if len(allCards) > topK {
		sampled = allCards[:topK]
		overflow = allCards[topK:]
	}

	// Build full DigestModuleCards for the sampled set.
	digestCards := make([]workerdomain.DigestModuleCard, 0, len(sampled))
	for _, c := range sampled {
		fi := fanIn[c.Module]
		fo := fanOut[c.Module]
		isHub := isSharedStateHub(c, fi)

		routes := make([]workerdomain.DigestRoute, 0, len(c.Routes))
		for _, r := range c.Routes {
			routes = append(routes, workerdomain.DigestRoute{
				Method: r.Method, Path: r.Path, Handler: r.Handler,
			})
		}
		digestCards = append(digestCards, workerdomain.DigestModuleCard{
			Module:           c.Module,
			File:             c.File,
			Functions:        c.Functions,
			Classes:          c.Classes,
			MutableState:     c.State,
			Routes:           routes,
			DocstringHead:    c.Docstring,
			LOC:              c.LOC,
			FanIn:            fi,
			FanOut:           fo,
			IsSharedStateHub: isHub,
		})
	}

	// Aggregate card for overflow modules.
	var aggCard *workerdomain.DigestAggregateCard
	if len(overflow) > 0 {
		agg := &workerdomain.DigestAggregateCard{}
		for _, c := range overflow {
			agg.ModuleCount++
			agg.TotalLOC += c.LOC
			agg.TotalFunctions += len(c.Functions)
			agg.TotalClasses += len(c.Classes)
			agg.MutableStateCount += len(c.State)
			agg.TotalRoutes += len(c.Routes)
		}
		aggCard = agg
	}

	// --- Shared-state hubs: mutable state + fan-in ≥ 2 ---
	var hubs []workerdomain.DigestSharedStateHub
	for _, c := range digestCards {
		if c.IsSharedStateHub {
			hubs = append(hubs, workerdomain.DigestSharedStateHub{
				Module: c.Module,
				State:  c.MutableState,
				FanIn:  c.FanIn,
			})
		}
	}
	// Also check overflow modules (they might be hubs even if not in sampled set).
	for _, c := range overflow {
		if isSharedStateHub(c, fanIn[c.Module]) {
			hubs = append(hubs, workerdomain.DigestSharedStateHub{
				Module: c.Module,
				State:  c.State,
				FanIn:  fanIn[c.Module],
			})
		}
	}
	sort.Slice(hubs, func(i, j int) bool {
		if hubs[i].FanIn != hubs[j].FanIn {
			return hubs[i].FanIn > hubs[j].FanIn
		}
		return hubs[i].Module < hubs[j].Module
	})

	// --- Entry-point signals ---
	// Count distinct routes by (method, path, handler). Two cards that are
	// byte-for-byte copies of the same file (a duplicated module) expose the
	// same handler at the same path+method, so this collapses the duplication
	// instead of double-counting it. Genuinely distinct routes — same path but a
	// different handler, e.g. two blueprints — are preserved.
	routeKeys := make(map[string]struct{})
	for _, c := range digestCards {
		for _, r := range c.Routes {
			routeKeys[r.Method+"\x00"+r.Path+"\x00"+r.Handler] = struct{}{}
		}
	}
	for _, c := range overflow {
		for _, r := range c.Routes {
			routeKeys[r.Method+"\x00"+r.Path+"\x00"+r.Handler] = struct{}{}
		}
	}
	totalRoutes := len(routeKeys)

	var blueprints []workerdomain.DigestBlueprint
	if data != nil {
		for _, bp := range data.Blueprints {
			blueprints = append(blueprints, workerdomain.DigestBlueprint{
				Name: bp.Name, File: bp.File, URLPrefix: bp.URLPrefix,
			})
		}
	}

	entryPoints := workerdomain.DigestEntryPoints{
		HasHTTPRoutes:   totalRoutes > 0,
		TotalRoutes:     totalRoutes,
		BlueprintCount:  len(blueprints),
		SingleBlueprint: len(blueprints) == 1,
	}

	// --- Domain-vs-infra classification ---
	var domainMods, infraMods, testMods []string
	if cls != nil {
		for _, m := range cls.Domain {
			domainMods = append(domainMods, string(m))
		}
		sort.Strings(domainMods)
		for _, m := range cls.Infra {
			infraMods = append(infraMods, string(m))
		}
		sort.Strings(infraMods)
		for _, m := range cls.Tests {
			testMods = append(testMods, string(m))
		}
		sort.Strings(testMods)
	}
	classification := workerdomain.DigestClassification{
		DomainModules: domainMods,
		InfraModules:  infraMods,
		TestModules:   testMods,
		DomainEmpty:   len(domainMods) == 0,
	}

	// --- Technologies ---
	var techs []string
	framework := ""
	if data != nil {
		techs = data.Technologies
		framework = data.Framework
	}

	return &workerdomain.AnalysisDigest{
		Technologies:        techs,
		Framework:           framework,
		Graph:               workerdomain.DigestGraph{Nodes: nodes, Edges: digestEdges},
		Clusters:            digestClusters,
		NoServiceBoundaries: noServiceBoundaries,
		LowConfidence:       lowConf,
		ModuleCards:         digestCards,
		AggregateCard:       aggCard,
		TotalModules:        totalModules,
		SampledModules:      len(sampled),
		Blueprints:          blueprints,
		EntryPoints:         entryPoints,
		Classification:      classification,
		SharedStateHubs:     hubs,
	}
}
