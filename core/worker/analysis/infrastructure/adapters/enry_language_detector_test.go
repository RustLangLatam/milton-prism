package adapters_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureRepo = "testdata/fixture-repo"

// TestDetect_LanguagesAndCounts verifies that the three programming files in the
// fixture repo are detected with the correct language names and line counts.
func TestDetect_LanguagesAndCounts(t *testing.T) {
	d := adapters.NewEnryLanguageDetector()
	langs, err := d.Detect(context.Background(), fixtureRepo)
	require.NoError(t, err)

	byName := make(map[string]struct {
		Files uint64
		Lines uint64
	})
	for _, l := range langs {
		byName[l.Name] = struct {
			Files uint64
			Lines uint64
		}{l.Files, l.Lines}
	}

	// Three programming files: Java, PHP, Python.
	assert.Contains(t, byName, "Java", "Java must be detected")
	assert.Contains(t, byName, "PHP", "PHP must be detected")
	assert.Contains(t, byName, "Python", "Python must be detected")

	// Each language contributes exactly one file.
	assert.Equal(t, uint64(1), byName["Java"].Files, "Java file count")
	assert.Equal(t, uint64(1), byName["PHP"].Files, "PHP file count")
	assert.Equal(t, uint64(1), byName["Python"].Files, "Python file count")

	// Exact line counts derived from the fixture files (see testdata/).
	assert.Equal(t, uint64(7), byName["Java"].Lines, "Main.java line count")
	assert.Equal(t, uint64(6), byName["PHP"].Lines, "index.php line count")
	assert.Equal(t, uint64(6), byName["Python"].Lines, "app.py line count")
}

// TestDetect_TotalFilesAndLines verifies that the aggregate totals match the
// sum of all three programming files.
func TestDetect_TotalFilesAndLines(t *testing.T) {
	d := adapters.NewEnryLanguageDetector()
	langs, err := d.Detect(context.Background(), fixtureRepo)
	require.NoError(t, err)

	var totalFiles, totalLines uint64
	for _, l := range langs {
		totalFiles += l.Files
		totalLines += l.Lines
	}
	assert.Equal(t, uint64(3), totalFiles, "total files (Java + PHP + Python)")
	assert.Equal(t, uint64(19), totalLines, "total lines (7 + 6 + 6)")
}

// TestDetect_SkipsVendor verifies that the file under vendor/ is not counted,
// which would otherwise appear as a Go file.
func TestDetect_SkipsVendor(t *testing.T) {
	d := adapters.NewEnryLanguageDetector()
	langs, err := d.Detect(context.Background(), fixtureRepo)
	require.NoError(t, err)

	for _, l := range langs {
		assert.NotEqual(t, "Go", l.Name, "vendored Go file must not appear in results")
	}
}

// TestDetect_ResultsAreSorted verifies that results are sorted by file count
// descending (all files count here is 1 so secondary sort by name applies).
func TestDetect_ResultsAreSorted(t *testing.T) {
	d := adapters.NewEnryLanguageDetector()
	langs, err := d.Detect(context.Background(), fixtureRepo)
	require.NoError(t, err)
	require.Len(t, langs, 3, "exactly 3 programming languages expected")

	// With equal file counts the secondary sort is alphabetical: Java < PHP < Python.
	assert.Equal(t, "Java", langs[0].Name)
	assert.Equal(t, "PHP", langs[1].Name)
	assert.Equal(t, "Python", langs[2].Name)
}

// TestDetect_EmptyDir verifies that an empty workspace returns an empty slice
// without error.
func TestDetect_EmptyDir(t *testing.T) {
	t.TempDir() // force temp dir existence
	dir := t.TempDir()
	d := adapters.NewEnryLanguageDetector()
	langs, err := d.Detect(context.Background(), dir)
	require.NoError(t, err)
	assert.Empty(t, langs)
}

// TestDetect_SkipsAssetsDir verifies that JS files under assets/ are excluded
// from language statistics so that admin-template libraries do not outweigh the
// actual backend language (e.g. PHP in a CodeIgniter project).
func TestDetect_SkipsAssetsDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// One PHP application file.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "application"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "application/Controller.php"),
		[]byte("<?php\nclass Controller {}\n"), 0644))

	// Many JS files under assets/ (simulates a vendored admin template).
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "assets", "libs"), 0755))
	for i := range 10 {
		path := filepath.Join(dir, "assets", "libs", fmt.Sprintf("lib%d.js", i))
		require.NoError(t, os.WriteFile(path, []byte("(function(){var x=1;})();\n"), 0644))
	}

	d := adapters.NewEnryLanguageDetector()
	langs, err := d.Detect(context.Background(), dir)
	require.NoError(t, err)

	byName := make(map[string]uint64)
	for _, l := range langs {
		byName[l.Name] = l.Files
	}
	assert.Contains(t, byName, "PHP", "PHP must be detected")
	assert.NotContains(t, byName, "JavaScript", "JS files inside assets/ must be excluded")
}

// TestDetect_SkipsStaticDir verifies that files under static/ are excluded.
func TestDetect_SkipsStaticDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.py"),
		[]byte("from flask import Flask\napp = Flask(__name__)\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "static"), 0755))
	for i := range 5 {
		path := filepath.Join(dir, "static", fmt.Sprintf("bundle%d.js", i))
		require.NoError(t, os.WriteFile(path, []byte("(function(){})();\n"), 0644))
	}

	d := adapters.NewEnryLanguageDetector()
	langs, err := d.Detect(context.Background(), dir)
	require.NoError(t, err)

	byName := make(map[string]uint64)
	for _, l := range langs {
		byName[l.Name] = l.Files
	}
	assert.Contains(t, byName, "Python", "Python must be detected")
	assert.NotContains(t, byName, "JavaScript", "JS files inside static/ must be excluded")
}
