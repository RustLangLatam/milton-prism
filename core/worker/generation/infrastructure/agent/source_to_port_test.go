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
// the certified Go profile, and is empty (agent-exit fallback) for uncertified
// profiles so the change never regresses them.
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

	// Uncertified profile: no command → invoker falls back to the agent's own exit.
	_, okPy := agent.VerifyCommandFor("python", "grpc", "articles")
	assert.False(t, okPy, "uncertified profile must not claim a deterministic gate")

	// Empty service name is never gated.
	_, okEmpty := agent.VerifyCommandFor("go", "grpc", "")
	assert.False(t, okEmpty)
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
