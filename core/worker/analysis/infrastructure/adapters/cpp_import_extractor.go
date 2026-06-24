package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/cpp"

	workerdomain "milton_prism/core/worker/analysis/domain"
)

// CppImportExtractor extracts #include directives and structural module-card
// metadata (functions, classes/structs/enums, namespaces, file-scope state,
// docstring, LOC) from C/C++ source files using tree-sitter.
//
// All tree-sitter internals are confined to this package (Canon dependency
// rule: adapters only). The cpp grammar parses both C and C++; we treat .c as
// C++ for graph purposes (the include semantics are identical).
//
// Quote vs. angle classification (the only #include signal we trust):
//   - #include "foo/bar.h"  (string_literal)      → CANDIDATE intra-repo edge.
//   - #include <vector>      (system_lib_string)   → system/third-party, NO edge
//     (discarded the same way Python stdlib imports are).
//
// Known limitations (static AST only):
//   - Macro-constructed include paths (#include MACRO) are not resolved.
//   - #ifdef-conditional includes are all walked (the grammar keeps both
//     branches) → possible overcount of candidate edges.
//   - Templates / header-only libraries blur the impl/header distinction; cards
//     are emitted per file regardless.
type CppImportExtractor struct{}

// NewCppImportExtractor returns a new CppImportExtractor.
func NewCppImportExtractor() *CppImportExtractor {
	return &CppImportExtractor{}
}

// cppHeaderExts are the file extensions treated as C/C++ headers.
var cppHeaderExts = map[string]bool{
	".h": true, ".hpp": true, ".hh": true, ".hxx": true, ".h++": true,
}

// cppImplExts are the file extensions treated as C/C++ translation units.
// .c is included and parsed by the cpp grammar (C is a subset for our purposes).
var cppImplExts = map[string]bool{
	".cpp": true, ".cc": true, ".cxx": true, ".c++": true, ".c": true,
}

// cppRawFile holds the data extracted from a single C/C++ source file.
type cppRawFile struct {
	RelPath string // path relative to the workspace root (slash-separated)
	Dir     string // slash-separated dir relative to workspace root ("" = root)
	IsHeader bool  // true for header extensions

	// QuoteIncludes are the raw quote-form include targets (#include "x"),
	// candidate intra-repo edges. System (angle-form) includes are discarded
	// here and never reach the resolver.
	QuoteIncludes []string

	Functions   []string // function definition names (free + method "Class::m")
	Classes     []string // "class:Name" | "struct:Name" | "enum:Name" | "namespace:Name"
	ModuleState []string // file/namespace-scope mutable declarations (best-effort)
	Docstring   string   // first 120 chars of the leading comment block
	Loc         uint32   // non-blank, non-comment line count
}

// ExtractFiles walks workspacePath for C/C++ source files, parses each with
// tree-sitter, and returns one cppRawFile per file. Build output and vendored
// dependency directories are skipped (see cppSkipDir). Per-file read/parse
// errors are skipped silently. Context cancellation aborts the walk.
func (e *CppImportExtractor) ExtractFiles(ctx context.Context, workspacePath string) ([]cppRawFile, error) {
	lang := cpp.GetLanguage()
	var files []cppRawFile

	err := filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if cppSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		isHeader := cppHeaderExts[ext]
		isImpl := cppImplExts[ext]
		if !isHeader && !isImpl {
			return nil
		}

		relPath, relErr := filepath.Rel(workspacePath, path)
		if relErr != nil {
			relPath = path
		}
		relPath = filepath.ToSlash(relPath)

		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		parser := sitter.NewParser()
		parser.SetLanguage(lang)
		tree, parseErr := parser.ParseCtx(ctx, nil, src)
		if parseErr != nil {
			return nil
		}

		f := extractCppFile(tree.RootNode(), src, relPath, isHeader)
		f.Loc = cppCountLOC(src)
		files = append(files, f)
		return nil
	})
	return files, err
}

// cppSkipDir reports whether a directory name should be pruned from the walk.
// build/, cmake-build-*, vendor/, third_party/, node_modules/, .git/.
func cppSkipDir(name string) bool {
	switch name {
	case ".git", "build", "vendor", "third_party", "node_modules", ".idea":
		return true
	}
	return strings.HasPrefix(name, "cmake-build-")
}

// extractCppFile walks the translation_unit assembling a cppRawFile. Includes
// are gathered tree-wide (they may appear after conditionals); structural cards
// are gathered from the file scope and recursed into namespace bodies.
func extractCppFile(root *sitter.Node, src []byte, relPath string, isHeader bool) cppRawFile {
	dir := ""
	if i := strings.LastIndexByte(relPath, '/'); i >= 0 {
		dir = relPath[:i]
	}
	f := cppRawFile{RelPath: relPath, Dir: dir, IsHeader: isHeader}

	// Leading comment block (before the first non-comment node) → docstring head.
	for i := 0; i < int(root.ChildCount()); i++ {
		c := root.Child(i)
		if c.Type() == "comment" {
			f.Docstring = cppCleanDocComment(cppText(c, src))
			break
		}
		if c.Type() != "\n" {
			break
		}
	}

	// Includes: walk the whole tree so #includes inside preproc conditionals are
	// still seen (overcount is documented and preferred over a silent miss).
	cppWalk(root, func(n *sitter.Node) {
		if n.Type() == "preproc_include" {
			if inc, isQuote, ok := cppParseInclude(n, src); ok && isQuote {
				f.QuoteIncludes = append(f.QuoteIncludes, inc)
			}
		}
	})

	// Structural cards: walk the file scope, recursing into namespaces.
	cppCollectCards(root, src, &f)
	return f
}

// cppParseInclude returns the include target text (without surrounding quotes
// or angle brackets) and whether it is a quote-form (string_literal) include.
// system_lib_string (angle-form) returns isQuote=false so the caller discards it.
func cppParseInclude(node *sitter.Node, src []byte) (target string, isQuote bool, ok bool) {
	pathNode := node.ChildByFieldName("path")
	if pathNode == nil {
		// Fallback: scan children for a path-bearing node.
		for i := 0; i < int(node.ChildCount()); i++ {
			c := node.Child(i)
			if c.Type() == "string_literal" || c.Type() == "system_lib_string" {
				pathNode = c
				break
			}
		}
	}
	if pathNode == nil {
		return "", false, false
	}
	switch pathNode.Type() {
	case "string_literal":
		raw := cppText(pathNode, src)
		return strings.Trim(raw, "\""), true, true
	case "system_lib_string":
		return "", false, true // recognised, but NOT an edge (angle-form)
	default:
		return "", false, false
	}
}

// cppCollectCards walks node's direct children collecting functions, type
// specifiers, namespaces and file-scope state. namespace_definition bodies are
// recursed so their members surface as file-level cards (namespaces are
// orthogonal to files in C++; we record the namespace name for context only).
func cppCollectCards(node *sitter.Node, src []byte, f *cppRawFile) {
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		switch c.Type() {
		case "function_definition":
			if name := cppFunctionName(c, src); name != "" {
				f.Functions = append(f.Functions, name)
			}
		case "template_declaration":
			// Templated function/class: recurse to reach the inner definition.
			cppCollectCards(c, src, f)
		case "class_specifier":
			if name := cppTypeName(c, src); name != "" {
				f.Classes = append(f.Classes, "class:"+name)
			}
		case "struct_specifier":
			if name := cppTypeName(c, src); name != "" {
				f.Classes = append(f.Classes, "struct:"+name)
			}
		case "enum_specifier":
			if name := cppTypeName(c, src); name != "" {
				f.Classes = append(f.Classes, "enum:"+name)
			}
		case "namespace_definition":
			if name := cppNamespaceName(c, src); name != "" {
				f.Classes = append(f.Classes, "namespace:"+name)
			}
			if body := c.ChildByFieldName("body"); body != nil {
				cppCollectCards(body, src, f)
			} else {
				// Fallback: recurse into the declaration_list child.
				cppRecurseDeclList(c, src, f)
			}
		case "declaration":
			if name := cppStateDeclName(c, src); name != "" {
				f.ModuleState = append(f.ModuleState, name)
			}
		case "preproc_ifdef", "preproc_if", "preproc_else", "preproc_elif",
			"linkage_specification", "declaration_list":
			// Structural passthrough wrappers: the preprocessor keeps every
			// conditional branch in the tree (documented overcount), and
			// extern "C" {} / namespace bodies wrap members. Recurse so their
			// definitions surface as file-level cards.
			cppCollectCards(c, src, f)
		}
	}
}

// cppRecurseDeclList recurses into a node's declaration_list children (the body
// of a namespace_definition when not exposed via the "body" field).
func cppRecurseDeclList(node *sitter.Node, src []byte, f *cppRawFile) {
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "declaration_list" {
			cppCollectCards(c, src, f)
		}
	}
}

// cppFunctionName returns the function name for a function_definition node.
// Free functions yield "name"; methods declared out-of-line as Class::method
// (qualified_identifier) yield "Class::method".
func cppFunctionName(node *sitter.Node, src []byte) string {
	decl := node.ChildByFieldName("declarator")
	if decl == nil {
		return ""
	}
	return cppDeclaratorName(decl, src)
}

// cppDeclaratorName descends a (possibly pointer/reference/parenthesised)
// declarator chain to the function_declarator and returns its declared name.
func cppDeclaratorName(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "function_declarator":
		if d := node.ChildByFieldName("declarator"); d != nil {
			return cppDeclaratorName(d, src)
		}
		return ""
	case "pointer_declarator", "reference_declarator", "parenthesized_declarator":
		if d := node.ChildByFieldName("declarator"); d != nil {
			return cppDeclaratorName(d, src)
		}
		// Fallback: first declarator-ish child.
		for i := 0; i < int(node.ChildCount()); i++ {
			c := node.Child(i)
			if strings.HasSuffix(c.Type(), "declarator") || c.Type() == "identifier" ||
				c.Type() == "field_identifier" || c.Type() == "qualified_identifier" ||
				c.Type() == "operator_name" || c.Type() == "destructor_name" {
				return cppDeclaratorName(c, src)
			}
		}
		return ""
	case "identifier", "field_identifier", "qualified_identifier",
		"operator_name", "destructor_name":
		return cppText(node, src)
	}
	// Last resort: look for a nested function_declarator.
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "function_declarator" {
			return cppDeclaratorName(c, src)
		}
	}
	return ""
}

// cppTypeName returns the declared name of a class/struct/enum specifier, or ""
// for an anonymous one (which we do not record as a card).
func cppTypeName(node *sitter.Node, src []byte) string {
	if n := node.ChildByFieldName("name"); n != nil {
		return cppText(n, src)
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Type() == "type_identifier" {
			return cppText(c, src)
		}
	}
	return ""
}

// cppNamespaceName returns the namespace name; "" for an anonymous namespace.
func cppNamespaceName(node *sitter.Node, src []byte) string {
	if n := node.ChildByFieldName("name"); n != nil {
		return cppText(n, src)
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Type() == "namespace_identifier" || c.Type() == "identifier" {
			return cppText(c, src)
		}
	}
	return ""
}

// cppStateDeclName returns the variable name of a file/namespace-scope
// declaration that carries mutable state (best-effort, low value). Declarations
// that are const-qualified are skipped: const data is not shared mutable state.
// Function prototypes (declaration wrapping a function_declarator) are skipped.
func cppStateDeclName(node *sitter.Node, src []byte) string {
	// Skip const declarations: the type field carries a type_qualifier "const".
	if cppDeclIsConst(node, src) {
		return ""
	}
	decl := node.ChildByFieldName("declarator")
	if decl == nil {
		return ""
	}
	// A function prototype is a declaration with a function_declarator — not state.
	if cppContainsFunctionDeclarator(decl) {
		return ""
	}
	name := cppPlainDeclaratorName(decl, src)
	if name == "" || name == "_" {
		return ""
	}
	return name
}

// cppDeclIsConst reports whether a declaration node is const-qualified at the
// top level (a type_qualifier child whose text is "const").
func cppDeclIsConst(node *sitter.Node, src []byte) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Type() == "type_qualifier" && cppText(c, src) == "const" {
			return true
		}
	}
	return false
}

// cppContainsFunctionDeclarator reports whether a declarator chain contains a
// function_declarator (i.e. the declaration is a function prototype).
func cppContainsFunctionDeclarator(node *sitter.Node) bool {
	if node.Type() == "function_declarator" {
		return true
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		if cppContainsFunctionDeclarator(node.Child(i)) {
			return true
		}
	}
	return false
}

// cppPlainDeclaratorName descends an init/array/pointer declarator to the bare
// identifier name of a non-function declaration.
func cppPlainDeclaratorName(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "identifier", "field_identifier", "qualified_identifier":
		return cppText(node, src)
	case "init_declarator", "array_declarator", "pointer_declarator",
		"reference_declarator", "parenthesized_declarator":
		if d := node.ChildByFieldName("declarator"); d != nil {
			return cppPlainDeclaratorName(d, src)
		}
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Type() == "identifier" || strings.HasSuffix(c.Type(), "declarator") {
			if name := cppPlainDeclaratorName(c, src); name != "" {
				return name
			}
		}
	}
	return ""
}

// ── helpers ─────────────────────────────────────────────────────────────────

// cppWalk performs a pre-order traversal invoking fn on every node.
func cppWalk(node *sitter.Node, fn func(*sitter.Node)) {
	fn(node)
	for i := 0; i < int(node.ChildCount()); i++ {
		cppWalk(node.Child(i), fn)
	}
}

// cppText returns the raw source bytes for the span of node.
func cppText(node *sitter.Node, src []byte) string {
	return string(src[node.StartByte():node.EndByte()])
}

// cppCleanDocComment strips // or /* */ delimiters from a leading comment and
// truncates to 120 chars (parity with the Go analyzer's docstring head).
func cppCleanDocComment(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "/*")
	raw = strings.TrimPrefix(raw, "//")
	raw = strings.TrimSuffix(raw, "*/")
	// Collapse leading "*" of each line of a block comment.
	var b strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimPrefix(line, "//")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(line)
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

// cppCountLOC counts non-blank, non-comment lines in C/C++ source, handling //
// and /* ... */ comment forms (parity with goCountLOC).
func cppCountLOC(src []byte) uint32 {
	var count uint32
	inBlock := false
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if inBlock {
			if strings.Contains(line, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "/*") {
			if !strings.Contains(trimmed[2:], "*/") {
				inBlock = true
			}
			continue
		}
		count++
	}
	return count
}

// cppToRawImports converts the quote-form includes of a parsed file into
// workerdomain.RawImport values (one per include). C++ has no module name, so
// Module carries the raw include text and Names carries the include basename.
// IsRelative is always false (include resolution is path-based, not dotted).
func cppToRawImports(f cppRawFile) []workerdomain.RawImport {
	out := make([]workerdomain.RawImport, 0, len(f.QuoteIncludes))
	for _, inc := range f.QuoteIncludes {
		base := inc
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		out = append(out, workerdomain.RawImport{
			ImportingFile: f.RelPath,
			Module:        inc,
			IsRelative:    false,
			Names:         []string{base},
		})
	}
	return out
}
