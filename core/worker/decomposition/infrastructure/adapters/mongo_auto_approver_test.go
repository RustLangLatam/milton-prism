package adapters

import (
	"testing"

	commonv1 "milton_prism/pkg/pb/gen/milton_prism/types/common/v1"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"google.golang.org/protobuf/proto"
)

func awaitingState() int32 {
	return int32(migrationv1.MigrationState_MIGRATION_STATE_AWAITING_APPROVAL)
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestAutoApproveDecision_Proceeds_WhenArmedAndGatesPass(t *testing.T) {
	doc := autoApproveDoc{AutoApprove: true, State: awaitingState()}
	proceed, reason, err := autoApproveDecision(doc)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !proceed {
		t.Fatalf("expected proceed=true, got false reason=%q", reason)
	}
}

func TestAutoApproveDecision_Skips_WhenNotArmed(t *testing.T) {
	doc := autoApproveDoc{AutoApprove: false, State: awaitingState()}
	proceed, _, err := autoApproveDecision(doc)
	if err != nil || proceed {
		t.Fatalf("expected skip with no error, got proceed=%v err=%v", proceed, err)
	}
}

func TestAutoApproveDecision_Skips_WhenNotAwaitingApproval(t *testing.T) {
	// Armed but already advanced past the approval gate.
	doc := autoApproveDoc{
		AutoApprove: true,
		State:       int32(migrationv1.MigrationState_MIGRATION_STATE_GENERATING),
	}
	proceed, _, err := autoApproveDecision(doc)
	if err != nil || proceed {
		t.Fatalf("expected skip with no error, got proceed=%v err=%v", proceed, err)
	}
}

func TestAutoApproveDecision_Skips_NoServiceBoundaries(t *testing.T) {
	doc := autoApproveDoc{
		AutoApprove: true,
		State:       awaitingState(),
		PlanBytes:   mustMarshal(t, &migrationv1.RestructurePlan{NoServiceBoundaries: true}),
	}
	proceed, reason, err := autoApproveDecision(doc)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if proceed {
		t.Fatalf("expected skip on no_service_boundaries")
	}
	if reason == "" {
		t.Fatalf("expected a skip reason")
	}
}

func TestAutoApproveDecision_Skips_NotMigrableWithoutOverride(t *testing.T) {
	doc := autoApproveDoc{
		AutoApprove:         true,
		State:               awaitingState(),
		AssessmentBytes:     mustMarshal(t, &commonv1.MigrabilityAssessment{Verdict: migrabilityVerdictNotMigrable}),
		MigrabilityOverride: false,
	}
	proceed, reason, err := autoApproveDecision(doc)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if proceed {
		t.Fatalf("NOT_MIGRABLE without override must block the one-shot run")
	}
	if reason == "" {
		t.Fatalf("expected a skip reason")
	}
}

func TestAutoApproveDecision_Proceeds_NotMigrableWithOverride(t *testing.T) {
	doc := autoApproveDoc{
		AutoApprove:         true,
		State:               awaitingState(),
		AssessmentBytes:     mustMarshal(t, &commonv1.MigrabilityAssessment{Verdict: migrabilityVerdictNotMigrable}),
		MigrabilityOverride: true,
	}
	proceed, _, err := autoApproveDecision(doc)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !proceed {
		t.Fatalf("override must lift the NOT_MIGRABLE block")
	}
}

func TestAutoApproveDecision_Proceeds_MigrableVerdict(t *testing.T) {
	doc := autoApproveDoc{
		AutoApprove:     true,
		State:           awaitingState(),
		AssessmentBytes: mustMarshal(t, &commonv1.MigrabilityAssessment{Verdict: "MIGRABLE"}),
	}
	proceed, _, err := autoApproveDecision(doc)
	if err != nil || !proceed {
		t.Fatalf("MIGRABLE verdict must proceed, got proceed=%v err=%v", proceed, err)
	}
}

func TestMigrabilityBlocked_CorruptAssessment_FailsClosed(t *testing.T) {
	doc := autoApproveDoc{AssessmentBytes: []byte{0xff, 0xff, 0xff}}
	if !migrabilityBlocked(doc) {
		t.Fatalf("a corrupt assessment must fail closed (block), not silently lift the gate")
	}
}
