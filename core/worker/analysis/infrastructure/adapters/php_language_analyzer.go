package adapters

import (
	"context"
	"sort"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.LanguageAnalyzer = (*PHPLanguageAnalyzer)(nil)

// PHPLanguageAnalyzer implements ports.LanguageAnalyzer for PHP workspaces using
// tree-sitter AST extraction (Phase 1) and PSR-4 namespace resolution (Phase 2).
//
// The dependency graph uses fully-qualified PHP class names as node identifiers
// (e.g. "BookStack\Entities\Controllers\BookController"), preserving the PHP
// namespace separator so that consumers can derive the original hierarchy.
//
// The DeterministicModuleClassifier falls through to its structural fan-in
// fallback for PHP because PHP FQNs use backslash separators (not dots), so
// the Python-tuned suffix heuristics do not apply. Framework-layer classification
// (Controllers / Services / Repositories) is deferred to Phase 3.
type PHPLanguageAnalyzer struct {
	extractor *PHPImportExtractor
}

// NewPHPLanguageAnalyzer returns a ready-to-use PHP analyzer.
func NewPHPLanguageAnalyzer() *PHPLanguageAnalyzer {
	return &PHPLanguageAnalyzer{extractor: NewPHPImportExtractor()}
}

// Language satisfies ports.LanguageAnalyzer. The value "PHP" matches go-enry's
// canonical name for PHP, which is what stage 2 populates in DetectedLanguage.Name.
func (a *PHPLanguageAnalyzer) Language() string { return "PHP" }

// FrameworkProfile returns static hints for the PHP ecosystem. Framework-specific
// layer hints (Laravel, Symfony) are populated in Phase 3.
func (a *PHPLanguageAnalyzer) FrameworkProfile() ports.FrameworkProfile {
	return ports.FrameworkProfile{Framework: "PHP"}
}

// ResolveImports parses all .php files in workspacePath, resolves PSR-4 namespaces
// from composer.json, and returns the internal class-level dependency graph.
// Each edge weight is 1 (one use declaration = one edge; PHP use statements are
// explicit and non-redundant, so no weighting heuristic is applied).
// External vendor dependencies (Illuminate\, Symfony\, …) are excluded.
func (a *PHPLanguageAnalyzer) ResolveImports(ctx context.Context, workspacePath string) ([]*analysisdomain.DependencyEdge, error) {
	files, err := a.extractor.ExtractFiles(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	resolver, err := NewPHPModuleResolver(workspacePath)
	if err != nil {
		// Missing composer.json is not an error — the repo may be a bare PHP
		// project without autoloading. Return no edges rather than failing.
		return nil, nil
	}

	raw := resolver.BuildGraphEdges(files)
	if len(raw) == 0 {
		return nil, nil
	}

	edges := make([]*analysisdomain.DependencyEdge, 0, len(raw))
	for _, r := range raw {
		edges = append(edges, &analysisdomain.DependencyEdge{
			FromModule: r.FromModule,
			ToModule:   r.ToModule,
			Weight:     1,
		})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromModule != edges[j].FromModule {
			return edges[i].FromModule < edges[j].FromModule
		}
		return edges[i].ToModule < edges[j].ToModule
	})
	return edges, nil
}

// ExtractCards returns one ModuleCard per PHP file that declares a namespace.
// Mapping to analysisdomain.ModuleCard:
//   - Module   = fully-qualified class name (namespace + "\" + class)
//   - File     = workspace-relative path
//   - Functions = declared method names (PHP has no module-level free functions in PSR-4)
//   - Classes   = [ClassName] with kind prefix ("class:", "interface:", "trait:", "enum:")
//   - ModuleLevelState = static property names (singletons / registries → state signals)
//   - Loc       = non-blank, non-comment line count
//
// PHP has no URL routing at the file level (routes live in route files, not
// controllers), so BlueprintInfo is always nil. Routes are deferred to Phase 3.
func (a *PHPLanguageAnalyzer) ExtractCards(ctx context.Context, workspacePath string) ([]*analysisdomain.ModuleCard, []*analysisdomain.BlueprintInfo, error) {
	files, err := a.extractor.ExtractFiles(ctx, workspacePath)
	if err != nil {
		return nil, nil, err
	}

	cards := make([]*analysisdomain.ModuleCard, 0, len(files))
	for _, f := range files {
		if f.NS == "" {
			continue // legacy file without namespace — not a PSR-4 module
		}

		module := f.NS
		if f.Class != "" {
			module = f.NS + `\` + f.Class
		}

		card := &analysisdomain.ModuleCard{
			Module:           module,
			File:             f.RelPath,
			Functions:        f.Methods,
			ModuleLevelState: f.StaticProps,
			Loc:              f.Loc,
		}

		if f.Class != "" {
			// Prefix the kind so consumers can distinguish class from interface/trait.
			card.Classes = []string{f.Kind + ":" + f.Class}
		}

		cards = append(cards, card)
	}

	sort.Slice(cards, func(i, j int) bool {
		return cards[i].Module < cards[j].Module
	})
	return cards, nil, nil
}
