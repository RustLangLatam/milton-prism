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

	// The grpc-api-gateway is only meaningful for the microservices+gRPC cell: it
	// is the gRPC→JSON transcoding entry point in front of N gRPC services. It is
	// therefore emitted ONLY when topology is MICROSERVICES AND transport is gRPC
	// (and use_api_gateway is requested). A MONOLITH is its own HTTP-native entry
	// point (no gateway), and an HTTP service speaks HTTP natively (no transcoder
	// needed) — both exclude the gateway regardless of use_api_gateway.
	// Target nil (old migrations, pre-protocol axis) keeps the legacy default of
	// including the gateway (those were all microservices+gRPC).
	useApiGateway := m.GetTarget() == nil ||
		(m.GetTarget().GetUseApiGateway() &&
			m.GetTarget().GetTopology() != domain.TargetTopologyMonolith &&
			m.GetTarget().GetInterServiceTransport() != domain.TransportHTTP)

	raw, err := s.fileArtifactReader.ListArtifacts(ctx, migrationID, "")
	if err != nil {
		return nil, fmt.Errorf("download-deliverable: read artifacts migration_id=%d: %w", migrationID, err)
	}

	inputs := make([]assembler.InputFile, len(raw))
	for i, f := range raw {
		inputs[i] = assembler.InputFile{Path: f.Path, Content: f.Content}
	}

	// Select the skeleton/post-step profile from the migration target: "python"
	// emits a Python-only deliverable (no Go scaffolding); "go" is unchanged.
	profile := outputProfileLabel(m.GetTarget())
	// Select the transport variant: Go + HTTP excludes the pkg/gateway/ subtree
	// (except common/error) because an HTTP-native service is its own entrypoint.
	protocol := protocolLabel(m.GetTarget())
	// Select the persistence-config variant: Go + SQL (PostgreSQL or MySQL/MariaDB,
	// both via GORM) emits a DATABASE_URL/DB_* .env.example instead of the Mongo
	// config.toml.example. UNSPECIFIED (Auto) canonicalises to "mongodb" here; the
	// worker resolved the real engine at generation time and the GORM repos already
	// carry their own .env via the agent.
	store := storeLabel(m.GetTarget())

	files, err := assembler.New(s.monorepoPath, useApiGateway, profile, protocol, store).Assemble(inputs)
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
