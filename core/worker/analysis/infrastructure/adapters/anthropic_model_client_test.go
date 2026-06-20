package adapters_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"milton_prism/core/worker/analysis/infrastructure/adapters"
	"milton_prism/core/worker/analysis/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestModelClient builds a client wired to srv with a synthetic API key.
// WithAPIKey is applied before the env-guard runs, so no env var is needed.
func newTestModelClient(t *testing.T, srv *httptest.Server, opts ...adapters.ModelClientOption) *adapters.AnthropicModelClient {
	t.Helper()
	allOpts := append(
		[]adapters.ModelClientOption{
			adapters.WithAPIKey("test-api-key-m0"),
			adapters.WithModelBaseURL(srv.URL),
		},
		opts...,
	)
	c, err := adapters.NewAnthropicModelClient(srv.Client(), allOpts...)
	require.NoError(t, err)
	return c
}

// ── Happy path ────────────────────────────────────────────────────────────────

func TestAnthropicModelClient_Complete_OK(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request structure.
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "test-api-key-m0", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		assert.Equal(t, "application/json", r.Header.Get("content-type"))

		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
			System    string `json:"system"`
			Messages  []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.NotEmpty(t, body.Model, "model must be set (default)")
		assert.Equal(t, "You are an expert code analyst.", body.System)
		require.Len(t, body.Messages, 1)
		assert.Equal(t, "user", body.Messages[0].Role)
		assert.Equal(t, "Describe this Flask app.", body.Messages[0].Content)

		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{
			"content": [{"type": "text", "text": "A Flask REST API for a blogging platform."}],
			"usage": {"input_tokens": 120, "output_tokens": 30}
		}`)
	}))
	defer srv.Close()

	c := newTestModelClient(t, srv)

	resp, err := c.Complete(context.Background(), ports.ModelRequest{
		System: "You are an expert code analyst.",
		Prompt: "Describe this Flask app.",
	})
	require.NoError(t, err)

	assert.Equal(t, "A Flask REST API for a blogging platform.", resp.Content)
	assert.Equal(t, 120, resp.InputTokens)
	assert.Equal(t, 30, resp.OutputTokens)
	// Default model is haiku ($0.80/M in, $4.00/M out).
	// Expected cost: 120/1e6*0.80 + 30/1e6*4.00 = 9.6e-5 + 1.2e-4 = ~0.000216 USD.
	assert.InDelta(t, 0.000216, resp.CostUSD, 1e-8)
}

// ── Custom model and max-tokens override ─────────────────────────────────────

func TestAnthropicModelClient_Complete_CustomModelAndTokens(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "claude-sonnet-4-6", body.Model)
		assert.Equal(t, 512, body.MaxTokens)

		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{
			"content": [{"type": "text", "text": "verdict"}],
			"usage": {"input_tokens": 200, "output_tokens": 50}
		}`)
	}))
	defer srv.Close()

	c := newTestModelClient(t, srv)

	resp, err := c.Complete(context.Background(), ports.ModelRequest{
		Model:     "claude-sonnet-4-6",
		Prompt:    "assess",
		MaxTokens: 512,
	})
	require.NoError(t, err)
	assert.Equal(t, "verdict", resp.Content)
	// Sonnet: $3.00/M in, $15.00/M out.
	// 200/1e6*3.00 + 50/1e6*15.00 = 6e-4 + 7.5e-4 = 0.00135 USD.
	assert.InDelta(t, 0.00135, resp.CostUSD, 1e-8)
}

// ── API error response ────────────────────────────────────────────────────────

func TestAnthropicModelClient_Complete_APIError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"max_tokens too large"}}`)
	}))
	defer srv.Close()

	c := newTestModelClient(t, srv)
	_, err := c.Complete(context.Background(), ports.ModelRequest{Prompt: "test"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "max_tokens too large")
}

// ── Missing API key ───────────────────────────────────────────────────────────

func TestAnthropicModelClient_MissingAPIKey(t *testing.T) {
	// Not parallel: uses t.Setenv to clear the environment variable.
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, err := adapters.NewAnthropicModelClient(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTHROPIC_API_KEY")
}
