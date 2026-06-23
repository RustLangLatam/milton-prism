package domain

import (
	"math"
	"testing"
)

func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestEstimateCostUSD_Opus48 verifies the opus-4-8 tiered estimate.
func TestEstimateCostUSD_Opus48(t *testing.T) {
	// 1M fresh input @5, 1M cache-write @6.25, 1M cache-read @0.50, 1M output @25.
	got := EstimateCostUSD("claude-opus-4-8[1m]", 1_000_000, 1_000_000, 1_000_000, 1_000_000)
	want := 5.0 + 6.25 + 0.50 + 25.0
	if !approxEqual(got, want) {
		t.Fatalf("opus-4-8 estimate = %v, want %v", got, want)
	}
}

// TestEstimateCostUSD_Sonnet verifies a different known tier.
func TestEstimateCostUSD_Sonnet(t *testing.T) {
	// 2M input @3 + 1M output @15 = 6 + 15 = 21.
	got := EstimateCostUSD("claude-sonnet-4-6", 2_000_000, 0, 0, 1_000_000)
	if !approxEqual(got, 21.0) {
		t.Fatalf("sonnet estimate = %v, want 21", got)
	}
}

// TestEstimateCostUSD_UnknownFallsBackToOpus verifies the conservative fallback.
func TestEstimateCostUSD_UnknownFallsBackToOpus(t *testing.T) {
	unknown := EstimateCostUSD("some-future-model-9", 1_000_000, 0, 0, 1_000_000)
	opus := EstimateCostUSD("claude-opus-4-8[1m]", 1_000_000, 0, 0, 1_000_000)
	if !approxEqual(unknown, opus) {
		t.Fatalf("unknown model estimate = %v, want opus fallback %v", unknown, opus)
	}
	// Empty model id also falls back.
	if !approxEqual(EstimateCostUSD("", 1_000_000, 0, 0, 0), 5.0) {
		t.Fatalf("empty model fresh-input estimate wrong")
	}
}

// TestEstimateCostUSD_Zero verifies zero tokens cost nothing.
func TestEstimateCostUSD_Zero(t *testing.T) {
	if got := EstimateCostUSD("claude-opus-4-8[1m]", 0, 0, 0, 0); got != 0 {
		t.Fatalf("zero tokens estimate = %v, want 0", got)
	}
}
