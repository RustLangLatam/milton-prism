package application

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"

	"milton_prism/core/services/migration/application/assembler"
	"milton_prism/core/services/migration/domain"
)

// DownloadDeliverable assembles the full Go monorepo deliverable (static skeleton
// + all generated artifacts for migrationID) and returns the ZIP bytes.
// The ZIP unpacks flat: go.mod sits at the archive root with no wrapping folder.
// Callable when the migration is in READY or PUSHED state.
func (s *Service) DownloadDeliverable(ctx context.Context, migrationID uint64) ([]byte, error) {
	if migrationID == 0 {
		return nil, domain.ErrMissingIdentifier
	}
	if s.monorepoPath == "" {
		return nil, fmt.Errorf("download-deliverable: PRISM_MONOREPO_PATH not configured on this instance")
	}

	m, err := s.repo.GetByID(ctx, migrationID, false)
	if err != nil {
		return nil, err
	}
	state := m.GetState()
	if state != domain.MigrationStateReady && state != domain.MigrationStatePushed {
		return nil, domain.ErrInvalidStateTransition
	}

	// Include the API gateway when Target is nil (field absent on old migrations)
	// or when explicitly enabled. proto3 bool zero == false == "not set", so we
	// default to include only when Target itself is missing; an explicit false excludes.
	// MONOLITH topology is HTTP-native and is its own entry point: never emit a
	// gateway regardless of use_api_gateway.
	useApiGateway := m.GetTarget() == nil ||
		(m.GetTarget().GetUseApiGateway() &&
			m.GetTarget().GetTopology() != domain.TargetTopologyMonolith)

	raw, err := s.fileArtifactReader.ListArtifacts(ctx, migrationID, "")
	if err != nil {
		return nil, fmt.Errorf("download-deliverable: read artifacts migration_id=%d: %w", migrationID, err)
	}

	inputs := make([]assembler.InputFile, len(raw))
	for i, f := range raw {
		inputs[i] = assembler.InputFile{Path: f.Path, Content: f.Content}
	}

	files, err := assembler.New(s.monorepoPath, useApiGateway).Assemble(inputs)
	if err != nil {
		return nil, fmt.Errorf("download-deliverable: assemble migration_id=%d: %w", migrationID, err)
	}

	return buildZip(files)
}

func buildZip(files []assembler.File) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		fh := &zip.FileHeader{
			Name:   f.Path,
			Method: zip.Deflate,
		}
		fw, err := w.CreateHeader(fh)
		if err != nil {
			return nil, fmt.Errorf("zip: create entry %s: %w", f.Path, err)
		}
		if _, err := fw.Write(f.Content); err != nil {
			return nil, fmt.Errorf("zip: write entry %s: %w", f.Path, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("zip: close: %w", err)
	}
	return buf.Bytes(), nil
}
