package adapters

import (
	"context"
	"sort"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.LanguageAnalyzer = (*GoLanguageAnalyzer)(nil)

// GoLanguageAnalyzer implements ports.LanguageAnalyzer for Go workspaces using
// tree-sitter AST extraction and module-path-based intra-repo import resolution.
//
// Graph node identifiers are package import paths
// (e.g. "example.com/app/internal/repo"), the canonical Go package identity.
// Every .go file in a directory shares its directory's import path, so the
// graph node is the directory's package, not the file. Only intra-repo edges
// appear in the graph; standard-library and third-party imports are discarded
// (they are Tier-1 manifest facts from go.mod).
type GoLanguageAnalyzer struct {
	extractor *GoImportExtractor
}

// NewGoLanguageAnalyzer returns a ready-to-use Go analyzer.
func NewGoLanguageAnalyzer() *GoLanguageAnalyzer {
	return &GoLanguageAnalyzer{extractor: NewGoImportExtractor()}
}

// Language satisfies ports.LanguageAnalyzer. "Go" matches go-enry's canonical
// name, which is what stage 2 populates in DetectedLanguage.Name.
func (a *GoLanguageAnalyzer) Language() string { return "Go" }

// FrameworkProfile returns a generic Go profile. The port takes no workspace
// path, so it cannot scan the resolved imports; the precise per-workspace HTTP
// framework is captured by detectGoFramework during ExtractCards and surfaced
// via the route/blueprint surface the clusterer consumes. The ecosystem
// default reported here is "Go".
func (a *GoLanguageAnalyzer) FrameworkProfile() ports.FrameworkProfile {
	return ports.FrameworkProfile{Framework: "Go"}
}

// ResolveImports parses all .go files in workspacePath and returns the weighted
// internal dependency graph. Each DependencyEdge.Weight is the number of import
// references from FromModule to ToModule (coupling count) at the package-import-
// path level. External imports (stdlib, third-party) produce no edges.
func (a *GoLanguageAnalyzer) ResolveImports(ctx context.Context, workspacePath string) ([]*analysisdomain.DependencyEdge, error) {
	files, err := a.extractor.ExtractFiles(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	resolver := NewGoModuleResolver(workspacePath)
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

// ExtractCards returns one ModuleCard per .go file (parity with Python), plus
// one BlueprintInfo per file that registers HTTP routes (the Go analogue of a
// Flask blueprint / Spring controller: a file grouping route handlers).
//
// Mapping to analysisdomain.ModuleCard:
//   - Module           = package import path of the file's directory
//   - File             = workspace-relative path
//   - Functions        = func + method names ("RecvType.Method" for methods)
//   - Classes          = "struct:Name" | "interface:Name" | "type:Name"
//   - ModuleLevelState = file-scope mutable var names (IsSharedStateHub signal)
//   - DocstringHead    = leading comment block before the package clause
//   - Routes           = router method registrations (method, path, handler)
//   - Loc              = non-blank, non-comment line count
func (a *GoLanguageAnalyzer) ExtractCards(ctx context.Context, workspacePath string) ([]*analysisdomain.ModuleCard, []*analysisdomain.BlueprintInfo, error) {
	files, err := a.extractor.ExtractFiles(ctx, workspacePath)
	if err != nil {
		return nil, nil, err
	}

	resolver := NewGoModuleResolver(workspacePath)

	cards := make([]*analysisdomain.ModuleCard, 0, len(files))
	var blueprints []*analysisdomain.BlueprintInfo

	for _, f := range files {
		module := resolver.dirImportPath(f.Dir)
		if module == "" {
			// No go.mod: fall back to the directory path so cards still emit.
			module = f.Dir
		}

		card := &analysisdomain.ModuleCard{
			Module:           module,
			File:             f.RelPath,
			Functions:        f.Functions,
			Classes:          f.Classes,
			ModuleLevelState: f.ModuleState,
			DocstringHead:    f.Docstring,
			Loc:              f.Loc,
		}
		for _, r := range f.Routes {
			card.Routes = append(card.Routes, &analysisdomain.RouteInfo{
				Method:  r.Method,
				Path:    r.Path,
				Handler: r.Handler,
			})
		}
		cards = append(cards, card)

		if len(f.Routes) > 0 {
			blueprints = append(blueprints, &analysisdomain.BlueprintInfo{
				Name: module,
				File: f.RelPath,
			})
		}
	}

	sort.Slice(cards, func(i, j int) bool {
		if cards[i].Module != cards[j].Module {
			return cards[i].Module < cards[j].Module
		}
		return cards[i].File < cards[j].File
	})
	sort.Slice(blueprints, func(i, j int) bool {
		if blueprints[i].Name != blueprints[j].Name {
			return blueprints[i].Name < blueprints[j].Name
		}
		return blueprints[i].File < blueprints[j].File
	})
	return cards, blueprints, nil
}

// detectGoFramework returns the primary HTTP framework name by scanning the set
// of (external) import paths against a fixed table. A concrete framework beats
// bare net/http. Returns "" when no HTTP framework import is present.
//
// Currently unused by the port surface (FrameworkProfile takes no workspace
// path); retained as the deterministic detection the lead can wire when the
// profile gains workspace awareness, mirroring javaIsSpringBoot.
func detectGoFramework(importPaths map[string]bool) string {
	switch {
	case importPaths["github.com/gin-gonic/gin"]:
		return "Gin"
	case importPaths["github.com/labstack/echo"] || importPaths["github.com/labstack/echo/v4"]:
		return "Echo"
	case importPaths["github.com/go-chi/chi"] || importPaths["github.com/go-chi/chi/v5"]:
		return "Chi"
	case importPaths["github.com/gofiber/fiber"] || importPaths["github.com/gofiber/fiber/v2"]:
		return "Fiber"
	case importPaths["google.golang.org/grpc"] && importPaths["github.com/grpc-ecosystem/grpc-gateway"]:
		return "grpc-gateway"
	case importPaths["net/http"]:
		return "net/http"
	}
	return ""
}
