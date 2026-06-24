package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

// GoImportExtractor extracts package clauses, import specs, declaration
// metadata, and HTTP route surface from Go source files using tree-sitter.
//
// All tree-sitter internals are confined to this package (Canon dependency
// rule: adapters only).
//
// Known limitations:
//   - Dynamic / reflective constructs (plugin, build tags excluding files) are
//     not modelled; static AST only.
//   - grpc-gateway routes are code-generated at build time and are not present
//     as literal handler registrations, so they are not captured (documented).
//   - Multi-module monorepos (nested go.mod files) are not resolved; the single
//     root go.mod is the module identity (documented in the resolver).
type GoImportExtractor struct{}

// NewGoImportExtractor returns a new GoImportExtractor.
func NewGoImportExtractor() *GoImportExtractor {
	return &GoImportExtractor{}
}

// goRoute is one HTTP route extracted from a router method call
// (e.g. r.GET("/users", handler) or mux.HandleFunc("/x", h)).
type goRoute struct {
	Method  string // HTTP method (GET/POST/...); "" for bare Handle/HandleFunc
	Path    string // URL path pattern, the first string-literal argument
	Handler string // best-effort handler identifier (last argument)
}

// goImportSpec is one resolved import spec as written in source.
type goImportSpec struct {
	Path  string // the import path, quotes stripped (e.g. "example.com/app/repo")
	Name  string // alias / last path segment recorded into RawImport.Names
	IsDot bool   // dot-import: `. "pkg"`
	IsBlk bool   // blank-import: `_ "pkg"`
}

// goRawFile holds the data extracted from a single .go source file.
type goRawFile struct {
	RelPath string         // path relative to the workspace root
	Dir     string         // slash-separated dir relative to workspace root ("" = root)
	Package string         // declared package name, e.g. "svc" (informational)
	Imports []goImportSpec // import specs (grouped and single)

	Functions   []string // top-level func + method names (method as "Recv.Method")
	Classes     []string // "struct:Name" | "interface:Name" | "type:Name"
	ModuleState []string // file-scope mutable var names (NOT const)
	Docstring   string   // first 120 chars of the leading comment block
	Loc         uint32   // non-blank, non-comment line count

	Routes []goRoute // HTTP routes detected in this file
}

// ExtractFiles walks workspacePath for .go files, parses each with tree-sitter,
// and returns one goRawFile per file. vendor/, .git/, node_modules/, testdata/
// directories are skipped. _test.go files ARE included (parity with Python,
// where the classifier marks tests).
//
// Context cancellation aborts the walk. Per-file parse errors are skipped silently.
func (e *GoImportExtractor) ExtractFiles(ctx context.Context, workspacePath string) ([]goRawFile, error) {
	lang := golang.GetLanguage()
	var files []goRawFile

	err := filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", ".git", "node_modules", ".idea":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
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

		f := extractGoFile(tree.RootNode(), src, relPath)
		f.Loc = goCountLOC(src)
		files = append(files, f)
		return nil
	})
	return files, err
}

// extractGoFile walks the top-level nodes of a parsed Go source_file and
// assembles a goRawFile. Inner scopes (function bodies) are traversed only to
// find HTTP route registrations.
func extractGoFile(root *sitter.Node, src []byte, relPath string) goRawFile {
	dir := ""
	if i := strings.LastIndexByte(relPath, '/'); i >= 0 {
		dir = relPath[:i]
	}
	f := goRawFile{RelPath: relPath, Dir: dir}

	docFound := false
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "comment":
			// Leading comment block before package_clause → docstring head.
			if !docFound && f.Package == "" {
				raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(goText(child, src)), "//"))
				if len(raw) > 120 {
					raw = raw[:120]
				}
				if raw != "" {
					f.Docstring = raw
					docFound = true
				}
			}
		case "package_clause":
			if id := child.ChildByFieldName("name"); id != nil {
				f.Package = goText(id, src)
			} else {
				// Fallback: first package_identifier child.
				for j := 0; j < int(child.ChildCount()); j++ {
					if c := child.Child(j); c.Type() == "package_identifier" {
						f.Package = goText(c, src)
						break
					}
				}
			}
		case "import_declaration":
			f.Imports = append(f.Imports, goParseImportDecl(child, src)...)
		case "function_declaration":
			if name := goChildFieldText(child, src, "name"); name != "" {
				f.Functions = append(f.Functions, name)
			}
		case "method_declaration":
			name := goChildFieldText(child, src, "name")
			recv := goReceiverType(child, src)
			if name != "" {
				if recv != "" {
					f.Functions = append(f.Functions, recv+"."+name)
				} else {
					f.Functions = append(f.Functions, name)
				}
			}
		case "type_declaration":
			f.Classes = append(f.Classes, goParseTypeDecl(child, src)...)
		case "var_declaration":
			f.ModuleState = append(f.ModuleState, goParseVarDecl(child, src)...)
		}
	}

	// Routes: scan the whole tree for router method calls.
	f.Routes = goExtractRoutes(root, src)
	return f
}

// goParseImportDecl handles both grouped (import ( ... )) and single
// (import "x") import declarations by recursing into an import_spec_list.
func goParseImportDecl(node *sitter.Node, src []byte) []goImportSpec {
	var out []goImportSpec
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		switch c.Type() {
		case "import_spec":
			if spec, ok := goParseImportSpec(c, src); ok {
				out = append(out, spec)
			}
		case "import_spec_list":
			for j := 0; j < int(c.ChildCount()); j++ {
				if cc := c.Child(j); cc.Type() == "import_spec" {
					if spec, ok := goParseImportSpec(cc, src); ok {
						out = append(out, spec)
					}
				}
			}
		}
	}
	return out
}

// goParseImportSpec parses a single import_spec node. The path field holds the
// quoted import path; the optional name field is an alias (package_identifier),
// a dot-import (dot), or a blank-import (blank_identifier).
func goParseImportSpec(node *sitter.Node, src []byte) (goImportSpec, bool) {
	pathNode := node.ChildByFieldName("path")
	if pathNode == nil {
		return goImportSpec{}, false
	}
	importPath := goStripString(goText(pathNode, src))
	if importPath == "" {
		return goImportSpec{}, false
	}

	spec := goImportSpec{Path: importPath}
	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		switch nameNode.Type() {
		case "package_identifier":
			spec.Name = goText(nameNode, src)
		case "dot":
			spec.IsDot = true
		case "blank_identifier":
			spec.IsBlk = true
		}
	}
	if spec.Name == "" {
		// Default name is the last path segment (informational; the coupling
		// edge is computed from Path, not Name).
		spec.Name = goLastSegment(importPath)
	}
	return spec, true
}

// goReceiverType returns the bare receiver type name of a method_declaration,
// stripping a leading pointer (*T → T) and any generic type arguments.
func goReceiverType(node *sitter.Node, src []byte) string {
	recv := node.ChildByFieldName("receiver")
	if recv == nil {
		return ""
	}
	for i := 0; i < int(recv.ChildCount()); i++ {
		pd := recv.Child(i)
		if pd.Type() != "parameter_declaration" {
			continue
		}
		typeNode := pd.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		return goBareTypeName(typeNode, src)
	}
	return ""
}

// goBareTypeName extracts a bare type identifier from a (possibly pointer or
// generic) type node, e.g. "*User" → "User", "Server[T]" → "Server".
func goBareTypeName(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "pointer_type":
		for i := 0; i < int(node.ChildCount()); i++ {
			c := node.Child(i)
			if c.Type() == "type_identifier" || c.Type() == "generic_type" || c.Type() == "pointer_type" {
				return goBareTypeName(c, src)
			}
		}
	case "generic_type":
		if t := node.ChildByFieldName("type"); t != nil {
			return goBareTypeName(t, src)
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			if c := node.Child(i); c.Type() == "type_identifier" {
				return goText(c, src)
			}
		}
	case "type_identifier":
		return goText(node, src)
	}
	return ""
}

// goParseTypeDecl returns the "kind:Name" entries for a type_declaration. A
// declaration may group multiple specs: `type ( A struct{}; B int )`. Both
// type_spec (definition) and type_alias (`type X = Y`) are handled.
func goParseTypeDecl(node *sitter.Node, src []byte) []string {
	var out []string
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		switch c.Type() {
		case "type_spec":
			name := goChildFieldText(c, src, "name")
			if name == "" {
				continue
			}
			kind := "type"
			if tn := c.ChildByFieldName("type"); tn != nil {
				switch tn.Type() {
				case "struct_type":
					kind = "struct"
				case "interface_type":
					kind = "interface"
				}
			}
			out = append(out, kind+":"+name)
		case "type_alias":
			if name := goChildFieldText(c, src, "name"); name != "" {
				out = append(out, "type:"+name)
			}
		}
	}
	return out
}

// goParseVarDecl returns the variable names declared at file scope by a
// var_declaration (mutable state). const declarations are a separate node type
// (const_declaration) and are never reached here.
func goParseVarDecl(node *sitter.Node, src []byte) []string {
	var out []string
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Type() != "var_spec" {
			continue
		}
		for j := 0; j < int(c.ChildCount()); j++ {
			if c.FieldNameForChild(j) == "name" {
				name := goText(c.Child(j), src)
				if name != "" && name != "_" {
					out = append(out, name)
				}
			}
		}
	}
	return out
}

// ── HTTP route extraction ──────────────────────────────────────────────────

// goRouteMethods maps router method selectors to the HTTP verb they register.
var goRouteMethods = map[string]string{
	"GET":        "GET",
	"POST":       "POST",
	"PUT":        "PUT",
	"DELETE":     "DELETE",
	"PATCH":      "PATCH",
	"HEAD":       "HEAD",
	"OPTIONS":    "OPTIONS",
	"Handle":     "",    // gin/echo/mux Handle(method?, path, …) → unknown verb
	"HandleFunc": "GET", // net/http ServeMux.HandleFunc("/path", h)
}

// goExtractRoutes walks the tree for call_expression nodes whose function is a
// selector_expression naming an HTTP router method, and records the path
// (first string literal arg) and a best-effort handler (last identifier arg).
func goExtractRoutes(root *sitter.Node, src []byte) []goRoute {
	var routes []goRoute
	var walk func(*sitter.Node)
	walk = func(node *sitter.Node) {
		if node.Type() == "call_expression" {
			if r, ok := goParseRouteCall(node, src); ok {
				routes = append(routes, r)
			}
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i))
		}
	}
	walk(root)
	return routes
}

// goParseRouteCall inspects a call_expression and returns a goRoute when the
// callee is router.<Verb>("/path", …, handler).
func goParseRouteCall(call *sitter.Node, src []byte) (goRoute, bool) {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Type() != "selector_expression" {
		return goRoute{}, false
	}
	field := fn.ChildByFieldName("field")
	if field == nil {
		return goRoute{}, false
	}
	method, ok := goRouteMethods[goText(field, src)]
	if !ok {
		return goRoute{}, false
	}

	args := call.ChildByFieldName("arguments")
	if args == nil {
		return goRoute{}, false
	}

	path := ""
	handler := ""
	for i := 0; i < int(args.ChildCount()); i++ {
		arg := args.Child(i)
		switch arg.Type() {
		case "interpreted_string_literal", "raw_string_literal":
			if path == "" {
				path = goStripString(goText(arg, src))
			}
		case "identifier":
			handler = goText(arg, src)
		case "selector_expression":
			// e.g. handlers.GetUser → take the field as the handler name.
			if fld := arg.ChildByFieldName("field"); fld != nil {
				handler = goText(fld, src)
			}
		}
	}
	if path == "" {
		return goRoute{}, false
	}
	return goRoute{Method: method, Path: path, Handler: handler}, true
}

// ── helpers ─────────────────────────────────────────────────────────────────

// goChildFieldText returns the text of the named field child, or "".
func goChildFieldText(node *sitter.Node, src []byte, field string) string {
	if c := node.ChildByFieldName(field); c != nil {
		return goText(c, src)
	}
	return ""
}

// goText returns the raw source bytes for the span of node.
func goText(node *sitter.Node, src []byte) string {
	return string(src[node.StartByte():node.EndByte()])
}

// goLastSegment returns the final path segment of an import path
// (e.g. "github.com/lib/pq" → "pq").
func goLastSegment(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// goStripString removes the surrounding quotes from an interpreted or raw Go
// string literal node text.
func goStripString(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '`' && s[len(s)-1] == '`') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// goCountLOC counts non-blank, non-comment lines in Go source, handling // and
// /* ... */ comment forms.
func goCountLOC(src []byte) uint32 {
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
