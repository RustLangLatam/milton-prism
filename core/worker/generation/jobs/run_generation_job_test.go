package jobs

import (
	"testing"
	"time"

	workerdomain "milton_prism/core/worker/generation/domain"
)

// TestTaskContextBudget proves the #6d per-service-scaled task budget:
// budget = baseTaskOverhead + numServices*perServiceBudget + persistOverhead,
// so a multi-service migration with a slow language no longer dies on the 2nd/3rd
// service, and the post-container persistence context stays valid.
func TestTaskContextBudget(t *testing.T) {
	cases := []struct {
		n    int
		want time.Duration
	}{
		{1, baseTaskOverhead + 1*perServiceBudget + persistOverhead},
		{2, baseTaskOverhead + 2*perServiceBudget + persistOverhead},
		{3, baseTaskOverhead + 3*perServiceBudget + persistOverhead},
		{6, baseTaskOverhead + 6*perServiceBudget + persistOverhead},
		// Clamp: < 1 service is treated as exactly one.
		{0, baseTaskOverhead + 1*perServiceBudget + persistOverhead},
		{-5, baseTaskOverhead + 1*perServiceBudget + persistOverhead},
	}
	for _, c := range cases {
		if got := taskContextBudget(c.n); got != c.want {
			t.Errorf("taskContextBudget(%d) = %s, want %s", c.n, got, c.want)
		}
	}
}

// TestTaskContextBudget_ScalesAndCoversHeavyService verifies two invariants the
// fix exists for:
//  1. the budget strictly grows with the number of services (a slow service can
//     never starve the next), and
//  2. each added service contributes at least one heavy-tier container timeout
//     (90 min) plus headroom, so persistence after the container exits is safe.
func TestTaskContextBudget_ScalesAndCoversHeavyService(t *testing.T) {
	const heavyContainerTimeout = 90 * time.Minute
	if perServiceBudget < heavyContainerTimeout {
		t.Fatalf("perServiceBudget=%s must be >= heavy container timeout %s (#6d/#6e lockstep)",
			perServiceBudget, heavyContainerTimeout)
	}
	prev := taskContextBudget(1)
	for n := 2; n <= 8; n++ {
		cur := taskContextBudget(n)
		if cur <= prev {
			t.Fatalf("budget did not grow: n=%d gave %s, n=%d gave %s", n-1, prev, n, cur)
		}
		if delta := cur - prev; delta < heavyContainerTimeout {
			t.Errorf("per-extra-service delta=%s < heavy container timeout %s", delta, heavyContainerTimeout)
		}
		prev = cur
	}
}

// TestPayloadServiceCount proves the service-count estimate: an explicit
// ServiceFilter yields its exact length (Camino B drive path), while an empty
// filter (generate-all) falls back to the conservative default estimate.
func TestPayloadServiceCount(t *testing.T) {
	if got := payloadServiceCount(workerdomain.JobPayload{ServiceFilter: []string{"user", "profile", "billing"}}); got != 3 {
		t.Errorf("explicit filter: got %d, want 3", got)
	}
	if got := payloadServiceCount(workerdomain.JobPayload{ServiceFilter: []string{"user"}}); got != 1 {
		t.Errorf("single filter: got %d, want 1", got)
	}
	if got := payloadServiceCount(workerdomain.JobPayload{}); got != defaultServiceCountEstimate {
		t.Errorf("generate-all: got %d, want %d", got, defaultServiceCountEstimate)
	}
	if got := payloadServiceCount(workerdomain.JobPayload{ServiceFilter: []string{}}); got != defaultServiceCountEstimate {
		t.Errorf("empty filter: got %d, want %d", got, defaultServiceCountEstimate)
	}
}
