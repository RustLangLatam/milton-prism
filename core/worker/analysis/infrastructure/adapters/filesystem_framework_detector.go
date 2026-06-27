package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.StructuralFrameworkDetector = (*FileSystemFrameworkDetector)(nil)

// fileMarkerRule matches a framework when all paths in requiredFiles exist as
// regular files and all paths in requiredDirs exist as directories, both
// relative to the workspace root.
type fileMarkerRule struct {
	displayName   string
	versionHint   string
	requiredFiles []string
	requiredDirs  []string
}

// frameworkRules is the detection table. Evaluated in order; multiple rules can
// match (e.g. two alternative Symfony marker sets). Deduplication within a
// single Detect call prevents the same displayName from appearing twice.
var frameworkRules = []fileMarkerRule{
	{
		displayName:   "CodeIgniter",
		versionHint:   "3.x",
		requiredFiles: []string{"system/core/CodeIgniter.php"},
		requiredDirs:  []string{"application"},
	},
	{
		displayName:   "CodeIgniter",
		versionHint:   "4.x",
		requiredFiles: []string{"spark"},
		requiredDirs:  []string{"app"},
	},
	{
		displayName:   "Laravel",
		requiredFiles: []string{"artisan"},
		requiredDirs:  []string{"app/Http"},
	},
	{
		// Symfony ≥ 4 with Kernel.php
		displayName:   "Symfony",
		requiredFiles: []string{"bin/console", "src/Kernel.php"},
	},
	{
		// Symfony ≥ 4 with bundles.php (Flex layout without standalone Kernel)
		displayName:   "Symfony",
		requiredFiles: []string{"bin/console", "config/bundles.php"},
	},
}

// composerEquivalents maps Composer package names to the canonical display name
// used in frameworkRules. When a manifest-detected technology already covers a
// framework, the structural detector skips it to avoid duplicate entries.
var composerEquivalents = map[string]string{
	"laravel/framework":        "Laravel",
	"symfony/symfony":          "Symfony",
	"symfony/framework-bundle": "Symfony",
	"codeigniter4/framework":   "CodeIgniter",
	"codeigniter/framework":    "CodeIgniter",
	"yiisoft/yii2":             "Yii",
	"cakephp/cakephp":          "CakePHP",
	"slim/slim":                "Slim",
	"laminas/laminas-mvc":      "Laminas",
}

// FileSystemFrameworkDetector implements ports.StructuralFrameworkDetector by
// checking for well-known files and directories that are only present when a
// specific framework was installed (or vendored) into the workspace.
type FileSystemFrameworkDetector struct{}

// NewFileSystemFrameworkDetector returns a ready FileSystemFrameworkDetector.
func NewFileSystemFrameworkDetector() *FileSystemFrameworkDetector {
	return &FileSystemFrameworkDetector{}
}

// Detect checks frameworkRules against workspacePath and returns any newly
// identified frameworks not already present in existing.
func (d *FileSystemFrameworkDetector) Detect(_ context.Context, workspacePath string, existing []*analysisdomain.Technology) ([]*analysisdomain.Technology, error) {
	var result []*analysisdomain.Technology
	// alreadyFound prevents the same displayName from being emitted twice when
	// multiple rules match the same framework (e.g. two Symfony marker sets).
	alreadyFound := make(map[string]bool)

	for _, rule := range frameworkRules {
		if alreadyFound[rule.displayName] {
			continue
		}
		if coversByManifest(existing, rule.displayName) {
			alreadyFound[rule.displayName] = true
			continue
		}
		if matchesRule(workspacePath, rule) {
			result = append(result, &analysisdomain.Technology{
				Name:            rule.displayName,
				DetectedVersion: rule.versionHint,
				Category:        "framework",
				Slug:            frameworkSlugForDisplay(rule.displayName),
			})
			alreadyFound[rule.displayName] = true
		}
	}

	// Spring Boot (Java) by manifest content. The Maven manifest parser already
	// emits Spring from an org.springframework groupID, but GRADLE projects
	// (build.gradle/.kts) are not parsed for framework dependencies, so a
	// Gradle-based Spring Boot service would otherwise show no framework. Detect it
	// structurally via javaIsSpringBoot (reads pom.xml/build.gradle/.kts for the
	// spring-boot marker, plus a @SpringBootApplication source annotation). Skipped
	// when a manifest framework already covers Spring, so Maven projects already
	// detected by groupID are never double-counted.
	if !alreadyFound["Spring"] && !coversByManifest(existing, "Spring") && javaIsSpringBoot(workspacePath) {
		result = append(result, &analysisdomain.Technology{
			Name:     "Spring",
			Category: "framework",
			Slug:     frameworkSlugForDisplay("Spring"),
		})
		alreadyFound["Spring"] = true
	}

	return result, nil
}

// coversByManifest returns true when existing already contains a framework
// entry with the given display name, or a Composer package name whose canonical
// display name matches it via composerEquivalents.
func coversByManifest(existing []*analysisdomain.Technology, displayName string) bool {
	lower := strings.ToLower(displayName)
	for _, t := range existing {
		if t.GetCategory() != "framework" {
			continue
		}
		name := strings.ToLower(t.GetName())
		if name == lower {
			return true
		}
		if mapped, ok := composerEquivalents[name]; ok && strings.ToLower(mapped) == lower {
			return true
		}
	}
	return false
}

// matchesRule returns true when all required files and directories from rule
// exist under workspacePath.
func matchesRule(workspacePath string, rule fileMarkerRule) bool {
	for _, f := range rule.requiredFiles {
		path := filepath.Join(workspacePath, filepath.FromSlash(f))
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return false
		}
	}
	for _, d := range rule.requiredDirs {
		path := filepath.Join(workspacePath, filepath.FromSlash(d))
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}
