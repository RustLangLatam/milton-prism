// Package assembler builds a complete, standalone Go monorepo deliverable
// from the static skeleton of the Milton Prism monorepo and the generated
// artifacts of a specific migration.
//
// Callers supply a list of InputFile records (path + UTF-8 content, already
// loaded from generation_file_artifacts) and the path to PRISM_MONOREPO_PATH.
// The assembler reads the static skeleton from disk and merges it with the
// generated files. Generated files always win when a path collides with a
// skeleton file (e.g. pkg/gateway/common/error/message_error.go).
//
// The output is a []File slice suitable for writing to a ZIP archive or to a
// git push. No service-specific logic lives here — callers own DB access.
package assembler

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// InputFile is one generated source file from generation_file_artifacts.
type InputFile struct {
	Path    string // canonical monorepo-relative path, e.g. core/services/user/wire.go
	Content string // UTF-8 source
}

// File is one file in the assembled deliverable.
type File struct {
	Path    string // monorepo-relative path
	Content []byte // file content
}

// Assembler merges a static skeleton from a monorepo root with generated artifacts.
type Assembler struct {
	skeletonRoot  string // absolute path to PRISM_MONOREPO_PATH
	useApiGateway bool   // whether to include the generated API gateway entrypoint
}

// New returns an Assembler that reads skeleton files from skeletonRoot.
// useApiGateway controls whether api-gateway/cmd/... is synthesised; false omits it.
func New(skeletonRoot string, useApiGateway bool) *Assembler {
	return &Assembler{skeletonRoot: skeletonRoot, useApiGateway: useApiGateway}
}

// Assemble returns the full set of files for a standalone, compilable deliverable.
// Generated artifacts override any skeleton file at the same relative path.
func (a *Assembler) Assemble(artifacts []InputFile) ([]File, error) {
	// 1. Collect skeleton files into a map keyed by relative path.
	skeleton := make(map[string][]byte)
	if err := a.walkSkeleton(skeleton); err != nil {
		return nil, fmt.Errorf("assembler: read skeleton: %w", err)
	}

	// 2. Merge: generated artifacts override skeleton at the same path.
	// Use a map to deduplicate; artifacts win over skeleton.
	merged := make(map[string][]byte, len(skeleton)+len(artifacts))
	for p, c := range skeleton {
		merged[p] = c
	}
	for _, f := range artifacts {
		if f.Path == "" {
			continue
		}
		merged[f.Path] = []byte(f.Content)
	}

	// 3. Append config.toml.example files (per-service, always), per-service
	// Makefiles, and the API gateway entrypoint (conditional on useApiGateway).
	// Neither ever contains real credentials.
	if err := generateConfigExamples(merged); err != nil {
		return nil, fmt.Errorf("assembler: config examples: %w", err)
	}
	if err := generateServiceMakefiles(merged); err != nil {
		return nil, fmt.Errorf("assembler: service makefiles: %w", err)
	}
	if a.useApiGateway {
		if err := generateGatewayCode(merged, a.skeletonRoot); err != nil {
			return nil, fmt.Errorf("assembler: gateway code: %w", err)
		}
	}

	// 4. Flatten to sorted slice for deterministic output.
	out := make([]File, 0, len(merged))
	for p, c := range merged {
		out = append(out, File{Path: p, Content: c})
	}
	sortFiles(out)
	return out, nil
}

// walkSkeleton reads all skeleton-eligible files from a.skeletonRoot.
func (a *Assembler) walkSkeleton(dst map[string][]byte) error {
	return filepath.WalkDir(a.skeletonRoot, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(a.skeletonRoot, abs)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel) // canonical forward-slash paths

		if d.IsDir() {
			if skipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}

		if !isSkeletonFile(rel) {
			return nil
		}

		content, readErr := os.ReadFile(abs)
		if readErr != nil {
			return fmt.Errorf("assembler: read %s: %w", rel, readErr)
		}
		dst[rel] = content
		return nil
	})
}

// skipDir returns true for directories that should be skipped entirely.
// This prunes large subtrees that never contribute skeleton files.
func skipDir(rel string) bool {
	// Top-level dirs to skip wholesale.
	skip := []string{
		".git", "infra", "docs", "python", "bin", "node_modules",
		"milton-prism-panel",
		// core/cmd and core/services contain platform service entrypoints —
		// only the generated service cmd dirs arrive via artifacts, not the skeleton.
		"core/cmd", "core/services",
		// api-gateway is the Milton Prism HTTP gateway, not part of the deliverable.
		"api-gateway",
		// Platform-only pb/gen subtrees: identity, migration, analysis, repository.
		// Shared types (pagination, query_params) are allowed via isSkeletonFile.
		"pkg/pb/gen/milton_prism/services",
		"pkg/pb/gen/milton_prism/types/identity",
		"pkg/pb/gen/milton_prism/types/migration",
		"pkg/pb/gen/milton_prism/types/analysis",
		"pkg/pb/gen/milton_prism/types/repository",
		"pkg/pb/gen/milton_prism/types/common",
		// protobuf source trees for platform services — buf configs at
		// protobuf/ root are included as exact files in isSkeletonFile.
		"protobuf/proto",
	}
	for _, s := range skip {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return false
}

// isSkeletonFile returns true when the file at rel should be included in the
// deliverable skeleton. Generated artifacts may still override it at merge time.
func isSkeletonFile(rel string) bool {
	// ── Exact root-level files ──────────────────────────────────────────────
	switch rel {
	case "go.mod", "go.sum", "Makefile":
		return true
	}

	// ── buf config files ────────────────────────────────────────────────────
	switch rel {
	case "protobuf/buf.yaml", "protobuf/buf.go.gen.yaml", "protobuf/buf.docs.gen.yaml":
		return true
	}

	// ── pkg/pb/gen — shared types and proto-registration packages ────────────
	// openapiv3 provides the blank-import side-effect used by every generated
	// .pb.go file; token/v1 is required by core/shared/auth_token.
	for _, dir := range []string{
		"pkg/pb/gen/openapiv3/",
		"pkg/pb/gen/milton_prism/types/token/",
		"pkg/pb/gen/milton_prism/types/pagination/",
		"pkg/pb/gen/milton_prism/types/query_params/",
	} {
		if strings.HasPrefix(rel, dir) && strings.HasSuffix(rel, ".go") {
			return true
		}
	}

	// ── grpc_client_sdk exclusions ───────────────────────────────────────────
	// builder.go is generic; the 3 platform-specific clients import platform
	// service stubs that are not present in the deliverable. Generated services
	// never call these clients directly, so they are safe to drop.
	switch rel {
	case "core/shared/grpc_client_sdk/grpc_analysis_client.go",
		"core/shared/grpc_client_sdk/grpc_identity_client.go",
		"core/shared/grpc_client_sdk/grpc_repository_client.go":
		return false
	}

	// ── pkg/gateway/common/error — all *_errors.go, not message_error.go ────
	// All *_errors.go files are pure map[string]string with no imports — safe
	// to include. message_error.go is generated by __pipeline__ and arrives
	// via artifacts (it references variables from all *_errors.go files).
	if strings.HasPrefix(rel, "pkg/gateway/common/error/") {
		return strings.HasSuffix(rel, "_errors.go")
	}

	// ── Recursive directories — all .go files ──────────────────────────────
	// pkg/gateway/ is included here (minus the error/ sub-dir handled above).
	for _, dir := range []string{
		"core/internal/",
		"core/shared/",
		"pkg/config/",
		"pkg/log/",
		"pkg/pb/impl/",
		"pkg/utils/",
		"pkg/gateway/",
	} {
		if strings.HasPrefix(rel, dir) && strings.HasSuffix(rel, ".go") {
			return true
		}
	}

	return false
}

// sortFiles sorts a File slice by path for deterministic output.
func sortFiles(files []File) {
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].Path < files[j-1].Path; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
}
