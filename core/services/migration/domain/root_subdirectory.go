package domain

import (
	"path"
	"strings"
)

// NormalizeRootSubdirectory validates and canonicalises the monorepo root
// subdirectory supplied on a Migration at creation. It is the API-side guard
// that mirrors the analysis worker's path check, so an invalid value is rejected
// synchronously instead of failing the async analysis job later.
//
// Rules:
//   - empty / "." → "" (the whole repository root; backward-compatible default),
//   - back-slashes are normalised to forward slashes,
//   - the cleaned path must be relative and must not escape the root
//     (no leading "/", no ".." component).
//
// Returns the cleaned forward-slash path on success, or ErrInvalidRootSubdirectory
// on an absolute / traversal path.
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
