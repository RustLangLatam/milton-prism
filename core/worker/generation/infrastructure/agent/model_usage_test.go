package agent

import "testing"

// TestParseClaudeOutput_DominantModelSingle verifies a single-model run reports
// that model.
func TestParseClaudeOutput_DominantModelSingle(t *testing.T) {
	blob := `{"type":"result","result":"ok","total_cost_usd":1.88,` +
		`"usage":{"input_tokens":1234,"cache_creation_input_tokens":5678,` +
		`"cache_read_input_tokens":9012,"output_tokens":3456},` +
		`"modelUsage":{"claude-opus-4-8[1m]":{"inputTokens":1234,"outputTokens":3456,"costUSD":1.88}}}`
	parsed, err := parseClaudeOutput(blob)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := parsed.DominantModel(); got != "claude-opus-4-8[1m]" {
		t.Fatalf("DominantModel = %q, want claude-opus-4-8[1m]", got)
	}
}

// TestParseClaudeOutput_DominantModelMultiPicksMostTokens verifies the model
// with the largest token footprint wins.
func TestParseClaudeOutput_DominantModelMultiPicksMostTokens(t *testing.T) {
	blob := `{"result":"ok","modelUsage":{` +
		`"claude-haiku-4-5":{"inputTokens":10,"outputTokens":5,"costUSD":0.01},` +
		`"claude-opus-4-8[1m]":{"inputTokens":1000,"outputTokens":2000,"costUSD":3.5}}}`
	parsed, err := parseClaudeOutput(blob)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := parsed.DominantModel(); got != "claude-opus-4-8[1m]" {
		t.Fatalf("DominantModel = %q, want claude-opus-4-8[1m] (most tokens)", got)
	}
}

// TestParseClaudeOutput_NoModelUsage verifies an absent modelUsage yields "".
func TestParseClaudeOutput_NoModelUsage(t *testing.T) {
	blob := `{"result":"ok","total_cost_usd":0.5,"usage":{"input_tokens":10,"output_tokens":20}}`
	parsed, err := parseClaudeOutput(blob)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := parsed.DominantModel(); got != "" {
		t.Fatalf("DominantModel = %q, want empty", got)
	}
}
