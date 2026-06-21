package domain

// MigrabilityVerdict is the structured output of the migrability assessor (M2).
// It is produced by a single LLM call that reasons over the AnalysisDigest;
// the schema is part of the prompt so the model returns it directly.
type MigrabilityVerdict struct {
	// Verdict is the top-level readiness signal.
	Verdict string `json:"verdict"` // MIGRABLE | PARTIAL | NOT_MIGRABLE
	// Summary is 1-2 sentences describing what the codebase is and its
	// architectural pattern (written for a human reader, not a tool).
	Summary string `json:"summary"`
	// Reasons explains why this verdict was reached.
	Reasons []string `json:"reasons"`
	// Blockers lists what prevents clean decomposition and what would need to
	// change. Empty for MIGRABLE; non-empty for PARTIAL and NOT_MIGRABLE.
	Blockers []string `json:"blockers"`
	// Confidence is the model's self-assessed certainty given the structural evidence.
	Confidence string `json:"confidence"` // HIGH | MEDIUM | LOW
}

const (
	VerdictMigrable    = "MIGRABLE"
	VerdictPartial     = "PARTIAL"
	VerdictNotMigrable = "NOT_MIGRABLE"
	// VerdictIncompleteNoStructuralData is emitted when deep structural analysis
	// was unavailable for the repo (no dependency graph or module cards), so there
	// is nothing to reason over. It is NOT a migrability judgement: it is an honest
	// degrade that short-circuits the score and the LLM. Distinct from a genuine
	// NOT_MIGRABLE (analysed repo with no domain layer) and from the decomposition
	// contract pipeline's Incomplete (analysed but no contracts derived).
	VerdictIncompleteNoStructuralData = "INCOMPLETE_NO_STRUCTURAL_DATA"
	// VerdictUnsupportedLanguage is emitted when the intake gate found the primary
	// language has no Tier-2 analyzer (today only PHP/Python are supported), so no
	// dependency graph could be produced. Distinct from the generic
	// INCOMPLETE_NO_STRUCTURAL_DATA: the cause is a known coverage gap, not a layout
	// the analyzer failed to parse. Lets the UI say "language not supported yet"
	// instead of a generic "no structural data".
	VerdictUnsupportedLanguage = "INCOMPLETE_UNSUPPORTED_LANGUAGE"
	// VerdictNotABackend is emitted when the intake gate classified the repository as
	// something other than a backend (frontend SPA, library, CLI, mobile). The
	// platform migrates backends today, so there is no migrability judgement to make.
	VerdictNotABackend = "INCOMPLETE_NOT_A_BACKEND"

	ConfidenceHigh   = "HIGH"
	ConfidenceMedium = "MEDIUM"
	ConfidenceLow    = "LOW"
)

// ReasonNoStructuralData is the single, non-LLM reason attached to an
// INCOMPLETE_NO_STRUCTURAL_DATA verdict. Prose stays minimal and deterministic —
// the verdict constant is the contract the UI localises.
const ReasonNoStructuralData = "Deep structural analysis produced no dependency graph or module cards for this repository, so migrability cannot be assessed. This usually means the analyzer could not parse the project layout (e.g. a pre-PSR-4 / global-namespace PHP codebase). Re-run after extending language support; do not treat this as a 'no domain' verdict."

// ReasonUnsupportedLanguage is the deterministic reason attached to an
// INCOMPLETE_UNSUPPORTED_LANGUAGE verdict. The %s placeholders are filled with the
// detected primary language and the supported set at assessment time.
const ReasonUnsupportedLanguage = "The primary language of this repository (%s) does not have a deep analyzer yet (supported: %s), so no dependency graph was produced and migrability cannot be assessed. This is a known coverage gap, not a 'no domain' verdict — analysis of supported-language repositories is unaffected."

// ReasonNotABackend is the deterministic reason attached to an
// INCOMPLETE_NOT_A_BACKEND verdict. The %s placeholder is filled with the detected
// codebase kind phrase at assessment time.
const ReasonNotABackend = "This repository was classified as a %s, not a backend service. The platform migrates backends today, so no migrability verdict applies. Tier-1 facts (technologies, vulnerabilities) are still available."

// TypedRecommendation is one structured next-step item derived deterministically
// from the verdict + structural findings. Not part of the LLM output.
type TypedRecommendation struct {
	RecKey string
	Params map[string]string
}
