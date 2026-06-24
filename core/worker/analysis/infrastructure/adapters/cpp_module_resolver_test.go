package adapters

import (
	"context"
	"testing"

	workerdomain "milton_prism/core/worker/analysis/domain"
)

// loadCppFixture parses the fixture and returns the files + a resolver.
func loadCppFixture(t *testing.T) ([]cppRawFile, *CppModuleResolver) {
	t.Helper()
	e := NewCppImportExtractor()
	files, err := e.ExtractFiles(context.Background(), "testdata/fixture-cpp")
	if err != nil {
		t.Fatalf("ExtractFiles: %v", err)
	}
	return files, NewCppModuleResolver(files)
}

func TestCppResolver_ResolutionOrder(t *testing.T) {
	_, r := loadCppFixture(t)

	// (a) common-root resolution: "geometry/shape.h" from src/shape.cpp resolves
	// via root/include/geometry/shape.h.
	if got := r.resolve("src/shape.cpp", "geometry/shape.h"); got != "include/geometry/shape.h" {
		t.Errorf("resolve(src/shape.cpp, geometry/shape.h) = %q, want include/geometry/shape.h", got)
	}

	// (b) dir-relative resolution wins: "util.h" from core/util.cpp resolves to
	// core/util.h even though the basename is ambiguous (collision suppression
	// only applies to the basename fallback, not to a real dir-relative hit).
	if got := r.resolve("core/util.cpp", "util.h"); got != "core/util.h" {
		t.Errorf("resolve(core/util.cpp, util.h) = %q, want core/util.h", got)
	}
}

func TestCppResolver_BasenameCollisionSuppressed(t *testing.T) {
	_, r := loadCppFixture(t)

	// "util.h" exists in both core/ and lib/. app/consumer.cpp has no
	// dir-relative or common-root match, so the only candidate is the ambiguous
	// basename → suppressed (honest miss, no wrong edge).
	if !r.index.ambiguousBasename["util.h"] {
		t.Fatalf("util.h should be marked ambiguous (core/util.h and lib/util.h)")
	}
	if got := r.resolve("app/consumer.cpp", "util.h"); got != "" {
		t.Errorf("resolve(app/consumer.cpp, util.h) = %q, want \"\" (ambiguous → suppressed)", got)
	}
}

func TestCppResolver_AngleIncludeNotAnEdge(t *testing.T) {
	files, r := loadCppFixture(t)

	// Build the full graph from all files' quote includes; assert no edge points
	// to a system header (angle includes never reach the resolver).
	weights := make(map[[2]string]uint32)
	for _, f := range files {
		for k, w := range r.BuildGraphEdges(cppToRawImports(f)) {
			weights[k] += w
		}
	}
	for k := range weights {
		if k[1] == "string" || k[1] == "cmath" {
			t.Errorf("edge to system header %q must not exist", k[1])
		}
	}
}

func TestCppResolver_GraphEdges(t *testing.T) {
	files, r := loadCppFixture(t)

	weights := make(map[[2]string]uint32)
	for _, f := range files {
		for k, w := range r.BuildGraphEdges(cppToRawImports(f)) {
			weights[k] += w
		}
	}

	want := map[[2]string]uint32{
		{"src/shape.cpp", "include/geometry/shape.h"}: 1, // common-root include
		{"core/util.cpp", "core/util.h"}:              1, // header/impl, dir-relative
	}
	if len(weights) != len(want) {
		t.Fatalf("got %d edges %v, want %d %v", len(weights), weights, len(want), want)
	}
	for k, w := range want {
		if weights[k] != w {
			t.Errorf("edge %v weight = %d, want %d", k, weights[k], w)
		}
	}
}

func TestCppResolver_FileLevelNodeIdentity(t *testing.T) {
	_, r := loadCppFixture(t)
	// Node identity is the workspace-relative FILE path: a resolved edge target
	// is a path that exists in the file index, not a synthesised module name.
	to := r.resolve("src/shape.cpp", "geometry/shape.h")
	if !r.index.paths[to] {
		t.Errorf("resolved target %q is not a known workspace file path", to)
	}
}

func TestCppResolver_NoSelfEdge(t *testing.T) {
	r := NewCppModuleResolver([]cppRawFile{{RelPath: "a/x.cpp"}})
	raw := []workerdomain.RawImport{{ImportingFile: "a/x.cpp", Module: "x.cpp", Names: []string{"x.cpp"}}}
	if edges := r.Resolve(raw); len(edges) != 0 {
		t.Errorf("self-include produced %v, want no edges", edges)
	}
}
