package domain_test

import (
	"testing"

	"milton_prism/core/services/migration/domain"
)

// TestIsGenerableHttpFramework pins the HTTP-framework matrix: the sub-axis only
// applies to HTTP (gRPC always passes), HTTP+UNSPECIFIED always passes (it
// canonicalises), and a concrete HTTP framework passes only when the (language,
// framework) cell is in supportedHttpFrameworkByLanguage.
func TestIsGenerableHttpFramework(t *testing.T) {
	cases := []struct {
		name      string
		lang      domain.TargetLanguage
		transport domain.Transport
		fw        domain.HttpFramework
		want      bool
	}{
		{"grpc ignores framework", domain.TargetLanguageGo, domain.TransportGRPC, domain.HttpFrameworkGoGin, true},
		{"http unspecified canonicalises", domain.TargetLanguageGo, domain.TransportHTTP, domain.HttpFrameworkUnspecified, true},
		{"go http net/http generable", domain.TargetLanguageGo, domain.TransportHTTP, domain.HttpFrameworkGoNetHTTP, true},
		{"go http gin generable", domain.TargetLanguageGo, domain.TransportHTTP, domain.HttpFrameworkGoGin, true},
		{"go http echo not generable", domain.TargetLanguageGo, domain.TransportHTTP, domain.HttpFrameworkGoEcho, false},
		{"go http chi not generable", domain.TargetLanguageGo, domain.TransportHTTP, domain.HttpFrameworkGoChi, false},
		{"go http fiber not generable", domain.TargetLanguageGo, domain.TransportHTTP, domain.HttpFrameworkGoFiber, false},
		{"python http gin not generable (no go-cell for python)", domain.TargetLanguagePython, domain.TransportHTTP, domain.HttpFrameworkGoGin, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.IsGenerableHttpFramework(tc.lang, tc.transport, tc.fw); got != tc.want {
				t.Errorf("IsGenerableHttpFramework(%v,%v,%v) = %v, want %v", tc.lang, tc.transport, tc.fw, got, tc.want)
			}
		})
	}
}

// TestCanonicalHttpFramework pins the canonicalisation: gRPC always yields
// UNSPECIFIED (ignored), HTTP+UNSPECIFIED yields the language default
// (Go → net/http), and HTTP+concrete is returned as-is.
func TestCanonicalHttpFramework(t *testing.T) {
	cases := []struct {
		name      string
		lang      domain.TargetLanguage
		transport domain.Transport
		fw        domain.HttpFramework
		want      domain.HttpFramework
	}{
		{"grpc forces unspecified", domain.TargetLanguageGo, domain.TransportGRPC, domain.HttpFrameworkGoGin, domain.HttpFrameworkUnspecified},
		{"http unspecified → go default", domain.TargetLanguageGo, domain.TransportHTTP, domain.HttpFrameworkUnspecified, domain.HttpFrameworkGoNetHTTP},
		{"http gin kept", domain.TargetLanguageGo, domain.TransportHTTP, domain.HttpFrameworkGoGin, domain.HttpFrameworkGoGin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.CanonicalHttpFramework(tc.lang, tc.transport, tc.fw); got != tc.want {
				t.Errorf("CanonicalHttpFramework(%v,%v,%v) = %v, want %v", tc.lang, tc.transport, tc.fw, got, tc.want)
			}
		})
	}
}
