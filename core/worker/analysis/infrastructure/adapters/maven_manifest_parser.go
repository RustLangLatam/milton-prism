package adapters

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.ManifestParser = (*MavenManifestParser)(nil)

// MavenManifestParser implements ports.ManifestParser for the Maven (Central)
// ecosystem. It parses pom.xml in the workspace root.
//
// Scope policy:
//   - compile (default) and runtime → included (production dependencies).
//   - provided → included (the system depends on it even if the container supplies it).
//   - test and system → excluded.
//
// Version-property references (${...}) are left as an empty string; the version
// resolver stage will fill the latest version regardless.
type MavenManifestParser struct{}

// NewMavenManifestParser returns a new MavenManifestParser.
func NewMavenManifestParser() *MavenManifestParser {
	return &MavenManifestParser{}
}

func (p *MavenManifestParser) Parse(_ context.Context, workspacePath string, _ workerdomain.Ecosystem) ([]workerdomain.Dependency, error) {
	pomPath := filepath.Join(workspacePath, "pom.xml")
	if _, err := os.Stat(pomPath); err != nil {
		return nil, nil
	}
	return parsePom(pomPath)
}

// ── pom.xml parsing ───────────────────────────────────────────────────────────

type mavenProject struct {
	Dependencies []mavenDep `xml:"dependencies>dependency"`
}

type mavenDep struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
}

func parsePom(path string) ([]workerdomain.Dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var project mavenProject
	if err := xml.Unmarshal(data, &project); err != nil {
		return nil, err
	}

	deps := make([]workerdomain.Dependency, 0, len(project.Dependencies))
	for _, d := range project.Dependencies {
		if skipMavenScope(d.Scope) {
			continue
		}
		cat, display, slug := mavenEntryFor(d.GroupID)
		deps = append(deps, workerdomain.Dependency{
			Ecosystem:   workerdomain.EcosystemMaven,
			Package:     d.GroupID + ":" + d.ArtifactID,
			Version:     resolvedVersion(d.Version),
			Category:    cat,
			Approximate: true,
			DisplayName: display,
			Slug:        slug,
		})
	}
	return deps, nil
}

// skipMavenScope returns true for scopes that represent non-production
// dependencies: test artefacts and system-local jars.
func skipMavenScope(scope string) bool {
	switch scope {
	case "test", "system":
		return true
	}
	return false
}

// resolvedVersion strips Maven property references (${...}) which cannot be
// resolved without evaluating the full POM inheritance chain. The version
// resolver stage (stage 4) queries the registry for the latest version
// regardless, so an empty string here causes no information loss.
func resolvedVersion(v string) string {
	if strings.HasPrefix(v, "${") {
		return ""
	}
	return v
}

// mavenEntryFor returns (category, displayName, slug) for a Maven groupID.
// Delegates to the framework catalog; unknown groupIDs get category "library".
func mavenEntryFor(groupID string) (category, displayName, slug string) {
	if e, ok := frameworkEntryForMaven(groupID); ok {
		return "framework", e.DisplayName, e.Slug
	}
	return "library", "", ""
}
