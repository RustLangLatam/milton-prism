package domain

import "strings"

// MaxFailureReasonLen caps the length of a user-visible failure reason.
const MaxFailureReasonLen = 200

// rateLimitKeywords are substrings that indicate a transient, retriable failure
// (rate-limit or server overload). They are the single source for transient
// classification, shared by the pipeline's backoff decision and the persisted
// FailureClass (via ClassifyFailure).
var rateLimitKeywords = []string{
	"rate limit", "rate_limit", "too many requests",
	"429", "overloaded", "server temporarily unavailable",
}

// IsTransientError reports whether a failure is worth retrying.
// invokeErr != nil (infrastructure failure, context deadline) is always transient.
// A clean invoker run that fails its gates is transient only when a rate-limit
// keyword appears in the failure reason; all other gate failures are permanent
// (a deterministic-gate red — the code did not compile / tests failed — is a
// DESIGN failure, not a throttle).
func IsTransientError(invokeErr error, failureReason string) bool {
	if invokeErr != nil {
		return true
	}
	lower := strings.ToLower(failureReason)
	for _, kw := range rateLimitKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ClassifyFailure maps a failed generation attempt to its FailureClass — the
// single source of truth for whether a retry is worth offering. A transient
// failure (rate-limit / overload / infrastructure / deadline) classifies as
// TRANSIENT; everything else (a deterministic-gate red or a not-migrable /
// incomplete-contract service) classifies as DESIGN. invokeErr is the infra
// error (nil on a clean run that merely failed its gates); reason is the raw,
// unsanitized failure text used for keyword detection.
func ClassifyFailure(invokeErr error, reason string) FailureClass {
	if IsTransientError(invokeErr, reason) {
		return FailureClassTransient
	}
	return FailureClassDesign
}

// SanitizeFailureReason reduces a raw agent failure blob (which may be the full
// Claude Code JSON envelope including total_cost_usd, session_id, usage and
// modelUsage) to a short, clean, user-facing technical message.
//
// The raw blob must NEVER reach the user-visible failure_reason field — it is
// logged server-side for diagnosis instead. This function caps the result at
// MaxFailureReasonLen chars, strips any JSON/braces, and maps known error
// patterns to friendly Spanish messages. Unknown failures collapse to a generic
// technical message.
func SanitizeFailureReason(raw string) string {
	lower := strings.ToLower(raw)

	// Known patterns → clean messages. Order matters: most specific first.
	switch {
	case strings.Contains(lower, "connectionrefused"),
		strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "unable to connect to api"),
		strings.Contains(lower, "econnrefused"),
		strings.Contains(lower, "dial tcp"):
		return "El agente de generación no pudo conectarse a la API (reintentar)."
	case strings.Contains(lower, "rate limit"),
		strings.Contains(lower, "rate_limit"),
		strings.Contains(lower, "429"),
		strings.Contains(lower, "overloaded"),
		strings.Contains(lower, "too many requests"):
		return "El agente de generación fue limitado por el proveedor de la API (reintentar)."
	case strings.Contains(lower, "timeout"),
		strings.Contains(lower, "deadline exceeded"),
		strings.Contains(lower, "context cancelled"),
		strings.Contains(lower, "context canceled"):
		return "La generación del servicio expiró por tiempo de espera."
	case strings.Contains(lower, "authentication"),
		strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "invalid api key"),
		strings.Contains(lower, "401"):
		return "El agente de generación no pudo autenticarse con la API."
	}

	// If the raw text looks like a JSON dump or carries sensitive billing/session
	// keys, never expose it: return the generic technical message.
	if looksLikeRawAgentBlob(raw) {
		return "La generación del servicio falló por un error técnico."
	}

	// Otherwise the text is a short plain message: collapse control chars/braces
	// and cap the length.
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		case '{', '}':
			return -1
		}
		return r
	}, raw)
	clean = strings.Join(strings.Fields(clean), " ")
	if clean == "" {
		return "La generación del servicio falló por un error técnico."
	}
	if len(clean) > MaxFailureReasonLen {
		clean = strings.TrimSpace(clean[:MaxFailureReasonLen])
	}
	return clean
}

// looksLikeRawAgentBlob reports whether raw appears to be the raw Claude Code
// stdout envelope or otherwise carries sensitive/noisy keys that must not be
// shown to the user.
func looksLikeRawAgentBlob(raw string) bool {
	if strings.ContainsAny(raw, "{}") {
		return true
	}
	lower := strings.ToLower(raw)
	for _, k := range []string{
		"total_cost_usd", "session_id", "modelusage", "model_usage",
		"\"usage\"", "stop_reason", "input_tokens", "output_tokens",
	} {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}
