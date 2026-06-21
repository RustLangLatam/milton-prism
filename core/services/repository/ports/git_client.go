package ports

import (
	"context"

	"milton_prism/core/services/repository/domain"
)

// GitClient is the driven port for git remote operations.
type GitClient interface {
	// ProbeSource checks remoteURL without cloning. It determines whether the
	// URL is reachable, its visibility (public/private), and whether the
	// supplied token grants read access. An empty token is valid for public repos.
	ProbeSource(ctx context.Context, remoteURL, token string) (*domain.SourceProbeResult, error)

	// TestConnection probes remoteURL using credentialRef and returns the
	// connection status. It does not update the repository record; the
	// application layer is responsible for persisting the result.
	TestConnection(ctx context.Context, remoteURL, credentialRef string) (domain.ConnectionStatus, error)

	// ListBranches returns the branches available on the remote.
	ListBranches(ctx context.Context, remoteURL, credentialRef string) ([]*domain.Branch, error)

	// PreflightTarget validates a push destination WITHOUT pushing anything.
	// It probes the git smart-HTTP receive-pack (write) endpoint to determine
	// reachability and whether writeToken grants push access, and lists the
	// remote refs to determine whether the target repository is empty (A.3).
	// No commit, no ref update, no clone is performed. writeToken is supplied as
	// HTTP Basic Auth only — never logged or embedded in any URL or error message.
	PreflightTarget(ctx context.Context, targetURL, writeToken string) (*domain.TargetPreflightResult, error)

	// PushResult initializes a temporary workspace, writes files preserving
	// directory structure, commits them with commitMessage (a default traceable
	// message is used when empty), and pushes to targetURL.
	// writeToken is supplied as HTTP Basic Auth only — never logged or embedded
	// in any URL or error message.
	PushResult(ctx context.Context, targetURL, writeToken string, files []*domain.PushFile, commitMessage string) (pushedBranch string, err error)
}
