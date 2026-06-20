package application

import (
	"context"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

// LanguageAnalyzerRegistry routes DependencyGraphBuilder.Build calls to the
// LanguageAnalyzer registered for each language name. It implements the
// DependencyGraphBuilder port so the pipeline wires it via WithGraphBuilder.
//
// Languages without a registered analyzer are holes: Build returns nil, nil
// and the pipeline logs "deep analysis not available for <lang>".
type LanguageAnalyzerRegistry struct {
	analyzers map[string]ports.LanguageAnalyzer
}

// NewLanguageAnalyzerRegistry creates an empty registry.
func NewLanguageAnalyzerRegistry() *LanguageAnalyzerRegistry {
	return &LanguageAnalyzerRegistry{analyzers: make(map[string]ports.LanguageAnalyzer)}
}

// Register adds a LanguageAnalyzer to the registry, keyed by a.Language().
// Registering a second analyzer for the same language replaces the first.
func (r *LanguageAnalyzerRegistry) Register(a ports.LanguageAnalyzer) {
	r.analyzers[a.Language()] = a
}

// Build implements ports.DependencyGraphBuilder. It delegates to the registered
// LanguageAnalyzer for lang. When no analyzer is registered for lang, it returns
// nil edges and no error — the pipeline treats nil edges as the hole condition.
func (r *LanguageAnalyzerRegistry) Build(ctx context.Context, workspacePath, lang string) ([]*analysisdomain.DependencyEdge, error) {
	a, ok := r.analyzers[lang]
	if !ok {
		return nil, nil
	}
	return a.ResolveImports(ctx, workspacePath)
}

// ExtractCards implements ports.ModuleCardProvider. It delegates to the
// registered LanguageAnalyzer for lang. Returns nil, nil, nil when no analyzer
// is registered — the pipeline treats nil cards as the hole condition.
func (r *LanguageAnalyzerRegistry) ExtractCards(ctx context.Context, workspacePath, lang string) ([]*analysisdomain.ModuleCard, []*analysisdomain.BlueprintInfo, error) {
	a, ok := r.analyzers[lang]
	if !ok {
		return nil, nil, nil
	}
	return a.ExtractCards(ctx, workspacePath)
}
