package ports

import "context"

// ProfileClient is the driven port for cross-service validation against the profile service.
// Called to confirm an author profile exists before creating or updating an article.
type ProfileClient interface {
	ValidateProfileExists(ctx context.Context, profileID uint64) error
}
