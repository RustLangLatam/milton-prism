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

var _ ports.ManifestParser = (*NuGetManifestParser)(nil)

// NuGetManifestParser implements ports.ManifestParser for the NuGet (.NET) ecosystem.
//
// Detection strategy:
//   - Prefers *.csproj files (SDK-style PackageReference). All .csproj files in
//     the workspace root are parsed and their results merged.
//   - Falls back to packages.config (classic NuGet format).
//
// Version-property references ($(PropertyName)) are stored as empty string,
// matching the same "version indeterminate" convention used for Maven parent BOMs.
// This applies to Central Package Management (Directory.Packages.props) too.
//
// packages.config entries with developmentDependency="true" are excluded.
// PackageReference has no standard dev-only marker; all references are included.
type NuGetManifestParser struct{}

// NewNuGetManifestParser returns a new NuGetManifestParser.
func NewNuGetManifestParser() *NuGetManifestParser {
	return &NuGetManifestParser{}
}

func (p *NuGetManifestParser) Parse(_ context.Context, workspacePath string, _ workerdomain.Ecosystem) ([]workerdomain.Dependency, error) {
	csprojFiles, err := filepath.Glob(filepath.Join(workspacePath, "*.csproj"))
	if err != nil {
		return nil, err
	}
	if len(csprojFiles) > 0 {
		return parseCsprojFiles(csprojFiles)
	}
	configPath := filepath.Join(workspacePath, "packages.config")
	if fileExists(configPath) {
		return parsePackagesConfig(configPath)
	}
	return nil, nil
}

// ── *.csproj parsing ──────────────────────────────────────────────────────────

type csprojProject struct {
	ItemGroups []csprojItemGroup `xml:"ItemGroup"`
}

type csprojItemGroup struct {
	PackageRefs []csprojPkgRef `xml:"PackageReference"`
}

// csprojPkgRef handles both the attribute form
// (<PackageReference Include="X" Version="1.0" />) and the element form
// (<PackageReference Include="X"><Version>1.0</Version></PackageReference>).
type csprojPkgRef struct {
	Include     string `xml:"Include,attr"`
	VersionAttr string `xml:"Version,attr"`
	VersionElem string `xml:"Version"`
}

func (r csprojPkgRef) version() string {
	if r.VersionAttr != "" {
		return r.VersionAttr
	}
	return r.VersionElem
}

func parseCsprojFiles(paths []string) ([]workerdomain.Dependency, error) {
	seen := make(map[string]bool)
	var deps []workerdomain.Dependency
	for _, path := range paths {
		fileDeps, err := parseSingleCsproj(path)
		if err != nil {
			return nil, err
		}
		for _, d := range fileDeps {
			if !seen[d.Package] {
				seen[d.Package] = true
				deps = append(deps, d)
			}
		}
	}
	return deps, nil
}

func parseSingleCsproj(path string) ([]workerdomain.Dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var project csprojProject
	if err := xml.Unmarshal(data, &project); err != nil {
		return nil, err
	}
	var deps []workerdomain.Dependency
	for _, ig := range project.ItemGroups {
		for _, ref := range ig.PackageRefs {
			if ref.Include == "" {
				continue
			}
			deps = append(deps, workerdomain.Dependency{
				Ecosystem:   workerdomain.EcosystemNuGet,
				Package:     ref.Include,
				Version:     nugetVersion(ref.version()),
				Category:    nugetCategory(ref.Include),
				Approximate: true,
			})
		}
	}
	return deps, nil
}

// ── packages.config parsing ───────────────────────────────────────────────────

type nugetPackagesConfig struct {
	Packages []nugetPkg `xml:"package"`
}

type nugetPkg struct {
	ID            string `xml:"id,attr"`
	Version       string `xml:"version,attr"`
	DevDependency string `xml:"developmentDependency,attr"`
}

func parsePackagesConfig(path string) ([]workerdomain.Dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg nugetPackagesConfig
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	deps := make([]workerdomain.Dependency, 0, len(cfg.Packages))
	for _, pkg := range cfg.Packages {
		if strings.EqualFold(pkg.DevDependency, "true") {
			continue
		}
		deps = append(deps, workerdomain.Dependency{
			Ecosystem: workerdomain.EcosystemNuGet,
			Package:   pkg.ID,
			Version:   pkg.Version,
			Category:  nugetCategory(pkg.ID),
		})
	}
	return deps, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// nugetVersion strips $(PropertyName) references that cannot be resolved
// without evaluating MSBuild properties (Central Package Management,
// Directory.Build.props, etc.). Stored as empty; stage 4 resolves latest.
func nugetVersion(v string) string {
	if strings.HasPrefix(v, "$(") {
		return ""
	}
	return v
}

// nugetCategory classifies ASP.NET Core packages as "framework". The prefix
// covers the entire web stack (MVC, Razor, SignalR, gRPC, etc.) under one
// Microsoft.AspNetCore namespace. Everything else is "library".
func nugetCategory(name string) string {
	if strings.HasPrefix(name, "Microsoft.AspNetCore.") {
		return "framework"
	}
	return "library"
}
