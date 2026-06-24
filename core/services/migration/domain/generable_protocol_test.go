package domain

import "testing"

// TestIsGenerableProtocol proves the (language, transport) generation matrix: every
// generable language supports BOTH gRPC and HTTP — the HTTP matrix is complete
// (Go, Python, Node and Rust all have a certified HTTP cell). A non-generable
// language is false for every transport. This is the single source of truth
// behind the MIG109 guard in CreateMigration.
func TestIsGenerableProtocol(t *testing.T) {
	cases := []struct {
		name      string
		lang      TargetLanguage
		transport Transport
		want      bool
	}{
		{"go_grpc", TargetLanguageGo, TransportGRPC, true},
		{"go_http", TargetLanguageGo, TransportHTTP, true}, // ← the first HTTP cell
		{"python_grpc", TargetLanguagePython, TransportGRPC, true},
		{"python_http", TargetLanguagePython, TransportHTTP, true}, // ← FastAPI-native HTTP cell
		{"node_grpc", TargetLanguageNode, TransportGRPC, true},
		{"node_http", TargetLanguageNode, TransportHTTP, true}, // ← Fastify-native HTTP cell
		{"rust_grpc", TargetLanguageRust, TransportGRPC, true},
		{"rust_http", TargetLanguageRust, TransportHTTP, true}, // axum-native HTTP cell
		{"java_grpc", TargetLanguageJava, TransportGRPC, true},
		{"java_http", TargetLanguageJava, TransportHTTP, true}, // ← Spring Boot HTTP-native cell (matrix complete, 5 langs)
		{"unspecified_lang_grpc", TargetLanguageUnspecified, TransportGRPC, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsGenerableProtocol(tc.lang, tc.transport); got != tc.want {
				t.Errorf("IsGenerableProtocol(%v, %v) = %v, want %v", tc.lang, tc.transport, got, tc.want)
			}
		})
	}
}

// TestUnsupportedProtocolError pins the MIG109 code and Failure message (the
// exact MIG107 pattern) so the gateway error map and panel contract stay stable.
func TestUnsupportedProtocolError(t *testing.T) {
	if ErrCodeUnsupportedProtocol != "MIG109" {
		t.Errorf("ErrCodeUnsupportedProtocol = %q, want MIG109", ErrCodeUnsupportedProtocol)
	}
	if ErrUnsupportedProtocol.Code != "MIG109" {
		t.Errorf("ErrUnsupportedProtocol.Code = %q, want MIG109", ErrUnsupportedProtocol.Code)
	}
	if ErrUnsupportedProtocol.Message != "Failure_Unsupported_Protocol" {
		t.Errorf("ErrUnsupportedProtocol.Message = %q, want Failure_Unsupported_Protocol", ErrUnsupportedProtocol.Message)
	}
}
