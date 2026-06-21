package adapters

import (
	"context"
	"testing"
)

// fixtureCI3 is the reduced CodeIgniter 3 project under testdata. It exercises
// every v1 convention construct plus the documented traps:
//   - Welcome: extends repo base MY_Controller, loads an existing model and an
//     existing library (real edges).
//   - Admin: extends framework base CI_Controller (no edge), and dynamic /
//     subfolder / nonexistent / helper loads (all no edges).
//   - autoload.php: globally loads user_model + session (bootstrap edges).
const fixtureCI3 = "testdata/fixture-ci3"

// TestCI3_ExactEdgeSet is the permanent ground-truth oracle for CodeIgniter 3
// convention resolution. It asserts the EXACT set of edges — neither a fabricated
// edge (trap: extends CI_*, dynamic load, subfolder load, nonexistent target,
// helper/view load) nor a missing real edge (extends repo base, literal
// model/library load, autoload.php global registration).
func TestCI3_ExactEdgeSet(t *testing.T) {
	ex := NewPHPImportExtractor()
	files, err := ex.ExtractFiles(context.Background(), fixtureCI3)
	if err != nil {
		t.Fatalf("ExtractFiles: %v", err)
	}
	if !isCI3Workspace(fixtureCI3) {
		t.Fatalf("isCI3Workspace=false for the CI3 fixture")
	}

	raw, _ := ci3ResolvedEdges(files, fixtureCI3)
	type edge struct{ from, to string }
	got := make(map[edge]bool, len(raw))
	for _, e := range raw {
		got[edge{e.FromModule, e.ToModule}] = true
	}

	const (
		welcome = "application/controllers/Welcome.php"
		mybase  = "application/core/MY_Controller.php"
		userMdl = "application/models/User_model.php"
		formLib = "application/libraries/Form_validation.php"
		session = "application/libraries/Session.php"
		boot    = "application/config/autoload.php"
	)
	want := map[edge]bool{
		{welcome, mybase}:  true, // extends MY_Controller (repo base)
		{welcome, userMdl}: true, // load->model('user_model')
		{welcome, formLib}: true, // load->library('form_validation')
		{boot, userMdl}:    true, // autoload['model'] = ['user_model']
		{boot, session}:    true, // autoload['libraries'] = ['session']
	}

	for w := range want {
		if !got[w] {
			t.Errorf("MISSING real edge (under-resolution): %q -> %q", w.from, w.to)
		}
	}
	for g := range got {
		if !want[g] {
			t.Errorf("UNEXPECTED edge (over-resolution): %q -> %q", g.from, g.to)
		}
	}
	if len(got) != len(want) {
		t.Errorf("edge count: got %d, want %d", len(got), len(want))
	}
}

// TestCI3_ExactCardsWithFile asserts the exact set of module cards (one per file
// under application/{controllers,models,libraries,core}), each carrying its File.
// The config/ bootstrap node is an edge source only and must NOT be a card.
func TestCI3_ExactCardsWithFile(t *testing.T) {
	a := NewPHPLanguageAnalyzer()
	cards, _, err := a.ExtractCards(context.Background(), fixtureCI3)
	if err != nil {
		t.Fatalf("ExtractCards: %v", err)
	}

	want := map[string]bool{
		"application/controllers/Welcome.php":       true,
		"application/controllers/Admin.php":         true,
		"application/core/MY_Controller.php":        true,
		"application/models/User_model.php":         true,
		"application/libraries/Form_validation.php": true,
		"application/libraries/Session.php":         true,
	}
	got := make(map[string]bool, len(cards))
	for _, c := range cards {
		got[c.GetModule()] = true
		if c.GetFile() == "" {
			t.Errorf("card %q has empty File", c.GetModule())
		}
		if c.GetFile() != c.GetModule() {
			t.Errorf("CI3 card File must equal Module (path identity): module=%q file=%q",
				c.GetModule(), c.GetFile())
		}
	}
	for w := range want {
		if !got[w] {
			t.Errorf("MISSING card: %q", w)
		}
	}
	for g := range got {
		if !want[g] {
			t.Errorf("UNEXPECTED card (non-module file emitted): %q", g)
		}
	}
}

// TestCI3_PSR4Untouched guards Fork A's isolation: a workspace that declares a
// PSR-4 autoload map is NOT a CI3 workspace even if it happens to carry CI3-like
// markers, so BookStack/Laravel keep using the PSR-4 path unchanged.
func TestCI3_PSR4Untouched(t *testing.T) {
	dir := t.TempDir()
	writeComposerJSON(t, dir, map[string]string{`App\`: "app/"})
	if isCI3Workspace(dir) {
		t.Fatalf("PSR-4 project misclassified as CI3")
	}
}
