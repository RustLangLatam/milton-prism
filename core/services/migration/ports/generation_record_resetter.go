package ports

import "context"

// GenerationRecordResetter resets the per-service generation records of failed
// services so a retry starts from a clean slate. It is the write-path companion
// to GenerationResultReader, operating on the same generation_results collection.
//
// ResetServiceRecords moves each named service's record back to the pending
// status and clears its failure fields (failure_reason, failure_class). This
// gives the panel instant feedback (the failed rows flip to pending) the moment
// RetryGeneration is accepted, before the worker picks the job up and overwrites
// them with fresh generating/done/failed outcomes.
type GenerationRecordResetter interface {
	ResetServiceRecords(ctx context.Context, migrationID uint64, serviceNames []string) error
}
