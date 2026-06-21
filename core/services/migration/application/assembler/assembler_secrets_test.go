package assembler

import "testing"

// TestAssertPayloadNoSecrets_CleanPayload_GrepZero proves the write-side gate
// returns grep=0 (nil) for a payload that carries no known dev credential.
func TestAssertPayloadNoSecrets_CleanPayload_GrepZero(t *testing.T) {
	payload := []File{
		{Path: "core/services/user/domain/domain.go", Content: []byte("package domain\n")},
		{Path: "core/cmd/user-services/config.toml.example", Content: []byte("[mongo]\nuri = \"mongodb://user:${MONGO_PASSWORD}@mongodb:27017\"\n")},
		{Path: "go.mod", Content: []byte("module example.com/app\n")},
	}
	if err := AssertPayloadNoSecrets(payload); err != nil {
		t.Fatalf("expected grep=0 (nil) on a clean payload, got: %v", err)
	}
}

// TestAssertPayloadNoSecrets_PlantedSecret_Detected proves the gate is NOT
// relaxed: a single file carrying a known dev credential is caught, and the
// error names the offending path (not the content).
func TestAssertPayloadNoSecrets_PlantedSecret_Detected(t *testing.T) {
	for _, secret := range knownSecrets {
		payload := []File{
			{Path: "core/services/user/domain/domain.go", Content: []byte("package domain\n")},
			{Path: "infra/leak.toml", Content: []byte("password = \"" + secret + "\"\n")},
		}
		err := AssertPayloadNoSecrets(payload)
		if err == nil {
			t.Fatalf("expected a leak to be detected for secret %q-prefix, got nil", secret[:6])
		}
		if got := err.Error(); !contains(got, "infra/leak.toml") {
			t.Fatalf("error must name the offending path; got: %v", got)
		}
		if contains(err.Error(), secret) {
			t.Fatalf("error must NOT echo the secret value; got: %v", err)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
