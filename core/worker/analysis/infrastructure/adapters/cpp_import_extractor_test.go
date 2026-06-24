package adapters

import (
	"context"
	"testing"
)

// findCppFile returns the parsed file with the given relPath, or fails.
func findCppFile(t *testing.T, files []cppRawFile, relPath string) cppRawFile {
	t.Helper()
	for _, f := range files {
		if f.RelPath == relPath {
			return f
		}
	}
	t.Fatalf("file %q not found among %d parsed files", relPath, len(files))
	return cppRawFile{}
}

func TestCppExtractor_QuoteVsAngleClassification(t *testing.T) {
	e := NewCppImportExtractor()
	files, err := e.ExtractFiles(context.Background(), "testdata/fixture-cpp")
	if err != nil {
		t.Fatalf("ExtractFiles: %v", err)
	}

	// src/shape.cpp has a quote include "geometry/shape.h" and an angle include
	// <cmath>; only the quote form is captured.
	shape := findCppFile(t, files, "src/shape.cpp")
	if len(shape.QuoteIncludes) != 1 || shape.QuoteIncludes[0] != "geometry/shape.h" {
		t.Fatalf("src/shape.cpp QuoteIncludes = %v, want [geometry/shape.h]", shape.QuoteIncludes)
	}

	// include/geometry/shape.h has only a system include <string> → none captured.
	hdr := findCppFile(t, files, "include/geometry/shape.h")
	if len(hdr.QuoteIncludes) != 0 {
		t.Fatalf("shape.h QuoteIncludes = %v, want none (only <string> system include)", hdr.QuoteIncludes)
	}
}

func TestCppExtractor_CardKinds(t *testing.T) {
	e := NewCppImportExtractor()
	files, err := e.ExtractFiles(context.Background(), "testdata/fixture-cpp")
	if err != nil {
		t.Fatalf("ExtractFiles: %v", err)
	}

	hdr := findCppFile(t, files, "include/geometry/shape.h")
	want := map[string]bool{
		"namespace:geo": false,
		"class:Shape":   false,
		"struct:Point":  false,
		"enum:Kind":     false,
	}
	for _, c := range hdr.Classes {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("shape.h Classes missing %q; got %v", k, hdr.Classes)
		}
	}

	// Free function inside the namespace surfaces as a card function.
	impl := findCppFile(t, files, "src/shape.cpp")
	hasCircleArea := false
	for _, fn := range impl.Functions {
		if fn == "circleArea" {
			hasCircleArea = true
		}
	}
	if !hasCircleArea {
		t.Errorf("src/shape.cpp Functions missing circleArea; got %v", impl.Functions)
	}
}

func TestCppExtractor_ModuleLevelState(t *testing.T) {
	e := NewCppImportExtractor()
	files, err := e.ExtractFiles(context.Background(), "testdata/fixture-cpp")
	if err != nil {
		t.Fatalf("ExtractFiles: %v", err)
	}
	// core/util.cpp has `static int g_counter = 0;` at file scope (mutable state).
	util := findCppFile(t, files, "core/util.cpp")
	found := false
	for _, s := range util.ModuleState {
		if s == "g_counter" {
			found = true
		}
	}
	if !found {
		t.Errorf("core/util.cpp ModuleState missing g_counter; got %v", util.ModuleState)
	}
}

func TestCppExtractor_DocstringAndLOC(t *testing.T) {
	e := NewCppImportExtractor()
	files, err := e.ExtractFiles(context.Background(), "testdata/fixture-cpp")
	if err != nil {
		t.Fatalf("ExtractFiles: %v", err)
	}
	hdr := findCppFile(t, files, "include/geometry/shape.h")
	if hdr.Docstring == "" {
		t.Errorf("shape.h Docstring empty, want leading comment text")
	}
	if hdr.Loc == 0 {
		t.Errorf("shape.h Loc = 0, want > 0")
	}
}

func TestCppCountLOC(t *testing.T) {
	src := []byte("// comment\nint a = 1;\n\n/* block\nstill block */\nint b = 2;\n")
	if got := cppCountLOC(src); got != 2 {
		t.Errorf("cppCountLOC = %d, want 2", got)
	}
}
