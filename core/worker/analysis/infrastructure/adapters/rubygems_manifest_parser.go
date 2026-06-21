package adapters

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.ManifestParser = (*RubyGemsManifestParser)(nil)

// RubyGemsManifestParser implements ports.ManifestParser for the RubyGems ecosystem.
//
// Detection strategy:
//   - Prefers Gemfile.lock (resolved, pinned versions for all gems in the
//     transitive closure). The Gemfile is also read to identify which direct
//     gems belong to :test or :development groups; those gems are excluded from
//     the output. Transitives of dev/test gems that appear only in the lock are
//     not traced — excluding them requires full graph traversal, which is out of
//     scope for Tier 1.
//   - Falls back to Gemfile alone: production gems (outside dev/test group
//     blocks) with versions stripped from constraints.
type RubyGemsManifestParser struct{}

// NewRubyGemsManifestParser returns a new RubyGemsManifestParser.
func NewRubyGemsManifestParser() *RubyGemsManifestParser {
	return &RubyGemsManifestParser{}
}

func (p *RubyGemsManifestParser) Parse(_ context.Context, workspacePath string, _ workerdomain.Ecosystem) ([]workerdomain.Dependency, error) {
	lockPath := filepath.Join(workspacePath, "Gemfile.lock")
	gemfilePath := filepath.Join(workspacePath, "Gemfile")

	if fileExists(lockPath) {
		return parseLockWithGemfile(lockPath, gemfilePath)
	}
	if fileExists(gemfilePath) {
		return parseGemfile(gemfilePath)
	}
	return nil, nil
}

// ── Gemfile.lock + Gemfile ────────────────────────────────────────────────────

func parseLockWithGemfile(lockPath, gemfilePath string) ([]workerdomain.Dependency, error) {
	specs, err := parseLockSpecs(lockPath)
	if err != nil {
		return nil, err
	}

	// devGems is populated from the Gemfile when it exists alongside the lock.
	// It is used as an exclusion set; missing Gemfile means no exclusions.
	devGems := make(map[string]bool)
	if fileExists(gemfilePath) {
		devGems, err = extractDevGemNames(gemfilePath)
		if err != nil {
			return nil, err
		}
	}

	deps := make([]workerdomain.Dependency, 0, len(specs))
	for name, version := range specs {
		if devGems[name] {
			continue
		}
		cat, display, slug := rubyEntryFor(name)
		deps = append(deps, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemRubyGems,
			Package:     name,
			Version:     version,
			Category:    cat,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return deps, nil
}

// parseLockSpecs reads the GEM/specs section of a Gemfile.lock and returns a
// map of gem name → pinned version. Only top-level spec entries (4-space indent)
// are captured; dependency lines of those entries (6+ spaces) are skipped.
func parseLockSpecs(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	specs := make(map[string]string)
	inSpecs := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimRight(line, " ") == "  specs:" {
			inSpecs = true
			continue
		}
		if !inSpecs {
			continue
		}
		// A blank line or a line with no leading space ends the specs block.
		if line == "" || (len(line) > 0 && line[0] != ' ') {
			inSpecs = false
			continue
		}
		// 4-space indent = top-level gem entry: "    name (version)"
		// 6+-space indent = dependency of that gem: skip.
		if !strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "      ") {
			continue
		}
		name, version, ok := parseLockSpecLine(strings.TrimPrefix(line, "    "))
		if ok {
			specs[name] = version
		}
	}
	return specs, nil
}

// parseLockSpecLine parses "name (version)" into its components.
// Returns ok=false if the line doesn't match the expected format.
func parseLockSpecLine(s string) (name, version string, ok bool) {
	i := strings.Index(s, " (")
	if i == -1 {
		return "", "", false
	}
	name = s[:i]
	version = strings.TrimSuffix(s[i+2:], ")")
	return name, version, true
}

// ── Gemfile parsing ───────────────────────────────────────────────────────────

var gemLineRe = regexp.MustCompile(`^gem\s+['"]([^'"]+)['"](?:\s*,\s*['"]([^'"]+)['"])?`)

// extractDevGemNames returns the set of gem names declared in :development or
// :test group blocks in the Gemfile.
func extractDevGemNames(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	devGems := make(map[string]bool)
	depth := 0      // overall block depth (do...end)
	devDepth := 0   // depth at which the outermost dev group was opened
	inDev := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isGroupBlockStart(line) {
			depth++
			if !inDev && isDevTestGroup(line) {
				inDev = true
				devDepth = depth
			}
		} else if line == "end" {
			if inDev && depth == devDepth {
				inDev = false
			}
			if depth > 0 {
				depth--
			}
		} else if inDev {
			if m := gemLineRe.FindStringSubmatch(line); m != nil {
				devGems[m[1]] = true
			}
		}
	}
	return devGems, scanner.Err()
}

// parseGemfile extracts production gem dependencies directly from the Gemfile
// (fallback when no Gemfile.lock is present). Gems in :development or :test
// group blocks are excluded.
func parseGemfile(path string) ([]workerdomain.Dependency, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var deps []workerdomain.Dependency
	depth := 0
	devDepth := 0
	inDev := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isGroupBlockStart(line) {
			depth++
			if !inDev && isDevTestGroup(line) {
				inDev = true
				devDepth = depth
			}
		} else if line == "end" {
			if inDev && depth == devDepth {
				inDev = false
			}
			if depth > 0 {
				depth--
			}
		} else if !inDev {
			if m := gemLineRe.FindStringSubmatch(line); m != nil {
				version := ""
				if len(m) > 2 && m[2] != "" {
					version = stripRubyConstraint(m[2])
				}
				cat, display, slug := rubyEntryFor(m[1])
				deps = append(deps, workerdomain.Dependency{
					Ecosystem:   workerdomain.EcosystemRubyGems,
					Package:     m[1],
					Version:     version,
					Category:    cat,
					Approximate: true,
					DisplayName: display,
					Slug:        slug,
				})
			}
		}
	}
	return deps, scanner.Err()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func isGroupBlockStart(line string) bool {
	return strings.HasPrefix(line, "group ") && strings.HasSuffix(line, " do")
}

func isDevTestGroup(line string) bool {
	return strings.Contains(line, ":test") || strings.Contains(line, ":development")
}

// stripRubyConstraint removes version operators from a Gemfile constraint so
// that "~> 3.1" becomes "3.1". Compound constraints take the lower bound.
func stripRubyConstraint(s string) string {
	s = strings.TrimSpace(s)
	// Take first specifier from compound constraints (">=1.0, <2.0").
	if i := strings.Index(s, ","); i != -1 {
		s = s[:i]
	}
	s = strings.TrimLeft(s, "~>=<! ")
	return strings.TrimSpace(s)
}

// rubyEntryFor returns (category, displayName, slug) for a RubyGems package.
// Delegates to the framework catalog; unknown gems get category "library".
func rubyEntryFor(name string) (category, displayName, slug string) {
	if e, ok := frameworkEntryForPkg(name); ok {
		return "framework", e.DisplayName, e.Slug
	}
	return "library", "", ""
}
