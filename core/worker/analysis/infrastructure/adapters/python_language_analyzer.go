package adapters

import (
	"context"
	"sort"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.LanguageAnalyzer = (*PythonLanguageAnalyzer)(nil)

// PythonLanguageAnalyzer implements ports.LanguageAnalyzer for Python workspaces.
// It composes PythonImportExtractor (Tarea 1) and PythonModuleResolver (Tarea 2)
// to produce a weighted dependency graph of intra-repo module edges.
type PythonLanguageAnalyzer struct {
	extractor *PythonImportExtractor
}

// NewPythonLanguageAnalyzer returns a ready-to-use Python analyzer.
func NewPythonLanguageAnalyzer() *PythonLanguageAnalyzer {
	return &PythonLanguageAnalyzer{extractor: NewPythonImportExtractor()}
}

// Language satisfies ports.LanguageAnalyzer. The value matches go-enry's
// canonical name for Python, which is what stage 2 populates in DetectedLanguage.Name.
func (a *PythonLanguageAnalyzer) Language() string { return "Python" }

// ResolveImports parses all .py files in workspacePath and returns the weighted
// internal dependency graph. Each DependencyEdge.Weight is the number of distinct
// import references from FromModule to ToModule (coupling count).
// External imports (stdlib, third-party) produce no edges.
func (a *PythonLanguageAnalyzer) ResolveImports(ctx context.Context, workspacePath string) ([]*analysisdomain.DependencyEdge, error) {
	rawImports, _, err := a.extractor.ExtractImports(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	if len(rawImports) == 0 {
		return nil, nil
	}

	resolver, err := NewPythonModuleResolver(workspacePath)
	if err != nil {
		return nil, err
	}

	weights := resolver.BuildGraphEdges(rawImports)
	if len(weights) == 0 {
		return nil, nil
	}

	edges := make([]*analysisdomain.DependencyEdge, 0, len(weights))
	for k, w := range weights {
		edges = append(edges, &analysisdomain.DependencyEdge{
			FromModule: k[0],
			ToModule:   k[1],
			Weight:     w,
		})
	}
	// Deterministic order so callers can rely on stable output.
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromModule != edges[j].FromModule {
			return edges[i].FromModule < edges[j].FromModule
		}
		return edges[i].ToModule < edges[j].ToModule
	})
	return edges, nil
}

// FrameworkProfile returns static Flask framework hints for use by the semantic
// clusterer (stage 7). Stage 6 does not consume this value.
func (a *PythonLanguageAnalyzer) FrameworkProfile() ports.FrameworkProfile {
	return ports.FrameworkProfile{Framework: "Flask"}
}

// ExtractCards performs two AST passes over the workspace: one for blueprint
// metadata (reusing ExtractImports) and one for per-module structural cards
// (ExtractModuleCards). Results are converted to proto-generated domain types.
func (a *PythonLanguageAnalyzer) ExtractCards(ctx context.Context, workspacePath string) ([]*analysisdomain.ModuleCard, []*analysisdomain.BlueprintInfo, error) {
	_, rawBPs, err := a.extractor.ExtractImports(ctx, workspacePath)
	if err != nil {
		return nil, nil, err
	}

	rawCards, err := a.extractor.ExtractModuleCards(ctx, workspacePath)
	if err != nil {
		return nil, nil, err
	}

	blueprints := make([]*analysisdomain.BlueprintInfo, 0, len(rawBPs))
	for _, bp := range rawBPs {
		blueprints = append(blueprints, &analysisdomain.BlueprintInfo{
			Name:      bp.Name,
			File:      bp.File,
			UrlPrefix: bp.URLPrefix,
		})
	}

	cards := make([]*analysisdomain.ModuleCard, 0, len(rawCards))
	for _, rc := range rawCards {
		card := &analysisdomain.ModuleCard{
			Module:           rc.Module,
			File:             rc.File,
			Functions:        rc.Functions,
			Classes:          rc.Classes,
			ModuleLevelState: rc.State,
			DocstringHead:    rc.Docstring,
			Loc:              rc.Loc,
		}
		for _, r := range rc.Routes {
			card.Routes = append(card.Routes, &analysisdomain.RouteInfo{
				Method:  r.Method,
				Path:    r.Path,
				Handler: r.Handler,
			})
		}
		cards = append(cards, card)
	}

	return cards, blueprints, nil
}
