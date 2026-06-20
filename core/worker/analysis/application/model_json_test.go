package application_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"milton_prism/core/worker/analysis/application"
	"milton_prism/core/worker/analysis/infrastructure/adapters"
	"milton_prism/core/worker/analysis/mocks"
	"milton_prism/core/worker/analysis/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ── test struct ───────────────────────────────────────────────────────────────

type verdict struct {
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

// ── CompleteJSON: valid JSON on first attempt ─────────────────────────────────

func TestCompleteJSON_ValidJSONOnFirstAttempt(t *testing.T) {
	t.Parallel()

	mc := new(mocks.MockModelClient)
	mc.On("Complete", mock.Anything, ports.ModelRequest{
		Purpose: "test",
		Prompt:  "assess",
	}).Return(ports.ModelResponse{
		Content:      `{"score":9,"reason":"clean boundaries"}`,
		InputTokens:  100,
		OutputTokens: 20,
		CostUSD:      0.001,
	}, nil)

	out, resp, err := application.CompleteJSON[verdict](context.Background(), mc, ports.ModelRequest{
		Purpose: "test",
		Prompt:  "assess",
	})

	require.NoError(t, err)
	assert.Equal(t, 9, out.Score)
	assert.Equal(t, "clean boundaries", out.Reason)
	// No retry: response is from the first call.
	assert.Equal(t, 100, resp.InputTokens)
	assert.Equal(t, 20, resp.OutputTokens)
	assert.InDelta(t, 0.001, resp.CostUSD, 1e-9)
	mc.AssertNumberOfCalls(t, "Complete", 1)
}

// Valid JSON wrapped in markdown fences is stripped and parsed correctly.
func TestCompleteJSON_JSONInMarkdownFences(t *testing.T) {
	t.Parallel()

	mc := new(mocks.MockModelClient)
	mc.On("Complete", mock.Anything, mock.AnythingOfType("ports.ModelRequest")).
		Return(ports.ModelResponse{
			Content: "```json\n{\"score\":7,\"reason\":\"needs work\"}\n```",
		}, nil)

	out, _, err := application.CompleteJSON[verdict](context.Background(), mc, ports.ModelRequest{Prompt: "p"})

	require.NoError(t, err)
	assert.Equal(t, 7, out.Score)
	assert.Equal(t, "needs work", out.Reason)
	mc.AssertNumberOfCalls(t, "Complete", 1)
}

// ── CompleteJSON: invalid first response, retry succeeds ─────────────────────

func TestCompleteJSON_InvalidFirstRetrySucceeds(t *testing.T) {
	t.Parallel()

	mc := new(mocks.MockModelClient)

	firstReq := ports.ModelRequest{Purpose: "semantic-clustering", Prompt: "cluster this"}

	// First call returns invalid JSON.
	mc.On("Complete", mock.Anything, firstReq).
		Return(ports.ModelResponse{
			Content:      "Sure, here are the clusters: [user, article]", // not JSON
			InputTokens:  80,
			OutputTokens: 15,
			CostUSD:      0.0005,
		}, nil).Once()

	// Second call (retry) returns valid JSON.
	mc.On("Complete", mock.Anything, mock.MatchedBy(func(req ports.ModelRequest) bool {
		// Retry must carry the parse error as feedback.
		return req.Purpose == "semantic-clustering" &&
			req.Prompt != firstReq.Prompt && // prompt was modified
			len(req.Prompt) > len(firstReq.Prompt)
	})).Return(ports.ModelResponse{
		Content:      `{"score":5,"reason":"ok"}`,
		InputTokens:  90,
		OutputTokens: 20,
		CostUSD:      0.0006,
	}, nil).Once()

	out, resp, err := application.CompleteJSON[verdict](context.Background(), mc, firstReq)

	require.NoError(t, err)
	assert.Equal(t, 5, out.Score)
	// Token counts are summed across both calls.
	assert.Equal(t, 170, resp.InputTokens)
	assert.Equal(t, 35, resp.OutputTokens)
	assert.InDelta(t, 0.0011, resp.CostUSD, 1e-9)
	mc.AssertNumberOfCalls(t, "Complete", 2)
}

// ── CompleteJSON: both attempts fail to produce valid JSON ────────────────────

func TestCompleteJSON_BothAttemptsFail(t *testing.T) {
	t.Parallel()

	mc := new(mocks.MockModelClient)
	mc.On("Complete", mock.Anything, mock.AnythingOfType("ports.ModelRequest")).
		Return(ports.ModelResponse{
			Content:      "not json at all",
			InputTokens:  50,
			OutputTokens: 10,
			CostUSD:      0.0003,
		}, nil)

	_, resp, err := application.CompleteJSON[verdict](context.Background(), mc, ports.ModelRequest{Prompt: "p"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON after retry")
	// Both token counts are still returned so the caller can log total cost.
	assert.Equal(t, 100, resp.InputTokens)
	assert.Equal(t, 20, resp.OutputTokens)
	assert.InDelta(t, 0.0006, resp.CostUSD, 1e-9)
	mc.AssertNumberOfCalls(t, "Complete", 2)
}

// ── CompleteJSON: model error on first call ───────────────────────────────────

func TestCompleteJSON_ModelErrorOnFirstCall(t *testing.T) {
	t.Parallel()

	mc := new(mocks.MockModelClient)
	mc.On("Complete", mock.Anything, mock.AnythingOfType("ports.ModelRequest")).
		Return(ports.ModelResponse{}, errors.New("network timeout"))

	_, _, err := application.CompleteJSON[verdict](context.Background(), mc, ports.ModelRequest{Prompt: "p"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "network timeout")
	// No retry when the first Complete call errors.
	mc.AssertNumberOfCalls(t, "Complete", 1)
}

// ── CompleteJSON: model error on retry ───────────────────────────────────────

func TestCompleteJSON_ModelErrorOnRetry(t *testing.T) {
	t.Parallel()

	mc := new(mocks.MockModelClient)
	// First call: invalid JSON (triggers retry).
	mc.On("Complete", mock.Anything, mock.AnythingOfType("ports.ModelRequest")).
		Return(ports.ModelResponse{
			Content:      "oops",
			InputTokens:  40,
			OutputTokens: 8,
			CostUSD:      0.0002,
		}, nil).Once()
	// Second call (retry): transport error.
	mc.On("Complete", mock.Anything, mock.AnythingOfType("ports.ModelRequest")).
		Return(ports.ModelResponse{}, errors.New("context deadline exceeded")).Once()

	_, resp, err := application.CompleteJSON[verdict](context.Background(), mc, ports.ModelRequest{Prompt: "p"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	// First call's tokens are included even though the retry errored.
	assert.Equal(t, 40, resp.InputTokens)
	mc.AssertNumberOfCalls(t, "Complete", 2)
}

// ── Purpose logging — adapter path via httptest ───────────────────────────────

// TestAnthropicModelClient_Complete_PurposeField verifies that a request with
// Purpose set is forwarded to the API without error and that the cost log path
// is exercised. Logging output is not captured; behavioural correctness (the
// log.Infof call is reached and does not panic) is guaranteed by the call
// completing successfully.
func TestAnthropicModelClient_Complete_PurposeField(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{
			"content": [{"type": "text", "text": "ok"}],
			"usage": {"input_tokens": 50, "output_tokens": 10}
		}`)
	}))
	defer srv.Close()

	c, err := adapters.NewAnthropicModelClient(srv.Client(),
		adapters.WithAPIKey("test-key"),
		adapters.WithModelBaseURL(srv.URL),
	)
	require.NoError(t, err)

	resp, err := c.Complete(context.Background(), ports.ModelRequest{
		Purpose: "migrability-assessment",
		Prompt:  "assess this repo",
	})

	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Content)
	// Cost for haiku: 50/1e6*0.80 + 10/1e6*4.00 = 4e-5 + 4e-5 = 8e-5 USD.
	assert.InDelta(t, 0.00008, resp.CostUSD, 1e-9)
}
