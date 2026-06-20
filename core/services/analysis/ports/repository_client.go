package ports

import "context"

// RepositoryClient is the driven port for interacting with the repository
// service. The analysis service calls it before creating an analysis run.
type RepositoryClient interface {
	// ValidateRepositoryExists returns nil when the repository exists,
	// domain.ErrRepositoryNotFound when it is not found, or domain.ErrInternal
	// for unexpected transport errors.
	ValidateRepositoryExists(ctx context.Context, repositoryID uint64) error
	// GetRemoteURL returns the remote URL and default branch of the repository
	// so the analysis worker can clone the source. Returns domain.ErrRepositoryNotFound
	// when the repository does not exist and domain.ErrInternal on transport errors.
	GetRemoteURL(ctx context.Context, repositoryID uint64) (remoteURL, defaultBranch string, err error)

	// ProbeConnection performs a live connection test against the repository's
	// remote using the stored credential. Also persists the updated
	// connection_status on the repository record.
	// Returns nil when the connection succeeds.
	// Returns domain.ErrRepoAuthFailed when the credential is rejected.
	// Returns domain.ErrRepoUnreachable when the remote cannot be reached.
	// Returns domain.ErrRepositoryNotFound when the repository record does not exist.
	ProbeConnection(ctx context.Context, repositoryID uint64) error

	// GetBranchSHA returns the HEAD commit SHA for the named branch of the
	// repository. Used by the standalone dedup check to determine whether
	// an existing COMPLETED analysis covers the same commit without cloning.
	// Returns domain.ErrRepositoryNotFound when the repository does not exist
	// or the branch is not found.
	GetBranchSHA(ctx context.Context, repositoryID uint64, branch string) (string, error)
}
