package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestPHP_AllTiers_ExactEdgeSet is the permanent ground-truth oracle for PHP
// reference resolution across all three tiers:
//   - Tier A: type-hints, new, ::method/::CONST/::class (same-namespace + alias).
//   - Tier B: extends / implements.
//   - Tier C: trait use in the class body.
//
// It writes a small PHP project exercising every construct plus every
// over-resolution trap, then asserts the EXACT edge set the resolver must produce
// — neither more nor fewer edges. A false edge (over-resolution: built-in,
// vendor, dynamic, self::, undefined name, nonexistent trait) or a missing real
// edge (under-resolution) fails the test.
func TestPHP_AllTiers_ExactEdgeSet(t *testing.T) {
	dir := t.TempDir()
	writeComposerJSON(t, dir, map[string]string{`App\`: "app/"})

	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Unit 1 — Tier A constructs + extends/implements/trait (B/C) + Tier A traps.
	write("app/Foo/Service.php", `<?php
namespace App\Foo;

use App\Bar\Helper;          // alias Helper → App\Bar\Helper
use App\Bar\Thing as T;      // explicit alias T → App\Bar\Thing
use Illuminate\Support\Str;  // vendor (external) — must never edge

class Service extends Base implements Contract  // Tier B → edges to Base, Contract
{
    use Mixin;                               // Tier C → edge to Mixin

    private ValueObject $vo;                 // same-ns property type-hint → edge
    protected static string $r = Registry::class; // same-ns ::class → edge

    public function run(Helper $h, ?T $t): Result // alias params + same-ns return → edges
    {
        $a = new Maker();                    // same-ns new → edge
        $s = Status::ACTIVE;                  // same-ns enum ::CASE → edge
        $b = BookSortMap::fromJson($t);      // same-ns static call → edge

        $e = new \Exception("x");            // built-in (leading \) → NO edge
        $z = new $dynamic();                 // dynamic new → NO edge
        $p = new Phantom();                  // same-ns but undefined → NO edge
        $l = Str::lower($a);                 // vendor via alias → NO edge
        return self::cached();               // self:: → NO edge
    }
}
`)

	// Unit 2 — Tier B/C traps: extends built-in, implements one internal + one
	// built-in, use one real trait + one nonexistent.
	write("app/Foo/Edge.php", `<?php
namespace App\Foo;

class Edge extends \Exception implements \JsonSerializable, Contract // builtin extends/impl → no edge; Contract → edge
{
    use Ghost;   // nonexistent trait → NO edge
    use Mixin;   // real trait → edge
}
`)

	// Defined modules referenced above (give the existence gate something to hit).
	write("app/Bar/Helper.php", "<?php\nnamespace App\\Bar;\nclass Helper {}\n")
	write("app/Bar/Thing.php", "<?php\nnamespace App\\Bar;\nclass Thing {}\n")
	write("app/Foo/ValueObject.php", "<?php\nnamespace App\\Foo;\nclass ValueObject {}\n")
	write("app/Foo/Result.php", "<?php\nnamespace App\\Foo;\nclass Result {}\n")
	write("app/Foo/Maker.php", "<?php\nnamespace App\\Foo;\nclass Maker {}\n")
	write("app/Foo/Registry.php", "<?php\nnamespace App\\Foo;\nclass Registry {}\n")
	write("app/Foo/Status.php", "<?php\nnamespace App\\Foo;\nenum Status { case ACTIVE; }\n")
	write("app/Foo/BookSortMap.php", "<?php\nnamespace App\\Foo;\nclass BookSortMap {}\n")
	write("app/Foo/Base.php", "<?php\nnamespace App\\Foo;\nclass Base {}\n")
	write("app/Foo/Contract.php", "<?php\nnamespace App\\Foo;\ninterface Contract {}\n")
	write("app/Foo/Mixin.php", "<?php\nnamespace App\\Foo;\ntrait Mixin {}\n")

	ex := NewPHPImportExtractor()
	files, err := ex.ExtractFiles(context.Background(), dir)
	if err != nil {
		t.Fatalf("ExtractFiles: %v", err)
	}
	r, err := NewPHPModuleResolver(dir)
	if err != nil {
		t.Fatalf("NewPHPModuleResolver: %v", err)
	}
	edges := r.BuildGraphEdges(files)

	type edge struct{ from, to string }
	got := make(map[edge]bool)
	for _, e := range edges {
		got[edge{e.FromModule, e.ToModule}] = true
	}

	const svc = `App\Foo\Service`
	const edg = `App\Foo\Edge`
	want := map[edge]bool{
		// Tier A (Service)
		{svc, `App\Foo\ValueObject`}: true, // property type-hint (same-ns)
		{svc, `App\Foo\Registry`}:    true, // ::class (same-ns)
		{svc, `App\Bar\Helper`}:      true, // alias param (also a use-edge)
		{svc, `App\Bar\Thing`}:       true, // explicit alias param (also a use-edge)
		{svc, `App\Foo\Result`}:      true, // return type-hint (same-ns)
		{svc, `App\Foo\Maker`}:       true, // new (same-ns)
		{svc, `App\Foo\Status`}:      true, // enum ::CASE (same-ns)
		{svc, `App\Foo\BookSortMap`}: true, // static call (same-ns)
		// Tier B (Service)
		{svc, `App\Foo\Base`}:     true, // extends
		{svc, `App\Foo\Contract`}: true, // implements
		// Tier C (Service)
		{svc, `App\Foo\Mixin`}: true, // trait use
		// Tier B/C (Edge): only the internal interface + the real trait
		{edg, `App\Foo\Contract`}: true, // implements (internal); \JsonSerializable/\Exception → no edge
		{edg, `App\Foo\Mixin`}:    true, // real trait; Ghost → no edge
	}

	for w := range want {
		if !got[w] {
			t.Errorf("MISSING real edge (under-resolution): %q → %q", w.from, w.to)
		}
	}
	// Every produced edge must be expected — catches over-resolution (false edges):
	// built-ins (\Exception, \JsonSerializable), vendor (Str), dynamic (new $x),
	// self::, undefined same-ns names (Phantom), nonexistent traits (Ghost).
	for g := range got {
		if !want[g] {
			t.Errorf("UNEXPECTED edge (over-resolution): %q → %q", g.from, g.to)
		}
	}
	if len(got) != len(want) {
		t.Errorf("edge count: got %d, want %d", len(got), len(want))
	}
}
