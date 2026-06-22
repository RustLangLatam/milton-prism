package application

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
)

// mkfile creates a file (and any parent dirs) under root.
func mkfile(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func TestDetectRootCandidates_RepoRootManifest_SingleRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkfile(t, dir, "go.mod")
	mkfile(t, dir, "internal/foo/foo.go")

	got, err := DetectRootCandidates(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("repo root has a manifest → expected no candidates (single root), got %v", got)
	}
	root, awaiting := ResolveSingleRoot(got)
	if awaiting || root != "" {
		t.Fatalf("expected auto repo root (\"\", false), got (%q, %v)", root, awaiting)
	}
}

func TestDetectRootCandidates_SingleChildRoot_AutoSelected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No manifest at repo root; exactly one child project.
	mkfile(t, dir, "backend/composer.json")
	mkfile(t, dir, "docs/readme.md")

	got, err := DetectRootCandidates(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"backend"}) {
		t.Fatalf("expected exactly [backend], got %v", got)
	}
	root, awaiting := ResolveSingleRoot(got)
	if awaiting || root != "backend" {
		t.Fatalf("single child root must auto-select, got (%q, %v)", root, awaiting)
	}
}

func TestDetectRootCandidates_MultiRoot_AwaitingSelection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkfile(t, dir, "services/api/go.mod")
	mkfile(t, dir, "services/web/package.json")
	mkfile(t, dir, "tools/cli/Cargo.toml")

	got, err := DetectRootCandidates(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"services/api", "services/web", "tools/cli"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	if len(got) < 2 {
		t.Fatalf("multi-root must yield >=2 candidates, got %d", len(got))
	}
	root, awaiting := ResolveSingleRoot(got)
	if !awaiting || root != "" {
		t.Fatalf("multi-root must await selection, got (%q, %v)", root, awaiting)
	}
}

func TestDetectRootCandidates_NestedDeduped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// One top-level project root with a nested sub-package manifest. The nested
	// package must NOT become a second candidate.
	mkfile(t, dir, "app/package.json")
	mkfile(t, dir, "app/packages/lib/package.json")

	got, err := DetectRootCandidates(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"app"}) {
		t.Fatalf("nested sub-package must be deduped under its root; expected [app], got %v", got)
	}
}

func TestDetectRootCandidates_IgnoresVendorAndNodeModules(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkfile(t, dir, "node_modules/leftpad/package.json")
	mkfile(t, dir, "vendor/dep/go.mod")
	mkfile(t, dir, "api/go.mod")

	got, err := DetectRootCandidates(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"api"}) {
		t.Fatalf("vendor/node_modules must be ignored; expected [api], got %v", got)
	}
}

func TestDetectRootCandidates_NoManifests_NoCandidates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkfile(t, dir, "README.md")
	mkfile(t, dir, "src/main.txt")

	got, err := DetectRootCandidates(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("no manifests → no candidates, got %v", got)
	}
	// ResolveSingleRoot treats this as the repo-root scope (honest no-op).
	if root, awaiting := ResolveSingleRoot(got); awaiting || root != "" {
		t.Fatalf("no candidates must resolve to repo root, got (%q, %v)", root, awaiting)
	}
}

// TestDetectRootCandidates_Notiplan_SuggestsRealBackend reproduces the notiplan
// layout: a Python backend with NO manifest at its own level, a stale copy, and a
// build/ output dir holding the only manifest. Manifest-only detection found 0
// roots and silently analysed the whole repo; the scoring detector must instead
// suggest the real `backend`, offer `backend_tmp` as a lower-ranked alternative,
// exclude `build`, and await selection.
func TestDetectRootCandidates_Notiplan_SuggestsRealBackend(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Root is just a container — no manifest/entrypoint/source at the top level.
	mkfile(t, dir, "compose.yaml")
	mkfile(t, dir, "Makefile")
	mkfile(t, dir, "README.md")
	// Real backend: entrypoint + source, no manifest.
	mkfile(t, dir, "backend/app.py")
	mkfile(t, dir, "backend/views.py")
	mkfile(t, dir, "backend/models.py")
	mkfile(t, dir, "backend/services/auth.py")
	// Stale copy: same shape, copy-looking name.
	mkfile(t, dir, "backend_tmp/app.py")
	mkfile(t, dir, "backend_tmp/views.py")
	mkfile(t, dir, "backend_tmp/models.py")
	// Build output dir holds the only manifest — must be ignored entirely.
	mkfile(t, dir, "build/backend/requirements.txt")

	got, err := DetectRootCandidates(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates (backend, backend_tmp), got %v", got)
	}
	if got[0] != "backend" {
		t.Fatalf("suggested root (candidates[0]) must be the real backend, got %q (full %v)", got[0], got)
	}
	// build/ must never be a candidate.
	for _, c := range got {
		if c == "build" || c == "build/backend" {
			t.Fatalf("build output dir must be excluded, got %v", got)
		}
	}
	root, awaiting := ResolveSingleRoot(got)
	if !awaiting || root != "" {
		t.Fatalf("ambiguous repo must await selection, got (%q, %v)", root, awaiting)
	}
}

// TestDetectRootCandidates_CopyNameRankedLast confirms a canonical dir outranks a
// copy-looking sibling regardless of alphabetical order, so candidates[0] (the
// suggested root) is always the canonical one.
func TestDetectRootCandidates_CopyNameRankedLast(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// "api_old" would sort BEFORE "service" alphabetically; scoring must still
	// rank the canonical, non-copy dir first.
	mkfile(t, dir, "api_old/main.go")
	mkfile(t, dir, "api_old/handler.go")
	mkfile(t, dir, "api_old/util.go")
	mkfile(t, dir, "service/go.mod")
	mkfile(t, dir, "service/main.go")

	got, err := DetectRootCandidates(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("expected ≥2 candidates, got %v", got)
	}
	if got[0] != "service" {
		t.Fatalf("canonical dir must be suggested first, got %q (full %v)", got[0], got)
	}
}

// TestDetectRootCandidates_CodeIgniter3_SingleRoot guards against the regression
// where a CI3 app (code split across application/ + system/, no root manifest) was
// mis-detected as a multi-root monorepo. The framework signature must pin it to a
// single root (repo root), so no prompt is raised.
func TestDetectRootCandidates_CodeIgniter3_SingleRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkfile(t, dir, "application/controllers/Welcome.php")
	mkfile(t, dir, "application/models/User_model.php")
	mkfile(t, dir, "application/config/autoload.php")
	mkfile(t, dir, "system/core/CodeIgniter.php")

	got, err := DetectRootCandidates(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("CI3 framework signature must pin a single root (no candidates), got %v", got)
	}
	if root, awaiting := ResolveSingleRoot(got); awaiting || root != "" {
		t.Fatalf("CI3 app must proceed at repo root without prompting, got (%q, %v)", root, awaiting)
	}
}

// ── scopeWorkspace ─────────────────────────────────────────────────────────────

func TestScopeWorkspace_EmptyAndDot_ReturnRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, sub := range []string{"", "."} {
		got, err := scopeWorkspace(root, sub)
		if err != nil {
			t.Fatalf("sub=%q: unexpected error: %v", sub, err)
		}
		if got != root {
			t.Fatalf("sub=%q: expected root %q, got %q", sub, root, got)
		}
	}
}

func TestScopeWorkspace_ValidSubdir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "services", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := scopeWorkspace(root, "services/api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(root, "services", "api")
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestScopeWorkspace_RejectsTraversal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, sub := range []string{"..", "../", "../escape", "services/../../escape"} {
		if _, err := scopeWorkspace(root, sub); err == nil {
			t.Fatalf("sub=%q: expected traversal rejection, got nil error", sub)
		}
	}
}

func TestScopeWorkspace_RejectsAbsolute(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	abs := "/etc"
	if runtime.GOOS == "windows" {
		abs = `C:\Windows`
	}
	if _, err := scopeWorkspace(root, abs); err == nil {
		t.Fatalf("absolute path %q must be rejected", abs)
	}
}

func TestScopeWorkspace_RejectsMissingDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := scopeWorkspace(root, "does/not/exist"); err == nil {
		t.Fatal("non-existent subdir must be rejected")
	}
}

func TestScopeWorkspace_RejectsFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mkfile(t, root, "notadir.txt")
	if _, err := scopeWorkspace(root, "notadir.txt"); err == nil {
		t.Fatal("a file (not a directory) must be rejected")
	}
}
