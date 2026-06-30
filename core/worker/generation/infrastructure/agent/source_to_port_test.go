package agent_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"milton_prism/core/worker/generation/infrastructure/agent"
	"milton_prism/core/worker/generation/ports"
)

// TestVerifyCommandFor_GoScopedToService asserts the deterministic gate command
// compiles AND tests the generated service subtree (not the whole monorepo) for
// the certified Go profile.
func TestVerifyCommandFor_GoScopedToService(t *testing.T) {
	t.Parallel()

	cmd, ok := agent.VerifyCommandFor("go", "grpc", "articles")
	assert.True(t, ok, "go profile must have a wired deterministic gate")
	assert.Contains(t, cmd, "go build ./core/services/articles/...")
	assert.Contains(t, cmd, "go test ./core/services/articles/... -count=1")

	// Empty profile defaults to Go.
	cmd2, ok2 := agent.VerifyCommandFor("", "grpc", "articles")
	assert.True(t, ok2)
	assert.Equal(t, cmd, cmd2)

	// Empty service name is never gated.
	_, okEmpty := agent.VerifyCommandFor("go", "grpc", "")
	assert.False(t, okEmpty)

	// Truly unknown profile: no command → invoker falls back to the agent's own exit.
	_, okUnknown := agent.VerifyCommandFor("elixir", "grpc", "articles")
	assert.False(t, okUnknown, "unknown profile must not claim a deterministic gate")
}

// TestVerifyCommandFor_AllProfilesWired asserts every supported output profile now
// has a real deterministic gate (build + test) scoped to the generated service,
// matching the certified on-disk deliverable layout for that language. The command
// is identical for the gRPC and HTTP cells (only the generated code differs).
func TestVerifyCommandFor_AllProfilesWired(t *testing.T) {
	t.Parallel()

	cases := []struct {
		profile string
		want    []string // substrings the command MUST contain
	}{
		{"python", []string{"cd python", "compileall -q services/user", "pytest services/user/tests"}},
		{"node", []string{"cd node", "npm install", "npm run build", "npm test"}},
		{"rust", []string{"cargo build --manifest-path rust/services/user/Cargo.toml", "cargo test --manifest-path rust/services/user/Cargo.toml"}},
		{"java", []string{"cd java", "mvn -B -pl services/user -am test"}},
		{"ruby", []string{"cd ruby", "bundle install", "cd services/user && bundle exec rspec"}},
		{"csharp", []string{"cd csharp/services/user", "dotnet test Tests"}},
		{"cpp", []string{"cd cpp", "cmake --build build", "ctest --test-dir build"}},
	}
	for _, tc := range cases {
		for _, proto := range []string{"grpc", "http"} {
			cmd, ok := agent.VerifyCommandFor(tc.profile, proto, "user")
			assert.Truef(t, ok, "%s/%s must have a wired deterministic gate", tc.profile, proto)
			for _, want := range tc.want {
				assert.Containsf(t, cmd, want, "%s/%s gate must contain %q", tc.profile, proto, want)
			}
		}
		// Empty service name is never gated, for every profile.
		_, ok := agent.VerifyCommandFor(tc.profile, "grpc", "  ")
		assert.Falsef(t, ok, "%s with blank service must not be gated", tc.profile)
	}
}

// TestSourceToPortSection_PortsLogicAndOracle asserts the prompt block carries the
// domain source verbatim (the logic to port), the explicit "translate, do not
// invent/stub" instruction, and the behaviour-test-oracle contract.
func TestSourceToPortSection_PortsLogicAndOracle(t *testing.T) {
	t.Parallel()

	files := []ports.SourceFile{
		{
			Path: "conduit/articles/models.py", Lang: "python", Role: "domain",
			Content: "class Article:\n    def __init__(self, title):\n        self.slug = slugify(title)\n    def favourite(self, p):\n        self.favoriters.append(p)\n",
		},
		{
			Path: "tests/test_articles.py", Lang: "python", Role: "test",
			Content: "def test_create_article_slug():\n    assert Article('Hello World').slug == 'hello-world'\n",
		},
	}

	out := agent.SourceToPortSection("Go", files)

	assert.Contains(t, out, "## Source to Port")
	assert.Contains(t, out, "TRANSLATE faithfully")
	assert.Contains(t, out, "DO NOT invent")
	// Domain logic is embedded verbatim so the agent ports it.
	assert.Contains(t, out, "self.slug = slugify(title)")
	assert.Contains(t, out, "path=conduit/articles/models.py")
	// Behaviour-test oracle: the input test is present and flagged as part of the gate.
	assert.Contains(t, out, "## Behaviour Tests")
	assert.Contains(t, out, "PORT them to Go")
	assert.Contains(t, out, "HERMETIC")
	assert.Contains(t, out, "test_create_article_slug")

	// No captured source → empty block (contract-only degradation, no regression).
	assert.Empty(t, agent.SourceToPortSection("Go", nil))

	// With only domain (no tests) → instruct to GENERATE characterization tests.
	domainOnly := agent.SourceToPortSection("Go", files[:1])
	assert.Contains(t, domainOnly, "GENERATE characterization tests")
	assert.True(t, strings.Contains(domainOnly, "at least one per RPC"))
}
