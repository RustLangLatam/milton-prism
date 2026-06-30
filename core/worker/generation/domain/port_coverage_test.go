package domain

import (
	"fmt"
	"strings"
	"testing"
)

// art is a tiny helper to build a generated FileArtifact from string content.
func art(path, content string) FileArtifact {
	return FileArtifact{Path: path, Content: []byte(content)}
}

func TestComputePortCoverage(t *testing.T) {
	type want struct {
		sourceMethodCount int
		portGapCount      int
		portedMethodCount int
		coverageRatio     float64
		measured          bool
		gapsLen           int
	}
	tests := []struct {
		name      string
		source    []SourceFileInput
		generated []FileArtifact
		want      want
	}{
		{
			name:      "empty source — not measured",
			source:    nil,
			generated: []FileArtifact{art("a.go", "// PORT-GAP: orphan marker")},
			// Markers still counted exactly, but ratio is N/A.
			want: want{sourceMethodCount: 0, portGapCount: 1, portedMethodCount: 0, coverageRatio: 0, measured: false, gapsLen: 1},
		},
		{
			name:      "empty symbols only — not measured",
			source:    []SourceFileInput{{Role: "domain", Symbols: nil}, {Role: "domain", Symbols: []string{""}}},
			generated: []FileArtifact{art("a.go", "clean file\nno markers here")},
			want:      want{sourceMethodCount: 0, portGapCount: 0, portedMethodCount: 0, coverageRatio: 0, measured: false, gapsLen: 0},
		},
		{
			name:      "symbols, zero gaps — 100%",
			source:    []SourceFileInput{{Role: "domain", Symbols: []string{"Create", "Update", "Delete", "List"}}},
			generated: []FileArtifact{art("svc.go", "func Create() {}\nfunc List() {}")},
			want:      want{sourceMethodCount: 4, portGapCount: 0, portedMethodCount: 4, coverageRatio: 1.0, measured: true, gapsLen: 0},
		},
		{
			name:   "gaps < symbols — partial",
			source: []SourceFileInput{{Role: "domain", Symbols: []string{"A", "B", "C", "D"}}},
			generated: []FileArtifact{art("svc.go",
				"line1\n// PORT-GAP: cannot port reflection\nline3\n# PORT-GAP: dynamic dispatch")},
			want: want{sourceMethodCount: 4, portGapCount: 2, portedMethodCount: 2, coverageRatio: 0.5, measured: true, gapsLen: 2},
		},
		{
			name:   "gaps == symbols — 0%",
			source: []SourceFileInput{{Role: "domain", Symbols: []string{"A", "B"}}},
			generated: []FileArtifact{art("svc.go",
				"// PORT-GAP: one\n-- PORT-GAP: two")},
			want: want{sourceMethodCount: 2, portGapCount: 2, portedMethodCount: 0, coverageRatio: 0.0, measured: true, gapsLen: 2},
		},
		{
			name:   "gaps > symbols — clamped 0% (denominator under-captured)",
			source: []SourceFileInput{{Role: "domain", Symbols: []string{"A"}}},
			generated: []FileArtifact{art("svc.go",
				"// PORT-GAP: one\n// PORT-GAP: two\n// PORT-GAP: three")},
			// ported clamps to 0, ratio clamps to 0; count stays exact at 3.
			want: want{sourceMethodCount: 1, portGapCount: 3, portedMethodCount: 0, coverageRatio: 0.0, measured: true, gapsLen: 3},
		},
		{
			name:   "multi-language comment leaders all detected",
			source: []SourceFileInput{{Role: "domain", Symbols: []string{"A", "B", "C", "D", "E", "F", "G", "H"}}},
			generated: []FileArtifact{art("multi.txt", strings.Join([]string{
				"// PORT-GAP: slash slash",
				"# PORT-GAP: hash",
				"-- PORT-GAP: sql dashes",
				"; PORT-GAP: semicolon",
				"<!-- PORT-GAP: html",
				"/* PORT-GAP: block open",
				" * PORT-GAP: block continuation",
				"x := y // PORT-GAP: trailing after code",
			}, "\n"))},
			want: want{sourceMethodCount: 8, portGapCount: 8, portedMethodCount: 0, coverageRatio: 0.0, measured: true, gapsLen: 8},
		},
		{
			name:   "token inside plain string literal NOT counted",
			source: []SourceFileInput{{Role: "domain", Symbols: []string{"A", "B"}}},
			generated: []FileArtifact{art("svc.go", strings.Join([]string{
				`msg := "PORT-GAP: this is data not a marker"`,
				`log.Print("prefix PORT-GAP: still data")`,
				"// PORT-GAP: the only real marker",
			}, "\n"))},
			// Only the genuine comment-led marker counts.
			want: want{sourceMethodCount: 2, portGapCount: 1, portedMethodCount: 1, coverageRatio: 0.5, measured: true, gapsLen: 1},
		},
		{
			name:      "test-role source excluded from denominator",
			source:    []SourceFileInput{{Role: "test", Symbols: []string{"TestA", "TestB", "TestC"}}, {Role: "domain", Symbols: []string{"Real"}}},
			generated: []FileArtifact{art("svc.go", "func Real() {}")},
			// Only the single domain symbol counts; the 3 test symbols are ignored.
			want: want{sourceMethodCount: 1, portGapCount: 0, portedMethodCount: 1, coverageRatio: 1.0, measured: true, gapsLen: 0},
		},
		{
			name:      "non-test role treated as domain",
			source:    []SourceFileInput{{Role: "", Symbols: []string{"X"}}, {Role: "controller", Symbols: []string{"Y"}}},
			generated: []FileArtifact{art("svc.go", "ok")},
			want:      want{sourceMethodCount: 2, portGapCount: 0, portedMethodCount: 2, coverageRatio: 1.0, measured: true, gapsLen: 0},
		},
		{
			name: "distinct symbol union dedupes across files",
			source: []SourceFileInput{
				{Role: "domain", Symbols: []string{"A", "B"}},
				{Role: "domain", Symbols: []string{"B", "C"}}, // B duplicate
			},
			generated: []FileArtifact{art("svc.go", "ok")},
			want:      want{sourceMethodCount: 3, portGapCount: 0, portedMethodCount: 3, coverageRatio: 1.0, measured: true, gapsLen: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputePortCoverage(tt.source, tt.generated)
			if got.SourceMethodCount != tt.want.sourceMethodCount {
				t.Errorf("SourceMethodCount = %d, want %d", got.SourceMethodCount, tt.want.sourceMethodCount)
			}
			if got.PortGapCount != tt.want.portGapCount {
				t.Errorf("PortGapCount = %d, want %d", got.PortGapCount, tt.want.portGapCount)
			}
			if got.PortedMethodCount != tt.want.portedMethodCount {
				t.Errorf("PortedMethodCount = %d, want %d", got.PortedMethodCount, tt.want.portedMethodCount)
			}
			if got.CoverageRatio != tt.want.coverageRatio {
				t.Errorf("CoverageRatio = %v, want %v", got.CoverageRatio, tt.want.coverageRatio)
			}
			if got.Measured != tt.want.measured {
				t.Errorf("Measured = %v, want %v", got.Measured, tt.want.measured)
			}
			if len(got.Gaps) != tt.want.gapsLen {
				t.Errorf("len(Gaps) = %d, want %d", len(got.Gaps), tt.want.gapsLen)
			}
		})
	}
}

// TestComputePortCoverage_GapsCap verifies the detail slice truncates at 200
// while PortGapCount remains exact.
func TestComputePortCoverage_GapsCap(t *testing.T) {
	const n = 250
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "// PORT-GAP: marker %d\n", i)
	}
	source := []SourceFileInput{{Role: "domain", Symbols: []string{"A"}}}
	got := ComputePortCoverage(source, []FileArtifact{art("big.go", b.String())})

	if got.PortGapCount != n {
		t.Errorf("PortGapCount = %d, want exact %d", got.PortGapCount, n)
	}
	if len(got.Gaps) != portGapSliceCap {
		t.Errorf("len(Gaps) = %d, want capped at %d", len(got.Gaps), portGapSliceCap)
	}
	// gaps>symbols ⇒ clamped.
	if got.PortedMethodCount != 0 || got.CoverageRatio != 0 {
		t.Errorf("expected clamped 0 coverage, got ported=%d ratio=%v", got.PortedMethodCount, got.CoverageRatio)
	}
	// First recorded gap is the first marker (deterministic order).
	if got.Gaps[0].Line != 1 || got.Gaps[0].Note != "marker 0" {
		t.Errorf("Gaps[0] = %+v, want line 1 note 'marker 0'", got.Gaps[0])
	}
}

// TestComputePortCoverage_SymbolAttribution verifies the nearest enclosing
// source symbol is attributed to a marker (enrichment only).
func TestComputePortCoverage_SymbolAttribution(t *testing.T) {
	source := []SourceFileInput{{Role: "domain", Symbols: []string{"CreateArticle", "ListArticles"}}}
	content := strings.Join([]string{
		"func CreateArticle() {",                  // line 1: nearest above first marker
		"    doStuff()",                           // line 2
		"    // PORT-GAP: slug uniqueness skipped", // line 3
		"}",                                        // line 4
		"func ListArticles() {",                   // line 5
		"    // PORT-GAP: pagination cursor TODO", // line 6
		"}",                                        // line 7
	}, "\n")
	got := ComputePortCoverage(source, []FileArtifact{art("articles.go", content)})

	if len(got.Gaps) != 2 {
		t.Fatalf("expected 2 gaps, got %d", len(got.Gaps))
	}
	if got.Gaps[0].Symbol != "CreateArticle" {
		t.Errorf("gap[0].Symbol = %q, want CreateArticle", got.Gaps[0].Symbol)
	}
	if got.Gaps[0].Line != 3 {
		t.Errorf("gap[0].Line = %d, want 3", got.Gaps[0].Line)
	}
	if got.Gaps[1].Symbol != "ListArticles" {
		t.Errorf("gap[1].Symbol = %q, want ListArticles", got.Gaps[1].Symbol)
	}
	if got.Gaps[0].Note != "slug uniqueness skipped" {
		t.Errorf("gap[0].Note = %q", got.Gaps[0].Note)
	}
}

// TestComputePortCoverage_SymbolAttributionNoneAbove verifies "" when no
// denominator symbol appears at-or-above the marker, and case-insensitive match.
func TestComputePortCoverage_SymbolAttribution_Edges(t *testing.T) {
	source := []SourceFileInput{{Role: "domain", Symbols: []string{"Handler"}}}

	// (a) marker before any symbol mention → "".
	c1 := "// PORT-GAP: top of file\ncall_handler_thing()\nHandler()"
	g1 := ComputePortCoverage(source, []FileArtifact{art("a.go", c1)})
	if len(g1.Gaps) != 1 || g1.Gaps[0].Symbol != "" {
		t.Errorf("expected empty symbol for top-of-file marker, got %+v", g1.Gaps)
	}

	// (b) case-insensitive identifier-boundary match: "handler" matches "Handler".
	c2 := "def handler():\n    pass  # PORT-GAP: x"
	g2 := ComputePortCoverage(source, []FileArtifact{art("b.py", c2)})
	if len(g2.Gaps) != 1 || g2.Gaps[0].Symbol != "Handler" {
		t.Errorf("expected case-insensitive Handler match, got %+v", g2.Gaps)
	}

	// (c) identifier boundary: substring "Handlers" must NOT match "Handler".
	c3 := "callHandlersList()\n// PORT-GAP: y"
	g3 := ComputePortCoverage(source, []FileArtifact{art("c.go", c3)})
	if len(g3.Gaps) != 1 || g3.Gaps[0].Symbol != "" {
		t.Errorf("expected no match for substring 'Handlers', got %+v", g3.Gaps)
	}
}

// TestComputePortCoverage_Deterministic ensures repeated calls yield identical
// results (no map-order leakage).
func TestComputePortCoverage_Deterministic(t *testing.T) {
	source := []SourceFileInput{
		{Role: "domain", Symbols: []string{"Zeta", "Alpha", "Mu"}},
		{Role: "domain", Symbols: []string{"Mu", "Beta"}},
	}
	gen := []FileArtifact{art("f.go", "Alpha()\nBeta()\n// PORT-GAP: g")}
	first := ComputePortCoverage(source, gen)
	for i := 0; i < 50; i++ {
		got := ComputePortCoverage(source, gen)
		if got.SourceMethodCount != first.SourceMethodCount ||
			got.CoverageRatio != first.CoverageRatio ||
			len(got.Gaps) != len(first.Gaps) ||
			got.Gaps[0].Symbol != first.Gaps[0].Symbol {
			t.Fatalf("non-deterministic result at iter %d: %+v vs %+v", i, got, first)
		}
	}
}
