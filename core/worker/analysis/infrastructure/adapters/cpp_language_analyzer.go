package adapters

import (
	"context"
	"sort"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.LanguageAnalyzer = (*CppLanguageAnalyzer)(nil)

// CppLanguageAnalyzer implements ports.LanguageAnalyzer for C/C++ workspaces
// using tree-sitter AST extraction and quote-form #include resolution.
//
// C++ has NO canonical build system, NO 1:1 file↔namespace mapping, and real
// include resolution depends on -I flags that are not present in the source.
// Expectation is set accordingly: this analyzer ships a best-effort intra-repo
// coupling GRAPH plus structural CARDS — Tier-2 graph+cards — with NO manifest
// ecosystem and NO route/framework surface in v1.
//
// Node identity is the workspace-relative FILE path (C++ has no module name;
// this is the only honest deterministic identity, coarser than Go/Python by
// design). FromModule/ToModule on every edge are file paths.
//
// FIDELITY LIMITS (deterministic-static-only; documented, not bugs):
//
//	(a) Includes resolved via the build system's -I search paths are missed —
//	    we cannot see the -I flags, so such edges are UNDERCOUNTED.
//	(b) Generated headers (protobuf *.pb.h, Qt moc_*, autotools config.h.in)
//	    are absent from the workspace until the build runs → no edge to them.
//	(c) #ifdef-conditional includes are ALL walked (the grammar keeps every
//	    branch) → possible OVERCOUNT of candidate edges.
//	(d) Namespaces are orthogonal to files; a namespace can span many files and
//	    a file can hold many namespaces — cards are therefore FILE-level, and a
//	    namespace name is recorded only as a "namespace:X" context entry.
//	(e) Templates / header-only libraries blur the impl/header boundary; cards
//	    are emitted per physical file regardless.
//	(f) No web-framework route surface for C++ v1 (see FrameworkProfile): even
//	    when a server header is detected we emit NO routes and NO blueprints.
//
// Why NO manifest in v1: CMake target names are not registry-resolvable or
// versioned; Conan recipes are Python that we will not execute (static-only);
// no candidate (CMake/Conan/vcpkg) maps to an OSV-scannable ecosystem, so a
// manifest would feed a dead-end vulnerability pipeline. C++ therefore ships
// with graph+cards only and a documented manifest limitation — no Ecosystem
// constant and no WithParser line are added for C++.
type CppLanguageAnalyzer struct {
	extractor *CppImportExtractor
}

// NewCppLanguageAnalyzer returns a ready-to-use C++ analyzer.
func NewCppLanguageAnalyzer() *CppLanguageAnalyzer {
	return &CppLanguageAnalyzer{extractor: NewCppImportExtractor()}
}

// Language satisfies ports.LanguageAnalyzer. "C++" matches go-enry's canonical
// name for .cpp/.cc/.hpp sources, which is what stage 2 populates in
// DetectedLanguage.Name. (Bare .h files are reported by enry as "C", an
// inherent ambiguity; this analyzer still parses every header on disk for the
// graph regardless of how enry labelled the workspace.)
func (a *CppLanguageAnalyzer) Language() string { return "C++" }

// FrameworkProfile returns NONE for C++ v1. C++ has no dominant HTTP framework
// convention we can resolve statically into a route surface, so we do not claim
// one: the Framework field is empty by default. Optionally a string hint is set
// when a well-known server header is included (drogon/, crow.h, httplib.h,
// oatpp/) — but this is a hint only; NO routes and NO blueprints are extracted
// for C++ (ExtractCards always returns nil routes / nil blueprints).
//
// The port takes no workspace path, so the hint cannot be derived here; the
// hint detection (cppServerFrameworkHint) runs during ExtractCards over the
// parsed includes and is retained for the lead to wire when the profile gains
// workspace awareness. Today this returns the honest empty profile.
func (a *CppLanguageAnalyzer) FrameworkProfile() ports.FrameworkProfile {
	return ports.FrameworkProfile{Framework: ""}
}

// ResolveImports parses every C/C++ source file in workspacePath and returns
// the weighted intra-repo include graph. Each DependencyEdge.Weight is the
// number of quote-form #include references from FromModule (file path) to
// ToModule (file path). Angle-form (<system>) includes produce no edges, and
// any quote include that does not resolve to exactly one known file is dropped.
func (a *CppLanguageAnalyzer) ResolveImports(ctx context.Context, workspacePath string) ([]*analysisdomain.DependencyEdge, error) {
	files, err := a.extractor.ExtractFiles(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	resolver := NewCppModuleResolver(files)

	weights := make(map[[2]string]uint32)
	for _, f := range files {
		for k, w := range resolver.BuildGraphEdges(cppToRawImports(f)) {
			weights[k] += w
		}
	}
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

// ExtractCards returns one ModuleCard per translation unit. An impl file
// (.cpp/.cc/...) and its same-stem header (.h/.hpp/...) in the same directory
// are grouped into ONE card keyed by the impl file's path, merging both files'
// functions/classes/state/docstring/LOC. Standalone files (header with no impl,
// or impl with no header) each yield their own card.
//
// Mapping to analysisdomain.ModuleCard:
//   - Module           = workspace-relative path of the card's primary file
//   - File             = same path (C++ node identity is the file)
//   - Functions        = function-definition names (free + "Class::method")
//   - Classes          = "class:Name" | "struct:Name" | "enum:Name" | "namespace:Name"
//   - ModuleLevelState = file/namespace-scope mutable declaration names
//   - DocstringHead    = leading comment block (impl preferred, else header)
//   - Loc              = summed non-blank, non-comment lines of the unit
//   - Routes           = always empty (no C++ route surface in v1)
//
// Blueprints are always nil for C++ v1.
func (a *CppLanguageAnalyzer) ExtractCards(ctx context.Context, workspacePath string) ([]*analysisdomain.ModuleCard, []*analysisdomain.BlueprintInfo, error) {
	files, err := a.extractor.ExtractFiles(ctx, workspacePath)
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, nil
	}

	// Index headers by (dir, stem) so an impl can claim its same-stem header.
	byKey := make(map[string]cppRawFile)
	for _, f := range files {
		if f.IsHeader {
			byKey[cppStemKey(f.RelPath)] = f
		}
	}

	claimedHeader := make(map[string]bool) // RelPath of headers merged into an impl

	var cards []*analysisdomain.ModuleCard
	for _, f := range files {
		if f.IsHeader {
			continue // handled via impl grouping or as standalone below
		}
		card := cppCardFromFile(f)
		if hdr, ok := byKey[cppStemKey(f.RelPath)]; ok {
			cppMergeFile(card, hdr)
			claimedHeader[hdr.RelPath] = true
		}
		cards = append(cards, card)
	}

	// Standalone headers (no same-stem impl in their directory).
	for _, f := range files {
		if f.IsHeader && !claimedHeader[f.RelPath] {
			cards = append(cards, cppCardFromFile(f))
		}
	}

	for _, c := range cards {
		sort.Strings(c.Functions)
		sort.Strings(c.Classes)
		sort.Strings(c.ModuleLevelState)
	}
	sort.Slice(cards, func(i, j int) bool {
		if cards[i].Module != cards[j].Module {
			return cards[i].Module < cards[j].Module
		}
		return cards[i].File < cards[j].File
	})

	// No blueprints for C++ v1.
	return cards, nil, nil
}

// cppCardFromFile builds a single-file ModuleCard (Routes intentionally empty).
func cppCardFromFile(f cppRawFile) *analysisdomain.ModuleCard {
	return &analysisdomain.ModuleCard{
		Module:           f.RelPath,
		File:             f.RelPath,
		Functions:        append([]string(nil), f.Functions...),
		Classes:          append([]string(nil), f.Classes...),
		ModuleLevelState: append([]string(nil), f.ModuleState...),
		DocstringHead:    f.Docstring,
		Loc:              f.Loc,
	}
}

// cppMergeFile folds a header's structural data into the impl-keyed card.
func cppMergeFile(card *analysisdomain.ModuleCard, hdr cppRawFile) {
	card.Functions = append(card.Functions, hdr.Functions...)
	card.Classes = append(card.Classes, hdr.Classes...)
	card.ModuleLevelState = append(card.ModuleLevelState, hdr.ModuleState...)
	if card.DocstringHead == "" {
		card.DocstringHead = hdr.Docstring
	}
	card.Loc += hdr.Loc
}

// cppStemKey returns "dir/stem" for a path, used to pair an impl file with its
// same-directory same-stem header (foo.cpp ↔ foo.h).
func cppStemKey(relPath string) string {
	dir := ""
	base := relPath
	if i := strings.LastIndexByte(relPath, '/'); i >= 0 {
		dir = relPath[:i]
		base = relPath[i+1:]
	}
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if dir == "" {
		return base
	}
	return dir + "/" + base
}

// cppServerFrameworkHint returns a non-binding framework hint when a well-known
// C++ server header appears among the quote/angle includes of the workspace.
// It NEVER drives route extraction (C++ v1 has no route surface); it is the
// deterministic detection retained for a future workspace-aware FrameworkProfile.
func cppServerFrameworkHint(includes []string) string {
	for _, inc := range includes {
		switch {
		case strings.HasPrefix(inc, "drogon/"):
			return "Drogon"
		case inc == "crow.h" || strings.HasPrefix(inc, "crow/"):
			return "Crow"
		case inc == "httplib.h":
			return "cpp-httplib"
		case strings.HasPrefix(inc, "oatpp/"):
			return "Oat++"
		}
	}
	return ""
}
