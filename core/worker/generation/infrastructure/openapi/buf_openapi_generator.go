// Package openapi provides the buf-backed implementation of the
// OpenAPIGenerator port. It runs `buf generate` with the deliverable OpenAPI
// template inside the generation-agent image (which carries protoc-gen-openapi
// on PATH) and returns the produced docs/openapi.yaml.
package openapi

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"milton_prism/core/worker/generation/ports"
	applog "milton_prism/pkg/log"
)

const (
	generationAgentImage  = "milton-prism-generation-agent:latest"
	generationNetworkName = "prism-generation"
	defaultCPUQuota       = 50_000         // 50% of one CPU
	defaultMemoryBytes    = int64(1) << 30 // 1 GiB
	defaultTimeout        = 10 * time.Minute

	// deliverableTemplate is the buf template (relative to protobuf/) that
	// emits exactly one ../docs/openapi.yaml from the deliverable's protos.
	deliverableTemplate = "buf.deliverable.openapi.yaml"
	// openAPIOutPath is where the template writes the spec, relative to the
	// workspace root (template out: ../docs, run from protobuf/).
	openAPIOutPath = "docs/openapi.yaml"

	// maxWorkspaceFileBytes mirrors the agent workspace cap: no legitimate
	// .proto/.go file approaches it; larger files are binaries/blobs.
	maxWorkspaceFileBytes = 512 * 1024
)

// workspaceExcludes are top-level directory names skipped when copying the
// monorepo into the ephemeral OpenAPI workspace. None contain protos needed by
// the deliverable template and several are large.
var workspaceExcludes = []string{
	".git", ".frontend", "frontend", "infra", "bin", "node_modules",
}

// platformServiceProtoPrefix is the workspace-relative path prefix of the
// PLATFORM service protos (migration/repository/identity/billing/analysis and
// any other Milton Prism gateway service). These are deliberately NOT copied
// into the OpenAPI workspace.
//
// DEFECT 1 root cause: `buf generate proto` compiles the WHOLE `proto` buf
// module (buf.yaml: modules: - path: proto), so every service proto under this
// tree lands in the FileDescriptorSet. The RustLangLatam protoc-gen-openapi
// fork iterates the FULL set of files in the descriptor (proto_file / imports)
// rather than honouring `file_to_generate` — so `--path` does NOT scope the
// emitted spec, and the deliverable openapi.yaml ends up carrying every
// platform endpoint (MigrationService, RepositoryService, …).
//
// The deterministic, content-correct fix is to keep the platform service protos
// out of the workspace entirely. Service protos never import other service
// protos (verified: they import only types/**, google/api, openapiv3), so
// dropping them does not break import resolution for the types they share. The
// migration's OWN generated service protos are written back via the overlay in
// Generate, so the spec contains EXACTLY the generated services and nothing
// else. types/** stay (message-only, define no services → no spurious paths).
const platformServiceProtoPrefix = "protobuf/proto/milton_prism/services"

// staleOpenAPIRelPath is the deliverable spec path inside the monorepo. The
// monorepo ships a committed docs/openapi.yaml that is the PLATFORM panel spec
// (MigrationService, RepositoryService, …). copyMonorepo must NOT copy it into
// the workspace: protoc-gen-openapi runs in merged output_mode and the agent
// (uid=1000) cannot truncate the root-owned 0644 copy, so a pre-existing file
// causes buf to leave the stale PLATFORM spec in place — the worker then reads
// it back verbatim (198 KB, full platform API) instead of the freshly generated
// deliverable spec. Excluding it guarantees buf writes a clean file from the
// migration's protos only. (This was the real DEFECT 1 cause; the platform
// service-proto exclusion above is also necessary but not sufficient on its own.)
const staleOpenAPIRelPath = "docs/openapi.yaml"

var _ ports.OpenAPIGenerator = (*BufOpenAPIGenerator)(nil)

// BufOpenAPIGenerator implements OpenAPIGenerator by running buf inside an
// ephemeral container provisioned by the given ContainerRunner.
type BufOpenAPIGenerator struct {
	runner      ports.ContainerRunner
	image       string
	networkName string
	cpuQuota    int64
	memoryBytes int64
	timeout     time.Duration
	goModCache  string
	// workspaceTempDir is the base dir for ephemeral workspaces; must be a host
	// path visible to the Docker daemon when running inside Docker (DooD).
	workspaceTempDir string
}

// NewBufOpenAPIGenerator constructs a generator backed by runner.
func NewBufOpenAPIGenerator(runner ports.ContainerRunner) *BufOpenAPIGenerator {
	return &BufOpenAPIGenerator{
		runner:      runner,
		image:       generationAgentImage,
		networkName: generationNetworkName,
		cpuQuota:    defaultCPUQuota,
		memoryBytes: defaultMemoryBytes,
		timeout:     defaultTimeout,
	}
}

// WithGoModCache mounts the host module cache read-only at /go/pkg/mod.
func (g *BufOpenAPIGenerator) WithGoModCache(hostPath string) *BufOpenAPIGenerator {
	g.goModCache = hostPath
	return g
}

// WithWorkspaceTempDir sets the base directory for ephemeral workspaces.
// Required when the worker itself runs inside Docker (DooD).
func (g *BufOpenAPIGenerator) WithWorkspaceTempDir(dir string) *BufOpenAPIGenerator {
	g.workspaceTempDir = dir
	return g
}

// Generate copies workspaceBase to a temp dir, writes protoArtifacts into it,
// runs `buf generate proto --template buf.deliverable.openapi.yaml` inside the
// agent container, and returns the bytes of the produced docs/openapi.yaml.
func (g *BufOpenAPIGenerator) Generate(ctx context.Context, workspaceBase string, protoArtifacts []ports.ProtoArtifact) ([]byte, error) {
	if len(protoArtifacts) == 0 {
		return nil, fmt.Errorf("openapi generator: no proto artifacts")
	}

	if err := g.runner.EnsureNetwork(ctx, g.networkName); err != nil {
		return nil, fmt.Errorf("openapi generator: network: %w", err)
	}

	tmpDir, err := os.MkdirTemp(g.workspaceTempDir, "prism-openapi-*")
	if err != nil {
		return nil, fmt.Errorf("openapi generator: mktemp: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := copyMonorepo(workspaceBase, tmpDir); err != nil {
		return nil, fmt.Errorf("openapi generator: copy monorepo: %w", err)
	}

	// DEFECT 1 guard: the platform service-proto tree must NOT survive the copy
	// (shouldExclude drops it). If it ever reappears (e.g. an overlaid generated
	// proto is mistakenly a platform service), the spec would carry platform
	// endpoints. Log the state so regressions are visible in worker logs.
	platformDir := filepath.Join(tmpDir, filepath.FromSlash(platformServiceProtoPrefix))
	if entries, statErr := os.ReadDir(platformDir); statErr == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		applog.Infof("openapi generator: service-proto dirs present after copy+overlay: %v", names)
	} else {
		applog.Infof("openapi generator: no service-proto dir after copy (will be created by overlay)")
	}

	// Overlay the generated protos. They overwrite any same-path file from the
	// base copy, so the spec reflects exactly the migration's protos.
	for _, a := range protoArtifacts {
		clean := filepath.Clean(filepath.FromSlash(a.Path))
		if strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(clean) {
			return nil, fmt.Errorf("openapi generator: unsafe artifact path %q", a.Path)
		}
		dst := filepath.Join(tmpDir, clean)
		if err := os.MkdirAll(filepath.Dir(dst), 0o777); err != nil {
			return nil, fmt.Errorf("openapi generator: mkdir for %s: %w", a.Path, err)
		}
		if err := os.WriteFile(dst, a.Content, 0o644); err != nil {
			return nil, fmt.Errorf("openapi generator: write %s: %w", a.Path, err)
		}
	}

	// The agent container runs as uid=1000 (prism); the worker copies dirs as
	// root. Widen dir perms so buf can write ../docs/openapi.yaml.
	if err := chmodDirs(tmpDir); err != nil {
		return nil, fmt.Errorf("openapi generator: chmod dirs: %w", err)
	}
	// Pre-create docs/ so buf can write into it regardless of skeleton state.
	if err := os.MkdirAll(filepath.Join(tmpDir, "docs"), 0o777); err != nil {
		return nil, fmt.Errorf("openapi generator: mkdir docs: %w", err)
	}

	mounts := []string{tmpDir + ":/workspace:rw"}
	if g.goModCache != "" {
		mounts = append(mounts, g.goModCache+":/go/pkg/mod:ro")
	}

	// Scope buf's emission to ONLY the migration's generated protos via one
	// `--path <relpath>` per artifact (relative to the buf root, protobuf/).
	// buf's --path filters which files are GENERATED; imports are still resolved
	// from the module, so common imported types (pagination, query_params, the
	// service's own types proto) still compile while the emitted openapi
	// paths/tags cover only the generated service(s) — not the whole platform
	// gateway API.
	pathFlags, err := bufPathFlags(protoArtifacts)
	if err != nil {
		return nil, fmt.Errorf("openapi generator: %w", err)
	}

	// buf must run from protobuf/ so the template's relative out (../docs) and
	// input (proto) resolve correctly.
	cmd := fmt.Sprintf("cd /workspace/protobuf && buf generate proto --template %s%s", deliverableTemplate, pathFlags)

	applog.Infof("openapi generator: running buf workspace=%s protos=%d cmd=%q", tmpDir, len(protoArtifacts), cmd)

	res, runErr := g.runner.Run(ctx, ports.RunRequest{
		Image:       g.image,
		Command:     []string{"sh", "-c", cmd},
		WorkDir:     "/workspace",
		BindMounts:  mounts,
		CPUQuota:    g.cpuQuota,
		MemoryBytes: g.memoryBytes,
		NetworkName: g.networkName,
		Timeout:     g.timeout,
	})
	if runErr != nil {
		return nil, fmt.Errorf("openapi generator: container: %w (stderr: %s)", runErr, truncate(res.Stderr))
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("openapi generator: buf exit=%d stderr=%s", res.ExitCode, truncate(res.Stderr))
	}

	doc, err := os.ReadFile(filepath.Join(tmpDir, openAPIOutPath))
	if err != nil {
		return nil, fmt.Errorf("openapi generator: read %s: %w (buf stdout: %s)", openAPIOutPath, err, truncate(res.Stdout))
	}
	return doc, nil
}

// bufRootPrefix is the workspace-relative prefix of the buf root. buf runs from
// /workspace/protobuf, so --path values must be relative to it: we strip this
// prefix from each artifact's workspace-relative path.
const bufRootPrefix = "protobuf/"

// bufPathFlags builds the trailing ` --path <p> --path <p>…` for the buf
// command, one entry per generated proto. Each artifact Path is
// workspace-relative (e.g. "protobuf/proto/milton_prism/services/user/v1/
// user_service.proto"); buf runs from protobuf/, so the --path value is the
// path relative to that root ("proto/milton_prism/services/user/v1/
// user_service.proto"). Paths are emitted forward-slashed (the container is
// Linux) and deduped, preserving input order.
func bufPathFlags(protoArtifacts []ports.ProtoArtifact) (string, error) {
	var b strings.Builder
	seen := make(map[string]bool, len(protoArtifacts))
	for _, a := range protoArtifacts {
		rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(a.Path)))
		if strings.HasPrefix(rel, "../") || filepath.IsAbs(filepath.FromSlash(rel)) {
			return "", fmt.Errorf("unsafe proto path %q", a.Path)
		}
		p := strings.TrimPrefix(rel, bufRootPrefix)
		if p == rel {
			// Path is not under the buf root; cannot scope it. This should not
			// happen for service/types protos.
			return "", fmt.Errorf("proto path %q is not under %q", a.Path, bufRootPrefix)
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		b.WriteString(" --path ")
		b.WriteString(p)
	}
	return b.String(), nil
}

func truncate(s string) string {
	const max = 2000
	if len(s) > max {
		return s[:max] + "…(truncated)"
	}
	return s
}

// copyMonorepo copies baseDir to dstDir, skipping workspaceExcludes, symlinks,
// and any file over maxWorkspaceFileBytes.
func copyMonorepo(baseDir, dstDir string) error {
	return filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(baseDir, path)
		if shouldExclude(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dstDir, rel), 0o755)
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Size() > maxWorkspaceFileBytes {
			return nil
		}
		return copyFile(path, filepath.Join(dstDir, rel))
	})
}

func shouldExclude(rel string) bool {
	if rel == "." {
		return false
	}
	// DEFECT 1: drop the platform service-proto tree so it never enters the
	// FileDescriptorSet that the openapi plugin walks. The migration's own
	// generated service protos are overlaid back in Generate after the copy.
	slash := filepath.ToSlash(rel)
	if slash == platformServiceProtoPrefix || strings.HasPrefix(slash, platformServiceProtoPrefix+"/") {
		return true
	}
	// Drop the committed PLATFORM docs/openapi.yaml so buf writes a fresh,
	// deliverable-scoped spec (see staleOpenAPIRelPath).
	if slash == staleOpenAPIRelPath {
		return true
	}
	top := strings.SplitN(rel, string(os.PathSeparator), 2)[0]
	for _, ex := range workspaceExcludes {
		if top == ex {
			return true
		}
	}
	return false
}

func chmodDirs(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		return os.Chmod(path, 0o777)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	buf := make([]byte, 32*1024)
	_, err = io.CopyBuffer(out, in, buf)
	return err
}
