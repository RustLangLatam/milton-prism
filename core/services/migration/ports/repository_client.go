package ports

import "context"

// PushFile is a single file to include in a git push operation.
type PushFile struct {
	Path    string // relative to repository root, e.g. "core/services/user/domain/domain.go"
	Content string // UTF-8 text content
}

// RepositoryClient is the driven port for repository service operations.
// The migration service calls this for pre-creation validation and for
// publishing generated artifacts to a target git repository.
type RepositoryClient interface {
	// FetchRepositoryURL validates that the repository exists and returns its
	// remote URL. Returns domain.ErrRepositoryNotFound when not found, or
	// domain.ErrInternal for unexpected transport errors.
	FetchRepositoryURL(ctx context.Context, repositoryID uint64) (remoteURL string, err error)

	// PushFiles commits files to a temporary workspace and pushes to targetURL.
	// writeToken is forwarded to the repository service as HTTP Basic Auth —
	// it is never stored, logged, or embedded in any error string.
	// Returns domain.ErrPushAuthFailed, ErrPushConflict, or ErrPushNetworkError
	// on push failures; domain.ErrInternal for unexpected errors.
	PushFiles(ctx context.Context, targetURL, writeToken string, files []PushFile, commitMessage string) (pushedBranch string, err error)

	// ProbeConnection performs a live connection test against the repository's
	// remote using the stored credential. Also persists the updated
	// connection_status on the repository record.
	// Returns nil when the connection succeeds.
	// Returns domain.ErrRepoAuthFailed when the credential is rejected.
	// Returns domain.ErrRepoUnreachable when the remote cannot be reached.
	// Returns domain.ErrRepositoryNotFound when the repository record does not exist.
	ProbeConnection(ctx context.Context, repositoryID uint64) error
}
