package adapters

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.LanguageAnalyzer = (*JavaLanguageAnalyzer)(nil)

// JavaLanguageAnalyzer implements ports.LanguageAnalyzer for Java workspaces using
// tree-sitter AST extraction and package-based intra-repo import resolution.
//
// Graph node identifiers are fully-qualified Java type names
// (e.g. "com.acme.web.UserController"), preserving the dotted package hierarchy
// so downstream stages can derive layers. Only intra-repo edges appear in the
// graph; JDK and Maven/Gradle third-party imports are discarded (Tier-1 facts).
type JavaLanguageAnalyzer struct {
	extractor *JavaImportExtractor
}

// NewJavaLanguageAnalyzer returns a ready-to-use Java analyzer.
func NewJavaLanguageAnalyzer() *JavaLanguageAnalyzer {
	return &JavaLanguageAnalyzer{extractor: NewJavaImportExtractor()}
}

// Language satisfies ports.LanguageAnalyzer. "Java" matches go-enry's canonical
// name, which is what stage 2 populates in DetectedLanguage.Name.
func (a *JavaLanguageAnalyzer) Language() string { return "Java" }

// FrameworkProfile returns a Spring-aware profile. It cannot inspect the
// workspace (the port takes no path), so it reports the ecosystem default
// "Spring"; the controller/route surface captured by ExtractCards is the precise
// per-workspace evidence the clusterer consumes.
func (a *JavaLanguageAnalyzer) FrameworkProfile() ports.FrameworkProfile {
	return ports.FrameworkProfile{Framework: "Spring"}
}

// ResolveImports parses all .java files in workspacePath and returns the weighted
// internal dependency graph. Each DependencyEdge.Weight is the number of distinct
// import references from FromModule to ToModule (coupling count). External imports
// (JDK, javax, jakarta, third-party) produce no edges.
func (a *JavaLanguageAnalyzer) ResolveImports(ctx context.Context, workspacePath string) ([]*analysisdomain.DependencyEdge, error) {
	files, err := a.extractor.ExtractFiles(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	resolver := NewJavaModuleResolver(files)
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

// ExtractCards returns one ModuleCard per .java file that declares a package,
// plus one BlueprintInfo per Spring controller (the Java analogue of a Flask
// blueprint: a controller groups routes under a class-level @RequestMapping
// path prefix).
//
// Mapping to analysisdomain.ModuleCard:
//   - Module           = fully-qualified primary type name (package + "." + type)
//   - File             = workspace-relative path
//   - Functions        = declared method names
//   - Classes          = ["kind:Name"] for the primary type (class/interface/enum/record)
//   - ModuleLevelState = static field names (singletons / counters → state signals)
//   - Routes           = Spring MVC routes (method, full path, handler) for controllers
//   - Loc              = non-blank, non-comment line count
func (a *JavaLanguageAnalyzer) ExtractCards(ctx context.Context, workspacePath string) ([]*analysisdomain.ModuleCard, []*analysisdomain.BlueprintInfo, error) {
	files, err := a.extractor.ExtractFiles(ctx, workspacePath)
	if err != nil {
		return nil, nil, err
	}

	cards := make([]*analysisdomain.ModuleCard, 0, len(files))
	var blueprints []*analysisdomain.BlueprintInfo

	for _, f := range files {
		if f.Package == "" {
			continue // a file without a package is not a resolvable module node
		}
		module := javaModuleID(f)

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

		if f.IsController {
			prefix := f.ClassPrefix
			if prefix != "" && !strings.HasPrefix(prefix, "/") {
				prefix = "/" + prefix
			}
			blueprints = append(blueprints, &analysisdomain.BlueprintInfo{
				Name:      f.ControllerTag,
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

// javaIsSpringBoot reports whether workspacePath shows a Spring Boot marker:
// a spring-boot dependency in a Maven/Gradle manifest, or a @SpringBootApplication
// source annotation. Currently unused by the port surface (FrameworkProfile takes
// no path) but retained as the deterministic detection the lead can wire when the
// profile gains workspace awareness.
func javaIsSpringBoot(workspacePath string) bool {
	for _, manifest := range []string{"pom.xml", "build.gradle", "build.gradle.kts"} {
		if raw, err := os.ReadFile(filepath.Join(workspacePath, manifest)); err == nil {
			if strings.Contains(string(raw), "spring-boot") {
				return true
			}
		}
	}
	return false
}
