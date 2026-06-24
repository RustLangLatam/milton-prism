package agent

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	workerdomain "milton_prism/core/worker/generation/domain"
	applog "milton_prism/pkg/log"
)

// artifactExcludeDirs are path segments that mark non-source trees the agent
// may materialise inside the workspace as a side effect of running language
// tooling (e.g. `pip install`, `npm install`, byte-compilation). DEFECT 2 root
// cause: the Python agent created a virtualenv and byte-compiled the project,
// dumping site-packages / __pycache__ into the workspace; diffFiles then saw
// thousands of "new" files and captureArtifacts persisted them all (the profile
// service alone produced 5759 artifacts). None of these belong to the GENERATED
// service, so any artifact whose relative path contains one of these segments
// is dropped before persistence.
//
// DEFECT 4 (Rust) root cause: the Rust agent runs `cargo build` inside the
// workspace; cargo's CARGO_HOME resolved under the workspace ($HOME/.cargo), so
// the entire crate registry — the index plus every downloaded crate's full
// source tree under .cargo/registry/src/…/<crate>/ — materialised in the
// workspace. diffFiles saw it all and captureArtifacts persisted 8552 registry
// files for a service whose real source is ~27 files. The registry is downloaded
// dependency source, NOT generated code, so .cargo / registry / .rustup are
// dropped here exactly like site-packages / node_modules.
//
// Matching is by path SEGMENT (not substring) so a legitimate file like
// core/services/user/venv_config.py is not excluded, while .venv/… is.
var artifactExcludeDirs = map[string]struct{}{
	".venv":         {}, // python virtualenv (pip/poetry/uv)
	"venv":          {}, // python virtualenv (common alt name)
	"env":           {}, // python virtualenv (common alt name)
	"site-packages": {}, // installed python deps (under any venv layout)
	"__pycache__":   {}, // python byte-compiled cache (*.pyc lives here)
	".mypy_cache":   {}, // mypy incremental cache
	".pytest_cache": {}, // pytest cache
	".ruff_cache":   {}, // ruff cache
	"node_modules":  {}, // node deps
	"target":        {}, // rust/cargo build output (debug/, release/, deps/, incremental)
	".cargo":        {}, // cargo home: registry index + downloaded crate sources + caches (DEFECT 4)
	".cargo-home":   {}, // cargo home under an explicit CARGO_HOME=$workspace/.cargo-home (DEFECT 4b: mig67 dumped 12983 registry files here)
	".rustup":       {}, // rustup toolchain home (if it lands in the workspace)
	".fingerprint":  {}, // cargo build fingerprints (under target/, defence-in-depth)
	".git":          {}, // any nested git metadata
	".tox":          {}, // tox environments
	"dist-info":     {}, // wheel metadata dirs (foo-1.0.dist-info)
	"egg-info":      {}, // egg metadata dirs (foo.egg-info)
}

// isExcludedArtifactPath reports whether rel sits inside a non-source tree that
// must never be captured as a generation artifact.
func isExcludedArtifactPath(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if _, bad := artifactExcludeDirs[seg]; bad {
			return true
		}
		// Wheel/egg metadata dirs are versioned (e.g. "flask-3.0.dist-info"),
		// so match by suffix in addition to the exact names above.
		if strings.HasSuffix(seg, ".dist-info") || strings.HasSuffix(seg, ".egg-info") {
			return true
		}
		// Byte-compiled / object files anywhere.
		if strings.HasSuffix(seg, ".pyc") || strings.HasSuffix(seg, ".pyo") {
			return true
		}
		// Rust/cargo compiled outputs and editor backups anywhere (defence in
		// depth — these normally live under target/ but a stray one elsewhere is
		// still not source the deliverable carries).
		if strings.HasSuffix(seg, ".rlib") || strings.HasSuffix(seg, ".rmeta") ||
			strings.HasSuffix(seg, ".rs.bk") {
			return true
		}
		// Cargo package-cache lock files (live at the .cargo home root: already
		// caught by the .cargo segment, but matched by name as belt-and-braces).
		if seg == ".package-cache" || seg == ".package-cache-mutate" ||
			seg == "CACHEDIR.TAG" {
			return true
		}
		// Cargo home under any CARGO_HOME=$workspace/<name> convention: the agent
		// may name it .cargo (caught above), .cargo-home, cargo-home, etc. Match any
		// segment that is a "cargo home" variant so the whole registry/index/src tree
		// under it is dropped (DEFECT 4b root cause: mig67's CARGO_HOME was the
		// workspace-local .cargo-home, which the .cargo segment alone did not catch).
		if seg == "cargo-home" || strings.HasPrefix(seg, ".cargo-") ||
			strings.HasPrefix(seg, "cargo-home") {
			return true
		}
		// Cargo lockfile: regenerated deterministically by `cargo build` from
		// Cargo.toml, so it is not source the deliverable must carry. Dropping it
		// keeps the Rust deliverable lock-free (the consumer regenerates it).
		if seg == "Cargo.lock" {
			return true
		}
	}
	return false
}

// captureArtifacts reads each path in paths from workspaceDir and returns the
// successfully read files as FileArtifacts. A file is dropped (with a warning)
// when:
//   - its path sits inside a non-source tree (venv / site-packages / caches /
//     node_modules — see artifactExcludeDirs): DEFECT 2;
//   - its byte count exceeds maxArtifactBytes (binary/archive that slipped into
//     the diff, must not approach MongoDB's 16 MB per-document limit);
//   - its content is not valid UTF-8: proto3 string fields (FileArtifact.content)
//     require valid UTF-8, and a non-UTF-8 payload makes the migration-services
//     gRPC marshal of GetGenerationArtifacts fail with "string field contains
//     invalid UTF-8" (DEFECT 3). Generated source is always valid UTF-8 text, so
//     anything that fails this check is a binary that must not be persisted.
func captureArtifacts(workspaceDir string, paths []string) []workerdomain.FileArtifact {
	out := make([]workerdomain.FileArtifact, 0, len(paths))
	for _, rel := range paths {
		if isExcludedArtifactPath(rel) {
			applog.Warningf("agent invoker: skip non-source artifact path=%s — not generated code", rel)
			continue
		}
		data, err := os.ReadFile(filepath.Join(workspaceDir, rel))
		if err != nil {
			applog.Warningf("agent invoker: capture artifact path=%s: %v", rel, err)
			continue
		}
		if len(data) > maxArtifactBytes {
			applog.Warningf("agent invoker: skip oversized artifact path=%s size=%d bytes (max=%d) — not source code",
				rel, len(data), maxArtifactBytes)
			continue
		}
		if !utf8.Valid(data) {
			applog.Warningf("agent invoker: skip non-UTF8 artifact path=%s size=%d bytes — not text source",
				rel, len(data))
			continue
		}
		out = append(out, workerdomain.FileArtifact{Path: rel, Content: data})
	}
	return out
}
