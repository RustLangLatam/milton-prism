package application

import (
	"testing"

	analysisdomain "milton_prism/core/services/analysis/domain"
)

// frameworkNames returns the names of all category="framework" technologies.
func frameworkNames(techs []*analysisdomain.Technology) []string {
	var out []string
	for _, t := range techs {
		if t.GetCategory() == "framework" {
			out = append(out, t.GetName())
		}
	}
	return out
}

func hasFramework(techs []*analysisdomain.Technology, name string) bool {
	for _, n := range frameworkNames(techs) {
		if n == name {
			return true
		}
	}
	return false
}

func langTech(name string) *analysisdomain.Technology {
	return &analysisdomain.Technology{Name: name, Category: "language"}
}

func blueprint(name, file string) *analysisdomain.BlueprintInfo {
	return &analysisdomain.BlueprintInfo{Name: name, File: file}
}

// TestInjectInferredFramework_FlaskGatedToPrimaryPython is the regression test for
// the "GO·Flask" bug. The BlueprintInfo type is shared across language analyzers
// (the Java/C# analyzers emit one per Spring/ASP.NET controller), and a polyglot
// repo can detect Python without being a Python app, so injectInferredFramework
// must only infer Flask when Python is the PRIMARY language — not merely present.
func TestInjectInferredFramework_FlaskGatedToPrimaryPython(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		techs      []*analysisdomain.Technology
		blueprints []*analysisdomain.BlueprintInfo
		primary    string
		wantFlask  bool
	}{
		{
			// Direction A: a genuine Flask (Python-primary) project still detects Flask.
			name:       "python primary blueprints detect flask",
			techs:      []*analysisdomain.Technology{langTech("Python")},
			blueprints: []*analysisdomain.BlueprintInfo{blueprint("auth", "app/auth/views.py")},
			primary:    "Python",
			wantFlask:  true,
		},
		{
			// Direction B (the bug): a Spring (Java-primary) repo whose controllers
			// were modeled as blueprints must NOT be reported as Flask.
			name:       "java primary spring blueprints do not detect flask",
			techs:      []*analysisdomain.Technology{langTech("Java")},
			blueprints: []*analysisdomain.BlueprintInfo{blueprint("UserController", "src/main/java/io/example/UserController.java")},
			primary:    "Java",
			wantFlask:  false,
		},
		{
			// Direction B (the bug): an ASP.NET Core (C#-primary) repo's controllers
			// must NOT be reported as Flask either.
			name:       "csharp primary aspnet blueprints do not detect flask",
			techs:      []*analysisdomain.Technology{langTech("C#")},
			blueprints: []*analysisdomain.BlueprintInfo{blueprint("ArticlesController", "src/Conduit/Features/Articles/ArticlesController.cs")},
			primary:    "C#",
			wantFlask:  false,
		},
		{
			// The exact "GO·Flask" repro: a Go-primary repo that also carries some
			// Python (with blueprints) must NOT be labelled Flask.
			name:       "go primary with python present does not detect flask",
			techs:      []*analysisdomain.Technology{langTech("Go"), langTech("Python")},
			blueprints: []*analysisdomain.BlueprintInfo{blueprint("api", "tools/scraper/api/__init__.py")},
			primary:    "Go",
			wantFlask:  false,
		},
		{
			// No blueprints at all: nothing is inferred regardless of language.
			name:       "no blueprints no inference",
			techs:      []*analysisdomain.Technology{langTech("Python")},
			blueprints: nil,
			primary:    "Python",
			wantFlask:  false,
		},
		{
			// Python-primary with a secondary frontend language: still Flask. The
			// gate is on PRIMARY, and Python is primary here.
			name:       "python primary with js secondary detect flask",
			techs:      []*analysisdomain.Technology{langTech("JavaScript"), langTech("Python")},
			blueprints: []*analysisdomain.BlueprintInfo{blueprint("api", "backend/api/__init__.py")},
			primary:    "Python",
			wantFlask:  true,
		},
		{
			// Case-insensitive primary match (go-enry casing variations).
			name:       "lowercase python primary detect flask",
			techs:      []*analysisdomain.Technology{langTech("Python")},
			blueprints: []*analysisdomain.BlueprintInfo{blueprint("auth", "app/auth/views.py")},
			primary:    "python",
			wantFlask:  true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := injectInferredFramework(tc.techs, tc.blueprints, tc.primary)
			if hasFramework(got, "Flask") != tc.wantFlask {
				t.Fatalf("injectInferredFramework Flask present = %v, want %v (frameworks=%v)",
					hasFramework(got, "Flask"), tc.wantFlask, frameworkNames(got))
			}
		})
	}
}

// TestInjectInferredFramework_ManifestWins verifies that a framework already
// present from manifest parsing (e.g. Spring from a Maven groupID) is never
// overwritten or supplemented by the blueprint-based Flask inference.
func TestInjectInferredFramework_ManifestWins(t *testing.T) {
	t.Parallel()

	techs := []*analysisdomain.Technology{
		langTech("Java"),
		{Name: "Spring", Category: "framework", Slug: "spring"},
	}
	// Even with Python somehow present and blueprints, an existing manifest
	// framework short-circuits the inference entirely.
	techsWithPython := append(append([]*analysisdomain.Technology{}, techs...), langTech("Python"))
	bps := []*analysisdomain.BlueprintInfo{blueprint("UserController", "UserController.java")}

	got := injectInferredFramework(techsWithPython, bps, "Java")
	if hasFramework(got, "Flask") {
		t.Fatalf("manifest framework present but Flask was injected anyway: %v", frameworkNames(got))
	}
	if !hasFramework(got, "Spring") {
		t.Fatalf("expected Spring to remain, got frameworks=%v", frameworkNames(got))
	}
}
