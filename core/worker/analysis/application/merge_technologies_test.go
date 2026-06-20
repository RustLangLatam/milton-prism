package application_test

import (
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
	"milton_prism/core/worker/analysis/application"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func lang(name string) *analysisdomain.Technology {
	return &analysisdomain.Technology{Name: name, Category: "language"}
}

func langWithVersion(name, detected string) *analysisdomain.Technology {
	return &analysisdomain.Technology{Name: name, Category: "language", DetectedVersion: detected}
}

func langWithLatest(name, latest string, status analysisdomain.TechnologyStatus) *analysisdomain.Technology {
	return &analysisdomain.Technology{Name: name, Category: "language", LatestVersion: latest, Status: status}
}

func lib(name, version string) *analysisdomain.Technology {
	return &analysisdomain.Technology{Name: name, Category: "library", DetectedVersion: version}
}

func fw(name, version string) *analysisdomain.Technology {
	return &analysisdomain.Technology{Name: name, Category: "framework", DetectedVersion: version}
}

func byKey(techs []*analysisdomain.Technology) map[string]*analysisdomain.Technology {
	m := make(map[string]*analysisdomain.Technology, len(techs))
	for _, t := range techs {
		m[t.GetName()+"/"+t.GetCategory()] = t
	}
	return m
}

// ── nil / empty inputs ────────────────────────────────────────────────────────

func TestMerge_NilExisting_ReturnsIncomingCopy(t *testing.T) {
	t.Parallel()
	incoming := []*analysisdomain.Technology{lang("Java"), lang("PHP")}
	result := application.MergeTechnologies(nil, incoming)
	require.Len(t, result, 2)
	m := byKey(result)
	assert.Contains(t, m, "Java/language")
	assert.Contains(t, m, "PHP/language")
}

func TestMerge_NilIncoming_ReturnsExistingCopy(t *testing.T) {
	t.Parallel()
	existing := []*analysisdomain.Technology{lang("Java")}
	result := application.MergeTechnologies(existing, nil)
	require.Len(t, result, 1)
	assert.Equal(t, "Java", result[0].GetName())
}

func TestMerge_BothNil_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	result := application.MergeTechnologies(nil, nil)
	assert.Empty(t, result)
}

// ── new key appended ──────────────────────────────────────────────────────────

func TestMerge_NewKeyInIncoming_Appended(t *testing.T) {
	t.Parallel()
	existing := []*analysisdomain.Technology{lang("Java")}
	incoming := []*analysisdomain.Technology{lib("Spring Boot", "3.2.0")}

	result := application.MergeTechnologies(existing, incoming)
	require.Len(t, result, 2)
	m := byKey(result)
	assert.Contains(t, m, "Java/language")
	assert.Contains(t, m, "Spring Boot/library")
}

// ── field fill (existing empty → incoming fills) ──────────────────────────────

func TestMerge_FillsEmptyDetectedVersion(t *testing.T) {
	t.Parallel()
	existing := []*analysisdomain.Technology{lang("Java")} // no detected_version
	incoming := []*analysisdomain.Technology{langWithVersion("Java", "17")}

	result := application.MergeTechnologies(existing, incoming)
	require.Len(t, result, 1)
	assert.Equal(t, "17", result[0].GetDetectedVersion())
}

func TestMerge_FillsEmptyLatestVersion(t *testing.T) {
	t.Parallel()
	existing := []*analysisdomain.Technology{lang("Java")}
	incoming := []*analysisdomain.Technology{langWithLatest("Java", "21", analysisdomain.TechnologyStatusOutdated)}

	result := application.MergeTechnologies(existing, incoming)
	require.Len(t, result, 1)
	assert.Equal(t, "21", result[0].GetLatestVersion())
	assert.Equal(t, analysisdomain.TechnologyStatusOutdated, result[0].GetStatus())
}

func TestMerge_FillsUnspecifiedStatus(t *testing.T) {
	t.Parallel()
	existing := []*analysisdomain.Technology{lang("PHP")} // Status = UNSPECIFIED (zero)
	incoming := []*analysisdomain.Technology{
		{Name: "PHP", Category: "language", Status: analysisdomain.TechnologyStatusCurrent},
	}

	result := application.MergeTechnologies(existing, incoming)
	require.Len(t, result, 1)
	assert.Equal(t, analysisdomain.TechnologyStatusCurrent, result[0].GetStatus())
}

// ── field preservation (existing non-empty → incoming does NOT overwrite) ─────

func TestMerge_PreservesExistingDetectedVersion(t *testing.T) {
	t.Parallel()
	existing := []*analysisdomain.Technology{langWithVersion("Java", "11")}
	incoming := []*analysisdomain.Technology{langWithVersion("Java", "17")} // different version

	result := application.MergeTechnologies(existing, incoming)
	require.Len(t, result, 1)
	assert.Equal(t, "11", result[0].GetDetectedVersion(), "existing version must not be overwritten")
}

func TestMerge_PreservesExistingNonUnspecifiedStatus(t *testing.T) {
	t.Parallel()
	existing := []*analysisdomain.Technology{
		{Name: "PHP", Category: "language", Status: analysisdomain.TechnologyStatusCurrent},
	}
	incoming := []*analysisdomain.Technology{
		{Name: "PHP", Category: "language", Status: analysisdomain.TechnologyStatusOutdated},
	}

	result := application.MergeTechnologies(existing, incoming)
	require.Len(t, result, 1)
	assert.Equal(t, analysisdomain.TechnologyStatusCurrent, result[0].GetStatus(),
		"existing status must not be overwritten")
}

// ── no duplicates ─────────────────────────────────────────────────────────────

func TestMerge_SameKeyTwice_NoDuplicate(t *testing.T) {
	t.Parallel()
	existing := []*analysisdomain.Technology{lang("Java")}
	incoming := []*analysisdomain.Technology{lang("Java"), lang("Java")}

	result := application.MergeTechnologies(existing, incoming)
	assert.Len(t, result, 1, "duplicate key must not produce a duplicate entry")
}

// ── does not mutate inputs ────────────────────────────────────────────────────

func TestMerge_DoesNotMutateExisting(t *testing.T) {
	t.Parallel()
	original := &analysisdomain.Technology{Name: "Java", Category: "language"}
	existing := []*analysisdomain.Technology{original}
	incoming := []*analysisdomain.Technology{langWithVersion("Java", "17")}

	application.MergeTechnologies(existing, incoming)

	assert.Equal(t, "", original.GetDetectedVersion(), "original entry must not be mutated")
}

// ── integration: inventory then manifest ─────────────────────────────────────

// TestMerge_InventoryPlusManifest is the scenario the task document specifies:
// writing inventory languages and then a simulated manifest dependency list
// must produce a technologies slice containing both, without duplicates and
// without losing any entry written by a previous stage.
func TestMerge_InventoryPlusManifest(t *testing.T) {
	t.Parallel()

	// Stage 2 — inventory (go-enry output, category = "language", no version yet).
	inventory := []*analysisdomain.Technology{
		lang("Java"),
		lang("PHP"),
	}

	// Stage 3 — manifest (Composer + Maven output, with detected_version set).
	// A manifest parser may also emit a language entry (e.g. from <java.version>
	// in pom.xml) — the merge must not create a duplicate.
	manifests := []*analysisdomain.Technology{
		{Name: "Java", Category: "language", DetectedVersion: "17"}, // overlaps inventory
		fw("Spring Boot", "3.2.0"),
		lib("symfony/console", "7.1.0"),
	}

	after1 := application.MergeTechnologies(nil, inventory)
	after2 := application.MergeTechnologies(after1, manifests)

	// Expected: Java/language, PHP/language, Spring Boot/framework, symfony/console/library.
	require.Len(t, after2, 4, "inventory + manifest must yield 4 distinct entries")

	m := byKey(after2)
	assert.Contains(t, m, "Java/language", "Java from inventory must be present")
	assert.Contains(t, m, "PHP/language", "PHP from inventory must not be lost")
	assert.Contains(t, m, "Spring Boot/framework", "Spring Boot from manifest must be appended")
	assert.Contains(t, m, "symfony/console/library", "library from manifest must be appended")

	// Manifest filled the detected_version that inventory left empty.
	assert.Equal(t, "17", m["Java/language"].GetDetectedVersion(),
		"detected_version must be filled from manifest")

	// PHP has no manifest entry — its entry must survive unchanged.
	assert.Equal(t, "", m["PHP/language"].GetDetectedVersion(),
		"PHP detected_version must remain empty (no manifest entry)")
}

// TestMerge_ThreeStages verifies that chaining three merges keeps all entries
// across all stages — simulating inventory → manifest → version-currency.
func TestMerge_ThreeStages(t *testing.T) {
	t.Parallel()

	stage2 := []*analysisdomain.Technology{lang("Python")}
	stage3 := []*analysisdomain.Technology{lib("Django", "4.2.0")}
	stage4 := []*analysisdomain.Technology{
		// Version resolver fills latest + status on existing entries.
		{Name: "Python", Category: "language", LatestVersion: "3.13", Status: analysisdomain.TechnologyStatusOutdated},
	}

	result := application.MergeTechnologies(nil, stage2)
	result = application.MergeTechnologies(result, stage3)
	result = application.MergeTechnologies(result, stage4)

	require.Len(t, result, 2)
	m := byKey(result)

	python := m["Python/language"]
	require.NotNil(t, python)
	assert.Equal(t, "3.13", python.GetLatestVersion())
	assert.Equal(t, analysisdomain.TechnologyStatusOutdated, python.GetStatus())

	assert.Contains(t, m, "Django/library", "library from stage 3 must survive through stage 4")
}
