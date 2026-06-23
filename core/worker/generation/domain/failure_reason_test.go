package domain

import (
	"strings"
	"testing"
)

// realMig30Blob is the actual raw stdout captured from a failed agent run
// (mig30, service "user"): the full Claude Code JSON envelope.
const realMig30Blob = `{"type":"result","subtype":"success","is_error":true,` +
	`"result":"FAIL: Error: Unable to connect to API (ConnectionRefused)",` +
	`"stop_reason":"stop_sequence",` +
	`"session_id":"a1b2c3d4-5678-90ab-cdef-1234567890ab",` +
	`"total_cost_usd":1.88,` +
	`"usage":{"input_tokens":1234,"cache_creation_input_tokens":5678,` +
	`"cache_read_input_tokens":9012,"output_tokens":3456},` +
	`"modelUsage":{"claude-opus-4-8[1m]":{"inputTokens":1234,"outputTokens":3456,` +
	`"costUSD":1.88}}}`

func assertClean(t *testing.T, got string) {
	t.Helper()
	if len(got) > MaxFailureReasonLen {
		t.Errorf("reason too long: %d > %d: %q", len(got), MaxFailureReasonLen, got)
	}
	for _, bad := range []string{"{", "}", "session_id", "total_cost_usd", "modelUsage", "modelusage", "usage", "input_tokens"} {
		if strings.Contains(strings.ToLower(got), strings.ToLower(bad)) {
			t.Errorf("reason leaks sensitive/raw token %q: %q", bad, got)
		}
	}
}

func TestSanitizeFailureReason_RealMig30Blob(t *testing.T) {
	got := SanitizeFailureReason(realMig30Blob)
	assertClean(t, got)
	want := "El agente de generación no pudo conectarse a la API (reintentar)."
	if got != want {
		t.Fatalf("connection-refused blob: got %q, want %q", got, want)
	}
}

func TestSanitizeFailureReason_KnownPatterns(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"rate limit", "anthropic API error: rate_limit_error (429)", "El agente de generación fue limitado por el proveedor de la API (reintentar)."},
		{"overloaded", "Error: model overloaded, try again", "El agente de generación fue limitado por el proveedor de la API (reintentar)."},
		{"timeout", "context deadline exceeded after 600s", "La generación del servicio expiró por tiempo de espera."},
		{"auth", "Error: invalid api key (401 Unauthorized)", "El agente de generación no pudo autenticarse con la API."},
		{"connrefused node", "Error: connect ECONNREFUSED 127.0.0.1:443", "El agente de generación no pudo conectarse a la API (reintentar)."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeFailureReason(tc.raw)
			assertClean(t, got)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeFailureReason_UnknownJSONIsGeneric(t *testing.T) {
	raw := `{"type":"result","result":"something weird happened","total_cost_usd":0.42,"session_id":"xyz"}`
	got := SanitizeFailureReason(raw)
	assertClean(t, got)
	want := "La generación del servicio falló por un error técnico."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSanitizeFailureReason_ShortPlainMessagePreserved(t *testing.T) {
	raw := "FAIL: tsc found 3 type errors in src/server.ts"
	got := SanitizeFailureReason(raw)
	assertClean(t, got)
	if got != raw {
		t.Fatalf("plain message should be preserved: got %q, want %q", got, raw)
	}
}

func TestSanitizeFailureReason_LongPlainMessageCapped(t *testing.T) {
	raw := strings.Repeat("a", 500)
	got := SanitizeFailureReason(raw)
	if len(got) > MaxFailureReasonLen {
		t.Fatalf("not capped: len=%d", len(got))
	}
}

func TestSanitizeFailureReason_EmptyIsGeneric(t *testing.T) {
	if got := SanitizeFailureReason(""); got != "La generación del servicio falló por un error técnico." {
		t.Fatalf("empty: got %q", got)
	}
}
