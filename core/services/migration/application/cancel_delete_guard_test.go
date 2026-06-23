package application

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"milton_prism/core/services/migration/domain"
)

// TestCancelDeleteStateGuards is the single source of truth for the cancel/delete
// model: CANCELABLE == in-progress, DELETABLE == finished (not in-progress). The
// two sets are complementary and total over the real (non-unspecified) states.
// READY is the corrected case: not cancelable, deletable.
func TestCancelDeleteStateGuards(t *testing.T) {
	cases := []struct {
		state          domain.MigrationState
		wantCancelable bool
		wantDeletable  bool
	}{
		// In-progress: cancelable, not deletable.
		{domain.MigrationStatePending, true, false},
		{domain.MigrationStateAnalyzing, true, false},
		{domain.MigrationStateDesigning, true, false},
		{domain.MigrationStateAwaitingApproval, true, false},
		{domain.MigrationStateGenerating, true, false},
		{domain.MigrationStateTesting, true, false},
		// Finished: not cancelable, deletable. READY is the corrected case.
		{domain.MigrationStateReady, false, true},
		{domain.MigrationStatePushed, false, true},
		{domain.MigrationStateFailed, false, true},
		{domain.MigrationStateCancelled, false, true},
		{domain.MigrationStateRestructuringReady, false, true},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.wantCancelable, isCancelableMigrationState(tc.state), "cancelable state=%v", tc.state)
		assert.Equal(t, tc.wantDeletable, isDeletableMigrationState(tc.state), "deletable state=%v", tc.state)
		// Complementary and total over real states.
		assert.NotEqual(t, isCancelableMigrationState(tc.state), isDeletableMigrationState(tc.state), "must be complementary state=%v", tc.state)
	}
}
