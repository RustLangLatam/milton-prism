package domain

// Score band constants — single source of truth for band derivation thresholds.
const (
	ScoreBandGood   = "good"   // Value >= 70
	ScoreBandMedium = "medium" // Value >= 40 && < 70
	ScoreBandBad    = "bad"    // Value < 40
)

// MigrabilityScore is the output of the deterministic scorer.
// Value is in [0, 100]; Breakdown lists the per-signal penalties.
type MigrabilityScore struct {
	Value     int
	Breakdown []ScoreComponent
	// ScoreBand is the quality tier derived from Value (good/medium/bad).
	ScoreBand          string
	StructuralFindings []StructuralFinding
	TypedBlockers      []TypedBlocker
}

// ScoreComponent records one structural signal's contribution to the score.
type ScoreComponent struct {
	Signal   string   // e.g. "domain_presence", "cluster_count"
	Penalty  int      // 0–N subtracted from 100
	Detail   string   // legacy prose explanation (kept for backward compat)
	Modules  []string // structured module names (god_modules, hub_severity)
	// Structured i18n key replacing Detail. When present, frontend renders
	// t(DetailKey, DetailParams) instead of Detail.
	DetailKey    string
	DetailParams map[string]string
}

// StructuralFinding is one typed structural issue detected by the scorer.
// kind: "shared_state" | "god_module" | "topology" | "cycle" (future — Tarjan gap)
// severity: "high" | "medium" | "low" (matches frontend FindingSev)
type StructuralFinding struct {
	Kind     string
	Severity string
	TitleKey string
	Params   map[string]string
	Modules  []string
}

// TypedBlocker is one structural pre-decomposition blocker.
type TypedBlocker struct {
	BlockerKey string
	Params     map[string]string
}
