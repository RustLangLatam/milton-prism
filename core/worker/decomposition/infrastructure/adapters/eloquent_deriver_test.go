package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	workerdomain "milton_prism/core/worker/decomposition/domain"
)

// --- reduced Laravel fixture (with deliberate traps) ---

const fixtureComposerJSON = `{
    "name": "acme/blog",
    "require": { "laravel/framework": "^11.0" },
    "autoload": {
        "psr-4": { "Acme\\": "app/" }
    }
}`

// bookModel exercises the happy path: $table, $fillable, $casts, a hasMany
// relation to a resolvable model (Page), a belongsTo FK column, and a @property
// docblock as the column source.
const bookModel = `<?php

namespace Acme\Books\Models;

use Acme\Books\Models\Page;
use Acme\Users\Models\User;
use Illuminate\Database\Eloquent\Relations\HasMany;
use Illuminate\Database\Eloquent\Relations\BelongsTo;

/**
 * @property int        $id
 * @property string     $name
 * @property string     $description
 * @property int        $author_id
 * @property bool       $published
 * @property Carbon     $created_at
 * @property Carbon     $updated_at
 * @property Collection $pages
 * @property User       $author
 */
class Book extends Model
{
    protected $table = 'books';
    protected $fillable = ['name', 'description'];
    protected $casts = ['published' => 'boolean'];

    public function pages(): HasMany
    {
        return $this->hasMany(Page::class);
    }

    public function author(): BelongsTo
    {
        return $this->belongsTo(User::class, 'author_id');
    }
}
`

// pageModel has no $table (convention → "pages") and no docblock; its columns
// come entirely from the migration. It also carries a TRAP: a relation to a
// model class that does not exist anywhere in the workspace (GhostModel).
const pageModel = `<?php

namespace Acme\Books\Models;

use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Illuminate\Database\Eloquent\Relations\HasMany;

class Page extends Model
{
    protected $fillable = ['name'];

    public function book(): BelongsTo
    {
        return $this->belongsTo(Book::class);
    }

    // TRAP: target model does not exist in the workspace.
    public function ghosts(): HasMany
    {
        return $this->hasMany(GhostModel::class);
    }
}
`

// userModel lives in another service (users); it makes the Book.author
// belongsTo relation resolvable and gives author_id a real target table.
const userModel = `<?php

namespace Acme\Users\Models;

/**
 * @property int    $id
 * @property string $name
 * @property string $email
 */
class User extends Model
{
    protected $table = 'users';
    protected $fillable = ['name', 'email'];
}
`

const pageMigration = `<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class extends Migration
{
    public function up(): void
    {
        Schema::create('pages', function (Blueprint $table) {
            $table->increments('id');
            $table->integer('book_id');
            $table->string('name');
            $table->longText('html');
            $table->boolean('draft');
            $table->nullableTimestamps();
        });
    }
};
`

// fixtureApiRoutes carries CRUD resource routes for Books plus two TRAPS:
// a non-CRUD custom action (export) and a non-CRUD nested sub-resource.
const fixtureApiRoutes = `<?php

use Acme\Books\Controllers as BookControllers;
use Illuminate\Support\Facades\Route;

Route::get('books', [BookControllers\BookApiController::class, 'list']);
Route::post('books', [BookControllers\BookApiController::class, 'create']);
Route::get('books/{id}', [BookControllers\BookApiController::class, 'read']);
Route::put('books/{id}', [BookControllers\BookApiController::class, 'update']);
Route::delete('books/{id}', [BookControllers\BookApiController::class, 'delete']);

// TRAP: non-CRUD custom action — must be TODO, not invented.
Route::get('books/{id}/export', [BookControllers\BookApiController::class, 'export']);
// TRAP: nested sub-resource — must be TODO.
Route::post('books/{id}/chapters', [BookControllers\BookApiController::class, 'addChapter']);
`

// writePHPWorkspace writes the reduced Laravel fixture into a temp workspace.
func writePHPWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for relPath, content := range files {
		full := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return dir
}

// bookStackTableMap maps fixture table names to owning services so cross-service
// FK resolution can be asserted (author_id → users via the users table).
var fixtureTableMap = map[string]string{
	"books": "books",
	"pages": "books",
	"users": "users",
}

func deriveFixture(t *testing.T) *workerdomain.DerivedContract {
	t.Helper()
	workspace := writePHPWorkspace(t, map[string]string{
		"composer.json":             fixtureComposerJSON,
		"app/Books/Models/Book.php": bookModel,
		"app/Books/Models/Page.php": pageModel,
		"app/Users/Models/User.php": userModel,
		"database/migrations/2020_01_01_000000_create_pages_table.php": pageMigration,
		"routes/api.php": fixtureApiRoutes,
	})
	cluster := workerdomain.Cluster{
		BlueprintGroup: `Acme\Books`,
		Modules: []workerdomain.Module{
			`Acme\Books\Models\Book`,
			`Acme\Books\Models\Page`,
		},
	}
	d := NewEloquentDeriver()
	contract, err := d.Derive(context.Background(), cluster, workspace, fixtureTableMap)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return contract
}

// TestEloquentDeriver_IsLaravelWorkspace verifies framework detection on the
// composer.json marker and the artisan marker.
func TestEloquentDeriver_IsLaravelWorkspace(t *testing.T) {
	t.Parallel()
	ws := writePHPWorkspace(t, map[string]string{"composer.json": fixtureComposerJSON})
	if !isLaravelWorkspace(ws) {
		t.Error("composer.json with laravel/framework must be detected as Laravel")
	}

	wsArtisan := writePHPWorkspace(t, map[string]string{"artisan": "#!/usr/bin/env php\n"})
	if !isLaravelWorkspace(wsArtisan) {
		t.Error("artisan marker must be detected as Laravel")
	}

	wsNone := writePHPWorkspace(t, map[string]string{"README.md": "hi"})
	if isLaravelWorkspace(wsNone) {
		t.Error("plain workspace must not be detected as Laravel")
	}
}

// TestEloquentDeriver_BookMessage is the primary acceptance test: the Book
// message derives from @property + $casts with AIP-compliant typed fields and a
// resolved cross-service FK.
func TestEloquentDeriver_BookMessage(t *testing.T) {
	t.Parallel()
	contract := deriveFixture(t)

	if contract.Incomplete {
		t.Fatalf("contract must not be incomplete, reason: %q", contract.IncompleteReason)
	}
	if len(contract.Messages) != 2 {
		t.Fatalf("expected 2 messages (Book, Page), got %d", len(contract.Messages))
	}

	var book *workerdomain.ProtoMessage
	for i := range contract.Messages {
		if contract.Messages[i].Name == "Book" {
			book = &contract.Messages[i]
		}
	}
	if book == nil {
		t.Fatal("Book message not found")
	}

	fields := make(map[string]workerdomain.ProtoField)
	for _, f := range book.Fields {
		fields[f.Name] = f
	}

	// AIP synthetic identifier + state.
	if f, ok := fields["identifier"]; !ok || f.Type != "uint64" || f.Number != 1 {
		t.Errorf("identifier field wrong: %+v ok=%v", f, ok)
	}
	if f, ok := fields["state"]; !ok || f.Number != 2 {
		t.Errorf("state field wrong: %+v ok=%v", f, ok)
	}

	// Scalar columns with exact proto types.
	wantTypes := map[string]string{
		"name":        "string",
		"description": "string",
		"published":   "bool",
		"create_time": "google.protobuf.Timestamp",
		"update_time": "google.protobuf.Timestamp",
	}
	for name, typ := range wantTypes {
		f, ok := fields[name]
		if !ok {
			t.Errorf("Book missing field %q", name)
			continue
		}
		if f.Type != typ {
			t.Errorf("Book.%s type: got %q, want %q", name, f.Type, typ)
		}
	}

	// author_id → author_identifier: integer FK, cross-service to users.
	f, ok := fields["author_identifier"]
	if !ok {
		t.Fatal("Book missing author_identifier (from author_id)")
	}
	if f.Type != "uint64" {
		t.Errorf("author_identifier type: got %q, want uint64", f.Type)
	}
	if !f.IsCrossFK {
		t.Error("author_identifier must be marked cross-service FK")
	}
	if f.RefTable != "users" {
		t.Errorf("author_identifier RefTable: got %q, want users", f.RefTable)
	}
	if f.RefService != "users" {
		t.Errorf("author_identifier RefService: got %q, want users", f.RefService)
	}

	// AIP soft-delete always present.
	for _, want := range []string{"delete_time", "purge_time"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("Book missing AIP soft-delete field %q", want)
		}
	}

	// Relationship author/pages are NOT proto fields.
	for _, bad := range []string{"author", "pages"} {
		if _, ok := fields[bad]; ok {
			t.Errorf("Book should NOT have %q as a proto field (it is a relation)", bad)
		}
	}

	// Resolved relations are annotated without TODO.
	relSet := strings.Join(book.Relationships, "\n")
	if !strings.Contains(relSet, "pages → Page") {
		t.Errorf("Book.Relationships missing 'pages → Page', got: %v", book.Relationships)
	}
	if !strings.Contains(relSet, "author → User") {
		t.Errorf("Book.Relationships missing 'author → User', got: %v", book.Relationships)
	}
	if strings.Contains(relSet, "TODO") {
		t.Errorf("Book relations should all resolve (no TODO), got: %v", book.Relationships)
	}
}

// TestEloquentDeriver_PageFromMigration verifies the Page columns come from the
// migration when the model has no docblock, and that the unresolvable relation
// is flagged TODO (the trap), not invented.
func TestEloquentDeriver_PageFromMigration(t *testing.T) {
	t.Parallel()
	contract := deriveFixture(t)

	var page *workerdomain.ProtoMessage
	for i := range contract.Messages {
		if contract.Messages[i].Name == "Page" {
			page = &contract.Messages[i]
		}
	}
	if page == nil {
		t.Fatal("Page message not found")
	}

	fields := make(map[string]workerdomain.ProtoField)
	for _, f := range page.Fields {
		fields[f.Name] = f
	}

	// Columns sourced from the migration schema.
	if f, ok := fields["name"]; !ok || f.Type != "string" {
		t.Errorf("Page.name from migration wrong: %+v ok=%v", f, ok)
	}
	if f, ok := fields["html"]; !ok || f.Type != "string" {
		t.Errorf("Page.html (longText) from migration wrong: %+v ok=%v", f, ok)
	}
	if f, ok := fields["draft"]; !ok || f.Type != "bool" {
		t.Errorf("Page.draft (boolean) from migration wrong: %+v ok=%v", f, ok)
	}
	// book_id → book_identifier cross-FK (intra-service, ref table books).
	if f, ok := fields["book_identifier"]; !ok || !f.IsCrossFK || f.RefTable != "books" {
		t.Errorf("Page.book_identifier FK wrong: %+v ok=%v", f, ok)
	}

	// TRAP: the relation to a nonexistent model must be a TODO, not a clean rel.
	rels := strings.Join(page.Relationships, "\n")
	if !strings.Contains(rels, "ghosts") || !strings.Contains(rels, "TODO") {
		t.Errorf("Page must flag the unresolvable 'ghosts → GhostModel' relation as TODO, got: %v", page.Relationships)
	}
	// The resolvable book relation stays clean.
	if !strings.Contains(rels, "book → Book") {
		t.Errorf("Page must keep 'book → Book' resolved, got: %v", page.Relationships)
	}
}

// TestEloquentDeriver_Routes verifies CRUD route classification and the two
// non-CRUD traps (export, nested chapters) become TODO entries.
func TestEloquentDeriver_Routes(t *testing.T) {
	t.Parallel()
	contract := deriveFixture(t)

	crud := make(map[string]bool)
	todo := make(map[string]bool)
	for _, rpc := range contract.RPCs {
		if rpc.IsTODO {
			todo[rpc.HTTPMethod+" "+rpc.Path] = true
		} else {
			crud[rpc.Name] = true
		}
	}

	for _, want := range []string{"ListBook", "CreateBook", "GetBook", "UpdateBook", "DeleteBook"} {
		if !crud[want] {
			t.Errorf("expected CRUD rpc %q, got crud=%v", want, crud)
		}
	}

	// TRAPS must be TODO.
	for _, want := range []string{"GET books/{id}/export", "POST books/{id}/chapters"} {
		if !todo[want] {
			t.Errorf("expected non-CRUD route %q to be TODO, got todo=%v", want, todo)
		}
	}
	if !contract.HasTODORoutes {
		t.Error("HasTODORoutes must be true when non-CRUD routes exist")
	}
}

// TestEloquentDeriver_ProtoAIPShape asserts the exact AIP-compliant .proto shape.
func TestEloquentDeriver_ProtoAIPShape(t *testing.T) {
	t.Parallel()
	contract := deriveFixture(t)
	proto := contract.ProtoContent
	t.Logf("\n--- derived proto ---\n%s\n--- end ---", proto)

	checks := []struct{ substr, desc string }{
		{`syntax = "proto3"`, "syntax declaration"},
		{`import "google/protobuf/timestamp.proto"`, "timestamp import"},
		{`package generated.books.v1`, "package"},
		{`message Book {`, "Book message"},
		{`uint64 identifier = 1`, "AIP identifier field 1"},
		{`BookState state = 2`, "AIP state field 2"},
		{`bool published = `, "boolean column from $casts"},
		{`google.protobuf.Timestamp create_time`, "create_time"},
		{`google.protobuf.Timestamp update_time`, "update_time"},
		{`google.protobuf.Timestamp delete_time`, "delete_time soft-delete"},
		{`google.protobuf.Timestamp purge_time`, "purge_time hard-delete"},
		{`uint64 author_identifier`, "FK column renamed + uint64"},
		{`(service: users)`, "cross-service FK annotation"},
		{`enum BookState {`, "BookState enum"},
		{`BOOK_UNSPECIFIED = 0`, "unspecified enum value"},
		{`service BookService {`, "BookService"},
		{`rpc CreateBook(`, "Create RPC"},
		{`// TODO: custom route: GET books/{id}/export`, "non-CRUD route TODO"},
		{`// Relationships`, "relationship annotations"},
		{`TODO: target model not found`, "unresolved relation TODO"},
	}
	for _, c := range checks {
		if !strings.Contains(proto, c.substr) {
			t.Errorf("proto missing %s: %q", c.desc, c.substr)
		}
	}
}

// TestEloquentDeriver_IncompleteMarker verifies the Incomplete flag fires when a
// model module is listed but no extractable columns exist.
func TestEloquentDeriver_IncompleteMarker(t *testing.T) {
	t.Parallel()
	// A class that is an Eloquent model but has only relations — no columns,
	// no docblock, no migration. Zero mappable fields → Incomplete.
	const emptyModel = `<?php
namespace Acme\Empty\Models;
use Illuminate\Database\Eloquent\Relations\HasMany;
class Widget extends Model
{
    public function gadgets(): HasMany { return $this->hasMany(Gadget::class); }
}
`
	workspace := writePHPWorkspace(t, map[string]string{
		"composer.json":               fixtureComposerJSON,
		"app/Empty/Models/Widget.php": emptyModel,
	})
	cluster := workerdomain.Cluster{
		BlueprintGroup: `Acme\Empty`,
		Modules:        []workerdomain.Module{`Acme\Empty\Models\Widget`},
	}
	d := NewEloquentDeriver()
	contract, err := d.Derive(context.Background(), cluster, workspace, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if !contract.Incomplete {
		t.Error("expected Incomplete=true when a model module yields zero columns")
	}
	if contract.IncompleteReason == "" {
		t.Error("expected a non-empty IncompleteReason")
	}
	t.Logf("IncompleteReason: %q", contract.IncompleteReason)
}

// TestPluralSnake exercises the convention table-name helper.
func TestPluralSnake(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"Book", "books"},
		{"Page", "pages"},
		{"Category", "categories"},
		{"BookShelf", "book_shelves"},
		{"Class", "classes"},
		{"User", "users"},
	}
	for _, tc := range cases {
		if got := pluralSnake(tc.in); got != tc.want {
			t.Errorf("pluralSnake(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEloquentFKHint verifies the FK heuristic respects the declared column type.
func TestEloquentFKHint(t *testing.T) {
	t.Parallel()
	// Integer-typed _id column → FK.
	if isFK, ref := eloquentFKHint("author_id", "Integer"); !isFK || ref != "authors" {
		t.Errorf("author_id Integer: isFK=%v ref=%q, want true/authors", isFK, ref)
	}
	// Untyped _id column → FK (defaults to int).
	if isFK, _ := eloquentFKHint("book_id", ""); !isFK {
		t.Error("book_id untyped should be FK")
	}
	// String-typed _id column (external_auth_id) → NOT a FK.
	if isFK, _ := eloquentFKHint("external_auth_id", "String"); isFK {
		t.Error("external_auth_id String must NOT be a FK")
	}
	// Plain column → not a FK.
	if isFK, _ := eloquentFKHint("name", "String"); isFK {
		t.Error("name must not be a FK")
	}
}
