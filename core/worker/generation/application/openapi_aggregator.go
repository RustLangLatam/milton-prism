package application

import (
	"context"
	"path/filepath"
	"strings"

	workerdomain "milton_prism/core/worker/generation/domain"
	"milton_prism/core/worker/generation/ports"
	applog "milton_prism/pkg/log"
)

// openAPIArtifactPath is the workspace-relative path of the deliverable's
// OpenAPI spec. It is profile-agnostic and lands in the deliverable for every
// output profile (the assembler leaves docs/ untouched).
const openAPIArtifactPath = "docs/openapi.yaml"

// isServiceProtoArtifact reports whether a persisted artifact path is a
// generated service or types proto destined for the deliverable's protobuf
// tree. Only these feed the OpenAPI generator; .go / config / other artifacts
// are ignored.
func isServiceProtoArtifact(path string) bool {
	p := filepath.ToSlash(path)
	if !strings.HasSuffix(p, ".proto") {
		return false
	}
	return strings.HasPrefix(p, "protobuf/proto/milton_prism/services/") ||
		strings.HasPrefix(p, "protobuf/proto/milton_prism/types/")
}

// assembleOpenAPI generates the deliverable's docs/openapi.yaml from the proto
// artifacts of every successfully generated service and persists it as a single
// "__pipeline__" artifact (the same synthetic service used by the error
// aggregator). Because the artifact path is docs/openapi.yaml, the deliverable
// assembler carries it into the package unchanged for every profile.
//
// This method never returns an error: a missing OpenAPI generator, an empty
// proto set, a buf failure, or a persist failure are all logged as warnings and
// the migration continues. The deliverable simply ships without the spec.
func (p *Pipeline) assembleOpenAPI(ctx context.Context, migrationID uint64, pkg *ports.GenerationPackage, final []workerdomain.ServiceGenerationRecord) {
	if p.openapiGen == nil {
		applog.Infof("generation-worker: openapi generator not configured — skipping docs/openapi.yaml migration_id=%d", migrationID)
		return
	}

	doneSet := make(map[string]bool, len(final))
	for _, r := range final {
		if r.Status == workerdomain.ServiceStatusDone {
			doneSet[r.ServiceName] = true
		}
	}

	// Collect generated service/type protos from every done service. Dedupe by
	// path so a proto shared across services (should not happen, but is safe)
	// is sent once.
	seen := make(map[string]bool)
	var protos []ports.ProtoArtifact
	for _, svc := range pkg.Services {
		if !doneSet[svc.Name] {
			continue
		}
		artifacts, err := p.store.ListArtifacts(ctx, migrationID, svc.Name)
		if err != nil {
			applog.Warningf("generation-worker: openapi list artifacts service=%s: %v", svc.Name, err)
			continue
		}
		for _, a := range artifacts {
			if !isServiceProtoArtifact(a.Path) || seen[a.Path] {
				continue
			}
			seen[a.Path] = true
			protos = append(protos, ports.ProtoArtifact{Path: a.Path, Content: a.Content})
		}
	}

	if len(protos) == 0 {
		applog.Warningf("generation-worker: openapi found no service protos — skipping docs/openapi.yaml migration_id=%d", migrationID)
		return
	}

	doc, err := p.openapiGen.Generate(ctx, p.monorepoRoot, protos)
	if err != nil {
		applog.Warningf("generation-worker: openapi generate migration_id=%d: %v", migrationID, err)
		return
	}
	if len(doc) == 0 {
		applog.Warningf("generation-worker: openapi generator produced empty document migration_id=%d", migrationID)
		return
	}

	if err := p.store.UpsertArtifacts(ctx, migrationID, errorAggregatorService, []workerdomain.FileArtifact{
		{Path: openAPIArtifactPath, Content: doc},
	}); err != nil {
		applog.Warningf("generation-worker: openapi persist docs/openapi.yaml migration_id=%d: %v", migrationID, err)
		return
	}
	applog.Infof("generation-worker: openapi assembled docs/openapi.yaml protos=%d bytes=%d migration_id=%d",
		len(protos), len(doc), migrationID)
}
