package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	analysisdomain "milton_prism/core/services/analysis/domain"
)

// findingByRule returns the first finding with the given rule, or nil.
func findingByRule(fs []*analysisdomain.SecurityFinding, rule string) *analysisdomain.SecurityFinding {
	for _, f := range fs {
		if f.GetRule() == rule {
			return f
		}
	}
	return nil
}

// containsRedaction asserts the secret value never leaks into the snippet.
func assertRedacted(t *testing.T, f *analysisdomain.SecurityFinding, secret string) {
	t.Helper()
	if f == nil {
		t.Fatalf("nil finding")
	}
	if strings.Contains(f.GetSnippet(), secret) {
		t.Fatalf("snippet leaked full secret %q: %q", secret, f.GetSnippet())
	}
	if !strings.Contains(f.GetSnippet(), "REDACTED") {
		t.Fatalf("snippet not redacted: %q", f.GetSnippet())
	}
}

// ── scanContent: positive detections ────────────────────────────────────────────

func TestScanContent_AWSSecretAccessKey_Detected(t *testing.T) {
	secret := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY1"
	content := "aws_secret_access_key = \"" + secret + "\"\n"
	fs := scanContent("config/aws.php", content)
	f := findingByRule(fs, "aws-secret-access-key")
	if f == nil {
		t.Fatalf("expected aws-secret-access-key finding, got %+v", fs)
	}
	if f.GetSeverity() != analysisdomain.SecuritySeverityHigh {
		t.Errorf("expected HIGH severity, got %v", f.GetSeverity())
	}
	if f.GetType() != analysisdomain.SecurityFindingTypeHardcodedSecret {
		t.Errorf("expected HARDCODED_SECRET type, got %v", f.GetType())
	}
	if f.GetLine() != 1 {
		t.Errorf("expected line 1, got %d", f.GetLine())
	}
	if f.GetConfidence() < 0.8 {
		t.Errorf("expected high confidence, got %v", f.GetConfidence())
	}
	assertRedacted(t, f, secret)
}

func TestScanContent_AWSAccessKeyID_Detected(t *testing.T) {
	content := "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\n"
	fs := scanContent(".env", content)
	if findingByRule(fs, "aws-access-key-id") == nil {
		t.Fatalf("expected aws-access-key-id finding, got %+v", fs)
	}
}

func TestScanContent_PrivateKeyPEM_Detected(t *testing.T) {
	content := "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----\n"
	fs := scanContent("keys/id_rsa", content)
	f := findingByRule(fs, "private-key-pem")
	if f == nil {
		t.Fatalf("expected private-key-pem finding, got %+v", fs)
	}
	if f.GetSeverity() != analysisdomain.SecuritySeverityHigh {
		t.Errorf("expected HIGH severity, got %v", f.GetSeverity())
	}
}

func TestScanContent_OpenAIKey_Detected(t *testing.T) {
	secret := "sk-abc123DEF456ghi789JKL012mno345"
	content := "client = OpenAI(api_key=\"" + secret + "\")\n"
	fs := scanContent("app.py", content)
	f := findingByRule(fs, "openai-api-key")
	if f == nil {
		t.Fatalf("expected openai-api-key finding, got %+v", fs)
	}
	assertRedacted(t, f, secret)
}

func TestScanContent_GitHubToken_Detected(t *testing.T) {
	content := "token = \"ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ012345\"\n"
	if findingByRule(scanContent("ci.sh", content), "github-token") == nil {
		t.Fatalf("expected github-token finding")
	}
}

func TestScanContent_JWT_Detected(t *testing.T) {
	content := "auth = \"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N\"\n"
	if findingByRule(scanContent("settings.py", content), "jwt-token") == nil {
		t.Fatalf("expected jwt-token finding")
	}
}

func TestScanContent_DBConnectionStringWithPassword_Detected(t *testing.T) {
	secret := "mysql://root:s3cr3tP4ss@db.internal:3306/app"
	content := "DATABASE_URL=" + secret + "\n"
	f := findingByRule(scanContent(".env", content), "db-connection-string")
	if f == nil {
		t.Fatalf("expected db-connection-string finding")
	}
	if f.GetSeverity() != analysisdomain.SecuritySeverityHigh {
		t.Errorf("expected HIGH severity, got %v", f.GetSeverity())
	}
}

func TestScanContent_GenericPassword_Detected(t *testing.T) {
	content := "$config['password'] = 'Tr0ub4dor&3xKcd';\n"
	f := findingByRule(scanContent("config.php", content), "generic-credential")
	if f == nil {
		t.Fatalf("expected generic-credential finding, got %+v", scanContent("config.php", content))
	}
	if f.GetSeverity() != analysisdomain.SecuritySeverityMedium {
		t.Errorf("expected MEDIUM severity for prose password, got %v", f.GetSeverity())
	}
	assertRedacted(t, f, "Tr0ub4dor&3xKcd")
}

func TestScanContent_GenericHighEntropySecret_HighSeverity(t *testing.T) {
	// A long, high-entropy generic value escalates to HIGH.
	content := "API_SECRET = \"a8Kd92Lf03Mz71Qp45Wx62Cv88Bn04Tr\"\n"
	f := findingByRule(scanContent("settings.py", content), "generic-credential")
	if f == nil {
		t.Fatalf("expected generic-credential finding")
	}
	if f.GetSeverity() != analysisdomain.SecuritySeverityHigh {
		t.Errorf("expected HIGH severity for high-entropy value, got %v", f.GetSeverity())
	}
}

// ── scanContent: honest negatives (NO false positives) ──────────────────────────

func TestScanContent_Placeholder_NotDetected(t *testing.T) {
	cases := []string{
		`password = "your-password-here"`,
		`API_KEY=changeme`,
		`secret: "CHANGEME"`,
		`$config['password'] = 'password';`,
		`token = "xxxxxxxx"`,
		`api_key = "YOUR_API_KEY"`,
		`db_password = "example"`,
		`secret = "TODO"`,
	}
	for _, line := range cases {
		fs := scanContent("config.php", line+"\n")
		if len(fs) != 0 {
			t.Errorf("placeholder produced a finding (false positive): %q -> %+v", line, fs)
		}
	}
}

func TestScanContent_EnvReference_NotDetected(t *testing.T) {
	// Values that are env/config references, not literal secrets.
	cases := []string{
		`'password' => env('DB_PASSWORD'),`,
		`API_KEY = process.env.API_KEY`,
		`secret = os.environ["APP_SECRET"]`,
		`password = "${DB_PASSWORD}"`,
		`api_key = "{{ vault_api_key }}"`,
		`db_password = "DB_PASSWORD"`,
	}
	for _, line := range cases {
		fs := scanContent("config.py", line+"\n")
		if len(fs) != 0 {
			t.Errorf("env reference produced a finding (false positive): %q -> %+v", line, fs)
		}
	}
}

func TestScanContent_CleanCode_NoFindings(t *testing.T) {
	content := `package main

import "fmt"

// computeTotal sums the order lines.
func computeTotal(prices []int) int {
	total := 0
	for _, p := range prices {
		total += p
	}
	return total
}

func main() {
	fmt.Println(computeTotal([]int{1, 2, 3}))
}
`
	fs := scanContent("main.go", content)
	if len(fs) != 0 {
		t.Fatalf("clean code produced findings: %+v", fs)
	}
}

func TestScanContent_ShortGenericValue_NotDetected(t *testing.T) {
	// Below genericMinValueLen: too short to be a credible secret.
	content := `password = "abc"`
	if fs := scanContent("c.php", content+"\n"); len(fs) != 0 {
		t.Errorf("short value produced a finding: %+v", fs)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────────

func TestIsPlaceholder(t *testing.T) {
	yes := []string{"", "changeme", "your-password-here", "${SECRET}", "aaaa", "****", "example.com/x", "REDACTED"}
	for _, v := range yes {
		if !isPlaceholder(v) {
			t.Errorf("expected placeholder: %q", v)
		}
	}
	no := []string{"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY1", "Tr0ub4dor&3xKcd"}
	for _, v := range no {
		if isPlaceholder(v) {
			t.Errorf("expected NOT placeholder: %q", v)
		}
	}
}

func TestMaskSecret_KeepsPrefixHidesRest(t *testing.T) {
	got := maskSecret("AKIAIOSFODNN7EXAMPLE")
	if !strings.HasPrefix(got, "AKIA") || !strings.Contains(got, "REDACTED") {
		t.Errorf("unexpected mask: %q", got)
	}
	if strings.Contains(got, "EXAMPLE") {
		t.Errorf("mask leaked tail: %q", got)
	}
}

// TestMaskSecret_MultibyteUTF8_StaysValid is the regression test for the BookStack
// hang: truncating a multi-byte UTF-8 value on a byte boundary produced an invalid-UTF-8
// snippet that made proto.Marshal fail and the persistence path loop forever. The mask
// must keep the snippet valid UTF-8 by truncating on rune boundaries.
func TestMaskSecret_MultibyteUTF8_StaysValid(t *testing.T) {
	// Japanese / Bengali strings with multi-byte runes (≥4 runes so truncation triggers).
	for _, secret := range []string{"パスワードが正しくありません", "প্রবেশপথ ভুল হয়েছে", "Ο κωδικός είναι λάθος"} {
		got := maskSecret(secret)
		if !utf8.ValidString(got) {
			t.Errorf("mask produced invalid UTF-8 for %q: %q", secret, got)
		}
		if !strings.Contains(got, "REDACTED") {
			t.Errorf("mask not redacted: %q", got)
		}
	}
}

// TestLooksLikeProse covers the value-shape gate that suppresses natural-language /
// translation values (the i18n over-flagging source).
func TestLooksLikeProse(t *testing.T) {
	prose := []string{"Wrong password", "パスワードが正しくありません", "Reset your token", "a b"}
	for _, v := range prose {
		if !looksLikeProse(v) {
			t.Errorf("expected prose: %q", v)
		}
	}
	tokens := []string{"Tr0ub4dor&3xKcd", "a8Kd92Lf03Mz71Qp45Wx", "sk-abc123DEF456"}
	for _, v := range tokens {
		if looksLikeProse(v) {
			t.Errorf("expected NOT prose (ASCII token): %q", v)
		}
	}
}

// TestScanContent_TranslationLabel_NotDetected asserts that an i18n string-table entry
// whose key matches the credential rule but whose value is a translation label is NOT
// flagged (BookStack lang/ files produced 503 such false positives before the fix).
func TestScanContent_TranslationLabel_NotDetected(t *testing.T) {
	cases := []string{
		`'password' => 'Wrong password provided',`,           // English prose
		`'password' => 'パスワードが正しくありません',`,                   // Japanese
		`'reset_password' => 'প্রবেশপথ পুনরায় সেট করুন',`,    // Bengali
		`'token' => 'Ο κωδικός είναι λάθος',`,                // Greek
	}
	for _, line := range cases {
		if fs := scanContent("lang/x/auth.php", line+"\n"); len(fs) != 0 {
			t.Errorf("translation label produced a finding (false positive): %q -> %+v", line, fs)
		}
	}
}

// TestScan_SkipsI18nAndTestTrees asserts the walk excludes lang/ translation tables and
// test/example trees while still flagging a real secret in app code.
func TestScan_SkipsI18nAndTestTrees(t *testing.T) {
	dir := t.TempDir()
	// Real secret in app code — must be found.
	writeFile(t, dir, "app/config.php", "<?php\n$db_password = 'Pr0dP4ss!w0rd#9';\n")
	// i18n label — must be skipped (lang/ dir).
	writeFile(t, dir, "lang/ja/auth.php", "<?php\nreturn ['password' => 'パスワードが正しくありません'];\n")
	// Test fixture with a secret-shaped value — must be skipped (tests/ dir).
	writeFile(t, dir, "tests/Fixtures/seed.php", "<?php\n$api_secret = 'a8Kd92Lf03Mz71Qp45Wx';\n")
	// Example config — must be skipped (.example suffix).
	writeFile(t, dir, ".env.example", "APP_PASSWORD=a8Kd92Lf03Mz71Qp45Wx\n")

	fs, err := NewSecurityScanner().Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if len(fs) != 1 {
		t.Fatalf("expected exactly 1 finding (app code only), got %d: %+v", len(fs), fs)
	}
	if fs[0].GetFile() != "app/config.php" {
		t.Errorf("expected finding in app/config.php, got %q", fs[0].GetFile())
	}
}

func TestIsExcludedPath(t *testing.T) {
	excluded := []string{
		"tests/auth_test.php", "app/Foo/__tests__/x.js", "spec/models/y.rb",
		"fixtures/data.json", "src/testdata/secrets.env", "examples/config.php",
		"demo/app.py", ".env.example", "config.dist.php", "settings.sample.yml",
	}
	for _, p := range excluded {
		if !isExcludedPath(p) {
			t.Errorf("expected excluded: %q", p)
		}
	}
	included := []string{
		"app/config.php", "src/auth/login.go", "contests/list.php", // not "tests"
		"config/database.php", ".env",
	}
	for _, p := range included {
		if isExcludedPath(p) {
			t.Errorf("expected NOT excluded: %q", p)
		}
	}
}

func TestLooksLikeEnvRef(t *testing.T) {
	if !looksLikeEnvRef("DB_PASSWORD") {
		t.Error("DB_PASSWORD should look like env ref")
	}
	if looksLikeEnvRef("Tr0ub4dor3") {
		t.Error("mixed-case literal should not look like env ref")
	}
}

// ── Scan (filesystem walk) ──────────────────────────────────────────────────────

func TestScan_WalksWorkspace_DetectsAndSkipsVendor(t *testing.T) {
	dir := t.TempDir()
	// A real secret in app code.
	writeFile(t, dir, "app/config.php", "<?php\n$db_password = 'Pr0dP4ss!w0rd#9';\n")
	// A secret inside vendor/ must be ignored (third-party, huge FP source).
	writeFile(t, dir, "vendor/lib/keys.php", "<?php\n$api_key = 'sk-vendorAAAA1111BBBB2222CCCC';\n")
	// A clean file.
	writeFile(t, dir, "app/util.php", "<?php\nfunction add($a,$b){return $a+$b;}\n")
	// A non-scannable binary-ish extension.
	writeFile(t, dir, "app/logo.png", "PNG\x00binarydata")

	scanner := NewSecurityScanner()
	fs, err := scanner.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if len(fs) != 1 {
		t.Fatalf("expected exactly 1 finding (app code only, vendor skipped), got %d: %+v", len(fs), fs)
	}
	if fs[0].GetFile() != "app/config.php" {
		t.Errorf("expected finding in app/config.php, got %q", fs[0].GetFile())
	}
	assertRedacted(t, fs[0], "Pr0dP4ss!w0rd#9")
}

func TestScan_EmptyWorkspace_NoFindingsNoError(t *testing.T) {
	fs, err := NewSecurityScanner().Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if len(fs) != 0 {
		t.Fatalf("expected 0 findings, got %+v", fs)
	}
}

func TestScan_EmptyPath_NoFindings(t *testing.T) {
	fs, err := NewSecurityScanner().Scan(context.Background(), "")
	if err != nil || fs != nil {
		t.Fatalf("expected nil,nil for empty path, got %+v, %v", fs, err)
	}
}

func TestScan_DeterministicOrdering(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b.env", "API_KEY=AKIAIOSFODNN7EXAMPLE\n")
	writeFile(t, dir, "a.env", "AWS_ACCESS_KEY_ID=AKIA1234567890ABCDEF\n")
	scanner := NewSecurityScanner()
	first, _ := scanner.Scan(context.Background(), dir)
	second, _ := scanner.Scan(context.Background(), dir)
	if len(first) != len(second) || len(first) == 0 {
		t.Fatalf("inconsistent counts: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].GetFile() != second[i].GetFile() || first[i].GetLine() != second[i].GetLine() {
			t.Fatalf("non-deterministic ordering at %d", i)
		}
	}
	// a.env sorts before b.env.
	if first[0].GetFile() != "a.env" {
		t.Errorf("expected a.env first (sorted), got %q", first[0].GetFile())
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
