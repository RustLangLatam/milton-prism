package adapters

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"milton_prism/core/worker/decomposition/ports"
	applog "milton_prism/pkg/log"
)

var _ ports.SourceAcquirer = (*GitWorkspaceAcquirer)(nil)

// GitWorkspaceAcquirer clones a git repository into a temporary workspace
// for the decomposition pipeline's stage 5 (contract derivation).
type GitWorkspaceAcquirer struct{}

// NewGitWorkspaceAcquirer creates a GitWorkspaceAcquirer.
func NewGitWorkspaceAcquirer(_ string) *GitWorkspaceAcquirer {
	return &GitWorkspaceAcquirer{}
}

// Acquire clones the repository at remoteURL into a fresh temporary directory.
// When branch is empty the remote's HEAD branch is used (git default).
func (a *GitWorkspaceAcquirer) Acquire(ctx context.Context, remoteURL, branch string) (string, func(), error) {
	noop := func() {}
	if remoteURL == "" || strings.HasPrefix(remoteURL, "repo://") {
		return "", noop, fmt.Errorf("git workspace acquirer: invalid URL %q", remoteURL)
	}

	workDir, err := os.MkdirTemp("", "prism-decomp-workspace-*")
	if err != nil {
		return "", noop, fmt.Errorf("git workspace acquirer: create temp dir: %w", err)
	}
	cleanup := func() {
		if removeErr := os.RemoveAll(workDir); removeErr != nil {
			applog.Warningf("decomposition-worker: failed to remove workspace %s: %v", workDir, removeErr)
		}
	}

	args := []string{"clone", "--depth=1", "--single-branch"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, remoteURL, workDir)

	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("git workspace acquirer: clone failed: %w\n%s", err, out)
	}

	applog.Infof("decomposition-worker: workspace acquired url=%s branch=%s path=%s", remoteURL, branch, workDir)
	return workDir, cleanup, nil
}
