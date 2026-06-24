package adapters

import (
	"context"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

func TestCppAnalyzer_LanguageIsEnryCanonical(t *testing.T) {
	a := NewCppLanguageAnalyzer()
	if a.Language() != "C++" {
		t.Errorf("Language() = %q, want C++ (enry canonical)", a.Language())
	}
}

func TestCppAnalyzer_FrameworkProfileNone(t *testing.T) {
	a := NewCppLanguageAnalyzer()
	if p := a.FrameworkProfile(); p.Framework != "" {
		t.Errorf("FrameworkProfile = %+v, want empty Framework (no C++ route surface in v1)", p)
	}
}

func TestCppAnalyzer_ResolveImportsGraph(t *testing.T) {
	a := NewCppLanguageAnalyzer()
	edges, err := a.ResolveImports(context.Background(), "testdata/fixture-cpp")
	if err != nil {
		t.Fatalf("ResolveImports: %v", err)
	}
	// Deterministically sorted: core/util.cpp edge precedes src/shape.cpp edge.
	want := []*analysisdomain.DependencyEdge{
		{FromModule: "core/util.cpp", ToModule: "core/util.h", Weight: 1},
		{FromModule: "src/shape.cpp", ToModule: "include/geometry/shape.h", Weight: 1},
	}
	if len(edges) != len(want) {
		t.Fatalf("got %d edges, want %d: %+v", len(edges), len(want), edges)
	}
	for i := range want {
		if edges[i].FromModule != want[i].FromModule ||
			edges[i].ToModule != want[i].ToModule ||
			edges[i].Weight != want[i].Weight {
			t.Errorf("edge[%d] = %+v, want %+v", i, edges[i], want[i])
		}
	}
}

func TestCppAnalyzer_ExtractCards(t *testing.T) {
	a := NewCppLanguageAnalyzer()
	cards, blueprints, err := a.ExtractCards(context.Background(), "testdata/fixture-cpp")
	if err != nil {
		t.Fatalf("ExtractCards: %v", err)
	}

	// No web-framework blueprints for C++ v1.
	if blueprints != nil {
		t.Errorf("blueprints = %v, want nil (no C++ blueprint surface)", blueprints)
	}

	byFile := make(map[string]*analysisdomain.ModuleCard)
	for _, c := range cards {
		byFile[c.File] = c
		// No route surface for C++ v1.
		if len(c.Routes) != 0 {
			t.Errorf("card %s has %d routes, want 0", c.File, len(c.Routes))
		}
	}

	// Translation-unit grouping: core/util.cpp + core/util.h → ONE card keyed by
	// the impl; the standalone header must NOT appear as its own card.
	if _, ok := byFile["core/util.h"]; ok {
		t.Errorf("core/util.h should be merged into core/util.cpp, not a standalone card")
	}
	utilCard, ok := byFile["core/util.cpp"]
	if !ok {
		t.Fatalf("expected a card keyed by core/util.cpp")
	}
	// Merged functions: helperCount is declared in util.h and defined in util.cpp.
	hasHelper := false
	for _, fn := range utilCard.Functions {
		if fn == "helperCount" {
			hasHelper = true
		}
	}
	if !hasHelper {
		t.Errorf("core/util.cpp card Functions = %v, want to include helperCount (merged from header)", utilCard.Functions)
	}

	// shape.cpp and shape.h are in DIFFERENT directories → NOT grouped; both
	// remain standalone cards (different stem keys).
	if _, ok := byFile["src/shape.cpp"]; !ok {
		t.Errorf("src/shape.cpp card missing")
	}
	shapeHdr, ok := byFile["include/geometry/shape.h"]
	if !ok {
		t.Fatalf("include/geometry/shape.h standalone card missing")
	}
	// Kind prefixes from the header card.
	wantKinds := map[string]bool{"class:Shape": false, "struct:Point": false, "enum:Kind": false, "namespace:geo": false}
	for _, c := range shapeHdr.Classes {
		if _, ok := wantKinds[c]; ok {
			wantKinds[c] = true
		}
	}
	for k, seen := range wantKinds {
		if !seen {
			t.Errorf("shape.h card Classes missing %q; got %v", k, shapeHdr.Classes)
		}
	}
}

func TestCppAnalyzer_ImplementsPort(t *testing.T) {
	var _ ports.LanguageAnalyzer = NewCppLanguageAnalyzer()
}

func TestCppServerFrameworkHint(t *testing.T) {
	cases := map[string]string{
		"drogon/HttpController.h": "Drogon",
		"crow.h":                  "Crow",
		"httplib.h":               "cpp-httplib",
		"oatpp/web/server/api/ApiController.hpp": "Oat++",
		"vector": "",
	}
	for inc, want := range cases {
		if got := cppServerFrameworkHint([]string{inc}); got != want {
			t.Errorf("cppServerFrameworkHint(%q) = %q, want %q", inc, got, want)
		}
	}
}
