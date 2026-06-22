package application

import (
	"testing"
	"unicode/utf8"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"google.golang.org/protobuf/proto"
)

func mkFinding(file string, line uint32, sev analysisdomain.SecuritySeverity, conf float32) *analysisdomain.SecurityFinding {
	return &analysisdomain.SecurityFinding{
		Type:     analysisdomain.SecurityFindingTypeHardcodedSecret,
		Severity: sev,
		File:     file,
		Line:     line,
		Rule:     "generic-credential",
		Snippet:  "x = ****REDACTED****",
		Confidence: conf,
	}
}

func TestCapSecurityFindings_BelowLimit_Unchanged(t *testing.T) {
	in := []*analysisdomain.SecurityFinding{
		mkFinding("a.php", 1, analysisdomain.SecuritySeverityMedium, 0.6),
		mkFinding("b.php", 2, analysisdomain.SecuritySeverityHigh, 0.9),
	}
	kept, dropped := capSecurityFindings(in)
	if dropped != 0 || len(kept) != 2 {
		t.Fatalf("expected 2 kept / 0 dropped, got %d / %d", len(kept), dropped)
	}
}

func TestCapSecurityFindings_OverLimit_KeepsHighestSignal(t *testing.T) {
	var in []*analysisdomain.SecurityFinding
	// 250 LOW findings...
	for i := 0; i < 250; i++ {
		in = append(in, mkFinding("low.php", uint32(i+1), analysisdomain.SecuritySeverityLow, 0.3))
	}
	// ...plus a handful of HIGH findings that MUST survive the cap.
	for i := 0; i < 5; i++ {
		in = append(in, mkFinding("high.php", uint32(i+1), analysisdomain.SecuritySeverityHigh, 0.95))
	}
	kept, dropped := capSecurityFindings(in)
	if len(kept) != MaxPersistedSecurityFindings {
		t.Fatalf("expected %d kept, got %d", MaxPersistedSecurityFindings, len(kept))
	}
	if dropped != len(in)-MaxPersistedSecurityFindings {
		t.Fatalf("unexpected dropped=%d", dropped)
	}
	highKept := 0
	for _, f := range kept {
		if f.GetSeverity() == analysisdomain.SecuritySeverityHigh {
			highKept++
		}
	}
	if highKept != 5 {
		t.Errorf("all 5 HIGH findings must survive the cap, got %d", highKept)
	}
	// Kept set is in canonical (file,line,rule) order.
	for i := 1; i < len(kept); i++ {
		if kept[i-1].GetFile() > kept[i].GetFile() {
			t.Fatalf("kept set not ordered by file at %d", i)
		}
	}
}

// TestCapSecurityFindings_SanitizesInvalidUTF8 is the persistence-side regression guard:
// even if a finding carries invalid UTF-8, the capped output must proto.Marshal cleanly.
func TestCapSecurityFindings_SanitizesInvalidUTF8(t *testing.T) {
	bad := mkFinding("lang/ja/auth.php", 23, analysisdomain.SecuritySeverityMedium, 0.6)
	bad.Snippet = "'password' => 'প\xe0****REDACTED****'," // truncated multibyte → invalid UTF-8
	bad.Description = "desc\xff"

	kept, _ := capSecurityFindings([]*analysisdomain.SecurityFinding{bad})
	for _, f := range kept {
		if !utf8.ValidString(f.GetSnippet()) || !utf8.ValidString(f.GetDescription()) {
			t.Fatalf("expected sanitised UTF-8, snippet=%q desc=%q", f.GetSnippet(), f.GetDescription())
		}
	}
	if _, err := proto.Marshal(&analysisdomain.AnalysisSummary{SecurityFindings: kept}); err != nil {
		t.Fatalf("capped findings must marshal, got: %v", err)
	}
}
