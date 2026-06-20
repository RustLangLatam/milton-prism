package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"milton_prism/core/worker/analysis/ports"
)

// CompleteJSON calls c.Complete and unmarshals the JSON in the response into T.
//
// If the model's response is not valid JSON, CompleteJSON makes one automatic
// retry. The retry message appends the original prompt with the parse error so
// the model can self-correct. If the retry also fails to produce valid JSON,
// CompleteJSON returns an error.
//
// The returned ModelResponse reflects the actual API calls made:
//   - successful first attempt: the first response as-is.
//   - retry path: Content is from the second call; InputTokens, OutputTokens,
//     and CostUSD are summed across both calls so callers always see total cost.
func CompleteJSON[T any](ctx context.Context, c ports.ModelClient, req ports.ModelRequest) (T, ports.ModelResponse, error) {
	var zero T

	resp, err := c.Complete(ctx, req)
	if err != nil {
		return zero, resp, err
	}

	var out T
	if parseErr := json.Unmarshal([]byte(extractJSON(resp.Content)), &out); parseErr == nil {
		return out, resp, nil
	} else { //nolint:revive // intentional: need parseErr in both branches
		// First response is not valid JSON — retry once with corrective feedback.
		retryReq := req
		retryReq.Prompt = req.Prompt + "\n\n" +
			"Your previous response was not valid JSON " +
			"(parse error: " + parseErr.Error() + ").\n" +
			"Return valid JSON only — no preamble, no markdown fences, no extra text."

		retryResp, retryErr := c.Complete(ctx, retryReq)
		combined := ports.ModelResponse{
			// Content comes from the retry; token counts and cost are summed so
			// callers always see the total cost of the CompleteJSON call.
			Content:      retryResp.Content,
			InputTokens:  resp.InputTokens + retryResp.InputTokens,
			OutputTokens: resp.OutputTokens + retryResp.OutputTokens,
			CostUSD:      resp.CostUSD + retryResp.CostUSD,
		}
		if retryErr != nil {
			return zero, combined, retryErr
		}
		if err2 := json.Unmarshal([]byte(extractJSON(retryResp.Content)), &out); err2 != nil {
			return zero, combined, fmt.Errorf("model returned invalid JSON after retry: %w", err2)
		}
		return out, combined, nil
	}
}

// extractJSON strips markdown code fences from s, returning the inner content.
// When no fences are present, s is returned trimmed so the caller's
// json.Unmarshal decides validity on the raw model output.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	// Strip ```json ... ``` fences.
	if i := strings.Index(s, "```json"); i >= 0 {
		s = s[i+7:]
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		return strings.TrimSpace(s)
	}
	// Strip plain ``` ... ``` fences.
	if strings.HasPrefix(s, "```") {
		s = s[3:]
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		return strings.TrimSpace(s)
	}
	return s
}
