package adapters_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthSchemeDetector_JWTFromPyJWTPackage(t *testing.T) {
	t.Parallel()
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemPyPI, "pyjwt"),
		dep(workerdomain.EcosystemPyPI, "flask"),
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, analysisdomain.AuthSchemeJWT, asd.GetScheme())
	assert.False(t, asd.GetUnknown())
	assert.Equal(t, "Authorization", asd.GetTokenHeader())
	assert.Contains(t, asd.GetEvidence(), "package: PyJWT (JWT)")
}

func TestAuthSchemeDetector_JWTFromFlaskJWTExtended(t *testing.T) {
	t.Parallel()
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemPyPI, "flask-jwt-extended"),
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, analysisdomain.AuthSchemeJWT, asd.GetScheme())
}

func TestAuthSchemeDetector_JWTFromComposerAndNpmAndMaven(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		eco workerdomain.Ecosystem
		pkg string
	}{
		{workerdomain.EcosystemComposer, "firebase/php-jwt"},
		{workerdomain.EcosystemComposer, "tymon/jwt-auth"},
		{workerdomain.EcosystemNpm, "jsonwebtoken"},
		{workerdomain.EcosystemNpm, "passport-jwt"},
		{workerdomain.EcosystemNpm, "@nestjs/jwt"},
		{workerdomain.EcosystemMaven, "jjwt"},
		{workerdomain.EcosystemMaven, "java-jwt"},
	} {
		d := adapters.NewAuthSchemeDetector()
		asd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{dep(tc.eco, tc.pkg)}, nil)
		require.NoError(t, err)
		assert.Equalf(t, analysisdomain.AuthSchemeJWT, asd.GetScheme(), "pkg %s", tc.pkg)
	}
}

func TestAuthSchemeDetector_OAuth2FromSpring(t *testing.T) {
	t.Parallel()
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemMaven, "spring-boot-starter-oauth2-resource-server"),
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, analysisdomain.AuthSchemeOAuth2, asd.GetScheme())
}

func TestAuthSchemeDetector_JWTBeatsBasicWhenBothPresent(t *testing.T) {
	t.Parallel()
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), "", []workerdomain.Dependency{
		dep(workerdomain.EcosystemNpm, "passport-http"),
		dep(workerdomain.EcosystemNpm, "jsonwebtoken"),
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, analysisdomain.AuthSchemeJWT, asd.GetScheme())
}

func TestAuthSchemeDetector_HSFromEnvSecret(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("JWT_SECRET=supersecretvalue123\n"), 0o600))
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), dir, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, analysisdomain.AuthSchemeJWT, asd.GetScheme())
	assert.Equal(t, "HS256", asd.GetSignatureAlg())
}

func TestAuthSchemeDetector_RSFromEnvPublicKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("JWT_PUBLIC_KEY=/etc/keys/pub.pem\n"), 0o600))
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), dir, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, analysisdomain.AuthSchemeJWT, asd.GetScheme())
	assert.Equal(t, "RS256", asd.GetSignatureAlg())
}

func TestAuthSchemeDetector_ExplicitJWTAlgoWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("JWT_SECRET=x\nJWT_ALGO=EdDSA\n"), 0o600))
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), dir, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, analysisdomain.AuthSchemeJWT, asd.GetScheme())
	assert.Equal(t, "EDDSA", asd.GetSignatureAlg())
}

func TestAuthSchemeDetector_BearerHeaderInSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"),
		[]byte("def handler(req):\n    token = req.headers.get('Authorization').split('Bearer ')[1]\n"), 0o600))
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), dir, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, analysisdomain.AuthSchemeJWT, asd.GetScheme())
	assert.Less(t, asd.GetConfidence(), float32(0.8))
}

func TestAuthSchemeDetector_HonestNone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc main() {}\n"), 0o600))
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), dir, []workerdomain.Dependency{
		dep(workerdomain.EcosystemNpm, "express"),
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, analysisdomain.AuthSchemeNone, asd.GetScheme())
	assert.True(t, asd.GetUnknown())
}

func TestAuthSchemeDetector_FrameworkSessionDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc main() {}\n"), 0o600))
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), dir, nil, []*analysisdomain.Technology{fwTech("laravel")})
	require.NoError(t, err)
	assert.Equal(t, analysisdomain.AuthSchemeSessionCookie, asd.GetScheme())
	assert.False(t, asd.GetUnknown())
}

func TestAuthSchemeDetector_VendoredTreeIgnored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	vendor := filepath.Join(dir, "node_modules", "lib")
	require.NoError(t, os.MkdirAll(vendor, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(vendor, "x.js"),
		[]byte("headers['Authorization'] = 'Bearer ' + t\n"), 0o600))
	d := adapters.NewAuthSchemeDetector()
	asd, err := d.Detect(context.Background(), dir, nil, nil)
	require.NoError(t, err)
	// A Bearer header buried in node_modules must NOT surface a scheme.
	assert.Equal(t, analysisdomain.AuthSchemeNone, asd.GetScheme())
	assert.True(t, asd.GetUnknown())
}
