package agent

import (
	"encoding/json"
	"strings"

	workerdomain "milton_prism/core/worker/generation/domain"
)

// credentialEnv returns the KEY=value string for the container Env slice.
// Detects the credential format by prefix so callers can supply either an
// ANTHROPIC_API_KEY (sk-ant-api03-…) or a claude.ai OAuth token (sk-ant-oat…).
// The returned string is never logged — see A.7.
func credentialEnv(cred string) string {
	if strings.HasPrefix(cred, "sk-ant-oat") {
		return "CLAUDE_CODE_OAUTH_TOKEN=" + cred
	}
	return "ANTHROPIC_API_KEY=" + cred
}

// claudeJSON is the JSON envelope that Claude Code emits with --output-format json.
type claudeJSON struct {
	Result       string      `json:"result"`
	TotalCostUSD float64     `json:"total_cost_usd"`
	Usage        claudeUsage `json:"usage"`
	// ModelUsage maps the model id (e.g. "claude-opus-4-8[1m]") to its per-model
	// token/cost breakdown. Present when one or more models were used in the run.
	ModelUsage map[string]claudeModelUsage `json:"modelUsage"`
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

// claudeModelUsage is the per-model breakdown under the "modelUsage" key.
type claudeModelUsage struct {
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CostUSD      float64 `json:"costUSD"`
}

// DominantModel returns the id of the model that consumed the most tokens
// (input+output) across the run. When the run used a single model this is just
// that model; when several were used (e.g. a sub-agent on a cheaper model) the
// one with the largest token footprint wins, which is the right attribution for
// a token-based cost estimate. Returns "" when no modelUsage was reported.
func (c claudeJSON) DominantModel() string {
	var best string
	var bestTokens int64 = -1
	for model, mu := range c.ModelUsage {
		tokens := mu.InputTokens + mu.OutputTokens
		// Deterministic tie-break by model id so the result is stable.
		if tokens > bestTokens || (tokens == bestTokens && model < best) {
			best = model
			bestTokens = tokens
		}
	}
	return best
}

// EffectiveInputTokens returns the total input token count across all billing
// tiers (fresh + cache-creation + cache-read). Use this for capacity planning;
// use total_cost_usd for actual billing — the three tiers have different rates.
func (u claudeUsage) EffectiveInputTokens() int64 {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// parseClaudeOutput extracts the structured fields from Claude Code's JSON
// stdout. The output may contain non-JSON preamble lines (progress messages)
// before the final JSON object — this function finds the last JSON object in
// the stream.
func parseClaudeOutput(stdout string) (*claudeJSON, error) {
	// Claude Code may emit progress text before the final JSON line.
	// Scan from the end for the last '{'-prefixed line.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var out claudeJSON
		if err := json.Unmarshal([]byte(line), &out); err == nil {
			return &out, nil
		}
	}
	// No valid JSON line found — attempt to parse the entire stdout.
	var out claudeJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SanitizeFailureReason reduces a raw agent failure blob to a short, clean,
// user-facing technical message. It delegates to the canonical implementation
// in the generation worker domain package. The raw blob must NEVER reach the
// user-visible failure_reason field — it is logged server-side instead.
func SanitizeFailureReason(raw string) string {
	return workerdomain.SanitizeFailureReason(raw)
}

// extractFailureReason scans the combined stdout+stderr of a failed agent run
// and returns the most informative snippet for triage.
func extractFailureReason(combined string) string {
	const maxLen = 2000
	// Look for gate-failure markers emitted by the generator prompt.
	keywords := []string{"FAIL", "Error", "error:", "cannot find", "undefined:"}
	for _, kw := range keywords {
		idx := strings.Index(combined, kw)
		if idx < 0 {
			continue
		}
		start := idx
		if start > 300 {
			start = idx - 300
		}
		end := idx + maxLen
		if end > len(combined) {
			end = len(combined)
		}
		return strings.TrimSpace(combined[start:end])
	}
	// Fall back to the tail of the output.
	if len(combined) > maxLen {
		return strings.TrimSpace(combined[len(combined)-maxLen:])
	}
	return strings.TrimSpace(combined)
}
