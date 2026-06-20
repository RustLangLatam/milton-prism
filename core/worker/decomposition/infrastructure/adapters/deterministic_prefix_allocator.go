package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"milton_prism/core/worker/decomposition/ports"
)

var _ ports.PrefixAllocator = (*DeterministicPrefixAllocator)(nil)

// DeterministicPrefixAllocator derives a 3-char uppercase error prefix from a
// service name. The first three uppercase characters of the name are used as
// the base (padded with "X" when the name is shorter). On collision, a counter
// suffix replaces the third character ("ART", "AR2", "AR3", ...).
//
// This is the v1 in-process allocator. The long-term integration point is the
// orchestrator registry (see decomposition spec §4, stage 4).
type DeterministicPrefixAllocator struct {
	mu       sync.Mutex
	assigned map[string]string // prefix → first service name that claimed it
}

// NewDeterministicPrefixAllocator returns a ready-to-use prefix allocator.
func NewDeterministicPrefixAllocator() *DeterministicPrefixAllocator {
	return &DeterministicPrefixAllocator{assigned: make(map[string]string)}
}

// Allocate returns a unique 3-char prefix for serviceName. The same service
// always returns the same prefix on repeated calls.
func (a *DeterministicPrefixAllocator) Allocate(_ context.Context, serviceName string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	upper := strings.ToUpper(serviceName)
	// Ensure at least 3 characters by padding with "X".
	for len(upper) < 3 {
		upper += "X"
	}
	base := upper[:3]

	// Try the clean base first ("ART"), then "AR2", "AR3", … on collision.
	candidate := base
	for i := 2; ; i++ {
		owner, taken := a.assigned[candidate]
		if !taken {
			a.assigned[candidate] = serviceName
			return candidate, nil
		}
		if owner == serviceName {
			return candidate, nil
		}
		if i > 99 {
			break
		}
		candidate = fmt.Sprintf("%s%d", base[:2], i)
	}

	return "", fmt.Errorf("prefix allocator exhausted candidates for service %q", serviceName)
}
