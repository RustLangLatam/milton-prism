package agent

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	applog "milton_prism/pkg/log"
)

// maxWorkspaceFileBytes is the file size threshold above which a file is not
// copied into the generation workspace. No legitimate .go or .proto file
// approaches this limit; compiled binaries and data blobs regularly exceed it.
// This acts as a universal backstop so future large files are silently excluded
// without needing to update any exclusion list.
const maxWorkspaceFileBytes = 512 * 1024 // 512 KiB

// workspaceExcludes are top-level directory names skipped when copying the
// monorepo into an ephemeral generation workspace. They contain no Go source
// needed for compilation, or are large enough to hurt copy performance.
var workspaceExcludes = []string{
	".git",
	".frontend", // stale frontend copy with ~200 MB of node_modules
	"frontend",
	"infra",
	"bin", // compiled worker binaries (~35 MB each)
}

// serviceArtifactDirs returns workspace-relative directory paths that a
// successful generation creates for the given service. These are removed
// before the agent runs so it starts with a clean slate.
func serviceArtifactDirs(serviceName string) []string {
	return []string{
		filepath.Join("core", "services", serviceName),
		filepath.Join("core", "cmd", serviceName+"-services"),
		filepath.Join("protobuf", "proto", "milton_prism", "types", serviceName),
		filepath.Join("protobuf", "proto", "milton_prism", "services", serviceName),
		filepath.Join("pkg", "pb", "gen", "milton_prism", "types", serviceName),
		filepath.Join("pkg", "pb", "gen", "milton_prism", "services", serviceName),
	}
}

// serviceArtifactFiles returns workspace-relative individual files that a
// successful generation creates for the given service.
func serviceArtifactFiles(serviceName string) []string {
	return []string{
		filepath.Join("pkg", "gateway", "common", "error", serviceName+"_errors.go"),
	}
}

// PrepareWorkspace copies the monorepo at baseDir to a fresh temp directory,
// removes pre-existing artifacts for serviceName (so the agent starts clean),
// and returns the workspace path plus a cleanup function that must be deferred.
// tempBaseDir controls where the temp dir is created; pass "" to use the OS
// default (/tmp). When running inside Docker (DooD), pass the host-mapped
// shared workspace path so the Docker daemon can resolve the bind mount.
func PrepareWorkspace(baseDir, serviceName, tempBaseDir string) (workspaceDir string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp(tempBaseDir, "prism-gen-"+serviceName+"-*")
	if err != nil {
		return "", nil, fmt.Errorf("workspace: mktemp: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	if err := copyMonorepo(baseDir, tmpDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("workspace: copy: %w", err)
	}

	// The generation-worker runs as root (uid=0); os.MkdirTemp and copyMonorepo
	// create directories owned by root. The agent container runs as prism
	// (uid=1000), which is "other" relative to root-owned dirs. Without explicit
	// write permission for "other", the agent cannot create new service files.
	// chmod 0777 grants write access; the workspace is ephemeral (cleaned up
	// immediately after the job), so the wide permission is safe here.
	if err := chmodWorkspaceDirs(tmpDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("workspace: chmod dirs: %w", err)
	}

	// Remove service-specific artifacts so the agent generates them fresh.
	for _, rel := range serviceArtifactDirs(serviceName) {
		if err := os.RemoveAll(filepath.Join(tmpDir, rel)); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("workspace: remove %s: %w", rel, err)
		}
	}
	for _, rel := range serviceArtifactFiles(serviceName) {
		path := filepath.Join(tmpDir, rel)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanup()
			return "", nil, fmt.Errorf("workspace: remove %s: %w", rel, err)
		}
	}

	// Patch the gateway error lookup to remove any reference to the service
	// being regenerated — the agent re-adds it as part of its generation.
	if err := removeServiceFromErrorLookup(tmpDir, serviceName); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("workspace: patch message_error.go: %w", err)
	}

	return tmpDir, cleanup, nil
}

// removeServiceFromErrorLookup removes the "<service>ErrorMessages," line from
// the lookupErrorMessage function in message_error.go so the workspace compiles
// cleanly before the agent regenerates the gateway error file.
func removeServiceFromErrorLookup(workspaceDir, serviceName string) error {
	path := filepath.Join(workspaceDir, "pkg", "gateway", "common", "error", "message_error.go")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	target := serviceName + "ErrorMessages,"
	lines := strings.Split(string(data), "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if !strings.Contains(line, target) {
			filtered = append(filtered, line)
		}
	}
	if len(filtered) == len(lines) {
		return nil // nothing to patch
	}
	return os.WriteFile(path, []byte(strings.Join(filtered, "\n")), 0644)
}

// fileSnapshot records mtime for every file under dir.
type fileSnapshot map[string]time.Time

// snapshotFiles walks dir and records the mtime of each regular file.
// Paths in the returned map are relative to dir.
func snapshotFiles(dir string) (fileSnapshot, error) {
	snap := make(fileSnapshot)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		snap[rel] = info.ModTime()
		return nil
	})
	return snap, err
}

// diffFiles returns paths that appear in after but not in before, or whose
// mtime is strictly after every mtime in before (new or modified files).
func diffFiles(before, after fileSnapshot) []string {
	var out []string
	for rel, mt := range after {
		if _, existed := before[rel]; !existed {
			out = append(out, rel)
			continue
		}
		if mt.After(before[rel]) {
			out = append(out, rel)
		}
	}
	return out
}

// chmodWorkspaceDirs walks dir and sets every directory to 0777 so the agent
// container (uid=1000, prism) can create files in directories that were copied
// from the monorepo and are owned by root (uid=0, the generation-worker user).
func chmodWorkspaceDirs(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		return os.Chmod(path, 0777)
	})
}

// copyMonorepo copies baseDir to dstDir, skipping workspaceExcludes and
// root-level binary/archive files that serve no purpose in a code-generation
// workspace (compiled Go binaries, zip archives, etc.).
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
		// Skip symlinks — the workspace needs no references outside the monorepo.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		// Skip root-level executables (compiled Go binaries) and archives: they
		// are never needed for generation and can be tens of MB each.
		if !d.IsDir() && isRootLevelBinary(rel, d) {
			return nil
		}
		// Universal size cap: no legitimate .go/.proto file exceeds 512 KiB;
		// any file that does is a binary or data blob and must not enter the
		// workspace regardless of its name, location, or extension.
		if !d.IsDir() {
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			if info.Size() > maxWorkspaceFileBytes {
				applog.Warningf("workspace: skip large file rel=%s size=%d bytes (max=%d)",
					rel, info.Size(), maxWorkspaceFileBytes)
				return nil
			}
		}
		dst := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		return copyFile(path, dst)
	})
}

// isRootLevelBinary reports whether rel is a root-level non-directory file
// that should not be copied into a generation workspace. It matches:
//   - known archive extensions (.zip, .tar, .tar.gz, .tar.bz, .tar.bz2)
//   - files with any execute bit set (ELF binaries built with go build)
//
// Only root-level entries (no path separator) are considered so that, e.g.,
// script files inside subdirectories are not accidentally excluded.
func isRootLevelBinary(rel string, d fs.DirEntry) bool {
	if strings.ContainsRune(rel, os.PathSeparator) || d.IsDir() {
		return false
	}
	lower := strings.ToLower(rel)
	for _, ext := range []string{".zip", ".tar", ".tar.gz", ".tar.bz", ".tar.bz2"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	info, err := d.Info()
	if err != nil {
		return false
	}
	return info.Mode()&0111 != 0
}

func shouldExclude(rel string) bool {
	top := strings.SplitN(rel, string(os.PathSeparator), 2)[0]
	for _, ex := range workspaceExcludes {
		if top == ex {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	buf := make([]byte, 32*1024)
	_, err = io.CopyBuffer(out, in, buf)
	return err
}

// writeCombinedPrompt writes the -p prompt content to workspaceDir/_prompt.md.
// The prompt references the generator prompt file and includes boundary spec
// and proto content inline so the agent has everything without a round-trip.
func writeCombinedPrompt(workspaceDir string, generatorPromptRef, serviceName, errorPrefix, outputProfile, boundarySpec, protoContent string) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("You are a code-generation agent. Your task is to materialise a complete Go microservice into this workspace by WRITING FILES using the Write and Edit tools. ")
	buf.WriteString("Do NOT output code as text blocks in your response — every file must be created on disk via tool calls.\n\n")
	buf.WriteString("Step 1: Read ")
	buf.WriteString(generatorPromptRef)
	buf.WriteString(" for the complete step-by-step generation workflow.\n")
	buf.WriteString("Step 2: Read docs/prism/milton-prism-architecture-canon.md and docs/prism/milton-prism-go-profile.md in full before writing any code.\n")
	buf.WriteString("Step 3: Follow the workflow exactly — write protos, run buf generate, write service code, run go build, run go test.\n\n")
	buf.WriteString("Generate a new service with the following inputs:\n\n")
	buf.WriteString("Service Name: ")
	buf.WriteString(serviceName)
	buf.WriteString("\nError Prefix: ")
	buf.WriteString(errorPrefix)
	buf.WriteString("\nOutput Profile: ")
	buf.WriteString(outputProfile)
	buf.WriteString("\n\n## Boundary Spec\n\n```yaml\n")
	buf.WriteString(strings.TrimSpace(boundarySpec))
	buf.WriteString("\n```\n\n## Proto Contract\n\n```proto\n")
	buf.WriteString(strings.TrimSpace(protoContent))
	buf.WriteString("\n```\n")

	promptPath := filepath.Join(workspaceDir, "_prompt.md")
	if err := os.WriteFile(promptPath, buf.Bytes(), 0644); err != nil {
		return "", err
	}
	return promptPath, nil
}
