package repositories_test

import (
	"context"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"milton_prism/core/services/repository/domain"
	"milton_prism/core/services/repository/infrastructure/repositories"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitHTTPServer stands up a real git smart-HTTP server backed by a bare repo on
// disk, using git-http-backend over CGI. It is a genuine local git remote with
// no network access and no real provider — the write-side equivalent of the
// bare-repo target used by the push tests. Optional basicAuth (user→pass) gates
// access so the "token rejected for push" branch can be exercised honestly.
//
// Returns the server's base URL for the bare repo (e.g. http://127.0.0.1:NNN/repo.git).
func gitHTTPServer(t *testing.T, bareRepoDir string, basicAuth map[string]string) string {
	t.Helper()

	backend, err := exec.LookPath("git-http-backend")
	if err != nil {
		// Fall back to the git-core exec path.
		out, lpErr := exec.Command("git", "--exec-path").Output()
		require.NoError(t, lpErr, "cannot locate git exec-path")
		backend = filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	}
	if _, statErr := os.Stat(backend); statErr != nil {
		t.Skipf("git-http-backend not available: %v", statErr)
	}

	// The parent dir of the bare repo is the GIT_PROJECT_ROOT; the repo is served
	// at /<basename>.
	projectRoot := filepath.Dir(bareRepoDir)
	repoName := filepath.Base(bareRepoDir)

	handler := &cgi.Handler{
		Path: backend,
		Dir:  projectRoot,
		Env: []string{
			"GIT_PROJECT_ROOT=" + projectRoot,
			"GIT_HTTP_EXPORT_ALL=1",
			"REMOTE_USER=preflight", // satisfies receive-pack push identity
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if basicAuth != nil {
			user, pass, ok := r.BasicAuth()
			if !ok || basicAuth[user] != pass {
				w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	return srv.URL + "/" + repoName
}

// initBareForHTTP creates a bare repo configured for smart-HTTP serving and
// returns its on-disk path. It enables http.receivepack so receive-pack
// discovery is advertised (i.e. push capability is probeable).
func initBareForHTTP(t *testing.T, parent string) string {
	t.Helper()
	dir := filepath.Join(parent, "target.git")
	_, err := git.PlainInit(dir, true)
	require.NoError(t, err)
	// Allow receive-pack over HTTP so the receive-pack discovery answers 200.
	cmd := exec.Command("git", "-C", dir, "config", "http.receivepack", "true")
	require.NoError(t, cmd.Run())
	return dir
}

// ── PreflightTarget happy path: reachable + can-push + empty ───────────────────

func TestPreflightTarget_EmptyTarget_ReachableCanPushEmpty(t *testing.T) {
	parent := t.TempDir()
	bare := initBareForHTTP(t, parent)
	url := gitHTTPServer(t, bare, nil)

	client := repositories.NewNoOpGitClient()
	res, err := client.PreflightTarget(context.Background(), url, "any-write-token")
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.True(t, res.Reachable, "empty served repo must be reachable")
	assert.True(t, res.CanPush, "receive-pack advertised → can push")
	assert.True(t, res.Empty, "freshly init'd bare repo must be empty")
	assert.Empty(t, res.ErrorMessage)
}

// ── PreflightTarget: target already has commits → not empty (A.3 blocker) ───────

func TestPreflightTarget_NonEmptyTarget_NotEmpty(t *testing.T) {
	parent := t.TempDir()
	bare := initBareForHTTP(t, parent)

	// Push one commit into the bare repo via the existing PushResult so it has refs.
	client := repositories.NewNoOpGitClient()
	_, err := client.PushResult(context.Background(), bare, "",
		[]*domain.PushFile{{Path: "seed.txt", Content: "seed\n"}}, "seed")
	require.NoError(t, err)

	url := gitHTTPServer(t, bare, nil)
	res, err := client.PreflightTarget(context.Background(), url, "tok")
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.True(t, res.Reachable)
	assert.True(t, res.CanPush)
	assert.False(t, res.Empty, "a target with a commit must report not-empty")
}

// ── PreflightTarget: token rejected for push → reachable, cannot push ──────────

func TestPreflightTarget_BadToken_CannotPush(t *testing.T) {
	parent := t.TempDir()
	bare := initBareForHTTP(t, parent)
	url := gitHTTPServer(t, bare, map[string]string{"valid-user": "valid-pass"})

	client := repositories.NewNoOpGitClient()
	res, err := client.PreflightTarget(context.Background(), url, "wrong-token")
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.True(t, res.Reachable, "server answered (401) → reachable")
	assert.False(t, res.CanPush, "rejected token cannot push")
	assert.Contains(t, res.ErrorMessage, "rejected")
}

// ── PreflightTarget: unreachable host → not reachable, legible message ─────────

func TestPreflightTarget_Unreachable_NotReachable(t *testing.T) {
	client := repositories.NewNoOpGitClient()
	res, err := client.PreflightTarget(context.Background(),
		"https://nonexistent.prism-preflight-local/org/repo.git", "tok")
	require.NoError(t, err, "transport failure is reported in-band, not as a Go error")
	require.NotNil(t, res)

	assert.False(t, res.Reachable)
	assert.NotEmpty(t, res.ErrorMessage)
}

// ── PreflightTarget: invalid URL → not reachable, no panic ─────────────────────

func TestPreflightTarget_InvalidURL_NotReachable(t *testing.T) {
	client := repositories.NewNoOpGitClient()
	res, err := client.PreflightTarget(context.Background(), "::::not-a-url", "tok")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.Reachable)
	assert.Contains(t, res.ErrorMessage, "Invalid target repository URL")
}

// ── PreflightTarget never pushes: target stays empty after a pre-flight ────────

func TestPreflightTarget_DoesNotMutateTarget(t *testing.T) {
	parent := t.TempDir()
	bare := initBareForHTTP(t, parent)
	url := gitHTTPServer(t, bare, nil)

	client := repositories.NewNoOpGitClient()
	_, err := client.PreflightTarget(context.Background(), url, "tok")
	require.NoError(t, err)

	// The bare repo must still have no HEAD — pre-flight wrote nothing.
	bareGit, openErr := git.PlainOpen(bare)
	require.NoError(t, openErr)
	_, refErr := bareGit.Head()
	assert.Error(t, refErr, "pre-flight must not create any ref on the target")
}
