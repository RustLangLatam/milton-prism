package adapters_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/infrastructure/adapters"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFrameworkDetector() *adapters.FileSystemFrameworkDetector {
	return adapters.NewFileSystemFrameworkDetector()
}

// scaffoldFiles creates the given file paths (relative to dir) as empty files,
// creating parent directories as needed.
func scaffoldFiles(t *testing.T, dir string, files ...string) {
	t.Helper()
	for _, f := range files {
		full := filepath.Join(dir, filepath.FromSlash(f))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(""), 0644))
	}
}

// scaffoldDirs creates the given directory paths (relative to dir).
func scaffoldDirs(t *testing.T, dir string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0755))
	}
}

// ── CodeIgniter 3 ─────────────────────────────────────────────────────────────

func TestFileSystemFrameworkDetector_DetectsCodeIgniter3(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scaffoldFiles(t, dir, "system/core/CodeIgniter.php")
	scaffoldDirs(t, dir, "application")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	require.Len(t, techs, 1)
	assert.Equal(t, "CodeIgniter", techs[0].GetName())
	assert.Equal(t, "3.x", techs[0].GetDetectedVersion())
	assert.Equal(t, "framework", techs[0].GetCategory())
}

func TestFileSystemFrameworkDetector_CI3_RequiresBothMarkers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Only system/core/CodeIgniter.php without the application/ dir.
	scaffoldFiles(t, dir, "system/core/CodeIgniter.php")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	assert.Empty(t, techs, "single marker must not trigger detection")
}

// ── CodeIgniter 4 ─────────────────────────────────────────────────────────────

func TestFileSystemFrameworkDetector_DetectsCodeIgniter4(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scaffoldFiles(t, dir, "spark")
	scaffoldDirs(t, dir, "app")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	require.Len(t, techs, 1)
	assert.Equal(t, "CodeIgniter", techs[0].GetName())
	assert.Equal(t, "4.x", techs[0].GetDetectedVersion())
}

// ── Laravel ───────────────────────────────────────────────────────────────────

func TestFileSystemFrameworkDetector_DetectsLaravel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scaffoldFiles(t, dir, "artisan")
	scaffoldDirs(t, dir, "app/Http")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	require.Len(t, techs, 1)
	assert.Equal(t, "Laravel", techs[0].GetName())
	assert.Equal(t, "framework", techs[0].GetCategory())
}

// ── Symfony ───────────────────────────────────────────────────────────────────

func TestFileSystemFrameworkDetector_DetectsSymfony_ViaKernelPhp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scaffoldFiles(t, dir, "bin/console", "src/Kernel.php")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	require.Len(t, techs, 1)
	assert.Equal(t, "Symfony", techs[0].GetName())
}

func TestFileSystemFrameworkDetector_DetectsSymfony_ViaBundlesPhp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scaffoldFiles(t, dir, "bin/console", "config/bundles.php")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	require.Len(t, techs, 1)
	assert.Equal(t, "Symfony", techs[0].GetName())
}

func TestFileSystemFrameworkDetector_Symfony_NoDuplicateWhenBothRulesMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Both Symfony marker sets present — must produce only one entry.
	scaffoldFiles(t, dir, "bin/console", "src/Kernel.php", "config/bundles.php")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	assert.Len(t, techs, 1, "two matching Symfony rules must not produce duplicates")
}

// ── empty workspace ───────────────────────────────────────────────────────────

func TestFileSystemFrameworkDetector_EmptyWorkspace_ReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	assert.Nil(t, techs)
}

// ── deduplication against manifest-detected technologies ─────────────────────

func TestFileSystemFrameworkDetector_SkipsWhenManifestAlreadyDetected_LaravelPackageName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scaffoldFiles(t, dir, "artisan")
	scaffoldDirs(t, dir, "app/Http")

	existing := []*analysisdomain.Technology{
		{Name: "laravel/framework", Category: "framework"},
	}

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, existing)
	require.NoError(t, err)
	assert.Empty(t, techs, "laravel/framework in existing must prevent duplicate Laravel entry")
}

func TestFileSystemFrameworkDetector_SkipsWhenManifestAlreadyDetected_DisplayName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scaffoldFiles(t, dir, "artisan")
	scaffoldDirs(t, dir, "app/Http")

	existing := []*analysisdomain.Technology{
		{Name: "Laravel", Category: "framework"},
	}

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, existing)
	require.NoError(t, err)
	assert.Empty(t, techs, "exact display-name match in existing must prevent duplicate")
}

func TestFileSystemFrameworkDetector_SkipsCI3_WhenCI4PackageDetected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// CI3 markers present but codeigniter4/framework already in Composer output.
	scaffoldFiles(t, dir, "system/core/CodeIgniter.php")
	scaffoldDirs(t, dir, "application")

	existing := []*analysisdomain.Technology{
		{Name: "codeigniter4/framework", Category: "framework"},
	}

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, existing)
	require.NoError(t, err)
	assert.Empty(t, techs, "codeigniter4/framework in existing must prevent duplicate CodeIgniter entry")
}

// ── multiple frameworks in same workspace ─────────────────────────────────────

func TestFileSystemFrameworkDetector_MultipleFrameworks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Unusual but valid: a monorepo with both a Laravel app and a Symfony bundle.
	scaffoldFiles(t, dir, "artisan", "bin/console", "src/Kernel.php")
	scaffoldDirs(t, dir, "app/Http")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	assert.Len(t, techs, 2)
	names := map[string]bool{}
	for _, t2 := range techs {
		names[t2.GetName()] = true
	}
	assert.True(t, names["Laravel"], "Laravel must be detected")
	assert.True(t, names["Symfony"], "Symfony must be detected")
}

// writeFile writes content to dir/rel, creating parent dirs.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0644))
}

// ── Spring Boot (Java / Gradle) ───────────────────────────────────────────────

func TestFileSystemFrameworkDetector_DetectsSpring_ViaGradle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Gradle Spring Boot project: the Maven manifest parser never sees this, so the
	// structural detector is the only thing that can surface the framework.
	writeFile(t, dir, "build.gradle", "plugins {\n  id 'org.springframework.boot' version '3.2.0'\n}\n")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	require.Len(t, techs, 1)
	assert.Equal(t, "Spring", techs[0].GetName())
	assert.Equal(t, "framework", techs[0].GetCategory())
	assert.Equal(t, "spring", techs[0].GetSlug())
}

func TestFileSystemFrameworkDetector_DetectsSpring_ViaGradleKts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "build.gradle.kts", `dependencies { implementation("org.springframework.boot:spring-boot-starter-web") }`)

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	require.Len(t, techs, 1)
	assert.Equal(t, "Spring", techs[0].GetName())
}

func TestFileSystemFrameworkDetector_DetectsSpring_ViaAnnotationOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A Gradle multi-module build whose root manifest omits the literal marker, but
	// an application class carries @SpringBootApplication — the annotation fallback.
	writeFile(t, dir, "build.gradle", "plugins { id 'java' }\n")
	writeFile(t, dir, "src/main/java/com/example/App.java",
		"package com.example;\nimport org.springframework.boot.autoconfigure.SpringBootApplication;\n@SpringBootApplication\npublic class App {}\n")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	require.Len(t, techs, 1)
	assert.Equal(t, "Spring", techs[0].GetName())
}

func TestFileSystemFrameworkDetector_Spring_SkipsWhenManifestAlreadyDetected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Maven project: the manifest parser already emitted Spring from the groupID, so
	// the structural detector must not add a duplicate.
	writeFile(t, dir, "build.gradle", "id 'org.springframework.boot'\n")
	existing := []*analysisdomain.Technology{
		{Name: "Spring", Category: "framework"},
	}

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, existing)
	require.NoError(t, err)
	assert.Empty(t, techs, "Spring already in existing must prevent a duplicate entry")
}

func TestFileSystemFrameworkDetector_Spring_NoFalsePositiveOnPlainGradle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A plain (non-Spring) Gradle project must NOT be labelled Spring.
	writeFile(t, dir, "build.gradle", "plugins { id 'java' }\ndependencies { implementation 'com.google.guava:guava:33.0.0-jre' }\n")

	techs, err := newFrameworkDetector().Detect(context.Background(), dir, nil)
	require.NoError(t, err)
	assert.Empty(t, techs, "a non-Spring Gradle project must not be detected as Spring")
}
