package assembler

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWalkSkeleton_UnreadableFileSkipped proves the resilience fix: a single
// unreadable skeleton file (here a 0000-perm file the process cannot read, the
// production symptom being a 0600 protobuf/buf.lock the distroless uid 65532
// could not read) must NOT abort the walk. walkSkeleton skips the offending
// file with a WARNING and still collects every readable file, so one unreadable
// optional file no longer 500s the whole DownloadDeliverable.
//
// The permission half is skipped when running as root (uid 0 bypasses the mode
// bits, so chmod 0000 does not deny the read); the missing-file half below runs
// unconditionally and covers the same skip-not-abort contract.
func TestWalkSkeleton_UnreadableFileSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0000 does not deny reads, so an unreadable file cannot be simulated")
	}
	root := t.TempDir()
	write := func(rel string, perm os.FileMode) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package shared\n"), perm); err != nil {
			t.Fatal(err)
		}
	}
	// A readable skeleton .go file (admitted by isSkeletonFile via core/shared/)
	// and an unreadable one (perm 0000) under the same admitted tree.
	write("core/shared/readable.go", 0o644)
	write("core/shared/unreadable.go", 0o000)
	// Restore perms so t.TempDir cleanup can remove the file.
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(root, "core", "shared", "unreadable.go"), 0o644)
	})

	a := New(root, false, "go", "grpc", "mongodb")
	dst := make(map[string][]byte)
	if err := a.walkSkeleton(dst); err != nil {
		t.Fatalf("walkSkeleton aborted on an unreadable file, want skip-with-warning: %v", err)
	}
	if _, ok := dst["core/shared/readable.go"]; !ok {
		t.Error("readable skeleton file was not collected")
	}
	if _, ok := dst["core/shared/unreadable.go"]; ok {
		t.Error("unreadable skeleton file was collected (read should have failed and been skipped)")
	}
}

// TestWalkSkeleton_MissingFileSkipped proves the same skip-not-abort contract for
// a skeleton entry that WalkDir reports but os.ReadFile cannot open — simulated
// with a dangling symlink (the target never exists), which surfaces an ENOENT
// read error regardless of the running uid. The dangling link is skipped with a
// warning and the readable sibling is still collected. This is the uid-independent
// homologue of the permission case above.
func TestWalkSkeleton_MissingFileSkipped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "core", "shared")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readable.go"), []byte("package shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A dangling symlink ending in .go: WalkDir yields it as a non-dir entry that
	// passes isSkeletonFile, but os.ReadFile fails because the target is absent.
	if err := os.Symlink(filepath.Join(dir, "does-not-exist.go"), filepath.Join(dir, "dangling.go")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	a := New(root, false, "go", "grpc", "mongodb")
	dst := make(map[string][]byte)
	if err := a.walkSkeleton(dst); err != nil {
		t.Fatalf("walkSkeleton aborted on a missing/dangling file, want skip-with-warning: %v", err)
	}
	if _, ok := dst["core/shared/readable.go"]; !ok {
		t.Error("readable skeleton file was not collected")
	}
	if _, ok := dst["core/shared/dangling.go"]; ok {
		t.Error("dangling skeleton symlink was collected (read should have failed and been skipped)")
	}
}
