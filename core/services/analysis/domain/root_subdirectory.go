package domain

import (
	"path"
	"strings"
)

// NormalizeRootSubdirectory validates and canonicalises a repository-relative
// monorepo root subdirectory supplied at analysis/migration creation. It is the
// API-side guard that mirrors the worker's scopeWorkspace path check, so an
// invalid value is rejected synchronously (with a typed error) instead of
// failing the async job.
//
// Rules:
//   - empty / "." → "" (the whole repository root; backward-compatible default),
//   - back-slashes are normalised to forward slashes (tolerate Windows input),
//   - the cleaned path must be relative and must not escape the root
//     (no leading "/", no ".." component).
//
// On success it returns the cleaned forward-slash path (e.g. "services/api").
// On a traversal/absolute path it returns ErrInvalidRootSubdirectory.
func NormalizeRootSubdirectory(raw string) (string, error) {
	rel := path.Clean(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"))
	if rel == "" || rel == "." {
		return "", nil
	}
	if path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", ErrInvalidRootSubdirectory
	}
	return rel, nil
}
