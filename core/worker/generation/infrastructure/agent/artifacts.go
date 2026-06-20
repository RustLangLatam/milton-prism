package agent

import (
	"os"
	"path/filepath"

	workerdomain "milton_prism/core/worker/generation/domain"
	applog "milton_prism/pkg/log"
)

// captureArtifacts reads each path in paths from workspaceDir and returns the
// successfully read files as FileArtifacts. Files whose byte count exceeds
// maxArtifactBytes are skipped with a warning: generated Go source and proto
// files are always well under that threshold, so anything larger is a binary
// or archive that ended up in the diff by mistake and must not reach MongoDB's
// 16 MB per-document limit.
func captureArtifacts(workspaceDir string, paths []string) []workerdomain.FileArtifact {
	out := make([]workerdomain.FileArtifact, 0, len(paths))
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(workspaceDir, rel))
		if err != nil {
			applog.Warningf("agent invoker: capture artifact path=%s: %v", rel, err)
			continue
		}
		if len(data) > maxArtifactBytes {
			applog.Warningf("agent invoker: skip oversized artifact path=%s size=%d bytes (max=%d) — not source code",
				rel, len(data), maxArtifactBytes)
			continue
		}
		out = append(out, workerdomain.FileArtifact{Path: rel, Content: data})
	}
	return out
}
