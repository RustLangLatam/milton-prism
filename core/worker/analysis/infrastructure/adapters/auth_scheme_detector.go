package adapters

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.AuthSchemeDetector = (*AuthSchemeDetector)(nil)

// AuthSchemeDetector deterministically identifies the request-authentication
// scheme the analysed backend uses (JWT, OAuth2, session cookie, API key, Basic,
// or an honest "none"). It is a pure function of declared packages, committed
// config (.env), the Authorization/Bearer header convention, and the framework
// default — NO LLM, NO source execution. It is intentionally conservative
// (Canon Lesson 11): a scheme is reported only when a concrete signal names it;
// otherwise scheme=NONE with Unknown=true (an honest "I don't know whether this
// code authenticates"), never a guess.
//
// Signal precedence (most authoritative first; the winner sets the scheme):
//  1. auth packages declared in parsed manifests — the code literally links the
//     auth library (firebase/php-jwt, PyJWT, jsonwebtoken, jjwt, authlib, …);
//  2. config files in the workspace (.env JWT_SECRET / JWT_PUBLIC_KEY / JWT_ALGO);
//  3. the Authorization: Bearer header convention observed in source;
//  4. a framework default (last resort, clearly labelled).
//
// For JWT the signature-algorithm variant is derived: HS* when a symmetric secret
// (JWT_SECRET) is configured, RS*/ES*/EdDSA when a PEM/public key or an explicit
// JWT_ALGO names an asymmetric family. It mirrors the workspace walk/exclusions of
// the SecurityScanner so vendored/test trees never produce a false signal.
type AuthSchemeDetector struct{}

// NewAuthSchemeDetector returns a ready AuthSchemeDetector.
func NewAuthSchemeDetector() *AuthSchemeDetector { return &AuthSchemeDetector{} }

// authSchemeDisplay maps a scheme enum to its human-readable name.
var authSchemeDisplay = map[analysisdomain.AuthScheme]string{
	analysisdomain.AuthSchemeNone:          "None",
	analysisdomain.AuthSchemeJWT:           "JWT",
	analysisdomain.AuthSchemeOAuth2:        "OAuth2",
	analysisdomain.AuthSchemeSessionCookie: "Session cookie",
	analysisdomain.AuthSchemeAPIKey:        "API key",
	analysisdomain.AuthSchemeBasic:         "HTTP Basic",
}

// authPackageRule matches an auth package by whole-token substring against the
// lowercased package name. Each rule names the scheme it implies plus the evidence.
type authPackageRule struct {
	substr   string
	scheme   analysisdomain.AuthScheme
	evidence string
}

// authPackageRules is the deterministic auth-package detection table, covering
// the four manifest ecosystems the analysis supports. JWT libraries dominate;
// OAuth2/Basic are recognised by their own libraries. Order is for evidence
// stability only — every matching rule contributes.
var authPackageRules = []authPackageRule{
	// ── JWT ──────────────────────────────────────────────────────────────────
	// PHP / Composer
	{"firebase/php-jwt", analysisdomain.AuthSchemeJWT, "package: firebase/php-jwt (JWT)"},
	{"tymon/jwt-auth", analysisdomain.AuthSchemeJWT, "package: tymon/jwt-auth (JWT)"},
	{"lcobucci/jwt", analysisdomain.AuthSchemeJWT, "package: lcobucci/jwt (JWT)"},
	{"php-jwt", analysisdomain.AuthSchemeJWT, "package: php-jwt (JWT)"},
	// Python / PyPI
	{"flask-jwt-extended", analysisdomain.AuthSchemeJWT, "package: flask-jwt-extended (JWT)"},
	{"djangorestframework-simplejwt", analysisdomain.AuthSchemeJWT, "package: djangorestframework-simplejwt (JWT)"},
	{"simplejwt", analysisdomain.AuthSchemeJWT, "package: simplejwt (JWT)"},
	{"python-jose", analysisdomain.AuthSchemeJWT, "package: python-jose (JWT)"},
	{"pyjwt", analysisdomain.AuthSchemeJWT, "package: PyJWT (JWT)"},
	// Node / npm
	{"jsonwebtoken", analysisdomain.AuthSchemeJWT, "package: jsonwebtoken (JWT)"},
	{"passport-jwt", analysisdomain.AuthSchemeJWT, "package: passport-jwt (JWT)"},
	{"@nestjs/jwt", analysisdomain.AuthSchemeJWT, "package: @nestjs/jwt (JWT)"},
	{"jose", analysisdomain.AuthSchemeJWT, "package: jose (JWT)"},
	// Java / Maven
	{"jjwt", analysisdomain.AuthSchemeJWT, "package: jjwt (JWT)"},
	{"java-jwt", analysisdomain.AuthSchemeJWT, "package: java-jwt (JWT)"},
	{"nimbus-jose-jwt", analysisdomain.AuthSchemeJWT, "package: nimbus-jose-jwt (JWT)"},

	// ── OAuth2 / OIDC ──────────────────────────────────────────────────────────
	{"spring-boot-starter-oauth2-resource-server", analysisdomain.AuthSchemeOAuth2, "package: spring-oauth2-resource-server (OAuth2)"},
	{"spring-security-oauth2-resource-server", analysisdomain.AuthSchemeOAuth2, "package: spring-oauth2-resource-server (OAuth2)"},
	{"authlib", analysisdomain.AuthSchemeOAuth2, "package: authlib (OAuth2)"},
	{"oauthlib", analysisdomain.AuthSchemeOAuth2, "package: oauthlib (OAuth2)"},
	{"passport-oauth2", analysisdomain.AuthSchemeOAuth2, "package: passport-oauth2 (OAuth2)"},
	{"league/oauth2-server", analysisdomain.AuthSchemeOAuth2, "package: league/oauth2-server (OAuth2)"},
	{"laravel/passport", analysisdomain.AuthSchemeOAuth2, "package: laravel/passport (OAuth2)"},

	// ── Basic ───────────────────────────────────────────────────────────────────
	{"passport-http", analysisdomain.AuthSchemeBasic, "package: passport-http (HTTP Basic)"},
	{"flask-httpauth", analysisdomain.AuthSchemeBasic, "package: flask-httpauth (HTTP Basic)"},
}

// jwtSecretRe matches a symmetric JWT secret env key (⇒ HS*).
var jwtSecretRe = regexp.MustCompile(`(?mi)^\s*(JWT_SECRET|JWT_SECRET_KEY|JWT_KEY|SECRET_KEY_BASE|JWT_PASSPHRASE)\s*=`)

// jwtPublicKeyRe matches an asymmetric JWT key env key (⇒ RS*/ES*/EdDSA).
var jwtPublicKeyRe = regexp.MustCompile(`(?mi)^\s*(JWT_PUBLIC_KEY|JWT_PRIVATE_KEY|JWT_PUBLIC|JWT_PRIVATE|JWT_KEY_PATH)\s*=`)

// jwtAlgoRe extracts an explicit JWT_ALGO value (e.g. HS256, RS256, ES256, EdDSA).
var jwtAlgoRe = regexp.MustCompile(`(?mi)^\s*(?:JWT_ALGO|JWT_ALGORITHM)\s*=\s*['"]?([A-Za-z0-9]+)`)

// jwtAnyEnvRe matches any JWT_* env key (weak JWT signal when no package surfaced).
var jwtAnyEnvRe = regexp.MustCompile(`(?mi)^\s*JWT_[A-Z0-9_]*\s*=`)

// bearerRe matches the Authorization: Bearer header convention in source.
var bearerRe = regexp.MustCompile(`(?i)Authorization[^\n]{0,40}Bearer`)

// Detect returns the deterministically detected authentication scheme and the
// evidence behind it. Errors are never returned for missing/unreadable files —
// the detector degrades to whatever signals it could read; a no-signal result is
// the honest scheme=NONE, Unknown=true.
func (a *AuthSchemeDetector) Detect(
	_ context.Context,
	workspacePath string,
	deps []workerdomain.Dependency,
	technologies []*analysisdomain.Technology,
) (*analysisdomain.AuthSchemeDetection, error) {
	var (
		pkgScheme   = analysisdomain.AuthSchemeUnspecified
		pkgEvidence string
		evidence    []string
	)

	// ── Signal 1: auth packages (most authoritative) ────────────────────────────
	for _, dep := range deps {
		name := strings.ToLower(dep.Package)
		for _, r := range authPackageRules {
			if tokenContains(name, r.substr) {
				if pkgScheme == analysisdomain.AuthSchemeUnspecified || authSchemeRank(r.scheme) > authSchemeRank(pkgScheme) {
					pkgScheme = r.scheme
					pkgEvidence = r.evidence
				}
				evidence = appendUnique(evidence, r.evidence)
				break // first (most specific) rule per package
			}
		}
	}

	// ── Signal 2: config files (.env) ───────────────────────────────────────────
	hasSecret, hasPubKey, algo, hasAnyJWTEnv := a.scanAuthEnv(workspacePath)
	if hasSecret {
		evidence = appendUnique(evidence, "config: .env JWT secret (symmetric, HS*)")
	}
	if hasPubKey {
		evidence = appendUnique(evidence, "config: .env JWT public/private key (asymmetric, RS*/ES*/EdDSA)")
	}
	if algo != "" {
		evidence = appendUnique(evidence, "config: .env JWT_ALGO="+algo)
	}

	// ── Signal 3: Authorization: Bearer header convention ────────────────────────
	bearer := a.scanBearerHeader(workspacePath)
	if bearer {
		evidence = appendUnique(evidence, "header: Authorization: Bearer")
	}

	det := &analysisdomain.AuthSchemeDetection{}

	// Resolve the winning scheme by precedence: package > config > header > default.
	switch {
	case pkgScheme != analysisdomain.AuthSchemeUnspecified:
		det.Scheme = pkgScheme
		det.Confidence = 0.95
		_ = pkgEvidence
	case hasSecret || hasPubKey || algo != "" || hasAnyJWTEnv:
		// JWT_* config without an explicit package — still a concrete JWT signal.
		det.Scheme = analysisdomain.AuthSchemeJWT
		det.Confidence = 0.8
		if hasAnyJWTEnv && !hasSecret && !hasPubKey && algo == "" {
			evidence = appendUnique(evidence, "config: .env JWT_* configuration present")
		}
	case bearer:
		// A Bearer header with no JWT package/config is most likely JWT but weak.
		det.Scheme = analysisdomain.AuthSchemeJWT
		det.Confidence = 0.55
	default:
		// ── Signal 4: framework default (last resort) ───────────────────────────
		if scheme, ev := authFrameworkDefault(technologies); scheme != analysisdomain.AuthSchemeUnspecified {
			det.Scheme = scheme
			det.Confidence = 0.4
			evidence = appendUnique(evidence, ev)
		} else {
			// Honest 'none' — no auth signal at all.
			det.Scheme = analysisdomain.AuthSchemeNone
			det.Unknown = true
			det.Confidence = 0.0
		}
	}

	det.SchemeName = authSchemeDisplay[det.Scheme]

	// JWT signature-algorithm variant + token header.
	if det.Scheme == analysisdomain.AuthSchemeJWT {
		det.SignatureAlg = resolveJWTAlg(algo, hasSecret, hasPubKey)
		det.TokenHeader = "Authorization"
	}

	det.Evidence = evidence
	return det, nil
}

// authSchemeRank orders schemes so a more specific signal (JWT) wins over a more
// generic one (Basic) when a manifest declares packages for several.
func authSchemeRank(s analysisdomain.AuthScheme) int {
	switch s {
	case analysisdomain.AuthSchemeJWT:
		return 5
	case analysisdomain.AuthSchemeOAuth2:
		return 4
	case analysisdomain.AuthSchemeSessionCookie:
		return 3
	case analysisdomain.AuthSchemeAPIKey:
		return 2
	case analysisdomain.AuthSchemeBasic:
		return 1
	default:
		return 0
	}
}

// resolveJWTAlg picks the JWT signature-algorithm family from the strongest signal:
// an explicit JWT_ALGO wins; otherwise an asymmetric key ⇒ RS256, a symmetric secret
// ⇒ HS256. Empty when nothing indicates a variant (the generator then accepts the
// idiomatic default for the stack).
func resolveJWTAlg(algo string, hasSecret, hasPubKey bool) string {
	if algo != "" {
		return strings.ToUpper(algo)
	}
	if hasPubKey {
		return "RS256"
	}
	if hasSecret {
		return "HS256"
	}
	return ""
}

// scanAuthEnv reads .env (and .example/.dist fallbacks) for JWT key/algo config.
// Best-effort: missing files yield all-false. Returns whether a symmetric secret,
// an asymmetric key, an explicit algo, and any JWT_* key were seen.
func (a *AuthSchemeDetector) scanAuthEnv(workspacePath string) (hasSecret, hasPubKey bool, algo string, hasAnyJWTEnv bool) {
	if workspacePath == "" {
		return false, false, "", false
	}
	for _, name := range []string{".env", ".env.example", ".env.dist", ".env.sample"} {
		body, err := os.ReadFile(filepath.Join(workspacePath, name))
		if err != nil {
			continue
		}
		text := string(body)
		if jwtSecretRe.MatchString(text) {
			hasSecret = true
		}
		if jwtPublicKeyRe.MatchString(text) {
			hasPubKey = true
		}
		if m := jwtAlgoRe.FindStringSubmatch(text); m != nil {
			algo = m[1]
		}
		if jwtAnyEnvRe.MatchString(text) {
			hasAnyJWTEnv = true
		}
		return hasSecret, hasPubKey, algo, hasAnyJWTEnv // first existing env file wins
	}
	return false, false, "", false
}

// scanBearerHeader walks the workspace for the Authorization: Bearer convention in
// source. It reuses the SecurityScanner's directory/path exclusions so vendored and
// test trees never produce a false signal. Stops at the first match (cheap).
func (a *AuthSchemeDetector) scanBearerHeader(workspacePath string) bool {
	if workspacePath == "" {
		return false
	}
	found := false
	_ = filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			if found {
				return filepath.SkipAll
			}
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isScannable(d.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(workspacePath, path)
		if relErr != nil {
			rel = path
		}
		if isExcludedPath(filepath.ToSlash(rel)) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() == 0 || info.Size() > maxScanFileBytes {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if bearerRe.Match(body) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// authFrameworkDefault returns the auth scheme a framework imposes by default when
// no package/config/header signal surfaced. Conservative: only frameworks whose
// default is unambiguous are listed, and the confidence is low (0.4) at the call site.
func authFrameworkDefault(technologies []*analysisdomain.Technology) (analysisdomain.AuthScheme, string) {
	// Laravel/Symfony/Django/Rails default to server-side session cookies for the
	// stateful web guard. We only assert this when the framework is present AND no
	// stronger signal won — so a Laravel API using tymon/jwt-auth already short-circuited.
	for _, slug := range []string{"laravel", "symfony", "django", "rails", "codeigniter"} {
		if hasFramework(technologies, slug) {
			return analysisdomain.AuthSchemeSessionCookie, "framework default: " + slug + " ⇒ session cookie"
		}
	}
	return analysisdomain.AuthSchemeUnspecified, ""
}
