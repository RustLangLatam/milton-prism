package application_test

import (
	"context"
	"testing"

	"milton_prism/core/services/migration/application"
	"milton_prism/core/services/migration/domain"
	"milton_prism/core/services/migration/mocks"
	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newSvcForRetry(t *testing.T) (*application.Service, *mocks.MockMigrationRepository, *mocks.MockGenerationResultReader, *mocks.MockGenerationRecordResetter, *mocks.MockGenerationJobEnqueuer) {
	t.Helper()
	repo := &mocks.MockMigrationRepository{}
	tx := &mocks.MockTransactionManager{}
	tx.On("WithTransaction", mock.Anything, mock.Anything).Return(nil)
	reader := &mocks.MockGenerationResultReader{}
	resetter := &mocks.MockGenerationRecordResetter{}
	enqueuer := &mocks.MockGenerationJobEnqueuer{}
	svc := application.NewService(repo, tx, nil, nil, nil, nil, enqueuer, nil, reader, nil, nil, nil, nil, nil, "").
		WithGenerationRecordResetter(resetter)
	return svc, repo, reader, resetter, enqueuer
}

func failedRec(name string) *migrationv1.ServiceGenerationRecord {
	return &migrationv1.ServiceGenerationRecord{ServiceName: name, Status: "failed"}
}
func doneRec(name string) *migrationv1.ServiceGenerationRecord {
	return &migrationv1.ServiceGenerationRecord{ServiceName: name, Status: "done"}
}

// TestRetryGeneration_AllFailed_ReArmsAndEnqueuesFailedOnly verifies the happy
// path: a FAILED migration with one done + one failed service retries only the
// failed service, resets its record, transitions to GENERATING, and enqueues with
// an explicit failed-only filter.
func TestRetryGeneration_AllFailed_ReArmsAndEnqueuesFailedOnly(t *testing.T) {
	svc, repo, reader, resetter, enqueuer := newSvcForRetry(t)

	repo.On("GetByID", mock.Anything, uint64(7), false).
		Return(&domain.Migration{Identifier: 7, State: domain.MigrationStateFailed}, nil)
	reader.On("ReadResults", mock.Anything, uint64(7)).
		Return([]*migrationv1.ServiceGenerationRecord{doneRec("user"), failedRec("order")}, nil)
	resetter.On("ResetServiceRecords", mock.Anything, uint64(7), []string{"order"}).Return(nil)
	repo.On("UpdateState", mock.Anything, uint64(7), domain.MigrationStateGenerating).Return(nil)
	repo.On("ClearFailureReason", mock.Anything, uint64(7)).Return(nil)
	enqueuer.On("EnqueueGeneration", mock.Anything, uint64(7), []string{"order"}).Return(nil)

	out, err := svc.RetryGeneration(context.Background(), 7, nil)
	require.NoError(t, err)
	require.Equal(t, domain.MigrationStateGenerating, out.GetState())
	resetter.AssertCalled(t, "ResetServiceRecords", mock.Anything, uint64(7), []string{"order"})
	enqueuer.AssertCalled(t, "EnqueueGeneration", mock.Anything, uint64(7), []string{"order"})
}

// TestRetryGeneration_NotFailedState_Rejected verifies a non-FAILED migration is
// rejected with the invalid-state-transition error.
func TestRetryGeneration_NotFailedState_Rejected(t *testing.T) {
	svc, repo, _, _, _ := newSvcForRetry(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).
		Return(&domain.Migration{Identifier: 7, State: domain.MigrationStateReady}, nil)

	_, err := svc.RetryGeneration(context.Background(), 7, nil)
	require.ErrorIs(t, err, domain.ErrInvalidStateTransition)
}

// TestRetryGeneration_NoFailedRecords_MIG224 verifies a FAILED migration with no
// failed service records returns MIG224.
func TestRetryGeneration_NoFailedRecords_MIG224(t *testing.T) {
	svc, repo, reader, _, _ := newSvcForRetry(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).
		Return(&domain.Migration{Identifier: 7, State: domain.MigrationStateFailed}, nil)
	reader.On("ReadResults", mock.Anything, uint64(7)).
		Return([]*migrationv1.ServiceGenerationRecord{doneRec("user")}, nil)

	_, err := svc.RetryGeneration(context.Background(), 7, nil)
	require.ErrorIs(t, err, domain.ErrNoFailedServices)
}

// TestRetryGeneration_FilterMissesFailedSet_MIG224 verifies a service_filter that
// does not intersect the failed set returns MIG224.
func TestRetryGeneration_FilterMissesFailedSet_MIG224(t *testing.T) {
	svc, repo, reader, _, _ := newSvcForRetry(t)
	repo.On("GetByID", mock.Anything, uint64(7), false).
		Return(&domain.Migration{Identifier: 7, State: domain.MigrationStateFailed}, nil)
	reader.On("ReadResults", mock.Anything, uint64(7)).
		Return([]*migrationv1.ServiceGenerationRecord{failedRec("order")}, nil)

	_, err := svc.RetryGeneration(context.Background(), 7, []string{"user"})
	require.ErrorIs(t, err, domain.ErrNoFailedServices)
}
