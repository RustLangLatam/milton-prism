package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	analysisports "milton_prism/core/worker/analysis/ports"
)

// Assessor produces a MigrabilityVerdict from an AnalysisDigest via a single
// LLM call. It is a deterministic prompt over distilled structural facts — no
// raw source code is sent to the model.
type Assessor struct {
	client analysisports.ModelClient
}

// NewAssessor constructs an Assessor backed by the given ModelClient.
func NewAssessor(client analysisports.ModelClient) *Assessor {
	return &Assessor{client: client}
}

// AssessResult bundles the verdict with the API call metadata.
type AssessResult struct {
	Verdict      *workerdomain.MigrabilityVerdict
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// Assess sends the digest to the LLM and returns a structured MigrabilityVerdict.
// score is the already-computed structural score; its typed_blockers are injected
// into the prompt so the LLM does not repeat structural issues already captured
// in the structured contract.
// The model call is counted toward the "migrability-assessment" cost line.
// language is a BCP-47 tag (e.g. "es", "de"). Empty → "en".
// Only prose fields (summary/reasons/blockers) are affected; module names,
// identifiers, and verdict constants remain in English regardless of language.
func (a *Assessor) Assess(ctx context.Context, digest *workerdomain.AnalysisDigest, score *workerdomain.MigrabilityScore, language string) (AssessResult, error) {
	prompt := buildAssessmentPrompt(digest, score, language)
	req := analysisports.ModelRequest{
		System:    assessmentSystemPrompt,
		Prompt:    prompt,
		MaxTokens: 1024,
		Purpose:   "migrability-assessment",
	}

	verdict, resp, err := completeJSONDecomp[workerdomain.MigrabilityVerdict](ctx, a.client, req)
	if err != nil {
		return AssessResult{}, fmt.Errorf("assessor: %w", err)
	}

	return AssessResult{
		Verdict:      &verdict,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
		CostUSD:      resp.CostUSD,
	}, nil
}

// assessmentSystemPrompt instructs the model to return only JSON, defines its
// role, and embeds the Prism v1 deployment contract so the model judges against
// the real target — not idealised pure microservices.
const assessmentSystemPrompt = `You are a software architecture analyst assessing whether a codebase can be decomposed into microservices using Milton Prism.

Milton Prism v1 deployment contract (judge against this, not against pure microservices):
- SHARED DATABASE: all generated services share a single database (shared_database=true). This is explicit declared debt, not a defect. A shared ORM layer or cross-service DB coupling is NOT a blocker.
- gRPC SYNCHRONOUS COMMUNICATION: inter-service calls use synchronous gRPC. Synchronous coupling between would-be services is expected and handled, not a defect.
- CROSS-SERVICE FOREIGN KEYS: treated as declared consistency debt, not as a structural blocker.

Your question is: "Can identifiable service boundaries be extracted from this codebase into separate deployable Go services, given that shared DB and synchronous gRPC are the output model?"

HONESTY CONTRACT — these rules are mandatory:
1. Do NOT include any numeric value in your output JSON fields (verdict, summary, reasons, blockers). Forbidden categories: module counts ("15 modules", "all N modules"), LOC ("1161 LOC"), function counts ("57 functions"), fan-in/fan-out values ("fan-in=19"), route counts ("54 routes"), percentages, or any other number. The structural report already displays these — repeating them in prose risks citing wrong values.
2. Module names (e.g. backend.var, conduit.articles.models) and technical labels (e.g. EXTRACT_DOMAIN, acantilado) are allowed — they are identifiers, not quantities.
3. Replace every number with a qualitative descriptor:
   - FORBIDDEN: "backend.funcs (1161 LOC, 57 functions, fan-in=12)" → REQUIRED: "backend.funcs is a god-module concentrating parameter management across the codebase"
   - FORBIDDEN: "all 15 modules are infrastructure" → REQUIRED: "the codebase has no domain layer — all modules are infrastructure"
   - FORBIDDEN: "backend.var with fan-in=19" → REQUIRED: "backend.var is a codebase-wide shared-state hub"
   - FORBIDDEN: "single Flask blueprint for all 54 routes" → REQUIRED: "a single Flask blueprint covers all routes with no boundary separation"

You receive deterministic structural metrics extracted by static analysis — no raw source code.
Respond ONLY with valid JSON matching the requested schema. No preamble, no markdown, no explanation outside the JSON object.`

// hubCouplingLevel converts a weighted fan-in count to a qualitative descriptor so
// the LLM receives severity context without seeing an exact value it can copy into prose.
func hubCouplingLevel(fanIn uint32) string {
	switch {
	case fanIn >= 15:
		return "codebase-wide"
	case fanIn >= 8:
		return "very-high"
	case fanIn >= 4:
		return "high"
	default:
		return "moderate"
	}
}

// locLevel converts a line count to a size descriptor.
func locLevel(loc uint32) string {
	switch {
	case loc >= 800:
		return "very-large"
	case loc >= 300:
		return "large"
	case loc >= 100:
		return "medium"
	default:
		return "small"
	}
}

// couplingLevel converts weighted coupling degree (fan_in+fan_out) to a descriptor.
func couplingLevel(fanIn, fanOut uint32) string {
	switch {
	case fanIn+fanOut >= 25:
		return "very-high"
	case fanIn+fanOut >= 12:
		return "high"
	case fanIn+fanOut >= 5:
		return "moderate"
	case fanIn+fanOut > 0:
		return "low"
	default:
		return "isolated"
	}
}

// funcLevel converts a function count to a descriptor.
func funcLevel(n int) string {
	switch {
	case n >= 50:
		return "excessive"
	case n >= 20:
		return "many"
	case n >= 8:
		return "moderate"
	default:
		return "few"
	}
}

// routeLevel converts a route count to a descriptor.
func routeLevel(n int) string {
	switch {
	case n >= 50:
		return "extensive"
	case n >= 20:
		return "many"
	case n >= 5:
		return "moderate"
	default:
		return "few"
	}
}

// buildAssessmentPrompt serialises the AnalysisDigest into a compact,
// human-readable prompt that presents only structural facts. The score's
// typed_blockers are injected to prevent the LLM from repeating structural
// issues already captured in the structured contract — the LLM should only
// add non-structural blockers the static analysis cannot detect.
func buildAssessmentPrompt(d *workerdomain.AnalysisDigest, score *workerdomain.MigrabilityScore, language string) string {
	var b strings.Builder

	b.WriteString("## Codebase Profile\n")
	if d.Framework != "" {
		b.WriteString(fmt.Sprintf("- Primary framework: %s\n", d.Framework))
	} else {
		b.WriteString("- Primary framework: unknown\n")
	}
	if len(d.Technologies) > 0 {
		b.WriteString(fmt.Sprintf("- Technologies: %s\n", strings.Join(d.Technologies, ", ")))
	}
	b.WriteString(fmt.Sprintf("- Dependency graph: %d modules, %d directed dependency edges\n",
		len(d.Graph.Nodes), len(d.Graph.Edges)))

	b.WriteString("\n## Service Boundary Analysis\n")
	if d.NoServiceBoundaries {
		b.WriteString("No service boundaries were detected. All modules were classified as infrastructure — " +
			"no domain layer was found.\n")
	} else {
		b.WriteString(fmt.Sprintf("%d service boundaries detected by community detection (blueprint-biased Louvain):\n",
			len(d.Clusters)))
		for _, c := range d.Clusters {
			b.WriteString(fmt.Sprintf("  - %s (%d modules)\n", c.BlueprintGroup, len(c.Modules)))
		}
		if d.LowConfidence {
			b.WriteString("  [LOW CONFIDENCE — modularity score below threshold; boundaries may not be stable]\n")
		}
	}

	b.WriteString("\n## Domain vs. Infrastructure Classification\n")
	if d.Classification.DomainEmpty {
		b.WriteString("- Domain modules: NONE\n")
		b.WriteString("- Infrastructure modules: ALL\n")
		b.WriteString("WARNING: Zero domain modules detected — the codebase has no domain layer. " +
			"All logic lives in infrastructure. This is the 'acantilado' pattern: a single " +
			"infrastructure slab with no bounded contexts.\n")
	} else {
		b.WriteString(fmt.Sprintf("- Domain modules: %d\n", len(d.Classification.DomainModules)))
		b.WriteString(fmt.Sprintf("- Infrastructure modules: %d\n", len(d.Classification.InfraModules)))
	}

	b.WriteString("\n## HTTP Entry Points\n")
	b.WriteString(fmt.Sprintf("- HTTP routes: %s\n", routeLevel(d.EntryPoints.TotalRoutes)))
	switch bc := d.EntryPoints.BlueprintCount; {
	case bc == 1:
		b.WriteString("- Blueprints (routing groups): single\n")
	case bc > 1:
		b.WriteString("- Blueprints (routing groups): multiple\n")
	default:
		b.WriteString("- Blueprints (routing groups): none detected\n")
	}
	if d.EntryPoints.SingleBlueprint {
		b.WriteString("- Single blueprint for the entire codebase — no boundary separation at the routing level.\n")
	}

	b.WriteString("\n## Shared-State Hubs (Global Mutable State)\n")
	if len(d.SharedStateHubs) == 0 {
		b.WriteString("None detected.\n")
	} else {
		b.WriteString("Modules with mutable state imported by ≥2 other modules.\n")
		b.WriteString("NOTE: infrastructure hubs (database sessions, ORM registries, web extensions, config) are accepted shared debt in Prism v1 — NOT blockers. " +
			"Only business-logic hubs (global vars holding domain data, god-module utility dumps spanning multiple domains) are real blockers.\n")
		for _, hub := range d.SharedStateHubs {
			b.WriteString(fmt.Sprintf("  - %s: state=%v, coupling=%s\n",
				hub.Module, hub.State, hubCouplingLevel(hub.FanIn)))
		}
	}

	b.WriteString("\n## Top Modules by Coupling (highest weighted degree)\n")
	limit := 12
	if len(d.ModuleCards) < limit {
		limit = len(d.ModuleCards)
	}
	for _, card := range d.ModuleCards[:limit] {
		line := fmt.Sprintf("  %s: loc=%s, coupling=%s, functions=%s",
			card.Module, locLevel(card.LOC), couplingLevel(card.FanIn, card.FanOut), funcLevel(len(card.Functions)))
		if len(card.MutableState) > 0 {
			line += fmt.Sprintf(", mutable_state=%v", card.MutableState)
		}
		if card.IsSharedStateHub {
			line += " [SHARED-STATE HUB]"
		}
		b.WriteString(line + "\n")
	}
	if d.AggregateCard != nil {
		b.WriteString("  ... plus additional modules not listed above\n")
	}

	// Inject already-identified structural blockers so the LLM does not repeat
	// what the typed contract already covers. The LLM's blockers field must
	// contain ONLY non-structural concerns (business logic coupling, data
	// ownership issues, external service dependencies) that static analysis
	// cannot detect from the graph alone.
	if len(score.TypedBlockers) > 0 {
		b.WriteString("\n## Structural blockers already captured by deterministic analysis\n")
		b.WriteString("The following structural issues are already recorded in the typed_blockers contract.\n")
		b.WriteString("DO NOT include them in your blockers array — they are already captured.\n")
		for _, tb := range score.TypedBlockers {
			b.WriteString(fmt.Sprintf("  - %s", tb.BlockerKey))
			if c, ok := tb.Params["count"]; ok {
				b.WriteString(fmt.Sprintf(" (count=%s)", c))
			}
			b.WriteString("\n")
		}
		b.WriteString("Your blockers array must contain ONLY blockers NOT listed above: semantic domain concerns,\n")
		b.WriteString("business-logic coupling not visible in the import graph, data ownership ambiguity,\n")
		b.WriteString("or external service dependencies. If no additional blockers exist, use [].\n")
	}

	// Inject language instruction when a non-English language is requested.
	// Module names, code identifiers, framework names, and verdict/confidence
	// constants must stay in English regardless of language — they are
	// identifiers, not prose, and the frontend i18n layer resolves the keys.
	lang := language
	if lang == "" {
		lang = "en"
	}
	if lang != "en" {
		b.WriteString(fmt.Sprintf(`
## Language instruction
Respond with the prose fields (summary, reasons, blockers) in %s.
The following MUST remain exactly in English regardless of this instruction:
- Module names (e.g. conduit.database, backend.funcs, conduit.articles.models)
- Code identifiers and package paths
- Framework and library names (Flask, SQLAlchemy, Django, etc.)
- Architectural pattern labels (acantilado, god-module, bounded context)
- Verdict values: MIGRABLE, PARTIAL, NOT_MIGRABLE
- Confidence values: HIGH, MEDIUM, LOW
Translate ONLY the natural-language explanation prose in summary, reasons, and blockers.
`, lang))
	}

	b.WriteString(`
---

Based on these structural facts, assess whether this codebase can be decomposed into Prism v1 services (shared DB, gRPC sync).

The numbers in the structural data above are FOR YOUR REASONING ONLY. Do NOT reproduce any of them in your JSON output.

Respond with exactly this JSON schema (all fields required):
{
  "verdict": "MIGRABLE" | "PARTIAL" | "NOT_MIGRABLE",

  "summary": "1-2 sentences. Describe what this codebase IS (type, framework, architectural pattern) and why it is or is not migrable. NO numbers — do not cite LOC, fan-in, module counts, route counts, or any metric. Name the pattern (acantilado, god-module, bounded contexts), not its measurements.",

  "reasons": [
    "Each reason describes ONE structural characteristic in qualitative terms. Name the module if relevant. DO NOT cite LOC, fan-in values, function counts, module counts, or route counts — those appear in the score breakdown. Write 'backend.funcs is a god-module concentrating dispatch and parameter management' not 'backend.funcs (1161 LOC, 57 functions, fan-in=12)'. Write 'no domain layer exists' not 'all 15 modules are infrastructure'."
  ],

  "blockers": [
    "Supplemental non-structural blockers ONLY — do not repeat any blocker listed in the 'Structural blockers already captured' section above. Write qualitative descriptions; no numeric metrics."
  ],

  "confidence": "HIGH" | "MEDIUM" | "LOW"
}

Verdict rules (judge against Prism v1 target — shared DB and synchronous gRPC are the output model, not defects):
- MIGRABLE: identifiable service boundaries exist and domain/infra separation is present. Shared ORM layers, cross-service DB coupling, and synchronous dependencies are NOT blockers — they are expected Prism v1 output. Leave blockers as [].
- PARTIAL: some service seams are visible but genuine structural problems require remediation first: god-modules that span multiple domain boundaries with no internal seams, or business-logic global state that tangles would-be services.
- NOT_MIGRABLE: no identifiable service boundaries. Real blockers (these apply regardless of shared-DB tolerance): zero domain layer (acantilado pattern — all logic in infrastructure), business-logic global mutable state with codebase-wide coupling, or god-modules so tangled that no seam can be drawn.
- blockers: list only what prevents extracting service boundaries under the Prism v1 model. Shared DB, ORM coupling, and gRPC-style synchronous dependencies are never blockers.
`)

	return b.String()
}

// completeJSONDecomp is a local copy of the analysis worker's CompleteJSON
// helper, using the analysis ports types. It is a pure function that makes one
// (or at most two, on retry) model calls.
func completeJSONDecomp[T any](ctx context.Context, c analysisports.ModelClient, req analysisports.ModelRequest) (T, analysisports.ModelResponse, error) {
	var zero T

	resp, err := c.Complete(ctx, req)
	if err != nil {
		return zero, resp, err
	}

	var out T
	if parseErr := json.Unmarshal([]byte(extractJSONDecomp(resp.Content)), &out); parseErr == nil {
		return out, resp, nil
	} else { //nolint:revive
		retryReq := req
		retryReq.Prompt = req.Prompt + "\n\n" +
			"Your previous response was not valid JSON " +
			"(parse error: " + parseErr.Error() + ").\n" +
			"Return valid JSON only — no preamble, no markdown fences, no extra text."

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
		if err2 := json.Unmarshal([]byte(extractJSONDecomp(retryResp.Content)), &out); err2 != nil {
			return zero, combined, fmt.Errorf("model returned invalid JSON after retry: %w", err2)
		}
		return out, combined, nil
	}
}

func extractJSONDecomp(s string) string {
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

// ComputeTypedRecommendations derives structured next-step items deterministically
// from the verdict and the structural findings in the score. It is called after the
// LLM verdict is known and its output goes into MigrabilityAssessment.typed_recommendations.
func ComputeTypedRecommendations(verdict string, score *workerdomain.MigrabilityScore) []workerdomain.TypedRecommendation {
	highCount := 0
	for _, f := range score.StructuralFindings {
		if f.Severity == "high" {
			highCount++
		}
	}
	highStr := strconv.Itoa(highCount)

	var recs []workerdomain.TypedRecommendation
	switch verdict {
	case workerdomain.VerdictMigrable:
		recs = append(recs, workerdomain.TypedRecommendation{RecKey: "rec.start_migration"})
		recs = append(recs, workerdomain.TypedRecommendation{RecKey: "rec.review_coupling_findings"})
		if highCount > 0 {
			recs = append(recs, workerdomain.TypedRecommendation{
				RecKey: "rec.resolve_high_findings",
				Params: map[string]string{"count": highStr},
			})
		}
		recs = append(recs, workerdomain.TypedRecommendation{RecKey: "rec.validate_architecture"})

	case workerdomain.VerdictPartial:
		if highCount > 0 {
			recs = append(recs, workerdomain.TypedRecommendation{
				RecKey: "rec.resolve_high_first",
				Params: map[string]string{"count": highStr},
			})
		}
		recs = append(recs, workerdomain.TypedRecommendation{RecKey: "rec.start_low_coupling"})
		recs = append(recs, workerdomain.TypedRecommendation{RecKey: "rec.validate_each_service"})

	case workerdomain.VerdictNotMigrable:
		// Derive specific rec counts from the typed blockers.
		for _, tb := range score.TypedBlockers {
			switch tb.BlockerKey {
			case "blocker.shared_state":
				if c, ok := tb.Params["count"]; ok {
					recs = append(recs, workerdomain.TypedRecommendation{
						RecKey: "rec.eliminate_shared_state",
						Params: map[string]string{"count": c},
					})
				}
			case "blocker.god_modules":
				if c, ok := tb.Params["count"]; ok {
					recs = append(recs, workerdomain.TypedRecommendation{
						RecKey: "rec.decompose_god_modules",
						Params: map[string]string{"count": c},
					})
				}
			case "blocker.cycles":
				if c, ok := tb.Params["count"]; ok {
					recs = append(recs, workerdomain.TypedRecommendation{
						RecKey: "rec.break_cycles",
						Params: map[string]string{"count": c},
					})
				}
			}
		}
		recs = append(recs, workerdomain.TypedRecommendation{RecKey: "rec.repeat_analysis"})
	}
	return recs
}
