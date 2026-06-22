package application

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// rootManifestNames are project-root marker files. A directory containing any of
// these is a strong project-root signal. Wildcard suffixes (".csproj") are in
// rootManifestExtensions. Lock files are deliberately excluded.
var rootManifestNames = []string{
	"package.json",     // Node / npm / yarn / pnpm
	"composer.json",    // PHP / Composer
	"go.mod",           // Go modules
	"requirements.txt", // Python (pip)
	"pyproject.toml",   // Python (PEP 518)
	"setup.py",         // Python (setuptools)
	"setup.cfg",        // Python (setuptools, declarative)
	"Pipfile",          // Python (pipenv)
	"pom.xml",          // Java / Maven
	"build.gradle",     // Java / Kotlin / Gradle (Groovy DSL)
	"build.gradle.kts", // Gradle (Kotlin DSL)
	"Gemfile",          // Ruby / Bundler
	"Cargo.toml",       // Rust / Cargo
}

// rootManifestExtensions are extension-based manifest markers.
var rootManifestExtensions = []string{
	".csproj", // .NET C# project
	".sln",    // .NET solution
	".fsproj", // .NET F# project
}

// entrypointNames are top-of-application files: a directory holding one is very
// likely a real codebase root even when it carries no dependency manifest (the
// common Python/legacy case where deps live elsewhere).
var entrypointNames = []string{
	"main.py", "app.py", "wsgi.py", "asgi.py", "manage.py", // Python / Django / Flask
	"artisan",                            // Laravel
	"index.js", "server.js", "app.js",    // Node
	"main.go",                            // Go (loose)
	"Application.java", "Main.java",      // Java
	"index.php",                          // PHP (flat / CI front controller)
}

// nestedEntrypointPaths are framework front controllers that live one level down.
var nestedEntrypointPaths = []string{
	"public/index.php", // Laravel / Symfony / CI4
	"src/index.php",
}

// frameworkRootSignatures are repo-relative files whose presence proves the
// repository root is a SINGLE framework application even without a top-level
// manifest or entrypoint. Without this, frameworks that split their code across
// several top-level directories (e.g. CodeIgniter 3's application/ + system/)
// would be mis-detected as a multi-root monorepo and wrongly prompt for a root.
var frameworkRootSignatures = []string{
	"system/core/CodeIgniter.php", // CodeIgniter 3 (app split across application/ + system/)
}

// sourceExtensions are recognized programming-language source files, used for the
// code-density signal so a manifest-less directory full of real code still ranks
// as a candidate root.
var sourceExtensions = map[string]struct{}{
	".py": {}, ".js": {}, ".ts": {}, ".jsx": {}, ".tsx": {},
	".go": {}, ".php": {}, ".java": {}, ".rb": {}, ".rs": {},
	".cs": {}, ".kt": {}, ".scala": {}, ".go.tmpl": {},
}

// ignoredDirNames are directories never treated as project roots: third-party,
// generated, build output, or non-code support dirs. Skipping them avoids both
// false "multi-root" prompts and selecting build artifacts as the analysis root.
var ignoredDirNames = map[string]struct{}{
	"node_modules": {}, "vendor": {}, ".git": {},
	"build": {}, "dist": {}, "target": {}, "out": {}, "bin": {}, "obj": {},
	".idea": {}, ".vscode": {}, ".github": {},
	"testdata": {}, "tests": {}, "test": {}, "__tests__": {},
	"docs": {}, "doc": {}, "examples": {}, "example": {},
	"__pycache__": {}, ".venv": {}, "venv": {}, "env": {},
	"migrations": {}, "fixtures": {},
}

// copyNameSuffixes mark a directory as a likely non-canonical copy (a stale,
// temporary, or backup duplicate). Such a dir can still be a valid candidate the
// user may pick, but it is penalised so the canonical sibling is suggested first.
var copyNameSuffixes = []string{"_tmp", "-tmp", "_bak", "-bak", "_old", "-old", "_copy", "-copy", ".bak", ".old"}
var copyNameContains = []string{"backup", "_temp", "-temp"}

// dirHasManifest reports whether dir directly contains a project-root manifest
// (non-recursive).
func dirHasManifest(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		for _, m := range rootManifestNames {
			if name == m {
				return true, nil
			}
		}
		lower := strings.ToLower(name)
		for _, ext := range rootManifestExtensions {
			if strings.HasSuffix(lower, ext) {
				return true, nil
			}
		}
	}
	return false, nil
}

// dirHasEntrypoint reports whether dir holds an application entrypoint, either
// directly or at one of the well-known nested front-controller paths.
func dirHasEntrypoint(dir string) bool {
	for _, name := range entrypointNames {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() {
			return true
		}
	}
	for _, rel := range nestedEntrypointPaths {
		if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

// countSourceFiles counts recognized source files under dir, bounded by depth and
// a hard cap so large trees never dominate the cost. Ignored dirs are skipped.
func countSourceFiles(dir string, maxDepth, cap int) int {
	count := 0
	var walk func(d string, depth int)
	walk = func(d string, depth int) {
		if depth > maxDepth || count >= cap {
			return
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			if count >= cap {
				return
			}
			name := e.Name()
			if e.IsDir() {
				if _, skip := ignoredDirNames[name]; skip || strings.HasPrefix(name, ".") {
					continue
				}
				walk(filepath.Join(d, name), depth+1)
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			if _, ok := sourceExtensions[ext]; ok {
				count++
			}
		}
	}
	walk(dir, 0)
	return count
}

// isCopyName reports whether a directory name looks like a non-canonical copy.
func isCopyName(name string) bool {
	lower := strings.ToLower(name)
	for _, suf := range copyNameSuffixes {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	for _, sub := range copyNameContains {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

// scoreDir computes a project-root likelihood score for dir and whether it is a
// viable candidate at all. Signals: manifest (strong), entrypoint (medium),
// source-code density (additive). A copy-looking name is penalised but stays
// viable so the user can still pick it.
func scoreDir(dir, name string) (score int, viable bool) {
	hasManifest, _ := dirHasManifest(dir)
	hasEntry := dirHasEntrypoint(dir)
	src := countSourceFiles(dir, 3, 40)

	if hasManifest {
		score += 100
	}
	if hasEntry {
		score += 40
	}
	if d := src * 3; d > 60 {
		score += 60
	} else {
		score += d
	}
	if isCopyName(name) {
		score -= 60
	}

	// Viable when there is a real project signal: a manifest, an entrypoint, or a
	// meaningful amount of source code.
	viable = hasManifest || hasEntry || src >= 3
	return score, viable
}

// rootIsProject reports whether the repository root is itself a single project —
// it directly carries a manifest, an entrypoint, or a meaningful amount of
// top-level source code. In that case detection returns no candidates and the
// analysis proceeds at the repository root (no prompt).
func rootIsProject(repoRoot string) bool {
	if has, err := dirHasManifest(repoRoot); err == nil && has {
		return true
	}
	if dirHasEntrypoint(repoRoot) {
		return true
	}
	// Framework signature files prove the root is one application even when its
	// code is split across several top-level dirs (e.g. CodeIgniter application/ +
	// system/), which would otherwise look like a multi-root monorepo.
	for _, sig := range frameworkRootSignatures {
		if fi, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(sig))); err == nil && !fi.IsDir() {
			return true
		}
	}
	// Top-level source files (depth 0 only) signal a flat single-project layout.
	if topLevelSourceCount(repoRoot) >= 3 {
		return true
	}
	return false
}

// topLevelSourceCount counts recognized source files directly in dir (depth 0).
func topLevelSourceCount(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if _, ok := sourceExtensions[ext]; ok {
			n++
		}
	}
	return n
}

type scoredCandidate struct {
	rel   string
	score int
}

// DetectRootCandidates scans a cloned repository tree for candidate project roots
// and returns them as repository-relative, forward-slash paths sorted BEST-FIRST
// (highest score first; ties broken lexicographically). The first element is the
// suggested root.
//
// Algorithm (deterministic, no LLM):
//   - If the repository root is itself a project (manifest / entrypoint / top-level
//     source) → return an empty slice (single root, proceed at repo root, no prompt).
//   - Otherwise score immediate child directories (and, for non-code "container"
//     dirs like services/ packages/ apps/, their children at depth 2). A directory
//     is a candidate when it shows a real project signal (manifest, entrypoint, or
//     ≥3 source files). The first signal-bearing dir on a path stops descent, so a
//     nested sub-package never produces a second candidate under an already-detected
//     root (dedupe-nested by construction).
//
// Returned slice semantics, matching AnalysisSummary.root_candidates:
//   - len 0  → single clear root (repo root): proceed automatically.
//   - len 1  → exactly one candidate subdir: the caller auto-selects it.
//   - len ≥2 → ambiguous monorepo: the caller awaits a user root selection, with
//     candidates[0] as the suggested default.
func DetectRootCandidates(repoRoot string) ([]string, error) {
	if rootIsProject(repoRoot) {
		return nil, nil
	}

	var scored []scoredCandidate
	if err := collectScored(repoRoot, repoRoot, 2, &scored); err != nil {
		return nil, err
	}

	// Best-first: higher score first, then lexicographic for determinism.
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].rel < scored[j].rel
	})

	out := make([]string, 0, len(scored))
	for _, c := range scored {
		out = append(out, c.rel)
	}
	return out, nil
}

// collectScored walks dir's children up to maxDepth levels deep, appending each
// viable (signal-bearing) directory with its score and NOT descending past one
// (so nested sub-packages are deduped away). Non-code container dirs without a
// signal are descended into to find nested projects (services/api, packages/web).
func collectScored(repoRoot, dir string, maxDepth int, out *[]scoredCandidate) error {
	if maxDepth <= 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if _, skip := ignoredDirNames[name]; skip {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue
		}
		child := filepath.Join(dir, name)
		score, viable := scoreDir(child, name)
		if viable {
			rel, relErr := filepath.Rel(repoRoot, child)
			if relErr != nil {
				return relErr
			}
			*out = append(*out, scoredCandidate{rel: filepath.ToSlash(rel), score: score})
			// This dir is a root; its sub-packages belong to it — do not descend.
			continue
		}
		// Non-code container — look one level deeper for nested projects.
		if err := collectScored(repoRoot, child, maxDepth-1, out); err != nil {
			return err
		}
	}
	return nil
}

// ResolveSingleRoot inspects the (best-first) detection result and decides the
// automatic (no-prompt) path:
//   - 0 candidates → repo root is the project (resolvedRoot "", awaiting false).
//   - 1 candidate  → auto-select it (resolvedRoot = that dir, awaiting false).
//   - ≥2 candidates → await selection (resolvedRoot "", awaiting true); the caller
//     persists the candidate list whose first element is the suggested default.
func ResolveSingleRoot(candidates []string) (resolvedRoot string, awaiting bool) {
	switch len(candidates) {
	case 0:
		return "", false
	case 1:
		return candidates[0], false
	default:
		return "", true
	}
}
