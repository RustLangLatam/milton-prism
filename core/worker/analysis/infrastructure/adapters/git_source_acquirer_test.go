// Internal package test so private functions (classifyCloneError, redactURL)
// are accessible without export.
package adapters

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ── classifyCloneError ────────────────────────────────────────────────────────

func TestClassifyCloneError_AuthFailure_CouldNotReadPassword(t *testing.T) {
	out := []byte("fatal: could not read Password for 'https://ghp_xxx@github.com': No such device or address\n")
	msg := classifyCloneError("https://ghp_xxx@github.com/org/repo", out)
	assert.Contains(t, msg, "Authentication failed")
	assert.Contains(t, msg, "access token is invalid")
	assert.NotContains(t, msg, "ghp_xxx", "token must be redacted")
}

func TestClassifyCloneError_AuthFailure_AuthenticationFailed(t *testing.T) {
	out := []byte("remote: Invalid username or password.\nfatal: Authentication failed for 'https://github.com/org/private'\n")
	msg := classifyCloneError("https://github.com/org/private", out)
	assert.Contains(t, msg, "Authentication failed")
}

func TestClassifyCloneError_NotFound_RepositoryNotFound(t *testing.T) {
	out := []byte("ERROR: Repository not found.\nfatal: Could not read from remote repository.\n")
	msg := classifyCloneError("https://github.com/org/nonexistent", out)
	assert.Contains(t, msg, "Repository not found")
}

func TestClassifyCloneError_NotFound_HTTP404(t *testing.T) {
	out := []byte("fatal: repository 'https://github.com/org/missing/' not found\n")
	msg := classifyCloneError("https://github.com/org/missing", out)
	assert.Contains(t, msg, "not found")
}

func TestClassifyCloneError_Network_CouldNotResolveHost(t *testing.T) {
	out := []byte("fatal: unable to access 'https://invalid.example.invalid/repo/': Could not resolve host: invalid.example.invalid\n")
	msg := classifyCloneError("https://invalid.example.invalid/repo", out)
	assert.Contains(t, msg, "Could not connect")
}

func TestClassifyCloneError_Generic_Fallback(t *testing.T) {
	out := []byte("exit status 128\nfatal: some unknown git error\n")
	msg := classifyCloneError("https://github.com/org/repo", out)
	assert.Contains(t, msg, "Clone failed")
	assert.Contains(t, msg, "check the URL and access token")
}

func TestClassifyCloneError_TokenRedactedFromSource(t *testing.T) {
	out := []byte("fatal: could not read Password for 'https://ghp_MYSECRET@github.com': No such device or address\n")
	msg := classifyCloneError("https://ghp_MYSECRET@github.com/org/repo", out)
	assert.NotContains(t, msg, "ghp_MYSECRET", "token in source URL must be stripped from user message")
	assert.Contains(t, msg, "github.com/org/repo")
}

// ── redactURL ─────────────────────────────────────────────────────────────────

func TestRedactURL_RemovesUserinfo(t *testing.T) {
	u := redactURL("https://ghp_TOKEN@github.com/org/repo")
	assert.Equal(t, "https://github.com/org/repo", u)
}

func TestRedactURL_NoUserinfo_Unchanged(t *testing.T) {
	u := redactURL("https://github.com/org/repo")
	assert.Equal(t, "https://github.com/org/repo", u)
}

func TestRedactURL_SSHUrl_Unchanged(t *testing.T) {
	u := redactURL("git@github.com:org/repo.git")
	assert.Equal(t, "git@github.com:org/repo.git", u)
}

func TestRedactURL_Empty_Unchanged(t *testing.T) {
	assert.Equal(t, "", redactURL(""))
}
