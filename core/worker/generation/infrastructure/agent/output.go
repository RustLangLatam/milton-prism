package agent

import (
	"encoding/json"
	"strings"
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
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
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
