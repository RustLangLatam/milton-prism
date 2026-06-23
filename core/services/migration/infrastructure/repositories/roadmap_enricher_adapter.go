package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/ports"
	analysisadapters "milton_prism/core/worker/analysis/infrastructure/adapters"
	analysisports "milton_prism/core/worker/analysis/ports"
	applog "milton_prism/pkg/log"
	billingv1 "milton_prism/pkg/pb/gen/milton_prism/types/billing/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ ports.RoadmapEnricher = (*RoadmapEnricherAdapter)(nil)

// RoadmapEnricherAdapter implements ports.RoadmapEnricher by sending the persisted
// roadmap's structural facts to the LLM and returning one narrative per action step.
// No raw source code is included in the prompt — only signal names, penalties, detail
// strings, and the deterministic action plan already present in the roadmap.
type RoadmapEnricherAdapter struct {
	client analysisports.ModelClient
	// recorder accounts LLM token spend in billing (best-effort). May be nil when
	// billing is not wired — recording is then skipped.
	recorder ports.UsageRecorder
}

// NewRoadmapEnricherAdapter constructs the adapter. recorder accounts LLM token
// spend in billing best-effort; pass nil to disable recording (e.g. billing not
// configured).
// Returns an error only when ANTHROPIC_API_KEY is absent from the environment.
func NewRoadmapEnricherAdapter(recorder ports.UsageRecorder) (*RoadmapEnricherAdapter, error) {
	client, err := analysisadapters.NewAnthropicModelClient(nil)
	if err != nil {
		return nil, fmt.Errorf("roadmap enricher: model client: %w", err)
	}
	return &RoadmapEnricherAdapter{client: client, recorder: recorder}, nil
}

// enrichmentSystemPrompt instructs the model to return only JSON and defines its role.
const enrichmentSystemPrompt = `You are a software architecture expert writing actionable restructuring guidance for a development team.

You receive a list of concrete restructuring steps derived from static analysis. The structural problems (fan-in counts, LOC, route counts, percentages) are already displayed to the reader in a separate section — DO NOT re-state them. The reader already knows what is wrong.

Your job: write the HOW for each step — what to do, in what order, with what concrete technique, and why it unblocks the next steps.

Rules:
- Do NOT invent new steps, change step order numbers, or alter kind, impact, or dependency links.
- Keep the exact same target modules named in each step — do not substitute other names.
- DO NOT re-describe the problem (no fan-in counts, LOC numbers, route counts, percentages — those are already shown). Start immediately with what to do.
- Each narrative must be 2–3 sentences: (1) the concrete technique to apply, (2) what to extract/split/wrap and how to structure it, (3) what this unblocks or enables next.
- Name concrete artefacts: interfaces, module names, file groupings, patterns (e.g. "wrap behind a StateManager interface", "extract into domain/user/ and domain/team/").
- Write for a senior engineer: direct, imperative, no hedging.
- Respond ONLY with valid JSON matching the requested schema. No preamble, no markdown, no extra text.`

// Enrich builds a structural prompt from the roadmap and calls the LLM to produce
// per-step narratives. Returns a RoadmapEnrichment with one EnrichedStep per ActionItem.
func (a *RoadmapEnricherAdapter) Enrich(ctx context.Context, userID, migrationID uint64, roadmap *domain.RestructuringRoadmap) (*domain.RoadmapEnrichment, error) {
	prompt := buildEnrichmentPrompt(roadmap)
	req := analysisports.ModelRequest{
		System:    enrichmentSystemPrompt,
		Prompt:    prompt,
		MaxTokens: 2048,
		Purpose:   "roadmap-enrichment",
	}

	type enrichedStepJSON struct {
		StepOrder int32  `json:"step_order"`
		Narrative string `json:"narrative"`
	}
	type enrichmentResponseJSON struct {
		Steps []enrichedStepJSON `json:"steps"`
	}

	result, resp, err := enricherComplete[enrichmentResponseJSON](ctx, a.client, req)
	if err != nil {
		return nil, fmt.Errorf("roadmap enricher: llm: %w", err)
	}

	// Record LLM token spend in billing (best-effort). A failure is logged and
	// swallowed — it must never break the enrichment.
	recordMigrationSpend(ctx, a.recorder, ports.UsageSpend{
		UserID:      userID,
		MigrationID: migrationID,
		Operation:   billingv1.UsageOperation_USAGE_OPERATION_MIGRATION,
		TokensIn:    int64(resp.InputTokens),
		TokensOut:   int64(resp.OutputTokens),
		CostUSD:     resp.CostUSD,
	})

	steps := make([]*migrationv1.EnrichedStep, len(result.Steps))
	for i, s := range result.Steps {
		steps[i] = &migrationv1.EnrichedStep{
			StepOrder: s.StepOrder,
			Narrative: s.Narrative,
		}
	}
	return &migrationv1.RoadmapEnrichment{
		Steps:        steps,
		CostUsd:      resp.CostUSD,
		EnrichedTime: timestamppb.New(time.Now().UTC()),
	}, nil
}

// buildEnrichmentPrompt serialises the roadmap's structural facts into a prompt.
// Only signal metadata, module names, and action plan steps are included — never raw code.
func buildEnrichmentPrompt(roadmap *domain.RestructuringRoadmap) string {
	var b strings.Builder

	if d := roadmap.GetDiagnosis(); d != nil {
		b.WriteString("## Codebase Diagnosis\n")
		b.WriteString(fmt.Sprintf("Verdict: %s\n", d.GetVerdict()))
		if d.GetSummary() != "" {
			b.WriteString(fmt.Sprintf("Summary: %s\n", d.GetSummary()))
		}
	}

	if len(roadmap.GetStructuralProblems()) > 0 {
		b.WriteString("\n## Structural Signals (static analysis findings)\n")
		for _, p := range roadmap.GetStructuralProblems() {
			b.WriteString(fmt.Sprintf("- signal=%s  severity=%s  detail: %s\n",
				p.GetSignal(), penaltyLevel(p.GetPenalty()), p.GetDetail()))
		}
	}

	if roadmap.GetBoundariesExplanation() != "" {
		b.WriteString(fmt.Sprintf("\n## Boundaries Explanation\n%s\n", roadmap.GetBoundariesExplanation()))
	}

	b.WriteString("\n## Deterministic Action Plan (steps to enrich)\n")
	for _, item := range roadmap.GetActionPlan() {
		dep := ""
		if item.GetDependsOnStep() > 0 {
			dep = fmt.Sprintf(" depends_on_step=%d", item.GetDependsOnStep())
		}
		blocking := ""
		if item.GetBlocking() {
			blocking = " [BLOCKING]"
		}
		b.WriteString(fmt.Sprintf("Step %d (kind=%s improvement=%s%s%s):\n",
			item.GetOrder(), item.GetKind(), impactLevel(item.GetImpact()), blocking, dep))
		b.WriteString(fmt.Sprintf("  subject: %s\n", item.GetSubject()))
		b.WriteString(fmt.Sprintf("  action:  %s\n\n", item.GetAction()))
	}

	b.WriteString(`---

Write a narrative for each step that tells the engineer WHAT TO DO and HOW — not what the problem is. The structural signals above already describe the problem. Go directly to the technique, the concrete action, and what it enables.

Respond with exactly this JSON (one entry per step):
{
  "steps": [
    { "step_order": 1, "narrative": "..." },
    { "step_order": 2, "narrative": "..." }
  ]
}`)

	return b.String()
}

// recordMigrationSpend records a single LLM spend event in billing best-effort.
// It is a no-op when recorder is nil (billing not wired). Any recording error is
// logged at warning level and swallowed — it must never break the LLM flow.
// Shared by the migration LLM adapters in this package.
func recordMigrationSpend(ctx context.Context, recorder ports.UsageRecorder, spend ports.UsageSpend) {
	if recorder == nil {
		return
	}
	if err := recorder.RecordUsage(ctx, spend); err != nil {
		applog.Warningf("migration: usage record failed user_id=%d migration_id=%d op=%s tokens_in=%d tokens_out=%d: %v — spend not accounted",
			spend.UserID, spend.MigrationID, spend.Operation.String(), spend.TokensIn, spend.TokensOut, err)
	}
}

// enricherComplete sends one model request, parses the JSON response, and retries
// once with a corrective message on parse failure.
func enricherComplete[T any](ctx context.Context, c analysisports.ModelClient, req analysisports.ModelRequest) (T, analysisports.ModelResponse, error) {
	var zero T

	resp, err := c.Complete(ctx, req)
	if err != nil {
		return zero, resp, err
	}

	var out T
	if parseErr := json.Unmarshal([]byte(extractJSONEnricher(resp.Content)), &out); parseErr == nil {
		return out, resp, nil
	}

	retryReq := req
	retryReq.Prompt = req.Prompt + "\n\nYour previous response was not valid JSON. Return valid JSON only — no preamble, no markdown fences, no extra text."
	retryResp, retryErr := c.Complete(ctx, retryReq)
	combined := analysisports.ModelResponse{
		Content:      retryResp.Content,
		InputTokens:  resp.InputTokens + retryResp.InputTokens,
		OutputTokens: resp.OutputTokens + retryResp.OutputTokens,
		CostUSD:      resp.CostUSD + retryResp.CostUSD,
	}
	if retryErr != nil {
		return zero, combined, retryErr
	}
	if err2 := json.Unmarshal([]byte(extractJSONEnricher(retryResp.Content)), &out); err2 != nil {
		return zero, combined, fmt.Errorf("model returned invalid JSON after retry: %w", err2)
	}
	return out, combined, nil
}

func extractJSONEnricher(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```json"); i >= 0 {
		s = s[i+7:]
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		return strings.TrimSpace(s)
	}
	if strings.HasPrefix(s, "```") {
		s = s[3:]
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		return strings.TrimSpace(s)
	}
	return s
}
