package application

import (
	"fmt"
	"strconv"

	workerdomain "milton_prism/core/worker/decomposition/domain"
)

// godFunctionThreshold is the minimum number of functions a module must export
// before it qualifies as a god-module candidate (combined with shared state).
const godFunctionThreshold = 20

// Score computes a deterministic migrability score in [0, 100] from the five
// structural signals in the digest. Higher means more migrable.
//
// Signal weights (max penalty → 100):
//   - domain_presence  40 pts — empty domain blocks all decomposition
//   - cluster_count    25 pts — no boundaries = monolith as-is
//   - hub_severity     20 pts — shared-state hubs require decoupling first
//   - god_modules      10 pts — high-function + shared-state modules
//   - routing_layout    5 pts — single-blueprint = no per-domain routing
//
// V1 accepted debt (shared DB, synchronous gRPC) is not penalised: those
// signals do not appear in the digest.
func Score(d *workerdomain.AnalysisDigest) *workerdomain.MigrabilityScore {
	total := 100
	signals := make([]workerdomain.ScoreComponent, 0, 5)

	// Pre-compute god-modules at function scope: needed in signal 4 AND in
	// StructuralFindings / TypedBlockers below.
	var godModules []string
	for _, c := range d.ModuleCards {
		if len(c.Functions) >= godFunctionThreshold && c.IsSharedStateHub {
			godModules = append(godModules, c.Module)
		}
	}

	// ── Signal 1: domain presence (max -40) ──────────────────────────────────
	{
		var p int
		var detail, detailKey string
		var detailParams map[string]string
		if d.Classification.DomainEmpty {
			p = 40
			detail = "no domain modules detected — automatic decomposition is structurally blocked"
			detailKey = "signal.domain_presence.blocked"
			detailParams = map[string]string{}
		} else {
			n := len(d.Classification.DomainModules) + len(d.Classification.InfraModules)
			if n > 0 {
				ratio := float64(len(d.Classification.DomainModules)) / float64(n)
				ratioStr := fmt.Sprintf("%.0f", ratio*100)
				switch {
				case ratio < 0.15:
					p = 25
					detail = fmt.Sprintf("domain ratio %.0f%% — very few domain modules relative to infrastructure", ratio*100)
					detailKey = "signal.domain_presence.very_low"
					detailParams = map[string]string{"ratio": ratioStr}
				case ratio < 0.30:
					p = 10
					detail = fmt.Sprintf("domain ratio %.0f%% — low domain-to-infra ratio", ratio*100)
					detailKey = "signal.domain_presence.low"
					detailParams = map[string]string{"ratio": ratioStr}
				default:
					detail = fmt.Sprintf("domain ratio %.0f%%", ratio*100)
					detailKey = "signal.domain_presence.ok"
					detailParams = map[string]string{"ratio": ratioStr}
				}
			}
		}
		total -= p
		signals = append(signals, workerdomain.ScoreComponent{
			Signal: "domain_presence", Penalty: p, Detail: detail,
			DetailKey: detailKey, DetailParams: detailParams,
		})
	}

	// ── Signal 2: cluster count (max -25) ────────────────────────────────────
	{
		n := len(d.Clusters)
		var p int
		var detail, detailKey string
		var detailParams map[string]string
		switch {
		case n == 0:
			p = 25
			detail = "no service boundaries detected — monolith cannot be decomposed as-is"
			detailKey = "signal.cluster_count.none"
			detailParams = map[string]string{}
		case n == 1:
			p = 15
			detail = "single cluster — one-service result does not decompose the monolith"
			detailKey = "signal.cluster_count.one"
			detailParams = map[string]string{}
		case n == 2:
			p = 5
			detail = "two clusters — limited decomposition scope"
			detailKey = "signal.cluster_count.two"
			detailParams = map[string]string{}
		default:
			detail = fmt.Sprintf("%d clusters detected", n)
			detailKey = "signal.cluster_count.ok"
			detailParams = map[string]string{"count": strconv.Itoa(n)}
		}
		total -= p
		signals = append(signals, workerdomain.ScoreComponent{
			Signal: "cluster_count", Penalty: p, Detail: detail,
			DetailKey: detailKey, DetailParams: detailParams,
		})
	}

	// ── Signal 3: hub severity (max -20) ─────────────────────────────────────
	// SharedStateHubs are sorted by FanIn desc (see distiller). The worst hub's
	// fan-in is normalised as: fanIn / (fanIn + totalNodes).  This stays in
	// [0,1) regardless of edge-weight magnitudes.
	{
		var p int
		var detail, detailKey string
		var detailParams map[string]string
		var hubModules []string
		for _, h := range d.SharedStateHubs {
			hubModules = append(hubModules, h.Module)
		}
		if len(d.SharedStateHubs) > 0 {
			worst := d.SharedStateHubs[0]
			totalNodes := len(d.Graph.Nodes)
			if totalNodes == 0 {
				totalNodes = 1
			}
			hubRatio := float64(worst.FanIn) / float64(uint32(totalNodes)+worst.FanIn)
			fanInStr := strconv.Itoa(int(worst.FanIn))
			switch {
			case hubRatio >= 0.5:
				p = 20
				detail = fmt.Sprintf("%s fan-in=%d — severe shared-state hub (concentrates %.0f%% of incoming coupling)", worst.Module, worst.FanIn, hubRatio*100)
				detailKey = "signal.hub_severity.severe"
				detailParams = map[string]string{
					"module": worst.Module,
					"fan_in": fanInStr,
					"pct":    fmt.Sprintf("%.0f", hubRatio*100),
				}
			case hubRatio >= 0.3:
				p = 12
				detail = fmt.Sprintf("%s fan-in=%d — moderate shared-state hub", worst.Module, worst.FanIn)
				detailKey = "signal.hub_severity.moderate"
				detailParams = map[string]string{"module": worst.Module, "fan_in": fanInStr}
			default:
				p = 5
				detail = fmt.Sprintf("%s fan-in=%d — shared-state hub present", worst.Module, worst.FanIn)
				detailKey = "signal.hub_severity.minor"
				detailParams = map[string]string{"module": worst.Module, "fan_in": fanInStr}
			}
		} else {
			detail = "no shared-state hubs"
			detailKey = "signal.hub_severity.none"
			detailParams = map[string]string{}
		}
		total -= p
		signals = append(signals, workerdomain.ScoreComponent{
			Signal: "hub_severity", Penalty: p, Detail: detail, Modules: hubModules,
			DetailKey: detailKey, DetailParams: detailParams,
		})
	}

	// ── Signal 4: god-modules (max -10) ──────────────────────────────────────
	// A god-module has ≥godFunctionThreshold exported functions AND shared state
	// (IsSharedStateHub=true), meaning it couples business logic with global state.
	//
	// Known gap (benign): only the top-250 module cards (by coupling degree) are
	// evaluated. God-modules are by definition high-coupling hubs, so they are
	// always in the top-250; a god-module that falls below this threshold would
	// have too few importers to matter for decomposition quality.
	{
		godCount := len(godModules)
		p := godCount * 5
		if p > 10 {
			p = 10
		}
		var detail, detailKey string
		var detailParams map[string]string
		if godCount == 0 {
			detail = "no god-modules detected"
			detailKey = "signal.god_modules.none"
			detailParams = map[string]string{}
		} else {
			detail = fmt.Sprintf("%d god-module(s): %v (≥%d functions + shared state)", godCount, godModules, godFunctionThreshold)
			detailKey = "signal.god_modules.found"
			detailParams = map[string]string{
				"count":     strconv.Itoa(godCount),
				"threshold": strconv.Itoa(godFunctionThreshold),
			}
		}
		total -= p
		signals = append(signals, workerdomain.ScoreComponent{
			Signal: "god_modules", Penalty: p, Detail: detail, Modules: godModules,
			DetailKey: detailKey, DetailParams: detailParams,
		})
	}

	// ── Signal 5: routing layout (max -5) ────────────────────────────────────
	{
		var p int
		var detail, detailKey string
		var detailParams map[string]string
		switch {
		case d.EntryPoints.SingleBlueprint && d.EntryPoints.HasHTTPRoutes:
			p = 5
			detail = fmt.Sprintf("all %d routes in a single blueprint — no per-domain routing separation", d.EntryPoints.TotalRoutes)
			detailKey = "signal.routing_layout.single_blueprint"
			detailParams = map[string]string{"routes": strconv.Itoa(d.EntryPoints.TotalRoutes)}
		case d.EntryPoints.HasHTTPRoutes:
			detail = fmt.Sprintf("%d routes across %d blueprints", d.EntryPoints.TotalRoutes, d.EntryPoints.BlueprintCount)
			detailKey = "signal.routing_layout.multiple"
			detailParams = map[string]string{
				"routes":     strconv.Itoa(d.EntryPoints.TotalRoutes),
				"blueprints": strconv.Itoa(d.EntryPoints.BlueprintCount),
			}
		default:
			detail = "no HTTP routes detected"
			detailKey = "signal.routing_layout.none"
			detailParams = map[string]string{}
		}
		total -= p
		signals = append(signals, workerdomain.ScoreComponent{
			Signal: "routing_layout", Penalty: p, Detail: detail,
			DetailKey: detailKey, DetailParams: detailParams,
		})
	}

	if total < 0 {
		total = 0
	}

	// ── ScoreBand ─────────────────────────────────────────────────────────────
	var band string
	switch {
	case total >= 70:
		band = workerdomain.ScoreBandGood
	case total >= 40:
		band = workerdomain.ScoreBandMedium
	default:
		band = workerdomain.ScoreBandBad
	}

	// ── StructuralFindings ────────────────────────────────────────────────────
	var structuralFindings []workerdomain.StructuralFinding

	// Shared-state hub findings (severity mirrors frontend computeFindings thresholds).
	for _, hub := range d.SharedStateHubs {
		var sev string
		switch {
		case hub.FanIn >= 10:
			sev = "high"
		case hub.FanIn >= 5:
			sev = "medium"
		default:
			sev = "low"
		}
		structuralFindings = append(structuralFindings, workerdomain.StructuralFinding{
			Kind:     "shared_state",
			Severity: sev,
			TitleKey: "finding.shared_state.hub",
			Params: map[string]string{
				"module":      hub.Module,
				"state_count": strconv.Itoa(len(hub.State)),
				"fan_in":      strconv.Itoa(int(hub.FanIn)),
			},
			Modules: []string{hub.Module},
		})
	}

	// God-module findings (always high severity by definition).
	for _, name := range godModules {
		var fnCount int
		var fanIn uint32
		for _, c := range d.ModuleCards {
			if c.Module == name {
				fnCount = len(c.Functions)
				fanIn = c.FanIn
				break
			}
		}
		structuralFindings = append(structuralFindings, workerdomain.StructuralFinding{
			Kind:     "god_module",
			Severity: "high",
			TitleKey: "finding.god_module",
			Params: map[string]string{
				"module":   name,
				"fn_count": strconv.Itoa(fnCount),
				"fan_in":   strconv.Itoa(int(fanIn)),
			},
			Modules: []string{name},
		})
	}

	// Topology finding: no domain layer (covers both structuralFallback and
	// explicit zero-domain-module cases — both result in DomainEmpty=true).
	if d.Classification.DomainEmpty {
		structuralFindings = append(structuralFindings, workerdomain.StructuralFinding{
			Kind:     "topology",
			Severity: "medium",
			TitleKey: "finding.topology.no_domain_layer",
			Params:   map[string]string{},
			Modules:  []string{},
		})
	}

	// ── Cycle findings (Tarjan SCC) ───────────────────────────────────────────
	// The scorer is the single source of truth for cycle vocabulary: kind="cycle"
	// is the canonical string; the frontend adopts it without translation.
	domainSet := make(map[string]bool, len(d.Classification.DomainModules))
	for _, m := range d.Classification.DomainModules {
		domainSet[m] = true
	}
	cycleFindings := detectCycles(d.Graph.Edges, domainSet)
	structuralFindings = append(structuralFindings, cycleFindings...)

	// ── TypedBlockers ─────────────────────────────────────────────────────────
	var typedBlockers []workerdomain.TypedBlocker

	if d.Classification.DomainEmpty {
		typedBlockers = append(typedBlockers, workerdomain.TypedBlocker{
			BlockerKey: "blocker.no_domain_layer",
			Params:     map[string]string{},
		})
	}
	if len(d.Clusters) == 0 {
		typedBlockers = append(typedBlockers, workerdomain.TypedBlocker{
			BlockerKey: "blocker.no_service_boundaries",
			Params:     map[string]string{},
		})
	}
	// Count high-severity shared-state findings for the shared_state blocker.
	highStateCount := 0
	for _, f := range structuralFindings {
		if f.Kind == "shared_state" && f.Severity == "high" {
			highStateCount++
		}
	}
	if highStateCount > 0 {
		typedBlockers = append(typedBlockers, workerdomain.TypedBlocker{
			BlockerKey: "blocker.shared_state",
			Params:     map[string]string{"count": strconv.Itoa(highStateCount)},
		})
	}
	if len(godModules) > 0 {
		typedBlockers = append(typedBlockers, workerdomain.TypedBlocker{
			BlockerKey: "blocker.god_modules",
			Params:     map[string]string{"count": strconv.Itoa(len(godModules))},
		})
	}
	// Emit blocker.cycles only for high-severity cycle findings.
	highCycleCount := 0
	for _, f := range cycleFindings {
		if f.Severity == "high" {
			highCycleCount++
		}
	}
	if highCycleCount > 0 {
		typedBlockers = append(typedBlockers, workerdomain.TypedBlocker{
			BlockerKey: "blocker.cycles",
			Params:     map[string]string{"count": strconv.Itoa(highCycleCount)},
		})
	}

	return &workerdomain.MigrabilityScore{
		Value:              total,
		Breakdown:          signals,
		ScoreBand:          band,
		StructuralFindings: structuralFindings,
		TypedBlockers:      typedBlockers,
	}
}
