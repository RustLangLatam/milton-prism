package application

import (
	"sort"
	"strings"
	"unicode/utf8"

	analysisdomain "milton_prism/core/services/analysis/domain"
)

// MaxPersistedSecurityFindings bounds how many findings are persisted on a summary.
// A repository with thousands of secret-shaped values would otherwise bloat the Mongo
// document; the cap keeps the highest-signal findings (severity desc, then confidence
// desc, then stable file/line order) and drops the long tail. The dropped count is
// surfaced separately by capSecurityFindings so the report can say "+N more".
const MaxPersistedSecurityFindings = 200

// capSecurityFindings returns at most MaxPersistedSecurityFindings findings, keeping the
// highest-signal ones (HIGH before MEDIUM before LOW; within a severity, higher
// confidence first; ties broken by the canonical file/line/rule order), how many were
// dropped, and ensures every persisted string field is valid UTF-8.
//
// The UTF-8 sanitisation is defence in depth: proto3 rejects invalid-UTF-8 string
// fields, so a single stray byte in a snippet would make the whole summary's
// proto.Marshal fail — which previously hung the persistence path and looped the job
// forever (the secret scanner's own redaction is already rune-safe, but any future
// field source is covered here too). Replacing invalid bytes with U+FFFD guarantees the
// summary always marshals.
func capSecurityFindings(findings []*analysisdomain.SecurityFinding) (kept []*analysisdomain.SecurityFinding, dropped int) {
	for _, f := range findings {
		sanitizeFindingUTF8(f)
	}
	if len(findings) <= MaxPersistedSecurityFindings {
		return findings, 0
	}
	ordered := make([]*analysisdomain.SecurityFinding, len(findings))
	copy(ordered, findings)
	// Stable sort by signal: severity desc, then confidence desc. SliceStable keeps the
	// incoming (file,line,rule) order for equal-signal findings, so the cap is deterministic.
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].GetSeverity() != ordered[j].GetSeverity() {
			return ordered[i].GetSeverity() > ordered[j].GetSeverity()
		}
		return ordered[i].GetConfidence() > ordered[j].GetConfidence()
	})
	kept = ordered[:MaxPersistedSecurityFindings]
	// Restore the canonical (file, line, rule) order on the kept subset so downstream
	// readers see stable ordering regardless of the cap.
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].GetFile() != kept[j].GetFile() {
			return kept[i].GetFile() < kept[j].GetFile()
		}
		if kept[i].GetLine() != kept[j].GetLine() {
			return kept[i].GetLine() < kept[j].GetLine()
		}
		return kept[i].GetRule() < kept[j].GetRule()
	})
	return kept, len(findings) - MaxPersistedSecurityFindings
}

// sanitizeFindingUTF8 replaces any invalid UTF-8 in a finding's string fields with the
// Unicode replacement character so the enclosing summary always proto.Marshals.
func sanitizeFindingUTF8(f *analysisdomain.SecurityFinding) {
	if f == nil {
		return
	}
	if !utf8.ValidString(f.GetFile()) {
		f.File = strings.ToValidUTF8(f.GetFile(), "�")
	}
	if !utf8.ValidString(f.GetDescription()) {
		f.Description = strings.ToValidUTF8(f.GetDescription(), "�")
	}
	if !utf8.ValidString(f.GetSnippet()) {
		f.Snippet = strings.ToValidUTF8(f.GetSnippet(), "�")
	}
	if !utf8.ValidString(f.GetRule()) {
		f.Rule = strings.ToValidUTF8(f.GetRule(), "�")
	}
}
