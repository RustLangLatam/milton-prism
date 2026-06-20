package adapters

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	enry "github.com/go-enry/go-enry/v2"

	workerdomain "milton_prism/core/worker/analysis/domain"
	"milton_prism/core/worker/analysis/ports"
)

var _ ports.LanguageDetector = (*EnryLanguageDetector)(nil)

// EnryLanguageDetector implements ports.LanguageDetector using go-enry,
// the Go port of GitHub's Linguist library.
type EnryLanguageDetector struct{}

// NewEnryLanguageDetector returns a new EnryLanguageDetector.
func NewEnryLanguageDetector() *EnryLanguageDetector {
	return &EnryLanguageDetector{}
}

// Detect walks workspacePath, detects programming and markup languages, and
// returns one DetectedLanguage per language with file and line counts.
//
// Excluded from detection (matching Linguist's rules):
//   - Vendored files (enry.IsVendor)
//   - Documentation (enry.IsDocumentation)
//   - Binary files (enry.IsBinary)
//   - Generated files (enry.IsGenerated)
//   - Files whose language is empty or has type Prose / Data
//
// Results are sorted by file count descending, then name ascending.
func (d *EnryLanguageDetector) Detect(ctx context.Context, workspacePath string) ([]workerdomain.DetectedLanguage, error) {
	type langStats struct {
		files uint64
		lines uint64
	}
	byLang := make(map[string]*langStats)

	err := filepath.WalkDir(workspacePath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Dangling symlinks or unreadable entries (e.g. a symlink pointing
			// outside the shallow-cloned workspace) are skipped silently.
			if errors.Is(walkErr, fs.ErrNotExist) || errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(workspacePath, path)
		if err != nil {
			return err
		}

		// Skip paths that linguist would exclude before reading content.
		if enry.IsVendor(rel) || enry.IsDocumentation(rel) {
			return nil
		}

		// Exclude conventional frontend-asset directories. PHP/Python/Ruby
		// projects often bundle an admin template whose vendored JS/CSS files
		// live under assets/ or public/vendor/, and those files would otherwise
		// inflate the JavaScript file count above the actual backend language.
		if isAssetDir(rel) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			// Skip files that can't be read (dangling symlinks, permission errors).
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}

		if enry.IsBinary(content) || enry.IsGenerated(rel, content) {
			return nil
		}

		lang := enry.GetLanguage(filepath.Base(path), content)
		if lang == "" {
			return nil
		}

		// Only track programming and markup (not prose, data, or unknown).
		ltype := enry.GetLanguageType(lang)
		if ltype != enry.Programming && ltype != enry.Markup {
			return nil
		}

		lines := countLines(content)

		if byLang[lang] == nil {
			byLang[lang] = &langStats{}
		}
		byLang[lang].files++
		byLang[lang].lines += lines

		return nil
	})
	if err != nil {
		return nil, err
	}

	result := make([]workerdomain.DetectedLanguage, 0, len(byLang))
	for name, s := range byLang {
		result = append(result, workerdomain.DetectedLanguage{
			Name:  name,
			Files: s.files,
			Lines: s.lines,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Files != result[j].Files {
			return result[i].Files > result[j].Files
		}
		return result[i].Name < result[j].Name
	})

	return result, nil
}

// isAssetDir returns true when rel is inside a top-level directory that
// conventionally holds vendored frontend assets: compiled CSS/JS bundles,
// admin-template libraries, fonts, and images. Files here are presentation
// layer artifacts, not application source code.
//
// Covers the three patterns we see in the wild:
//   - assets/   — used by CodeIgniter, Symfony admin templates, Rails
//   - static/   — used by Django, Flask, and generic PHP projects
//   - public/vendor/ — Composer-managed front-end deps (e.g. Bower via Packagist)
func isAssetDir(rel string) bool {
	top := strings.SplitN(rel, "/", 2)[0]
	switch top {
	case "assets", "static":
		return true
	}
	return strings.HasPrefix(rel, "public/vendor/")
}

// countLines returns the number of lines in content. A file ending with a
// newline is not double-counted (standard Unix convention).
func countLines(content []byte) uint64 {
	if len(content) == 0 {
		return 0
	}
	n := uint64(bytes.Count(content, []byte("\n")))
	// A file that has no trailing newline has one more line than newline count.
	if content[len(content)-1] != '\n' {
		n++
	}
	return n
}
