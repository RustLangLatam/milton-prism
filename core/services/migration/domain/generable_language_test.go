package domain

import "testing"

// TestIsGenerableLanguage_Node proves the Node and Rust target languages are now
// generable (their profiles were filled: profile doc + generator prompt +
// assembler skeleton), so CreateMigration no longer rejects them with MIG107
// Unsupported_Target_Language. Go and Python remain generable; only the
// unspecified zero value remains a hole.
func TestIsGenerableLanguage_Node(t *testing.T) {
	cases := []struct {
		name string
		lang TargetLanguage
		want bool
	}{
		{"go", TargetLanguageGo, true},
		{"python", TargetLanguagePython, true},
		{"node", TargetLanguageNode, true}, // filled profile
		{"rust", TargetLanguageRust, true}, // filled profile (E10)
		{"java", TargetLanguageJava, true}, // ← the new filled profile (5th language)
		{"unspecified", TargetLanguageUnspecified, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsGenerableLanguage(tc.lang); got != tc.want {
				t.Errorf("IsGenerableLanguage(%v) = %v, want %v", tc.lang, got, tc.want)
			}
		})
	}
}
