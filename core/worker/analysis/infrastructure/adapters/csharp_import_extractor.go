package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/csharp"
)

// CSharpImportExtractor extracts namespace declarations, using directives, and
// type metadata from C# source files using tree-sitter.
//
// Known limitations:
//   - Reflection / Activator.CreateInstance dynamic loads are not tracked.
//   - Only the first declared namespace per file owns the file's module identity;
//     nested namespaces still contribute their declared types to the type set.
//   - using aliases (using Foo = A.B.C;) record both the alias and the target.
type CSharpImportExtractor struct{}

// NewCSharpImportExtractor returns a new CSharpImportExtractor.
func NewCSharpImportExtractor() *CSharpImportExtractor {
	return &CSharpImportExtractor{}
}

// csharpUsing is one using directive as written in source.
type csharpUsing struct {
	Namespace string // the imported namespace FQN, e.g. "Acme.Services"
	Alias     string // alias name when `using X = ...;`, else ""
	IsGlobal  bool   // `global using ...;`
}

// csharpRoute is one ASP.NET Core route mapped from controller attributes or a
// minimal-API Map* call.
type csharpRoute struct {
	Method  string // HTTP method (GET/POST/...); "ANY" for a bare [Route]/MapMethods
	Path    string // URL path pattern (class [Route] prefix + action template)
	Handler string // action method name, or "" for a minimal-API endpoint
}

// csharpRawFile holds the data extracted from a single .cs source file.
type csharpRawFile struct {
	RelPath     string        // path relative to the workspace root
	Namespace   string        // primary declared namespace FQN, e.g. "Acme.Web"
	Namespaces  []string      // every namespace declared in the file (incl. nested)
	Usings      []csharpUsing // using directives
	Types       []string      // top-level/namespaced type names declared in the file
	PrimaryType string        // first type declared (the module's identity)
	PrimaryKind string        // "class" | "interface" | "record" | "struct"
	Methods     []string      // method names declared in the primary type
	StaticState []string      // static field names declared in the primary type
	Loc         uint32        // non-blank, non-comment line count

	// ASP.NET Core web surface.
	IsController  bool          // [ApiController]/[Controller] or Controller-suffixed class
	ClassPrefix   string        // class-level [Route("...")] template, "" if none
	ControllerTag string        // primary type name, used as the blueprint identity
	Routes        []csharpRoute // attribute routes + minimal-API endpoints
}

// ExtractFiles walks workspacePath for .cs files, parses each with tree-sitter,
// and returns one csharpRawFile per file. bin/, obj/, .git/, .vs/ and packages/
// are skipped.
//
// Context cancellation aborts the walk. Per-file parse errors are skipped silently.
func (e *CSharpImportExtractor) ExtractFiles(ctx context.Context, workspacePath string) ([]csharpRawFile, error) {
	lang := csharp.GetLanguage()
	var files []csharpRawFile

	err := filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			switch d.Name() {
			case "bin", "obj", ".git", ".vs", "packages", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".cs") {
			return nil
		}

		relPath, relErr := filepath.Rel(workspacePath, path)
		if relErr != nil {
			relPath = path
		}
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

		f := extractCSharpFile(tree.RootNode(), src, relPath)
		f.Loc = csharpCountLOC(src)
		files = append(files, f)
		return nil
	})
	return files, err
}

// extractCSharpFile walks a parsed C# compilation_unit and assembles a
// csharpRawFile. It handles both block namespaces (namespace X { ... }) and
// file-scoped namespaces (namespace X;), which apply to the rest of the file.
func extractCSharpFile(root *sitter.Node, src []byte, relPath string) csharpRawFile {
	f := csharpRawFile{RelPath: relPath}

	var walk func(node *sitter.Node, currentNS string)
	walk = func(node *sitter.Node, currentNS string) {
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			switch child.Type() {
			case "using_directive":
				if u, ok := csharpParseUsing(child, src); ok {
					f.Usings = append(f.Usings, u)
				}
			case "file_scoped_namespace_declaration":
				ns := csharpChildFieldText(child, src, "name")
				f.recordNamespace(ns)
				// The rest of this declaration's children live under this namespace.
				walk(child, ns)
			case "namespace_declaration":
				ns := csharpChildFieldText(child, src, "name")
				f.recordNamespace(ns)
				if body := child.ChildByFieldName("body"); body != nil {
					walk(body, ns)
				}
			case "class_declaration":
				f.recordType(child, src, "class", currentNS)
			case "interface_declaration":
				f.recordType(child, src, "interface", currentNS)
			case "record_declaration", "record_struct_declaration":
				f.recordType(child, src, "record", currentNS)
			case "struct_declaration":
				f.recordType(child, src, "struct", currentNS)
			case "global_statement":
				// Top-level statements (minimal API). Scan for Map* endpoint calls.
				f.Routes = append(f.Routes, csharpExtractMinimalAPIRoutes(child, src)...)
			}
		}
	}
	walk(root, "")
	return f
}

// recordNamespace registers a declared namespace; the first one owns the file's
// module identity.
func (f *csharpRawFile) recordNamespace(ns string) {
	if ns == "" {
		return
	}
	f.Namespaces = append(f.Namespaces, ns)
	if f.Namespace == "" {
		f.Namespace = ns
	}
}

// recordType registers a type declaration under namespace ns. The first type
// declared in the file becomes its primary module identity; its members, static
// state, and ASP.NET web surface are extracted.
func (f *csharpRawFile) recordType(node *sitter.Node, src []byte, kind, ns string) {
	name := csharpChildFieldText(node, src, "name")
	if name == "" {
		return
	}
	f.Types = append(f.Types, name)
	if f.PrimaryType != "" {
		return
	}
	f.PrimaryType = name
	f.PrimaryKind = kind
	if f.Namespace == "" {
		f.Namespace = ns
	}

	attrs := csharpTypeAttributes(node, src)
	for _, at := range attrs {
		switch at.name {
		case "ApiController", "Controller":
			f.IsController = true
		case "Route":
			if f.ClassPrefix == "" {
				f.ClassPrefix = at.firstStringArg
			}
		}
	}
	// Convention: a class whose name ends in "Controller" is an MVC controller
	// even without an explicit [ApiController]/[Controller] attribute.
	if strings.HasSuffix(name, "Controller") {
		f.IsController = true
	}

	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	f.Methods, f.StaticState = csharpExtractMembers(body, src)
	if f.IsController {
		f.ControllerTag = name
		f.Routes = append(f.Routes, csharpExtractAttributeRoutes(body, src, f.ClassPrefix)...)
	}
}

// csharpParseUsing parses a using_directive into a csharpUsing.
// Handles `using A.B;`, `using X = A.B.C;`, and `global using A.B;`.
//
// tree-sitter shapes the alias form two ways across grammar revisions: either as
// a `name_equals` child (the alias), or as an `identifier` followed by `=` and
// then the target name. Both are handled: when `=` is present, the identifier
// before it is the alias and the name after it is the target namespace.
func csharpParseUsing(node *sitter.Node, src []byte) (csharpUsing, bool) {
	u := csharpUsing{}
	var preEquals, postEquals []string
	sawEquals := false
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		switch c.Type() {
		case "global":
			u.IsGlobal = true
		case "=":
			sawEquals = true
		case "name_equals":
			// `using X =` — the alias is the identifier inside name_equals.
			u.Alias = csharpFirstIdentifier(c, src)
		case "identifier", "qualified_name":
			if sawEquals {
				postEquals = append(postEquals, csharpText(c, src))
			} else {
				preEquals = append(preEquals, csharpText(c, src))
			}
		}
	}

	if len(postEquals) > 0 {
		// Alias form `using X = A.B;`: alias is the pre-`=` identifier (unless
		// already captured as name_equals); target is the post-`=` name.
		if u.Alias == "" && len(preEquals) > 0 {
			u.Alias = preEquals[len(preEquals)-1]
		}
		u.Namespace = postEquals[len(postEquals)-1]
	} else if len(preEquals) > 0 {
		// Plain form `using A.B;`: the name is the target namespace.
		u.Namespace = preEquals[len(preEquals)-1]
	}

	if u.Namespace == "" {
		return csharpUsing{}, false
	}
	return u, true
}

// csharpFirstIdentifier returns the first identifier text under node.
func csharpFirstIdentifier(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "identifier" {
			return csharpText(c, src)
		}
	}
	return ""
}

// csharpAttribute is one attribute: its short name and first string argument.
type csharpAttribute struct {
	name           string
	firstStringArg string
}

// csharpTypeAttributes returns the attributes attached to a type declaration by
// scanning its attribute_list children.
func csharpTypeAttributes(node *sitter.Node, src []byte) []csharpAttribute {
	var out []csharpAttribute
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "attribute_list" {
			out = append(out, csharpCollectAttributes(c, src)...)
		}
	}
	return out
}

// csharpCollectAttributes extracts every attribute inside an attribute_list.
func csharpCollectAttributes(list *sitter.Node, src []byte) []csharpAttribute {
	var out []csharpAttribute
	for i := 0; i < int(list.ChildCount()); i++ {
		c := list.Child(i)
		if c.Type() != "attribute" {
			continue
		}
		at := csharpAttribute{name: csharpShortName(csharpChildFieldText(c, src, "name"))}
		if at.name == "" {
			// `name` field is not always set; fall back to the first identifier/qualified_name.
			at.name = csharpShortName(csharpFirstNameText(c, src))
		}
		for j := 0; j < int(c.ChildCount()); j++ {
			if a := c.Child(j); a.Type() == "attribute_argument_list" {
				at.firstStringArg = csharpFirstStringArg(a, src)
			}
		}
		if at.name != "" {
			out = append(out, at)
		}
	}
	return out
}

// csharpFirstNameText returns the first identifier/qualified_name text under node.
func csharpFirstNameText(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Type() == "identifier" || c.Type() == "qualified_name" {
			return csharpText(c, src)
		}
	}
	return ""
}

// csharpFirstStringArg returns the value of the first string literal in an
// attribute_argument_list (the route template).
func csharpFirstStringArg(args *sitter.Node, src []byte) string {
	var walk func(n *sitter.Node) string
	walk = func(n *sitter.Node) string {
		if n.Type() == "string_literal" {
			return csharpStringLiteralValue(n, src)
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			if v := walk(n.Child(i)); v != "" {
				return v
			}
		}
		return ""
	}
	return walk(args)
}

// csharpExtractMembers returns method names and static field names from a type body.
func csharpExtractMembers(body *sitter.Node, src []byte) (methods, staticState []string) {
	for i := 0; i < int(body.ChildCount()); i++ {
		member := body.Child(i)
		switch member.Type() {
		case "method_declaration":
			if name := csharpChildFieldText(member, src, "name"); name != "" {
				methods = append(methods, name)
			}
		case "field_declaration":
			if csharpHasStaticModifier(member) {
				staticState = append(staticState, csharpFieldDeclaratorNames(member, src)...)
			}
		}
	}
	return
}

// csharpHasStaticModifier reports whether a declaration carries a `static` modifier.
func csharpHasStaticModifier(node *sitter.Node) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "modifier" {
			for j := 0; j < int(c.ChildCount()); j++ {
				if c.Child(j).Type() == "static" {
					return true
				}
			}
		}
	}
	return false
}

// csharpFieldDeclaratorNames returns the variable names in a field_declaration.
// Shape: field_declaration → variable_declaration → variable_declarator(name).
func csharpFieldDeclaratorNames(node *sitter.Node, src []byte) []string {
	var names []string
	for i := 0; i < int(node.ChildCount()); i++ {
		vd := node.Child(i)
		if vd.Type() != "variable_declaration" {
			continue
		}
		for j := 0; j < int(vd.ChildCount()); j++ {
			if decl := vd.Child(j); decl.Type() == "variable_declarator" {
				if n := csharpChildFieldText(decl, src, "name"); n != "" {
					names = append(names, n)
				} else if n := csharpFirstIdentifier(decl, src); n != "" {
					names = append(names, n)
				}
			}
		}
	}
	return names
}

// csharpExtractAttributeRoutes maps method-level ASP.NET attributes ([HttpGet],
// [HttpPost], [Route], ...) to csharpRoute entries. classPrefix is the class
// [Route] template, prepended to each action template.
func csharpExtractAttributeRoutes(body *sitter.Node, src []byte, classPrefix string) []csharpRoute {
	var routes []csharpRoute
	for i := 0; i < int(body.ChildCount()); i++ {
		member := body.Child(i)
		if member.Type() != "method_declaration" {
			continue
		}
		handler := csharpChildFieldText(member, src, "name")
		for _, at := range csharpMethodAttributes(member, src) {
			method, ok := csharpHttpMethod(at.name)
			if !ok {
				continue
			}
			routes = append(routes, csharpRoute{
				Method:  method,
				Path:    csharpJoinPath(classPrefix, at.firstStringArg),
				Handler: handler,
			})
		}
	}
	return routes
}

// csharpMethodAttributes returns attributes attached to a method_declaration.
func csharpMethodAttributes(node *sitter.Node, src []byte) []csharpAttribute {
	var out []csharpAttribute
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "attribute_list" {
			out = append(out, csharpCollectAttributes(c, src)...)
		}
	}
	return out
}

// csharpHttpMethod maps an ASP.NET HTTP attribute name to its HTTP method.
func csharpHttpMethod(name string) (string, bool) {
	switch name {
	case "HttpGet":
		return "GET", true
	case "HttpPost":
		return "POST", true
	case "HttpPut":
		return "PUT", true
	case "HttpDelete":
		return "DELETE", true
	case "HttpPatch":
		return "PATCH", true
	case "HttpHead":
		return "HEAD", true
	case "Route":
		return "ANY", true
	}
	return "", false
}

// csharpMinimalAPIMethod maps a minimal-API endpoint method name to its HTTP method.
func csharpMinimalAPIMethod(name string) (string, bool) {
	switch name {
	case "MapGet":
		return "GET", true
	case "MapPost":
		return "POST", true
	case "MapPut":
		return "PUT", true
	case "MapDelete":
		return "DELETE", true
	case "MapPatch":
		return "PATCH", true
	case "MapMethods":
		return "ANY", true
	}
	return "", false
}

// csharpExtractMinimalAPIRoutes scans a global_statement subtree for minimal-API
// endpoint registrations (app.MapGet("/x", ...), app.MapPost("/y", ...)).
func csharpExtractMinimalAPIRoutes(node *sitter.Node, src []byte) []csharpRoute {
	var routes []csharpRoute
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Type() == "invocation_expression" {
			if r, ok := csharpMatchMapCall(n, src); ok {
				routes = append(routes, r)
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(node)
	return routes
}

// csharpMatchMapCall tests one invocation_expression for the minimal-API endpoint
// shape app.Map<Verb>("/path", ...) and extracts the route when it matches.
func csharpMatchMapCall(call *sitter.Node, src []byte) (csharpRoute, bool) {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Type() != "member_access_expression" {
		return csharpRoute{}, false
	}
	methodName := ""
	if n := fn.ChildByFieldName("name"); n != nil {
		methodName = csharpText(n, src)
	}
	httpMethod, ok := csharpMinimalAPIMethod(methodName)
	if !ok {
		return csharpRoute{}, false
	}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return csharpRoute{}, false
	}
	path := csharpFirstStringArg(args, src)
	if path == "" {
		return csharpRoute{}, false
	}
	return csharpRoute{Method: httpMethod, Path: csharpJoinPath("", path), Handler: ""}, true
}

// csharpJoinPath concatenates a class-level route template with an action
// template, normalising slashes.
func csharpJoinPath(prefix, path string) string {
	prefix = strings.Trim(prefix, "/")
	path = strings.Trim(path, "/")
	switch {
	case prefix == "" && path == "":
		return "/"
	case prefix == "":
		return "/" + path
	case path == "":
		return "/" + prefix
	default:
		return "/" + prefix + "/" + path
	}
}

// csharpShortName returns the last dotted segment of an attribute/namespace name.
func csharpShortName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// csharpStringLiteralValue returns the inner text of a C# string_literal node.
// The inner content node is string_literal_content; quotes are stripped otherwise.
func csharpStringLiteralValue(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "string_literal_content" {
			return csharpText(c, src)
		}
	}
	return strings.Trim(csharpText(node, src), `"`)
}

// csharpChildFieldText returns the text of the named field child, or "".
func csharpChildFieldText(node *sitter.Node, src []byte, field string) string {
	if c := node.ChildByFieldName(field); c != nil {
		return csharpText(c, src)
	}
	return ""
}

// csharpText returns the raw source bytes for the span of node.
func csharpText(node *sitter.Node, src []byte) string {
	return string(src[node.StartByte():node.EndByte()])
}

// csharpCountLOC counts non-blank, non-comment lines in C# source.
func csharpCountLOC(src []byte) uint32 {
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
