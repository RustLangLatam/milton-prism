package domain

import (
	"regexp"
	"sort"
	"strings"
)

// Fase 4 §1c — deterministic port-coverage compute.
//
// ComputePortCoverage measures how much of a service's captured source behaviour
// was actually ported into the generated deliverable. It is a PURE, deterministic
// function (no I/O, no clock, no map-iteration order leaking out) so it can be
// table-tested exhaustively and produces byte-identical results for identical
// input. The bar it feeds is a *signal*, not an audited percentage (§3, §6 #1):
// each PORT-GAP marker counts as one un-ported "method-equivalent".
//
// Import-cycle decision: the blueprint's nominal signature uses
// []ports.SourceFile, but the ports package imports this domain package
// (workerdomain "milton_prism/core/worker/generation/domain"), so importing
// ports here would create a cycle. We therefore (a) reuse the existing in-package
// FileArtifact for the generated side and (b) accept a MINIMAL local input type,
// SourceFileInput, for the source side. The caller (pipeline.go) adapts
// []ports.SourceFile → []SourceFileInput at the single wiring site; only the two
// fields this metric needs (Role, Symbols) are carried, keeping this file
// self-contained and cycle-free.

// SourceFileInput is the minimal projection of a captured source file that the
// coverage metric needs. It deliberately carries only Role and Symbols (the two
// fields the denominator depends on) so package domain need not import the ports
// package — see the import-cycle note above. The caller maps each
// ports.SourceFile to one SourceFileInput.
type SourceFileInput struct {
	// Role is "domain" (a service-owned source file whose logic must be ported)
	// or "test" (a behaviour oracle). Anything that is NOT exactly "test" is
	// treated as domain for denominator purposes (§1c #1).
	Role string
	// Symbols are the classes/functions declared in the file (from the analysis
	// card). May be empty when no card was captured (Symbols is omitempty
	// upstream), in which case the file contributes nothing to the denominator.
	Symbols []string
}

// PortGap is the per-marker detail for one PORT-GAP comment found in the
// generated deliverable. It is best-effort UI enrichment; the counts/ratio on
// PortCoverage do NOT depend on any field here except via PortGapCount.
type PortGap struct {
	// File is the generated artifact path the marker was found in.
	File string
	// Line is the 1-based line number of the marker within that artifact.
	Line int
	// Symbol is the best-effort nearest source symbol enclosing the marker
	// (case-insensitive, identifier-boundary match at-or-above the marker line),
	// or "" when none of the denominator symbols appear above the marker.
	Symbol string
	// Note is the trimmed text following "PORT-GAP:" on the marker line.
	Note string
}

// PortCoverage is the deterministic per-service port-coverage summary computed at
// generation persist time. CoverageRatio is only meaningful when Measured is
// true; when Measured is false the service is "contract-only" (no source symbols
// were captured) and upstream MUST render "contract-only", never 0% or 100%
// (§3 — the single most important correctness rule).
type PortCoverage struct {
	// SourceMethodCount is the denominator: distinct domain-role symbols across
	// the source files. 0 ⇒ contract-only.
	SourceMethodCount int
	// PortGapCount is the EXACT (uncapped) total of PORT-GAP markers across the
	// generated files. May exceed SourceMethodCount when the denominator was
	// under-captured.
	PortGapCount int
	// PortedMethodCount is max(SourceMethodCount-PortGapCount, 0).
	PortedMethodCount int
	// CoverageRatio is PortedMethodCount/SourceMethodCount clamped to [0,1] when
	// Measured, else 0.0.
	CoverageRatio float64
	// Measured reports SourceMethodCount > 0.
	Measured bool
	// Gaps is the per-marker detail, CAPPED at portGapSliceCap entries for UI
	// payload safety (PortGapCount stays exact). May be shorter than
	// PortGapCount when truncated; may be empty even when PortGapCount > 0 only
	// if the cap is 0 (it is not).
	Gaps []PortGap
}

// portGapSliceCap bounds the per-service Gaps detail slice (§1c #2, §6 #5) so a
// pathological deliverable cannot bloat the GetMigration payload toward the
// 1 MiB gateway dial. PortGapCount is always exact; only this list truncates.
const portGapSliceCap = 200

// portGapLineRe matches a PORT-GAP marker on a single line.
//
// Heuristic (LOCKED v1, §6 #3): we match the bare PORT-GAP token only when it is
// immediately preceded — with optional intervening spaces/tabs — by a recognised
// comment leader: "//" (C/Go/Java/JS), "#" (Python/Ruby/shell), "--" (SQL/Lua/
// Haskell), ";" (Lisp/asm/ini), "<!--" (HTML/XML), "/*" (block open), or "*"
// (block-comment continuation line). The leader requirement is what rejects the
// common false positive of the token sitting inside a plain string literal, e.g.
//     msg := "PORT-GAP: this is data"     // (no comment leader before token → not matched)
// because there a quote, not a leader, immediately precedes the token.
//
// Documented limitation: this is a line-regex, not a comment-aware parser, so it
// CANNOT distinguish a comment leader that itself lives inside a string literal,
// e.g.  s := "// PORT-GAP: x"  WOULD be counted. That over-count is the accepted
// v1 trade-off; a comment-aware scan is deferred (§6 #3). RE2 has no lookbehind,
// so we capture the leader in group 1 (unused) and the note in group 2.
var portGapLineRe = regexp.MustCompile(`(//|#|--|;|<!--|/\*|\*)[ \t]*PORT-GAP:[ \t]*(.*)$`)

// ComputePortCoverage implements Fase 4 §1c. It is pure and deterministic.
func ComputePortCoverage(source []SourceFileInput, generated []FileArtifact) PortCoverage {
	// 1. Denominator: union of distinct domain-role symbols (exact, case-sensitive).
	symbolSet := make(map[string]struct{})
	for _, sf := range source {
		if sf.Role == "test" { // any non-"test" role counts as domain (§1c #1)
			continue
		}
		for _, sym := range sf.Symbols {
			if sym == "" {
				continue
			}
			symbolSet[sym] = struct{}{}
		}
	}
	sourceMethodCount := len(symbolSet)
	measured := sourceMethodCount > 0

	// Deterministic, sorted symbol list for attribution (map order is random).
	symbols := make([]string, 0, len(symbolSet))
	for sym := range symbolSet {
		symbols = append(symbols, sym)
	}
	sort.Strings(symbols)
	// Pre-compile identifier-boundary matchers once per distinct symbol.
	symbolRes := make([]*regexp.Regexp, len(symbols))
	for i, sym := range symbols {
		symbolRes[i] = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(sym) + `([^A-Za-z0-9_]|$)`)
	}

	// 2. Gap scan: line-by-line across every generated artifact.
	cov := PortCoverage{
		SourceMethodCount: sourceMethodCount,
		Measured:          measured,
	}
	for _, art := range generated {
		lines := strings.Split(string(art.Content), "\n")
		for idx, line := range lines {
			m := portGapLineRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			cov.PortGapCount++ // exact, uncapped
			if len(cov.Gaps) >= portGapSliceCap {
				continue // cap detail slice; count keeps climbing
			}
			gap := PortGap{
				File: art.Path,
				Line: idx + 1, // 1-based
				Note: strings.TrimSpace(m[2]),
			}
			// 4. Symbol attribution (best-effort, never affects counts/ratio):
			// nearest denominator symbol on a line at-or-above the marker.
			gap.Symbol = nearestSymbol(lines, idx, symbols, symbolRes)
			cov.Gaps = append(cov.Gaps, gap)
		}
	}

	// 3. Numerator + ratio.
	ported := sourceMethodCount - cov.PortGapCount
	if ported < 0 {
		ported = 0
	}
	cov.PortedMethodCount = ported
	if measured {
		ratio := float64(ported) / float64(sourceMethodCount)
		if ratio < 0 {
			ratio = 0
		}
		if ratio > 1 {
			ratio = 1
		}
		cov.CoverageRatio = ratio
	}
	return cov
}

// nearestSymbol returns the denominator symbol whose identifier appears on the
// nearest line at-or-above markerIdx, scanning upward. On a single line, the
// lexicographically-first matching symbol wins (symbols is pre-sorted), keeping
// the result deterministic. Returns "" when no symbol is found above the marker.
func nearestSymbol(lines []string, markerIdx int, symbols []string, symbolRes []*regexp.Regexp) string {
	for i := markerIdx; i >= 0; i-- {
		line := lines[i]
		for j, re := range symbolRes {
			if re.MatchString(line) {
				return symbols[j]
			}
		}
	}
	return ""
}
