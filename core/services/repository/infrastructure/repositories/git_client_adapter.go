package repositories

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"milton_prism/core/services/repository/domain"
	"milton_prism/core/services/repository/ports"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/object"
	gogitplumbing "github.com/go-git/go-git/v5/plumbing"
)

var _ ports.GitClient = (*NoOpGitClient)(nil)

// NoOpGitClient implements ports.GitClient.
// ProbeSource uses the git smart-HTTP protocol for probing.
// PushResult uses go-git (pure Go) — no git binary required.
type NoOpGitClient struct {
	httpClient *http.Client
}

// NewNoOpGitClient returns a NoOpGitClient.
func NewNoOpGitClient() *NoOpGitClient {
	return &NoOpGitClient{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > 0 {
					for key, val := range via[0].Header {
						if _, ok := req.Header[key]; !ok {
							req.Header[key] = val
						}
					}
				}
				return nil
			},
		},
	}
}

// ProbeSource checks remoteURL without cloning.
// Uses the git smart-HTTP discovery endpoint (info/refs?service=git-upload-pack)
// to determine reachability and visibility.
func (c *NoOpGitClient) ProbeSource(ctx context.Context, remoteURL, token string) (*domain.SourceProbeResult, error) {
	infoRefsURL, err := buildInfoRefsURL(remoteURL)
	if err != nil {
		return &domain.SourceProbeResult{
			Reachable:    false,
			ErrorMessage: "Invalid repository URL.",
		}, nil
	}

	status, connErr := c.httpProbe(ctx, infoRefsURL, "")
	if connErr != nil {
		return &domain.SourceProbeResult{
			Reachable:    false,
			ErrorMessage: friendlyHTTPError(connErr),
		}, nil
	}

	switch status {
	case http.StatusOK:
		return &domain.SourceProbeResult{
			Reachable:  true,
			Visibility: domain.RepositoryVisibilityPublic,
			AuthValid:  true,
		}, nil

	case http.StatusUnauthorized, http.StatusForbidden:
		if token == "" {
			return &domain.SourceProbeResult{
				Reachable:  true,
				Visibility: domain.RepositoryVisibilityPrivate,
				AuthValid:  false,
			}, nil
		}
		status2, connErr2 := c.httpProbe(ctx, infoRefsURL, token)
		if connErr2 != nil || status2 != http.StatusOK {
			return &domain.SourceProbeResult{
				Reachable:    true,
				Visibility:   domain.RepositoryVisibilityPrivate,
				AuthValid:    false,
				ErrorMessage: "Token was rejected by the remote. Verify it has read access.",
			}, nil
		}
		return &domain.SourceProbeResult{
			Reachable:  true,
			Visibility: domain.RepositoryVisibilityPrivate,
			AuthValid:  true,
		}, nil

	case http.StatusNotFound:
		// GitHub returns 404 for private repos with no credentials.
		if token == "" {
			return &domain.SourceProbeResult{
				Reachable:  true,
				Visibility: domain.RepositoryVisibilityPrivate,
				AuthValid:  false,
			}, nil
		}
		status2, connErr2 := c.httpProbe(ctx, infoRefsURL, token)
		if connErr2 != nil || status2 != http.StatusOK {
			return &domain.SourceProbeResult{
				Reachable:    false,
				ErrorMessage: "Repository not found or token has insufficient access.",
			}, nil
		}
		return &domain.SourceProbeResult{
			Reachable:  true,
			Visibility: domain.RepositoryVisibilityPrivate,
			AuthValid:  true,
		}, nil

	default:
		return &domain.SourceProbeResult{
			Reachable:    false,
			ErrorMessage: fmt.Sprintf("Unexpected response from the remote (HTTP %d).", status),
		}, nil
	}
}

// PushResult commits files to a temporary git repository and pushes to targetURL.
// writeToken is passed exclusively as HTTP Basic Auth — it is never embedded in
// any URL, log message, or error string.
func (c *NoOpGitClient) PushResult(ctx context.Context, targetURL, writeToken string, files []*domain.PushFile, commitMessage string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "prism-push-*")
	if err != nil {
		return "", domain.ErrInternal
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Init local repo and set default branch to "main".
	repo, err := git.PlainInit(tmpDir, false)
	if err != nil {
		return "", domain.ErrInternal
	}
	if err := repo.Storer.SetReference(
		gogitplumbing.NewSymbolicReference(
			gogitplumbing.HEAD,
			gogitplumbing.NewBranchReferenceName("main"),
		),
	); err != nil {
		return "", domain.ErrInternal
	}

	// Write all files, creating intermediate directories as needed.
	for _, f := range files {
		if err := validateRelativePath(f.Path); err != nil {
			return "", domain.ErrMissingPayload
		}
		full := filepath.Join(tmpDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return "", domain.ErrInternal
		}
		if err := os.WriteFile(full, []byte(f.Content), 0644); err != nil {
			return "", domain.ErrInternal
		}
	}

	// Stage all files.
	w, err := repo.Worktree()
	if err != nil {
		return "", domain.ErrInternal
	}
	if _, err := w.Add("."); err != nil {
		return "", domain.ErrInternal
	}

	if commitMessage == "" {
		commitMessage = defaultCommitMessage()
	}
	if _, err := w.Commit(commitMessage, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Milton Prism",
			Email: "prism@miltonprism.io",
			When:  time.Now(),
		},
	}); err != nil {
		return "", domain.ErrInternal
	}

	// Add remote using the clean target URL (no credentials in URL).
	if _, err := repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{targetURL},
	}); err != nil {
		return "", domain.ErrInternal
	}

	// Push — writeToken lives only in BasicAuth, never in a URL or log entry.
	const branch = "main"
	pushOpts := &git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{"refs/heads/main:refs/heads/main"},
		Progress:   nil,
	}
	if writeToken != "" {
		pushOpts.Auth = &githttp.BasicAuth{
			Username: writeToken,
			Password: "x-oauth-basic",
		}
	}
	if err := repo.PushContext(ctx, pushOpts); err != nil && err != git.NoErrAlreadyUpToDate {
		return "", mapPushError(err)
	}

	return branch, nil
}

// TestConnection probes remoteURL using ProbeSource and maps the result to a
// ConnectionStatus. No data is cloned or stored.
func (c *NoOpGitClient) TestConnection(ctx context.Context, remoteURL, token string) (domain.ConnectionStatus, error) {
	result, err := c.ProbeSource(ctx, remoteURL, token)
	if err != nil {
		return domain.ConnectionStatusUnreachable, err
	}
	if !result.Reachable {
		return domain.ConnectionStatusUnreachable, nil
	}
	if !result.AuthValid {
		return domain.ConnectionStatusAuthFailed, nil
	}
	return domain.ConnectionStatusOK, nil
}

// ListBranches enumerates branches on the remote using the go-git smart-HTTP
// transport. The token is supplied as HTTP Basic Auth; it is never embedded in
// any log or error string.
func (c *NoOpGitClient) ListBranches(_ context.Context, remoteURL, token string) ([]*domain.Branch, error) {
	opts := &git.ListOptions{}
	if token != "" {
		opts.Auth = &githttp.BasicAuth{Username: token, Password: "x-oauth-basic"}
	}
	// nil storer is safe for List — the storer is only needed for fetch/clone ops.
	rem := git.NewRemote(nil, &config.RemoteConfig{Name: "origin", URLs: []string{remoteURL}})
	refs, err := rem.List(opts)
	if err != nil {
		return nil, mapBranchListError(err)
	}

	// Determine the default branch. Prefer the HEAD symref; fall back to
	// matching HEAD's hash against branch hashes when no symref is present.
	defaultBranch := ""
	headHash := gogitplumbing.ZeroHash
	for _, ref := range refs {
		if ref.Name() != gogitplumbing.HEAD {
			continue
		}
		if ref.Type() == gogitplumbing.SymbolicReference {
			defaultBranch = strings.TrimPrefix(ref.Target().String(), "refs/heads/")
		} else {
			headHash = ref.Hash()
		}
	}

	var branches []*domain.Branch
	for _, ref := range refs {
		name := ref.Name().String()
		if !strings.HasPrefix(name, "refs/heads/") {
			continue
		}
		shortName := strings.TrimPrefix(name, "refs/heads/")
		isDefault := shortName == defaultBranch
		if !isDefault && headHash != gogitplumbing.ZeroHash {
			isDefault = ref.Hash() == headHash
		}
		branches = append(branches, &domain.Branch{
			Name:      shortName,
			CommitSha: ref.Hash().String(),
			IsDefault: isDefault,
		})
	}
	return branches, nil
}

// mapBranchListError classifies a go-git remote listing error into a typed
// domain error. The raw error message is never forwarded to callers.
func mapBranchListError(err error) error {
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "authentication required"),
		strings.Contains(lower, "authorization failed"),
		strings.Contains(lower, "invalid credentials"),
		strings.Contains(lower, "invalid username or password"),
		strings.Contains(lower, "401"), strings.Contains(lower, "403"):
		return domain.ErrForbiddenAccess
	case strings.Contains(lower, "repository not found"),
		strings.Contains(lower, "not found"),
		strings.Contains(lower, "404"),
		strings.Contains(lower, "does not exist"):
		return domain.ErrRepositoryNotFound
	default:
		return domain.ErrConnectionFailed
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// defaultCommitMessage returns a standard traceable commit message.
func defaultCommitMessage() string {
	return fmt.Sprintf("chore: Milton Prism generated output [%s]", time.Now().UTC().Format(time.RFC3339))
}

// validateRelativePath rejects empty paths and any path that escapes the repo
// root via ".." traversal.
func validateRelativePath(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(p) {
		return fmt.Errorf("path escapes repository root")
	}
	return nil
}

// mapPushError translates a go-git push error into a typed domain error.
// The raw error string is never exposed to the caller — only the typed code.
func mapPushError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "authentication required"),
		strings.Contains(lower, "authorization failed"),
		strings.Contains(lower, "invalid credentials"),
		strings.Contains(lower, "remote: invalid username or password"),
		strings.Contains(lower, "401"), strings.Contains(lower, "403"):
		return domain.ErrPushAuthFailed
	case strings.Contains(lower, "rejected"),
		strings.Contains(lower, "non-fast-forward"),
		strings.Contains(lower, "pre-receive hook"):
		return domain.ErrPushConflict
	case strings.Contains(lower, "no such host"),
		strings.Contains(lower, "lookup"),
		strings.Contains(lower, "dial tcp"),
		strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "context deadline exceeded"):
		return domain.ErrPushNetworkError
	default:
		return domain.ErrInternal
	}
}

// buildInfoRefsURL appends the git smart-HTTP discovery path to remoteURL.
func buildInfoRefsURL(remoteURL string) (string, error) {
	u, err := url.Parse(remoteURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	base := strings.TrimSuffix(u.String(), "/")
	return base + "/info/refs?service=git-upload-pack", nil
}

// httpProbe sends a GET to infoRefsURL and returns the HTTP status code.
func (c *NoOpGitClient) httpProbe(ctx context.Context, infoRefsURL, token string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, infoRefsURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "git/2.0")
	if token != "" {
		req.SetBasicAuth(token, "x-oauth-basic")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

func friendlyHTTPError(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "lookup") ||
		strings.Contains(lower, "dial tcp") {
		return "Could not resolve the host. Check the URL and your connection."
	}
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "context deadline") {
		return "Connection timed out. The host may be unreachable."
	}
	return "The remote host could not be reached. Check the URL and your connection."
}
