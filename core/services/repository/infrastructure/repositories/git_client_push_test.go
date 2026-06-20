package repositories_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"milton_prism/core/services/repository/domain"
	"milton_prism/core/services/repository/infrastructure/repositories"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bareRepo creates a bare git repository in a temp directory and returns its
// absolute path. It is used as a push target — equivalent to a remote hosted
// on GitHub but running entirely on disk with no network access.
func bareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	_, err := git.PlainInit(dir, true) // true = bare
	require.NoError(t, err)
	return dir
}

// cloneFrom clones from a bare repo's main branch and returns the clone root.
// The bare repo HEAD defaults to refs/heads/master (go-git default) even after
// a push to refs/heads/main, so we must specify the branch explicitly.
func cloneFrom(t *testing.T, targetDir string) string {
	t.Helper()
	cloneDir := t.TempDir()
	_, err := git.PlainClone(cloneDir, false, &git.CloneOptions{
		URL:           targetDir,
		ReferenceName: "refs/heads/main",
	})
	require.NoError(t, err)
	return cloneDir
}

// ── PushResult happy-path ─────────────────────────────────────────────────────

func TestPushResult_Success_SingleFile(t *testing.T) {
	target := bareRepo(t)
	client := repositories.NewNoOpGitClient()

	files := []*domain.PushFile{
		{Path: "main.go", Content: "package main\n\nfunc main() {}\n"},
	}

	branch, err := client.PushResult(context.Background(), target, "", files, "test: initial commit")
	require.NoError(t, err)
	assert.Equal(t, "main", branch)

	// Verify the file landed in the bare repo by cloning from it.
	cloneDir := cloneFrom(t, target)
	content, err := os.ReadFile(filepath.Join(cloneDir, "main.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main\n\nfunc main() {}\n", string(content))
}

func TestPushResult_Success_PreservesDirectoryStructure(t *testing.T) {
	target := bareRepo(t)
	client := repositories.NewNoOpGitClient()

	files := []*domain.PushFile{
		{Path: "cmd/server/main.go", Content: "package main\n"},
		{Path: "internal/service/svc.go", Content: "package service\n"},
		{Path: "go.mod", Content: "module example.com/app\n\ngo 1.21\n"},
	}

	_, err := client.PushResult(context.Background(), target, "", files, "")
	require.NoError(t, err)

	cloneDir := cloneFrom(t, target)

	for _, f := range files {
		full := filepath.Join(cloneDir, filepath.FromSlash(f.Path))
		data, readErr := os.ReadFile(full)
		require.NoError(t, readErr, "expected file %s to exist after push", f.Path)
		assert.Equal(t, f.Content, string(data), "content mismatch for %s", f.Path)
	}
}

func TestPushResult_Success_CommitMessagePropagates(t *testing.T) {
	target := bareRepo(t)
	client := repositories.NewNoOpGitClient()

	files := []*domain.PushFile{{Path: "README.md", Content: "# Hello\n"}}
	msg := "feat: traceable commit abc-123"

	_, err := client.PushResult(context.Background(), target, "", files, msg)
	require.NoError(t, err)

	cloneDir := cloneFrom(t, target)
	repo, err := git.PlainOpen(cloneDir)
	require.NoError(t, err)

	head, err := repo.Head()
	require.NoError(t, err)
	commit, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)

	assert.Equal(t, msg, commit.Message)
	assert.Equal(t, "Milton Prism", commit.Author.Name)
	assert.Equal(t, "prism@miltonprism.io", commit.Author.Email)
}

func TestPushResult_Success_DefaultCommitMessage(t *testing.T) {
	target := bareRepo(t)
	client := repositories.NewNoOpGitClient()

	files := []*domain.PushFile{{Path: "a.txt", Content: "x\n"}}

	_, err := client.PushResult(context.Background(), target, "", files, "")
	require.NoError(t, err)

	cloneDir := cloneFrom(t, target)
	repo, err := git.PlainOpen(cloneDir)
	require.NoError(t, err)

	head, err := repo.Head()
	require.NoError(t, err)
	commit, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)

	// Default message starts with "chore: Milton Prism"
	assert.Contains(t, commit.Message, "Milton Prism")
}

func TestPushResult_Success_BranchIsMain(t *testing.T) {
	target := bareRepo(t)
	client := repositories.NewNoOpGitClient()

	_, err := client.PushResult(context.Background(), target, "", []*domain.PushFile{
		{Path: "f.txt", Content: "hi\n"},
	}, "test: branch check")
	require.NoError(t, err)

	// The bare repo should have a refs/heads/main reference.
	bareGit, err := git.PlainOpen(target)
	require.NoError(t, err)
	ref, err := bareGit.Reference("refs/heads/main", true)
	require.NoError(t, err)
	assert.False(t, ref.Hash().IsZero())
}

// ── PushResult error paths ────────────────────────────────────────────────────

func TestPushResult_EmptyFiles_ReturnsErrInternal(t *testing.T) {
	// Empty worktree: go-git Commit on an empty index returns an error.
	// The application layer guards against empty file lists, but the adapter
	// should still handle the underlying error gracefully.
	target := bareRepo(t)
	client := repositories.NewNoOpGitClient()

	_, err := client.PushResult(context.Background(), target, "", []*domain.PushFile{}, "")
	// An empty add + commit yields go-git "nothing to commit" — mapped to ErrInternal.
	assert.Error(t, err)
}

func TestPushResult_PathTraversal_Rejected(t *testing.T) {
	target := bareRepo(t)
	client := repositories.NewNoOpGitClient()

	files := []*domain.PushFile{
		{Path: "../escape.go", Content: "package main\n"},
	}

	_, err := client.PushResult(context.Background(), target, "", files, "")
	assert.ErrorIs(t, err, domain.ErrMissingPayload)
}

func TestPushResult_AbsolutePath_Rejected(t *testing.T) {
	target := bareRepo(t)
	client := repositories.NewNoOpGitClient()

	files := []*domain.PushFile{
		{Path: "/etc/passwd", Content: "bad\n"},
	}

	_, err := client.PushResult(context.Background(), target, "", files, "")
	assert.ErrorIs(t, err, domain.ErrMissingPayload)
}

func TestPushResult_InvalidTargetURL_ReturnsError(t *testing.T) {
	client := repositories.NewNoOpGitClient()

	files := []*domain.PushFile{{Path: "a.go", Content: "package a\n"}}

	// A non-existent host triggers a network error mapped to ErrPushNetworkError.
	_, err := client.PushResult(context.Background(), "https://nonexistent.prism-test-local/org/repo", "", files, "")
	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrPushNetworkError)
}
