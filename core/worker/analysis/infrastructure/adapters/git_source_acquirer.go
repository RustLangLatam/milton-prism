package adapters

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
	applog "milton_prism/pkg/log"
)

var _ ports.SourceAcquirer = (*GitSourceAcquirer)(nil)

// GitSourceAcquirer clones a git repository into a temporary workspace
// directory using the system git binary. The returned cleanup function removes
// the temporary directory; callers must always invoke it.
type GitSourceAcquirer struct {
	// hintBranch is an optional static default when the job payload provides no
	// branch. Empty means "detect from remote" (preferred).
	hintBranch string
}

// NewGitSourceAcquirer creates a GitSourceAcquirer. Pass an empty string (the
// normal case) to let the adapter detect the real default branch from the
// remote's HEAD reference rather than assuming "main".
func NewGitSourceAcquirer(hintBranch string) *GitSourceAcquirer {
	return &GitSourceAcquirer{hintBranch: hintBranch}
}

// Acquire clones the repository at source into a fresh temporary directory and
// returns the workspace path and the resolved HEAD commit SHA. source must be a
// valid git URL (https or ssh). A "repo://ID" placeholder is treated as an error
// — the caller must resolve the real URL before calling Acquire.
//
// Branch selection priority:
//  1. The branch argument (from job payload's default_branch, set at analysis dispatch time).
//  2. The constructor hint (static fallback, rarely used).
//  3. Remote detection via git ls-remote --symref (when 1 and 2 are both empty).
//  4. No --branch flag at all (git picks the remote HEAD) when detection also fails.
//
// If a branch is specified but the clone fails, Acquire detects the real default
// branch from the remote's HEAD symref and retries once. This tolerates stale
// repository records whose default_branch field does not match the remote.
//
// commitSHA is the resolved HEAD after a successful clone. An empty string is
// returned when rev-parse fails — callers treat it as best-effort.
func (a *GitSourceAcquirer) Acquire(ctx context.Context, source, branch string) (string, string, func(), error) {
	noop := func() {}
	if source == "" || strings.HasPrefix(source, "repo://") {
		return "", "", noop, fmt.Errorf("git source acquirer: invalid source URL %q — pass a real remote URL", source)
	}

	workDir, err := os.MkdirTemp("", "prism-workspace-*")
	if err != nil {
		return "", "", noop, fmt.Errorf("git source acquirer: create temp dir: %w", err)
	}

	// Determine which branch to clone.
	effectiveBranch := branch
	if effectiveBranch == "" {
		effectiveBranch = a.hintBranch
	}
	if effectiveBranch == "" {
		// Neither job payload nor static hint supplied a branch: probe the remote.
		detected := detectDefaultBranch(ctx, source)
		if detected != "" {
			applog.Infof("analysis-worker: detected default branch=%s source=%s", detected, redactURL(source))
			effectiveBranch = detected
		} else {
			applog.Warningf("analysis-worker: branch detection failed for source=%s — cloning without --branch (git picks HEAD)", redactURL(source))
		}
	}

	ws, cleanup, cloneErr := cloneRepo(ctx, workDir, source, effectiveBranch)
	if cloneErr != nil && effectiveBranch != "" {
		// The stored branch may be stale (e.g. repo renamed "master"→"main" or
		// vice-versa). Probe the remote's real default branch and retry once.
		applog.Warningf("analysis-worker: clone failed branch=%s — detecting real default branch source=%s err=%v",
			effectiveBranch, redactURL(source), cloneErr)
		if detected := detectDefaultBranch(ctx, source); detected != "" && detected != effectiveBranch {
			applog.Infof("analysis-worker: retrying with detected branch=%s source=%s", detected, redactURL(source))
			ws, cleanup, cloneErr = cloneRepo(ctx, workDir, source, detected)
			if cloneErr == nil {
				return ws, resolveHEAD(ctx, ws), cleanup, nil
			}
		}
		// Detection either failed or same branch — try without --branch as last resort.
		applog.Warningf("analysis-worker: retrying without --branch source=%s", redactURL(source))
		ws, cleanup, cloneErr = cloneRepo(ctx, workDir, source, "")
		if cloneErr != nil {
			_ = os.RemoveAll(workDir)
			return "", "", noop, cloneErr
		}
	} else if cloneErr != nil {
		_ = os.RemoveAll(workDir)
		return "", "", noop, cloneErr
	}

	return ws, resolveHEAD(ctx, ws), cleanup, nil
}

// cloneRepo performs a shallow clone of source into workDir/src.
// When branch is empty the --branch flag is omitted and git picks the remote HEAD.
// On failure it returns a *workerdomain.CloneError whose Message is a user-facing
// description (no credentials) and Detail holds the raw git output for operator logs.
func cloneRepo(ctx context.Context, workDir, source, branch string) (string, func(), error) {
	noop := func() {}
	args := []string{"clone", "--depth=1"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, source, "src")

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		ce := &workerdomain.CloneError{
			Message: classifyCloneError(source, out),
			Detail:  string(out),
		}
		applog.Warningf("analysis-worker: clone failed source=%s branch=%s: %s\ndetail: %s",
			redactURL(source), branch, ce.Message, ce.Detail)
		return "", noop, ce
	}

	ws := filepath.Join(workDir, "src")
	cleanup := func() { _ = os.RemoveAll(workDir) }
	applog.Infof("analysis-worker: repository cloned source=%s branch=%s workspace=%s", redactURL(source), branch, ws)
	return ws, cleanup, nil
}

// classifyCloneError converts raw git output into a user-facing error string.
// The source URL is redacted so no credentials appear in the returned message.
func classifyCloneError(source string, out []byte) string {
	lower := strings.ToLower(string(out))
	safe := redactURL(source)

	// "could not read Password" appears when git tries to prompt for credentials
	// interactively but there is no TTY — indicates bad or missing token.
	// Check this before "No such device or address" which can appear in the same
	// line as a consequence of the missing TTY, not as a network failure.
	if strings.Contains(lower, "could not read password") ||
		strings.Contains(lower, "authentication failed") ||
		strings.Contains(lower, "invalid username or password") ||
		strings.Contains(lower, "http 403") ||
		strings.Contains(lower, ": 403") {
		return fmt.Sprintf("Authentication failed for %s: the access token is invalid or does not have read permission on this repository.", safe)
	}

	// Repository not found (private repo with wrong token also shows 404 in some hosts).
	if strings.Contains(lower, "repository not found") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "http 404") ||
		strings.Contains(lower, ": 404") ||
		strings.Contains(lower, "does not exist") {
		return fmt.Sprintf("Repository not found at %s: verify the URL is correct and the token has access.", safe)
	}

	// Network / DNS failures (without the TTY-missing pattern above).
	if strings.Contains(lower, "could not resolve host") ||
		strings.Contains(lower, "no such device or address") ||
		strings.Contains(lower, "unable to connect") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "network is unreachable") {
		return fmt.Sprintf("Could not connect to %s: check the URL or network connectivity.", safe)
	}

	return fmt.Sprintf("Clone failed for %s: check the URL and access token.", safe)
}

// redactURL removes the userinfo component (token/password) from a URL so
// that it is safe to include in logs and user-facing error messages.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL
	}
	u.User = nil
	return u.String()
}

// ResolveRemoteBranchSHA returns the HEAD commit SHA for the given branch on
// the remote without cloning. Uses git ls-remote so the network round-trip is
// minimal (one HTTP request, no data transfer). Returns an empty string when
// the probe fails (auth error, network issue, branch not found) — callers treat
// it as best-effort and fall back to normal analysis.
func ResolveRemoteBranchSHA(ctx context.Context, remoteURL, branch string) string {
	if remoteURL == "" || branch == "" {
		return ""
	}
	ref := "refs/heads/" + branch
	cmd := exec.CommandContext(ctx, "git", "ls-remote", remoteURL, ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return ""
	}
	// Output format: "<sha>\t<refname>"
	parts := strings.Fields(line)
	if len(parts) < 1 {
		return ""
	}
	return parts[0]
}

// resolveHEAD runs git rev-parse HEAD inside workspacePath and returns the full
// commit SHA. Returns an empty string when the command fails (e.g. no commits,
// detached HEAD with no SHA). Callers treat the SHA as best-effort.
func resolveHEAD(ctx context.Context, workspacePath string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", workspacePath, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// detectDefaultBranch queries the remote's HEAD symref via git ls-remote to
// find the actual default branch without cloning. Returns an empty string when
// the probe fails (network error, auth failure, or unexpected output) so the
// caller falls back to cloning without --branch.
//
// Expected output when the remote exposes a symref:
//
//	ref: refs/heads/master\tHEAD
//	abc123...\tHEAD
//
// The symref line uses a tab between the ref and the refname; Fields() would
// split it incorrectly, so we split on tab and strip the known prefix from the
// first column.
func detectDefaultBranch(ctx context.Context, source string) string {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--symref", source, "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	const symrefPrefix = "ref: refs/heads/"
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, symrefPrefix) {
			// line: "ref: refs/heads/master\tHEAD"
			ref := strings.SplitN(line, "\t", 2)[0] // "ref: refs/heads/master"
			return strings.TrimPrefix(ref, symrefPrefix)
		}
	}
	return ""
}
