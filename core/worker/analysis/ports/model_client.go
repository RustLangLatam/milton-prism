package ports

import "context"

// ModelRequest carries the inputs for a single, non-streaming model call.
type ModelRequest struct {
	// Model is the model identifier (e.g. "claude-haiku-4-5-20251001").
	// Empty means the adapter uses its configured default.
	Model string
	// System is the system-turn content; empty is valid.
	System string
	// Prompt is the user-turn message.
	Prompt string
	// MaxTokens caps the response length; zero means the adapter default.
	MaxTokens int
	// Purpose identifies the caller for cost attribution in logs
	// (e.g. "migrability-assessment", "semantic-clustering").
	// Empty is valid; the adapter skips the cost log line when unset.
	Purpose string
}

// ModelResponse carries the model's output and reported token usage.
type ModelResponse struct {
	// Content is the text produced by the model.
	Content string
	// InputTokens is the request token count as reported by the API.
	InputTokens int
	// OutputTokens is the response token count as reported by the API.
	OutputTokens int
	// CostUSD is the estimated cost in US dollars, derived from reported token
	// counts and the model's published pricing. This is advisory: rates may
	// change and the estimate is rounded to the nearest cent at the adapter.
	CostUSD float64
}

// ModelClient sends a single, non-streaming model call and returns the
// response. Implementations must never log the API key or any value
// derived from it.
type ModelClient interface {
	Complete(ctx context.Context, req ModelRequest) (ModelResponse, error)
}
