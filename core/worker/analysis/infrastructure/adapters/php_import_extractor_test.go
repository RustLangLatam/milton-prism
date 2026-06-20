package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/php"
)

// parsePHP is a test helper that parses inline PHP source and returns the root node.
func parsePHP(t *testing.T, src string) (*sitter.Node, []byte) {
	t.Helper()
	srcBytes := []byte(src)
	parser := sitter.NewParser()
	parser.SetLanguage(php.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, srcBytes)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return tree.RootNode(), srcBytes
}

// --- namespace_definition ---

func TestPHPExtract_Namespace(t *testing.T) {
	src := `<?php
namespace BookStack\Entities\Controllers;
class BookController {}
`
	root, srcBytes := parsePHP(t, src)
	f := extractPHPFile(root, srcBytes, "app/Entities/Controllers/BookController.php")

	if f.NS != `BookStack\Entities\Controllers` {
		t.Errorf("namespace: got %q, want %q", f.NS, `BookStack\Entities\Controllers`)
	}
	if f.Class != "BookController" {
		t.Errorf("class: got %q, want %q", f.Class, "BookController")
	}
	if f.Kind != "class" {
		t.Errorf("kind: got %q, want %q", f.Kind, "class")
	}
}

// --- standard use declarations ---

func TestPHPExtract_StandardUse(t *testing.T) {
	src := `<?php
namespace BookStack\Entities\Controllers;

use BookStack\Activity\ActivityQueries;
use BookStack\Entities\Repos\BookRepo;
use Illuminate\Http\Request;

class BookController {}
`
	root, srcBytes := parsePHP(t, src)
	f := extractPHPFile(root, srcBytes, "app/Entities/Controllers/BookController.php")

	want := []string{
		`BookStack\Activity\ActivityQueries`,
		`BookStack\Entities\Repos\BookRepo`,
		`Illuminate\Http\Request`,
	}
	if len(f.Uses) != len(want) {
		t.Fatalf("uses count: got %d, want %d\n  got: %v", len(f.Uses), len(want), f.Uses)
	}
	for i, w := range want {
		if f.Uses[i] != w {
			t.Errorf("uses[%d]: got %q, want %q", i, f.Uses[i], w)
		}
	}
}

// --- aliased use: alias is discarded, canonical FQN is kept ---

func TestPHPExtract_AliasedUse(t *testing.T) {
	src := `<?php
namespace App\Controllers;

use App\Services\UserService as US;
use App\Repositories\UserRepository as Repo;

class UserController {}
`
	root, srcBytes := parsePHP(t, src)
	f := extractPHPFile(root, srcBytes, "app/Controllers/UserController.php")

	want := []string{
		`App\Services\UserService`,
		`App\Repositories\UserRepository`,
	}
	if len(f.Uses) != len(want) {
		t.Fatalf("uses count: got %d, want %d\n  got: %v", len(f.Uses), len(want), f.Uses)
	}
	for i, w := range want {
		if f.Uses[i] != w {
			t.Errorf("uses[%d]: got %q, want %q", i, f.Uses[i], w)
		}
	}
}

// --- grouped use declarations ---

func TestPHPExtract_GroupedUse(t *testing.T) {
	src := `<?php
namespace App\Http\Controllers;

use App\Models\{User, Post, Comment};
use App\Services\{UserService, PostService};

class HomeController {}
`
	root, srcBytes := parsePHP(t, src)
	f := extractPHPFile(root, srcBytes, "app/Http/Controllers/HomeController.php")

	want := []string{
		`App\Models\User`,
		`App\Models\Post`,
		`App\Models\Comment`,
		`App\Services\UserService`,
		`App\Services\PostService`,
	}
	if len(f.Uses) != len(want) {
		t.Fatalf("uses count: got %d, want %d\n  got: %v", len(f.Uses), len(want), f.Uses)
	}
	for i, w := range want {
		if f.Uses[i] != w {
			t.Errorf("uses[%d]: got %q, want %q", i, f.Uses[i], w)
		}
	}
}

// --- use function / use const are ignored ---

func TestPHPExtract_UseFunctionAndConstSkipped(t *testing.T) {
	src := `<?php
namespace App\Helpers;

use function App\Support\format_date;
use const App\Config\MAX_RETRY;
use App\Services\UserService;

class Helper {}
`
	root, srcBytes := parsePHP(t, src)
	f := extractPHPFile(root, srcBytes, "app/Helpers/Helper.php")

	// Only the class use must appear, not the function or const ones.
	if len(f.Uses) != 1 || f.Uses[0] != `App\Services\UserService` {
		t.Errorf("uses: got %v, want [App\\Services\\UserService]", f.Uses)
	}
}

// --- interface and trait declarations ---

func TestPHPExtract_InterfaceAndTrait(t *testing.T) {
	srcIface := `<?php
namespace App\Contracts;
interface Listable {}
`
	root, srcBytes := parsePHP(t, srcIface)
	f := extractPHPFile(root, srcBytes, "app/Contracts/Listable.php")
	if f.Class != "Listable" || f.Kind != "interface" {
		t.Errorf("interface: got class=%q kind=%q", f.Class, f.Kind)
	}

	srcTrait := `<?php
namespace App\Traits;
trait HasAudit {}
`
	root, srcBytes = parsePHP(t, srcTrait)
	f = extractPHPFile(root, srcBytes, "app/Traits/HasAudit.php")
	if f.Class != "HasAudit" || f.Kind != "trait" {
		t.Errorf("trait: got class=%q kind=%q", f.Class, f.Kind)
	}
}

// --- class members: methods and properties ---

func TestPHPExtract_ClassMembers(t *testing.T) {
	src := `<?php
namespace App\Services;

class UserService {
    protected UserRepository $userRepo;
    private int $maxRetry;

    public function findById(int $id): ?User { return null; }
    public function createUser(array $data): User { return new User(); }
}
`
	root, srcBytes := parsePHP(t, src)
	f := extractPHPFile(root, srcBytes, "app/Services/UserService.php")

	wantMethods := []string{"findById", "createUser"}
	if len(f.Methods) != len(wantMethods) {
		t.Fatalf("methods count: got %d, want %d\n  got: %v", len(f.Methods), len(wantMethods), f.Methods)
	}
	for i, w := range wantMethods {
		if f.Methods[i] != w {
			t.Errorf("methods[%d]: got %q, want %q", i, f.Methods[i], w)
		}
	}

	wantProps := []string{"userRepo", "maxRetry"}
	if len(f.Props) != len(wantProps) {
		t.Fatalf("props count: got %d, want %d\n  got: %v", len(f.Props), len(wantProps), f.Props)
	}
	for i, w := range wantProps {
		if f.Props[i] != w {
			t.Errorf("props[%d]: got %q, want %q", i, f.Props[i], w)
		}
	}
	// Instance properties are not static.
	if len(f.StaticProps) != 0 {
		t.Errorf("static props: expected none, got %v", f.StaticProps)
	}
}

// --- static properties are detected as state signals ---

func TestPHPExtract_StaticProps(t *testing.T) {
	src := `<?php
namespace App\Singletons;

class Registry {
    protected static ?self $instance = null;
    private static array $bindings = [];
    public string $name;

    public function getInstance(): static { return self::$instance; }
}
`
	root, srcBytes := parsePHP(t, src)
	f := extractPHPFile(root, srcBytes, "app/Singletons/Registry.php")

	// All props (static and instance).
	if len(f.Props) != 3 {
		t.Fatalf("props count: got %d, want 3\n  got: %v", len(f.Props), f.Props)
	}
	// Only the two static ones appear in StaticProps.
	wantStatic := []string{"instance", "bindings"}
	if len(f.StaticProps) != len(wantStatic) {
		t.Fatalf("static props count: got %d, want %d\n  got: %v", len(f.StaticProps), len(wantStatic), f.StaticProps)
	}
	for i, w := range wantStatic {
		if f.StaticProps[i] != w {
			t.Errorf("static props[%d]: got %q, want %q", i, f.StaticProps[i], w)
		}
	}
}

// --- file without namespace: NS stays empty ---

func TestPHPExtract_NoNamespace(t *testing.T) {
	src := `<?php
function helper() { return true; }
`
	root, srcBytes := parsePHP(t, src)
	f := extractPHPFile(root, srcBytes, "helpers.php")
	if f.NS != "" {
		t.Errorf("expected empty namespace, got %q", f.NS)
	}
}

// --- ExtractFiles: integration with real filesystem ---

func TestPHPExtractFiles_WalksAndParses(t *testing.T) {
	dir := t.TempDir()

	writeFile := func(relPath, content string) {
		full := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("app/Services/OrderService.php", `<?php
namespace App\Services;
use App\Repositories\OrderRepository;
use Illuminate\Support\Facades\Log;
class OrderService {
    public function createOrder(): void {}
}
`)
	writeFile("app/Repositories/OrderRepository.php", `<?php
namespace App\Repositories;
use App\Models\Order;
class OrderRepository {}
`)
	// vendor file — must be skipped
	writeFile("vendor/illuminate/support/helpers.php", `<?php
function array_add() {}
`)

	extractor := NewPHPImportExtractor()
	files, err := extractor.ExtractFiles(context.Background(), dir)
	if err != nil {
		t.Fatalf("ExtractFiles: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files (vendor skipped), got %d", len(files))
	}

	// Build a map for stable lookup.
	byRel := make(map[string]phpRawFile)
	for _, f := range files {
		byRel[filepath.ToSlash(f.RelPath)] = f
	}

	svc, ok := byRel["app/Services/OrderService.php"]
	if !ok {
		t.Fatal("OrderService.php not found in results")
	}
	if svc.NS != `App\Services` {
		t.Errorf("NS: got %q, want %q", svc.NS, `App\Services`)
	}
	if svc.Class != "OrderService" {
		t.Errorf("Class: got %q, want %q", svc.Class, "OrderService")
	}
	if len(svc.Uses) != 2 {
		t.Errorf("uses: got %v, want 2 entries", svc.Uses)
	}
}
