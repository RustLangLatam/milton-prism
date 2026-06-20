package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"milton_prism/core/worker/analysis/ports"
	applog "milton_prism/pkg/log"
)

var _ ports.ModelClient = (*AnthropicModelClient)(nil)

const (
	anthropicDefaultBaseURL   = "https://api.anthropic.com"
	anthropicAPIVersion       = "2023-06-01"
	anthropicDefaultModel     = "claude-haiku-4-5-20251001"
	anthropicDefaultMaxTokens = 1024
)

// anthropicPricing maps a model-name prefix to its published input and output
// USD rates per 1 M tokens. Rates are approximate — cost is advisory only.
var anthropicPricing = []struct {
	prefix       string
	inputPerMil  float64
	outputPerMil float64
}{
	{"claude-haiku-4-5", 0.80, 4.00},
	{"claude-sonnet-4-6", 3.00, 15.00},
	{"claude-opus-4-8", 15.00, 75.00},
}

// ModelClientOption is a functional option for AnthropicModelClient.
type ModelClientOption func(*AnthropicModelClient)

// WithModelBaseURL overrides the Anthropic API base URL. Use in tests to
// redirect calls to a local httptest.Server without touching production paths.
func WithModelBaseURL(baseURL string) ModelClientOption {
	return func(c *AnthropicModelClient) {
		c.baseURL = baseURL
	}
}

// WithAPIKey injects an API key directly instead of reading it from the
// environment. Intended for tests; in production prefer NewAnthropicModelClient
// which reads ANTHROPIC_API_KEY from the environment.
func WithAPIKey(key string) ModelClientOption {
	return func(c *AnthropicModelClient) {
		c.apiKey = key
	}
}

// AnthropicModelClient calls the Anthropic Messages API directly (single call,
// non-streaming). The API key is read from ANTHROPIC_API_KEY at construction
// time and is never logged.
type AnthropicModelClient struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
}

// NewAnthropicModelClient builds an adapter. Options (including WithAPIKey for
// tests) are applied first; if no key is available after options, the
// ANTHROPIC_API_KEY environment variable is checked. Returns an error if the
// key is absent from both sources.
func NewAnthropicModelClient(httpClient *http.Client, opts ...ModelClientOption) (*AnthropicModelClient, error) {
	c := buildAnthropicClient(os.Getenv("ANTHROPIC_API_KEY"), anthropicDefaultBaseURL, httpClient, opts...)
	if c.apiKey == "" {
		return nil, errors.New("ANTHROPIC_API_KEY not set")
	}
	return c, nil
}

func buildAnthropicClient(key, baseURL string, httpClient *http.Client, opts ...ModelClientOption) *AnthropicModelClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	c := &AnthropicModelClient{
		httpClient: httpClient,
		apiKey:     key,
		baseURL:    baseURL,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// ── Anthropic API wire types ──────────────────────────────────────────────────

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type anthropicErrorResponse struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ── Complete ──────────────────────────────────────────────────────────────────

// Complete sends req to the Anthropic Messages endpoint and returns the
// model's first text block plus usage stats.
func (c *AnthropicModelClient) Complete(ctx context.Context, req ports.ModelRequest) (ports.ModelResponse, error) {
	model := req.Model
	if model == "" {
		model = anthropicDefaultModel
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = anthropicDefaultMaxTokens
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: maxTok,
		System:    req.System,
		Messages:  []anthropicMessage{{Role: "user", Content: req.Prompt}},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return ports.ModelResponse{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/messages", bytes.NewReader(encoded))
	if err != nil {
		return ports.ModelResponse{}, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ports.ModelResponse{}, fmt.Errorf("anthropic: http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return ports.ModelResponse{}, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr anthropicErrorResponse
		_ = json.Unmarshal(respBytes, &apiErr)
		msg := apiErr.Error.Message
		if msg == "" {
			msg = string(respBytes)
		}
		return ports.ModelResponse{}, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, msg)
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return ports.ModelResponse{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	var content string
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			content = block.Text
			break
		}
	}

	cost := estimateAnthropicCost(model, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens)
	if req.Purpose != "" {
		applog.Infof("anthropic: complete purpose=%s input_tokens=%d output_tokens=%d cost_usd=%.6f",
			req.Purpose, apiResp.Usage.InputTokens, apiResp.Usage.OutputTokens, cost)
	}

	return ports.ModelResponse{
		Content:      content,
		InputTokens:  apiResp.Usage.InputTokens,
		OutputTokens: apiResp.Usage.OutputTokens,
		CostUSD:      cost,
	}, nil
}

// estimateAnthropicCost returns the estimated USD cost for the given model and
// token counts. Returns 0 for unknown models; cost is advisory only.
func estimateAnthropicCost(model string, inputTok, outputTok int) float64 {
	for _, p := range anthropicPricing {
		if strings.HasPrefix(model, p.prefix) {
			return float64(inputTok)/1e6*p.inputPerMil + float64(outputTok)/1e6*p.outputPerMil
		}
	}
	return 0
}
