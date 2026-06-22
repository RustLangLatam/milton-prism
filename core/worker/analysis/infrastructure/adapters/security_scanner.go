package adapters

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.SecurityScanner = (*SecurityScanner)(nil)

// SecurityScanner deterministically detects code-level security findings IN the
// analysed source — today, hardcoded credentials/secrets in cleartext. It is a pure
// function of file contents: it walks the workspace, runs a fixed pattern table plus
// an entropy heuristic over each candidate source/config file, and reports a finding
// only when a concrete secret-shaped value surfaces.
//
// Honesty (Canon Lesson 11): the scanner is conservative by construction.
//   - High-signal provider patterns (AWS keys, private keys, JWTs, sk-/ghp_ tokens)
//     fire at high confidence — these tokens have no benign reading.
//   - Generic assignments (password/secret/api_key = "...") fire only when the value
//     is NOT a recognisable placeholder/example, is long enough, and is not an obvious
//     env/template reference. Their confidence is lower (MEDIUM).
//   - Placeholders, examples, and template references are explicitly suppressed so the
//     scanner does not produce false positives on `password = "your-password-here"`,
//     `API_KEY=changeme`, `${SECRET}`, `env('APP_KEY')`, etc.
//
// Secrets are always redacted in the reported snippet; the raw value is never echoed.
type SecurityScanner struct{}

// NewSecurityScanner returns a ready SecurityScanner.
func NewSecurityScanner() *SecurityScanner { return &SecurityScanner{} }

// ── Tunables ────────────────────────────────────────────────────────────────────

const (
	// maxScanFileBytes caps how much of any single file is scanned. Secrets live near
	// the top of config; this bounds the cost of a pathological large file.
	maxScanFileBytes = 1 << 20 // 1 MiB
	// maxLineLen skips minified/blob lines (data URIs, bundled JS) that produce only
	// high-entropy noise, never real assigned secrets.
	maxLineLen = 600
	// genericMinValueLen is the minimum length a generic secret value must have before
	// a generic password/api_key assignment is treated as a real secret.
	genericMinValueLen = 8
	// entropyMinValueLen is the minimum length for a value to be entropy-tested.
	entropyMinValueLen = 20
	// entropyBitsThreshold is the Shannon-entropy (bits/char) threshold above which a
	// long opaque value is considered key-like rather than prose.
	entropyBitsThreshold = 3.6
)

// scannableExts are the file extensions the scanner reads. Source + common config
// formats; binaries, images, and lockfiles are skipped.
var scannableExts = map[string]bool{
	".php": true, ".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".go": true, ".rb": true, ".java": true, ".cs": true, ".env": true,
	".yml": true, ".yaml": true, ".json": true, ".ini": true, ".cfg": true,
	".conf": true, ".properties": true, ".toml": true, ".xml": true, ".sh": true,
	".tf": true,
}

// scannableNames are exact filenames worth scanning even without a known extension
// (dotfiles like .env, .env.local).
var scannableNames = map[string]bool{
	".env": true, ".env.local": true, ".env.production": true,
	".env.development": true, ".netrc": true,
}

// skipDirs are directories never worth scanning: dependency trees, VCS, build output,
// and vendored frameworks (a secret in vendored third-party code is not THIS repo's
// finding, and produces huge false-positive volume).
//
// i18n / translation trees (lang, locale, locales, translations, i18n) are skipped
// for the same honesty reason: their string tables are natural-language UI labels
// (`'password' => 'Wrong password'`) whose key names collide with the generic
// credential rule but whose values are translations, never secrets. Including them
// produced hundreds of false positives (e.g. BookStack: 503 of 531 findings lived in
// lang/). Test/fixture trees are excluded by isExcludedPath (path-aware) below.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "venv": true,
	".venv": true, "dist": true, "build": true, "__pycache__": true,
	"site-packages": true, "bower_components": true, ".idea": true,
	"target": true,
	// i18n / translation string tables — UI labels, not secrets.
	"lang": true, "locale": true, "locales": true, "translations": true,
	"i18n": true, "languages": true,
}

// excludedPathComponents are path components that mark a file as a test, fixture,
// example, or demo. A secret-shaped value in such a file is overwhelmingly a sample
// or fixture, not a production credential (Lesson 11: avoid false positives). Matched
// against whole, lowercased path components so "contests/" never matches "tests".
var excludedPathComponents = map[string]bool{
	"tests": true, "test": true, "__tests__": true, "spec": true, "specs": true,
	"fixtures": true, "fixture": true, "testdata": true, "mocks": true,
	"examples": true, "example": true, "demo": true, "demos": true, "samples": true,
}

// excludedFileSuffixes mark example/template config files: a value in `.env.example`
// or `config.dist.php` is a documented sample, not a live secret.
var excludedFileSuffixes = []string{
	".example", ".dist", ".sample", ".template", ".tmpl", ".tpl",
}

// isExcludedPath reports whether a workspace-relative path (forward-slash separated)
// belongs to a test/fixture/example/demo tree or is an example/template config file.
// Such locations are excluded from the security scan to avoid false positives on
// sample credentials.
func isExcludedPath(rel string) bool {
	lower := strings.ToLower(rel)
	for _, suf := range excludedFileSuffixes {
		if strings.HasSuffix(lower, suf) {
			return true
		}
		// Also catch `.env.example.php`, `config.dist.yml`, etc. (suffix before ext).
		if i := strings.LastIndex(lower, suf+"."); i != -1 {
			return true
		}
	}
	for _, part := range strings.Split(lower, "/") {
		if excludedPathComponents[part] {
			return true
		}
	}
	return false
}

// ── Detection rules ──────────────────────────────────────────────────────────────

// patternRule is a high-signal regex detector for a recognisable secret shape. The
// matched value has no benign reading, so these fire at high confidence regardless of
// the surrounding key name.
type patternRule struct {
	rule        string
	description string
	re          *regexp.Regexp
	severity    analysisdomain.SecuritySeverity
	confidence  float32
	// group is the submatch index holding the secret value to redact (0 = whole match).
	group int
}

var patternRules = []patternRule{
	{
		rule:        "aws-access-key-id",
		description: "Hardcoded AWS access key ID",
		re:          regexp.MustCompile(`\b((?:AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA)[0-9A-Z]{16})\b`),
		severity:    analysisdomain.SecuritySeverityHigh,
		confidence:  0.95,
		group:       1,
	},
	{
		rule:        "aws-secret-access-key",
		description: "Hardcoded AWS secret access key",
		re:          regexp.MustCompile(`(?i)aws_secret_access_key\s*[:=]\s*['"]?([A-Za-z0-9/+=]{40})['"]?`),
		severity:    analysisdomain.SecuritySeverityHigh,
		confidence:  0.9,
		group:       1,
	},
	{
		rule:        "private-key-pem",
		description: "Hardcoded private key (PEM block)",
		re:          regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`),
		severity:    analysisdomain.SecuritySeverityHigh,
		confidence:  0.97,
		group:       0,
	},
	{
		rule:        "openai-api-key",
		description: "Hardcoded OpenAI-style API key (sk-…)",
		re:          regexp.MustCompile(`\b(sk-[A-Za-z0-9]{20,})\b`),
		severity:    analysisdomain.SecuritySeverityHigh,
		confidence:  0.9,
		group:       1,
	},
	{
		rule:        "github-token",
		description: "Hardcoded GitHub token",
		re:          regexp.MustCompile(`\b((?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{20,})\b`),
		severity:    analysisdomain.SecuritySeverityHigh,
		confidence:  0.95,
		group:       1,
	},
	{
		rule:        "slack-token",
		description: "Hardcoded Slack token",
		re:          regexp.MustCompile(`\b(xox[baprs]-[A-Za-z0-9-]{10,})\b`),
		severity:    analysisdomain.SecuritySeverityHigh,
		confidence:  0.92,
		group:       1,
	},
	{
		rule:        "google-api-key",
		description: "Hardcoded Google API key",
		re:          regexp.MustCompile(`\b(AIza[A-Za-z0-9_\-]{35})\b`),
		severity:    analysisdomain.SecuritySeverityHigh,
		confidence:  0.9,
		group:       1,
	},
	{
		rule:        "stripe-secret-key",
		description: "Hardcoded Stripe secret key",
		re:          regexp.MustCompile(`\b((?:sk|rk)_live_[A-Za-z0-9]{20,})\b`),
		severity:    analysisdomain.SecuritySeverityHigh,
		confidence:  0.95,
		group:       1,
	},
	{
		rule:        "jwt-token",
		description: "Hardcoded JSON Web Token (JWT)",
		re:          regexp.MustCompile(`\b(eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})\b`),
		severity:    analysisdomain.SecuritySeverityHigh,
		confidence:  0.85,
		group:       1,
	},
	{
		rule:        "db-connection-string",
		description: "Hardcoded credentials in a database connection string",
		re:          regexp.MustCompile(`\b([a-z][a-z0-9+]*://[^:/\s'"]+:([^@/\s'"]+)@[^\s'"]+)`),
		severity:    analysisdomain.SecuritySeverityHigh,
		confidence:  0.85,
		group:       1,
	},
}

// genericAssignRe matches a generic credential assignment: a key whose name contains
// password/secret/passwd/api_key/apikey/token/access_key, assigned a quoted literal.
// PHP `=>` and `:` (YAML/JSON) and `=` (env/ini/code) forms are all matched.
// The key may be followed by a closing array-access bracket/quote (PHP
// $cfg['password'] = …, JS obj["apiKey"] = …) before the assignment operator.
var genericAssignRe = regexp.MustCompile(
	`(?i)\b([a-z0-9_\-]*(?:passwd|password|secret|api[_\-]?key|access[_\-]?key|auth[_\-]?token|client[_\-]?secret|token))\b['"]?\s*[\]\)]?\s*(?:=>|=|:)\s*['"]([^'"]+)['"]`,
)

// placeholderValues are values that are clearly NOT real secrets. A generic match
// whose value (lowercased) is one of these is suppressed (no false positive).
var placeholderValues = map[string]bool{
	"": true, "password": true, "passwd": true, "secret": true, "changeme": true,
	"change_me": true, "changethis": true, "your_password": true, "your-password": true,
	"yourpassword": true, "your_password_here": true, "your-password-here": true,
	"example": true, "test": true, "test123": true, "todo": true, "tbd": true,
	"none": true, "null": true, "nil": true, "false": true, "true": true,
	"xxx": true, "xxxx": true, "xxxxx": true, "placeholder": true, "redacted": true,
	"your_api_key": true, "your-api-key": true, "your_api_key_here": true,
	"your_secret": true, "your-secret": true, "your_token": true, "your-token": true,
	"api_key": true, "apikey": true, "token": true, "string": true, "value": true,
	"foo": true, "bar": true, "baz": true, "abc123": true, "1234": true, "12345": true,
	"123456": true, "password123": true, "admin": true, "root": true, "default": true,
	"sample": true, "dummy": true, "mypassword": true, "secretkey": true, "secret_key": true,
}

// placeholderSubstrings, when present in a value, mark it as a template/example, not a
// real secret. Covers env interpolation, mustache/blade/jinja templates, and explicit
// "your-…-here" markers.
var placeholderSubstrings = []string{
	"${", "{{", "}}", "<%", "%>", "<your", "your_", "your-", "_here", "-here",
	"changeme", "change_me", "replace", "example.com", "placeholder", "xxxxxxxx",
	"...", "****", "redacted", "env(", "process.env", "getenv", "os.environ",
}

// Scan walks the workspace and returns the deterministically detected security
// findings, sorted stably by (file, line, rule). Unreadable files are skipped; the
// scan never fails the analysis (best-effort, returns whatever it could read).
func (s *SecurityScanner) Scan(_ context.Context, workspacePath string) ([]*analysisdomain.SecurityFinding, error) {
	if workspacePath == "" {
		return nil, nil
	}
	var findings []*analysisdomain.SecurityFinding
	// seen dedups identical (file,line,rule) findings (a line can match one rule once).
	seen := map[string]bool{}

	_ = filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
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
		info, err := d.Info()
		if err != nil || info.Size() == 0 || info.Size() > maxScanFileBytes {
			return nil
		}
		rel, relErr := filepath.Rel(workspacePath, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		// Skip test/fixture/example/demo trees and example/template config files:
		// secret-shaped values there are samples, not live credentials.
		if isExcludedPath(rel) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, f := range scanContent(rel, string(body)) {
			key := f.GetFile() + "\x00" + f.GetSnippet() + "\x00" + f.GetRule()
			if seen[key] {
				continue
			}
			seen[key] = true
			findings = append(findings, f)
		}
		return nil
	})

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].GetFile() != findings[j].GetFile() {
			return findings[i].GetFile() < findings[j].GetFile()
		}
		if findings[i].GetLine() != findings[j].GetLine() {
			return findings[i].GetLine() < findings[j].GetLine()
		}
		return findings[i].GetRule() < findings[j].GetRule()
	})
	return findings, nil
}

// scanContent runs every detector over the file content and returns the findings.
// Exposed at package scope (lowercase) so it can be unit-tested directly without a
// filesystem fixture.
func scanContent(file, content string) []*analysisdomain.SecurityFinding {
	var out []*analysisdomain.SecurityFinding
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if len(line) > maxLineLen {
			continue
		}
		lineNo := uint32(i + 1)

		// 1) High-signal pattern rules.
		for _, r := range patternRules {
			m := r.re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			value := m[r.group]
			if isPlaceholder(value) {
				continue
			}
			out = append(out, &analysisdomain.SecurityFinding{
				Type:        analysisdomain.SecurityFindingTypeHardcodedSecret,
				Severity:    r.severity,
				File:        file,
				Line:        lineNo,
				Description: r.description,
				Snippet:     redactLine(line, value),
				Rule:        r.rule,
				Confidence:  r.confidence,
			})
		}

		// 2) Generic credential assignments (key name + quoted literal).
		if m := genericAssignRe.FindStringSubmatch(line); m != nil {
			key, value := m[1], m[2]
			if !isPlaceholder(value) && len(value) >= genericMinValueLen && !looksLikeEnvRef(value) && !looksLikeProse(value) {
				// Severity/confidence rise when the value is long and high-entropy
				// (a random key-like blob), since prose passwords are weaker evidence.
				severity := analysisdomain.SecuritySeverityMedium
				confidence := float32(0.6)
				if len(value) >= entropyMinValueLen && shannonEntropyBits(value) >= entropyBitsThreshold {
					severity = analysisdomain.SecuritySeverityHigh
					confidence = 0.8
				}
				out = append(out, &analysisdomain.SecurityFinding{
					Type:        analysisdomain.SecurityFindingTypeHardcodedSecret,
					Severity:    severity,
					File:        file,
					Line:        lineNo,
					Description: "Hardcoded credential assigned to " + key,
					Snippet:     redactLine(line, value),
					Rule:        "generic-credential",
					Confidence:  confidence,
				})
			}
		}
	}
	return out
}

// isScannable reports whether a filename is worth reading.
func isScannable(name string) bool {
	if scannableNames[name] {
		return true
	}
	// Treat any .env* file as scannable (.env.local already covered, but also .env.prod).
	if strings.HasPrefix(name, ".env") {
		return true
	}
	return scannableExts[strings.ToLower(filepath.Ext(name))]
}

// isPlaceholder reports whether a value is clearly NOT a real secret (an example,
// template reference, or env interpolation). Conservative: when in doubt it returns
// false (i.e. treats the value as a candidate secret) only for the high-signal pattern
// rules, which already constrain the shape; for generic matches the length/entropy
// gates apply separately.
func isPlaceholder(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return true
	}
	lower := strings.ToLower(v)
	if placeholderValues[lower] {
		return true
	}
	for _, sub := range placeholderSubstrings {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	// A value that is all the same character (aaaa, 0000, ****) is a placeholder.
	if isRepeatedChar(v) {
		return true
	}
	return false
}

// looksLikeEnvRef reports whether a generic value is actually an environment / config
// reference rather than a literal secret (e.g. "DB_PASSWORD", "APP_SECRET"). Such a
// value is an UPPER_SNAKE identifier with no lowercase and no secret-like entropy — it
// names a variable, it is not the variable's value.
func looksLikeEnvRef(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return true
	}
	for _, r := range v {
		if !(r >= 'A' && r <= 'Z') && r != '_' && !(r >= '0' && r <= '9') {
			return false
		}
	}
	// All-uppercase identifier with an underscore: a config key name, not a secret.
	return strings.Contains(v, "_")
}

// looksLikeProse reports whether a generic value is natural-language text rather than
// a secret token. Real credentials (passwords, API keys, tokens) are compact and drawn
// from a restricted ASCII alphabet; they do NOT contain internal whitespace and are not
// written in a non-Latin script. UI strings and translation labels — the dominant false
// positive on i18n string tables (`'password' => 'Wrong password'`, `'パスワードが…'`) — do.
// Suppressing them removes the over-flagging without hiding real ASCII secrets.
//
// A value is treated as prose when it contains internal whitespace OR any non-ASCII
// rune (a real secret is ASCII; a translation label frequently is not). Leading/trailing
// whitespace is trimmed first so a padded ASCII token is still scanned.
func looksLikeProse(value string) bool {
	v := strings.TrimSpace(value)
	for _, r := range v {
		if r == ' ' || r == '\t' {
			return true // internal whitespace — not a token
		}
		if r > 127 {
			return true // non-ASCII script — a translation/label, not an ASCII secret
		}
	}
	return false
}

// redactLine returns line with secret replaced by a masked form that preserves a short
// prefix for recognisability but never echoes the full secret.
func redactLine(line, secret string) string {
	if secret == "" {
		return strings.TrimSpace(line)
	}
	masked := maskSecret(secret)
	return strings.TrimSpace(strings.Replace(line, secret, masked, 1))
}

// maskSecret keeps up to the first 4 runes and replaces the rest with a marker.
//
// Truncation is on RUNE boundaries, never bytes: slicing secret[:4] mid-rune on a
// multi-byte UTF-8 value (e.g. a translation string) yields an invalid-UTF-8 snippet,
// which proto.Marshal rejects ("string field contains invalid UTF-8") and which would
// otherwise hang the persistence path. Counting runes keeps the snippet valid UTF-8.
func maskSecret(secret string) string {
	prefix := secret
	if r := []rune(secret); len(r) > 4 {
		prefix = string(r[:4])
	}
	return prefix + "****REDACTED****"
}

// isRepeatedChar reports whether s is a single character repeated (and length ≥ 3).
func isRepeatedChar(s string) bool {
	if len(s) < 3 {
		return false
	}
	first := s[0]
	for i := 1; i < len(s); i++ {
		if s[i] != first {
			return false
		}
	}
	return true
}

// shannonEntropyBits returns the Shannon entropy of s in bits per character. Reserved
// for the entropy heuristic; kept deterministic and allocation-light.
func shannonEntropyBits(s string) float64 {
	if s == "" {
		return 0
	}
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	n := float64(len(s))
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}
