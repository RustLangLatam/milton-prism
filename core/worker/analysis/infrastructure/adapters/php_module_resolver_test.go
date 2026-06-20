package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeComposerJSON writes a minimal composer.json with the given PSR-4 map.
func writeComposerJSON(t *testing.T, dir string, psr4 map[string]string) {
	t.Helper()
	payload := map[string]interface{}{
		"autoload": map[string]interface{}{
			"psr-4": psr4,
		},
	}
	raw, _ := json.Marshal(payload)
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- IsInternal ---

func TestPHPResolver_IsInternal(t *testing.T) {
	dir := t.TempDir()
	writeComposerJSON(t, dir, map[string]string{
		`BookStack\`:          "app/",
		`Database\Factories\`: "database/factories/",
		`Database\Seeders\`:   "database/seeders/",
	})

	r, err := NewPHPModuleResolver(dir)
	if err != nil {
		t.Fatalf("NewPHPModuleResolver: %v", err)
	}

	cases := []struct {
		fqn      string
		internal bool
	}{
		{`BookStack\Entities\Controllers\BookController`, true},
		{`BookStack\Services\AttachmentService`, true},
		{`Database\Factories\UserFactory`, true},
		{`Database\Seeders\DatabaseSeeder`, true},
		{`Illuminate\Http\Request`, false},
		{`Symfony\Component\HttpFoundation\Request`, false},
		{`Exception`, false},
		{`Carbon\Carbon`, false},
	}

	for _, tc := range cases {
		got := r.IsInternal(tc.fqn)
		if got != tc.internal {
			t.Errorf("IsInternal(%q): got %v, want %v", tc.fqn, got, tc.internal)
		}
	}
}

// --- BuildGraphEdges: basic case ---

func TestPHPResolver_BuildGraphEdges_Basic(t *testing.T) {
	dir := t.TempDir()
	writeComposerJSON(t, dir, map[string]string{
		`App\`: "app/",
	})

	r, err := NewPHPModuleResolver(dir)
	if err != nil {
		t.Fatalf("NewPHPModuleResolver: %v", err)
	}

	files := []phpRawFile{
		{
			RelPath: "app/Services/OrderService.php",
			NS:      `App\Services`,
			Class:   "OrderService",
			Uses: []string{
				`App\Repositories\OrderRepository`, // internal
				`Illuminate\Support\Facades\Log`,   // external — must be excluded
			},
		},
		{
			RelPath: "app/Repositories/OrderRepository.php",
			NS:      `App\Repositories`,
			Class:   "OrderRepository",
			Uses: []string{
				`App\Models\Order`, // internal
			},
		},
	}

	edges := r.BuildGraphEdges(files)

	// Expected edges:
	//   App\Services\OrderService → App\Repositories\OrderRepository
	//   App\Repositories\OrderRepository → App\Models\Order
	type edge struct{ from, to string }
	got := make(map[edge]bool)
	for _, e := range edges {
		got[edge{e.FromModule, e.ToModule}] = true
	}

	want := []edge{
		{`App\Services\OrderService`, `App\Repositories\OrderRepository`},
		{`App\Repositories\OrderRepository`, `App\Models\Order`},
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing edge: %q → %q", w.from, w.to)
		}
	}
	// Illuminate edge must NOT be present.
	if got[edge{`App\Services\OrderService`, `Illuminate\Support\Facades\Log`}] {
		t.Error("external edge (Illuminate) must not appear in graph")
	}
}

// --- BuildGraphEdges: deduplication ---

func TestPHPResolver_BuildGraphEdges_Dedup(t *testing.T) {
	dir := t.TempDir()
	writeComposerJSON(t, dir, map[string]string{`App\`: "app/"})

	r, _ := NewPHPModuleResolver(dir)

	files := []phpRawFile{
		{
			NS:    `App\Controllers`,
			Class: "UserController",
			Uses:  []string{`App\Services\UserService`, `App\Services\UserService`},
		},
	}

	edges := r.BuildGraphEdges(files)
	if len(edges) != 1 {
		t.Errorf("expected 1 deduplicated edge, got %d", len(edges))
	}
}

// --- BuildGraphEdges: files without namespace are skipped ---

func TestPHPResolver_BuildGraphEdges_SkipsNoNamespace(t *testing.T) {
	dir := t.TempDir()
	writeComposerJSON(t, dir, map[string]string{`App\`: "app/"})

	r, _ := NewPHPModuleResolver(dir)

	files := []phpRawFile{
		{RelPath: "helpers.php", NS: "", Uses: []string{`App\Services\UserService`}},
		{RelPath: "app/Services/UserService.php", NS: `App\Services`, Class: "UserService"},
	}

	edges := r.BuildGraphEdges(files)
	if len(edges) != 0 {
		t.Errorf("expected 0 edges (no-namespace files skipped, second file has no uses), got %d: %v", len(edges), edges)
	}
}

// --- BuildGraphEdges: self-edges are discarded ---

func TestPHPResolver_BuildGraphEdges_NoSelfEdge(t *testing.T) {
	dir := t.TempDir()
	writeComposerJSON(t, dir, map[string]string{`App\`: "app/"})

	r, _ := NewPHPModuleResolver(dir)

	files := []phpRawFile{
		{
			NS:    `App\Services`,
			Class: "UserService",
			// A file should never use itself, but guard anyway.
			Uses: []string{`App\Services\UserService`},
		},
	}

	edges := r.BuildGraphEdges(files)
	if len(edges) != 0 {
		t.Errorf("expected 0 edges (self-edge), got %d: %v", len(edges), edges)
	}
}

// --- NewPHPModuleResolver: longest prefix wins ---

func TestPHPResolver_LongestPrefixOrder(t *testing.T) {
	dir := t.TempDir()
	writeComposerJSON(t, dir, map[string]string{
		`Database\`:           "database/",
		`Database\Factories\`: "database/factories/",
	})

	r, err := NewPHPModuleResolver(dir)
	if err != nil {
		t.Fatalf("NewPHPModuleResolver: %v", err)
	}

	// Both prefixes should match their respective FQNs.
	if !r.IsInternal(`Database\Factories\UserFactory`) {
		t.Error("Database\\Factories\\UserFactory should be internal")
	}
	if !r.IsInternal(`Database\Seeders\DatabaseSeeder`) {
		t.Error("Database\\Seeders\\DatabaseSeeder should be internal (matched by Database\\)")
	}
	// Verify prefix order: Database\Factories\ must come before Database\.
	if len(r.internalPrefixes) < 2 || len(r.internalPrefixes[0]) <= len(r.internalPrefixes[1]) {
		t.Errorf("prefixes not sorted by descending length: %v", r.internalPrefixes)
	}
}
