package application

import (
	analysisdomain "milton_prism/core/services/analysis/domain"
	workerdomain "milton_prism/core/worker/decomposition/domain"
	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
)

// ToWorkerGraph converts analysis dependency edges to the decomposition worker's
// Graph type, which Louvain and Distill operate on.
func ToWorkerGraph(edges []*analysisdomain.DependencyEdge) *workerdomain.Graph {
	g := &workerdomain.Graph{Edges: make([]workerdomain.Edge, 0, len(edges))}
	for _, e := range edges {
		g.Edges = append(g.Edges, workerdomain.Edge{
			From:   workerdomain.Module(e.GetFromModule()),
			To:     workerdomain.Module(e.GetToModule()),
			Weight: e.GetWeight(),
		})
	}
	return g
}

// ToWorkerClassification converts an analysis ModuleClassification to the
// decomposition worker's Classification. Application + Infra modules both map
// to cls.Infra — neither carries own domain identity in the decomposition sense.
func ToWorkerClassification(mc *analysisdomain.ModuleClassification) *workerdomain.Classification {
	cls := &workerdomain.Classification{
		StructuralFallback: mc.GetStructuralFallback(),
	}
	for _, m := range mc.GetDomainModules() {
		cls.Domain = append(cls.Domain, workerdomain.Module(m))
	}
	for _, m := range mc.GetApplicationModules() {
		cls.Infra = append(cls.Infra, workerdomain.Module(m))
	}
	for _, m := range mc.GetInfraModules() {
		cls.Infra = append(cls.Infra, workerdomain.Module(m))
	}
	for _, m := range mc.GetTestModules() {
		cls.Tests = append(cls.Tests, workerdomain.Module(m))
	}
	return cls
}

// ToWorkerSummaryCards converts analysis module cards and blueprints to the
// SummaryCards type consumed by Distill. techs may be nil; when present, the
// first framework-category technology is promoted to SummaryCards.Framework.
func ToWorkerSummaryCards(
	cards []*analysisdomain.ModuleCard,
	blueprints []*analysisdomain.BlueprintInfo,
	techs []*analysisdomain.Technology,
) *workerdomain.SummaryCards {
	sc := &workerdomain.SummaryCards{}
	for _, c := range cards {
		mc := workerdomain.SummaryModuleCard{
			Module:    c.GetModule(),
			File:      c.GetFile(),
			Functions: c.GetFunctions(),
			Classes:   c.GetClasses(),
			State:     c.GetModuleLevelState(),
			Docstring: c.GetDocstringHead(),
			LOC:       c.GetLoc(),
		}
		for _, r := range c.GetRoutes() {
			mc.Routes = append(mc.Routes, workerdomain.SummaryRoute{
				Method:  r.GetMethod(),
				Path:    r.GetPath(),
				Handler: r.GetHandler(),
			})
		}
		sc.ModuleCards = append(sc.ModuleCards, mc)
	}
	for _, b := range blueprints {
		sc.Blueprints = append(sc.Blueprints, workerdomain.SummaryBlueprint{
			Name:      b.GetName(),
			File:      b.GetFile(),
			URLPrefix: b.GetUrlPrefix(),
		})
	}
	for _, t := range techs {
		sc.Technologies = append(sc.Technologies, t.GetName())
		if t.GetCategory() == "framework" && sc.Framework == "" {
			sc.Framework = t.GetName()
		}
	}
	return sc
}

// ToProtoMigrabilityScore converts the decomposition worker's internal score
// type to the shared commonv1.MigrabilityScore proto message.
func ToProtoMigrabilityScore(score *workerdomain.MigrabilityScore) *commonv1.MigrabilityScore {
	out := &commonv1.MigrabilityScore{
		Value:     int32(score.Value),
		ScoreBand: scoreBandToProto(score.ScoreBand),
	}
	for _, c := range score.Breakdown {
		out.Signals = append(out.Signals, &commonv1.ScoreSignal{
			Signal:       c.Signal,
			Penalty:      int32(c.Penalty),
			Detail:       c.Detail,
			Modules:      c.Modules,
			DetailKey:    c.DetailKey,
			DetailParams: c.DetailParams,
		})
	}
	for _, f := range score.StructuralFindings {
		out.StructuralFindings = append(out.StructuralFindings, &commonv1.StructuralFinding{
			Kind:     f.Kind,
			Severity: f.Severity,
			TitleKey: f.TitleKey,
			Params:   f.Params,
			Modules:  f.Modules,
		})
	}
	for _, tb := range score.TypedBlockers {
		out.TypedBlockers = append(out.TypedBlockers, &commonv1.TypedBlocker{
			BlockerKey: tb.BlockerKey,
			Params:     tb.Params,
		})
	}
	return out
}

func scoreBandToProto(band string) commonv1.ScoreBand {
	switch band {
	case workerdomain.ScoreBandGood:
		return commonv1.ScoreBand_SCORE_BAND_GOOD
	case workerdomain.ScoreBandMedium:
		return commonv1.ScoreBand_SCORE_BAND_MEDIUM
	case workerdomain.ScoreBandBad:
		return commonv1.ScoreBand_SCORE_BAND_BAD
	default:
		return commonv1.ScoreBand_SCORE_BAND_UNSPECIFIED
	}
}
