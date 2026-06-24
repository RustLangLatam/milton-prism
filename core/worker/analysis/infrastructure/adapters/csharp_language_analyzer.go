package adapters

import (
	"context"
	"sort"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.LanguageAnalyzer = (*CSharpLanguageAnalyzer)(nil)

// CSharpLanguageAnalyzer implements ports.LanguageAnalyzer for C# workspaces using
// tree-sitter AST extraction and namespace-based intra-repo import resolution.
//
// Graph node identifiers are fully-qualified C# type names
// (e.g. "Acme.Web.UserController"), preserving the dotted namespace hierarchy.
// Only intra-repo edges appear in the graph; BCL (System.*) and NuGet third-party
// usings are discarded (Tier-1 facts).
type CSharpLanguageAnalyzer struct {
	extractor *CSharpImportExtractor
}

// NewCSharpLanguageAnalyzer returns a ready-to-use C# analyzer.
func NewCSharpLanguageAnalyzer() *CSharpLanguageAnalyzer {
	return &CSharpLanguageAnalyzer{extractor: NewCSharpImportExtractor()}
}

// Language satisfies ports.LanguageAnalyzer. "C#" matches go-enry's canonical
// name, which is what stage 2 populates in DetectedLanguage.Name.
func (a *CSharpLanguageAnalyzer) Language() string { return "C#" }

// FrameworkProfile returns an ASP.NET Core-aware profile. The port takes no
// workspace path, so it reports the ecosystem default "ASP.NET Core"; the
// controller/route surface from ExtractCards is the precise per-workspace evidence.
func (a *CSharpLanguageAnalyzer) FrameworkProfile() ports.FrameworkProfile {
	return ports.FrameworkProfile{Framework: "ASP.NET Core"}
}

// ResolveImports parses all .cs files in workspacePath and returns the weighted
// internal dependency graph. Each DependencyEdge.Weight is the coupling count from
// FromModule to ToModule. External usings (System.*, NuGet) produce no edges.
func (a *CSharpLanguageAnalyzer) ResolveImports(ctx context.Context, workspacePath string) ([]*analysisdomain.DependencyEdge, error) {
	files, err := a.extractor.ExtractFiles(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	resolver := NewCSharpModuleResolver(files)
	weights := resolver.BuildGraphEdges(files)
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
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromModule != edges[j].FromModule {
			return edges[i].FromModule < edges[j].FromModule
		}
		return edges[i].ToModule < edges[j].ToModule
	})
	return edges, nil
}

// ExtractCards returns one ModuleCard per .cs file that declares a namespace,
// plus one BlueprintInfo per ASP.NET controller (the C# analogue of a Flask
// blueprint: a controller groups routes under a class-level [Route] template).
// Minimal-API endpoints (app.MapGet/MapPost) are captured as routes on their
// declaring file's card; that file gets a blueprint too when it carries routes.
//
// Mapping to analysisdomain.ModuleCard:
//   - Module           = fully-qualified primary type (namespace + "." + type)
//   - File             = workspace-relative path
//   - Functions        = declared method names
//   - Classes          = ["kind:Name"] (class/interface/record/struct)
//   - ModuleLevelState = static field names
//   - Routes           = ASP.NET routes (method, full path, handler)
//   - Loc              = non-blank, non-comment line count
func (a *CSharpLanguageAnalyzer) ExtractCards(ctx context.Context, workspacePath string) ([]*analysisdomain.ModuleCard, []*analysisdomain.BlueprintInfo, error) {
	files, err := a.extractor.ExtractFiles(ctx, workspacePath)
	if err != nil {
		return nil, nil, err
	}

	cards := make([]*analysisdomain.ModuleCard, 0, len(files))
	var blueprints []*analysisdomain.BlueprintInfo

	for _, f := range files {
		module := csharpModuleID(f)
		if module == "" {
			// A file with no namespace and no type (e.g. a pure top-level-statement
			// Program.cs) still surfaces minimal-API routes; identify it by file path.
			if len(f.Routes) == 0 {
				continue
			}
			module = f.RelPath
		}

		card := &analysisdomain.ModuleCard{
			Module:           module,
			File:             f.RelPath,
			Functions:        f.Methods,
			ModuleLevelState: f.StaticState,
			Loc:              f.Loc,
		}
		if f.PrimaryType != "" {
			card.Classes = []string{f.PrimaryKind + ":" + f.PrimaryType}
		}
		for _, r := range f.Routes {
			card.Routes = append(card.Routes, &analysisdomain.RouteInfo{
				Method:  r.Method,
				Path:    r.Path,
				Handler: r.Handler,
			})
		}
		cards = append(cards, card)

		// A blueprint is emitted for an attribute-routed controller, and for any
		// file carrying minimal-API routes (the route-group identity).
		if f.IsController || (f.PrimaryType == "" && len(f.Routes) > 0) {
			name := f.ControllerTag
			if name == "" {
				name = f.PrimaryType
			}
			if name == "" {
				name = f.RelPath
			}
			prefix := f.ClassPrefix
			if prefix != "" && !strings.HasPrefix(prefix, "/") {
				prefix = "/" + prefix
			}
			blueprints = append(blueprints, &analysisdomain.BlueprintInfo{
				Name:      name,
				File:      f.RelPath,
				UrlPrefix: prefix,
			})
		}
	}

	sort.Slice(cards, func(i, j int) bool {
		return cards[i].Module < cards[j].Module
	})
	sort.Slice(blueprints, func(i, j int) bool {
		if blueprints[i].Name != blueprints[j].Name {
			return blueprints[i].Name < blueprints[j].Name
		}
		return blueprints[i].File < blueprints[j].File
	})
	return cards, blueprints, nil
}
