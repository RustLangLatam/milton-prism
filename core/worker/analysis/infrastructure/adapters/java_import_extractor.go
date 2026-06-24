package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"
)

// JavaImportExtractor extracts package declarations, import statements, and type
// metadata from Java source files using tree-sitter.
//
// Known limitations:
//   - Reflection / Class.forName dynamic loads are not tracked (static AST only).
//   - Only the package declaration + top-level type declarations are captured;
//     nested (inner) type declarations are not promoted to module nodes.
//   - Wildcard imports (import com.acme.model.*) are recorded with a trailing
//     ".*" marker; the resolver expands them against the repo's own packages.
type JavaImportExtractor struct{}

// NewJavaImportExtractor returns a new JavaImportExtractor.
func NewJavaImportExtractor() *JavaImportExtractor {
	return &JavaImportExtractor{}
}

// javaImport is one import declaration as written in source.
type javaImport struct {
	FQN        string // fully-qualified target, e.g. "com.acme.svc.UserService" or "com.acme.model" for a wildcard
	IsStatic   bool   // `import static ...`
	IsWildcard bool   // `import ...*` (FQN holds the package, member elided)
}

// javaRoute is one Spring MVC route mapped from controller annotations.
type javaRoute struct {
	Method  string // HTTP method (GET/POST/...); "" for a bare @RequestMapping
	Path    string // URL path pattern, class @RequestMapping prefix + method mapping
	Handler string // handler method name
}

// javaRawFile holds the data extracted from a single .java source file.
type javaRawFile struct {
	RelPath     string       // path relative to the workspace root
	Package     string       // declared package FQN, e.g. "com.acme.web"
	Imports     []javaImport // top-level import declarations
	Types       []string     // top-level type names (class/interface/enum/record)
	PrimaryType string       // first top-level type name (the module's identity)
	PrimaryKind string       // "class" | "interface" | "enum" | "record"
	Methods     []string     // method names declared in the primary type
	StaticState []string     // static field names declared in the primary type (state signal)
	Loc         uint32       // non-blank, non-comment line count

	// Spring web surface (populated only when a controller annotation is present).
	IsController  bool        // @RestController or @Controller on the primary type
	ClassPrefix   string      // class-level @RequestMapping path prefix, "" if none
	ControllerTag string      // primary type name, used as the blueprint identity
	Routes        []javaRoute // method-level mappings with the class prefix applied
}

// ExtractFiles walks workspacePath for .java files, parses each with tree-sitter,
// and returns one javaRawFile per file. target/, build/, out/, .git/, .gradle/
// and node_modules/ are skipped.
//
// Context cancellation aborts the walk. Per-file parse errors are skipped silently.
func (e *JavaImportExtractor) ExtractFiles(ctx context.Context, workspacePath string) ([]javaRawFile, error) {
	lang := java.GetLanguage()
	var files []javaRawFile

	err := filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			switch d.Name() {
			case "target", "build", "out", ".git", ".gradle", "node_modules", ".idea":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".java") {
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

		f := extractJavaFile(tree.RootNode(), src, relPath)
		f.Loc = javaCountLOC(src)
		files = append(files, f)
		return nil
	})
	return files, err
}

// extractJavaFile walks the top-level nodes of a parsed Java program and assembles
// a javaRawFile.
func extractJavaFile(root *sitter.Node, src []byte, relPath string) javaRawFile {
	f := javaRawFile{RelPath: relPath}

	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "package_declaration":
			f.Package = javaPackageName(child, src)
		case "import_declaration":
			if imp, ok := javaParseImport(child, src); ok {
				f.Imports = append(f.Imports, imp)
			}
		case "class_declaration":
			f.recordType(child, src, "class")
		case "interface_declaration":
			f.recordType(child, src, "interface")
		case "enum_declaration":
			f.recordType(child, src, "enum")
		case "record_declaration":
			f.recordType(child, src, "record")
		}
	}
	return f
}

// recordType registers a top-level type declaration. The first type seen becomes
// the file's primary module identity; its members, static state, and Spring web
// surface are extracted. Subsequent top-level types contribute their names only.
func (f *javaRawFile) recordType(node *sitter.Node, src []byte, kind string) {
	name := javaChildFieldText(node, src, "name")
	if name == "" {
		return
	}
	f.Types = append(f.Types, name)
	if f.PrimaryType != "" {
		return
	}
	f.PrimaryType = name
	f.PrimaryKind = kind

	anns := javaTypeAnnotations(node, src)
	for _, a := range anns {
		switch a.name {
		case "RestController", "Controller":
			f.IsController = true
		case "RequestMapping":
			if f.ClassPrefix == "" {
				f.ClassPrefix = a.firstStringArg
			}
		}
	}

	body := javaTypeBody(node)
	if body == nil {
		return
	}
	f.Methods, f.StaticState = javaExtractMembers(body, src)

	if f.IsController {
		f.ControllerTag = name
		f.Routes = javaExtractRoutes(body, src, f.ClassPrefix)
	}
}

// javaTypeBody returns the class_body / interface_body / enum_body / record body
// declaration_list node of a type declaration, or nil.
func javaTypeBody(node *sitter.Node) *sitter.Node {
	if b := node.ChildByFieldName("body"); b != nil {
		return b
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		switch c.Type() {
		case "class_body", "interface_body", "enum_body":
			return c
		}
	}
	return nil
}

// javaPackageName returns the dotted package name from a package_declaration node,
// e.g. "com.acme.web". The name is the scoped_identifier/identifier child.
func javaPackageName(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Type() == "scoped_identifier" || c.Type() == "identifier" {
			return javaText(c, src)
		}
	}
	return ""
}

// javaParseImport parses an import_declaration node into a javaImport.
// Handles `import a.b.C;`, `import static a.b.C.m;`, and `import a.b.*;`.
func javaParseImport(node *sitter.Node, src []byte) (javaImport, bool) {
	imp := javaImport{}
	var scoped *sitter.Node
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		switch c.Type() {
		case "static":
			imp.IsStatic = true
		case "asterisk":
			imp.IsWildcard = true
		case "scoped_identifier", "identifier":
			scoped = c
		}
	}
	if scoped == nil {
		return javaImport{}, false
	}
	imp.FQN = javaText(scoped, src)
	if imp.FQN == "" {
		return javaImport{}, false
	}
	return imp, true
}

// javaAnnotation is one annotation: its short name and the first string argument
// (the URL path for @RequestMapping/@GetMapping/etc.), empty when none.
type javaAnnotation struct {
	name           string
	firstStringArg string
}

// javaTypeAnnotations returns the annotations attached to a type declaration by
// scanning its `modifiers` child (where tree-sitter places marker_annotation and
// annotation nodes).
func javaTypeAnnotations(node *sitter.Node, src []byte) []javaAnnotation {
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "modifiers" {
			return javaCollectAnnotations(c, src)
		}
	}
	return nil
}

// javaCollectAnnotations extracts every annotation directly under a modifiers node.
func javaCollectAnnotations(modifiers *sitter.Node, src []byte) []javaAnnotation {
	var out []javaAnnotation
	for i := 0; i < int(modifiers.ChildCount()); i++ {
		c := modifiers.Child(i)
		switch c.Type() {
		case "marker_annotation":
			if n := javaChildFieldText(c, src, "name"); n != "" {
				out = append(out, javaAnnotation{name: javaShortName(n)})
			}
		case "annotation":
			a := javaAnnotation{name: javaShortName(javaChildFieldText(c, src, "name"))}
			if args := c.ChildByFieldName("arguments"); args != nil {
				a.firstStringArg = javaFirstStringArg(args, src)
			}
			if a.name != "" {
				out = append(out, a)
			}
		}
	}
	return out
}

// javaFirstStringArg returns the value of the first string literal found in an
// annotation_argument_list. Handles both the positional form @GetMapping("/x")
// and the named form @RequestMapping(value = "/x") / @RequestMapping(path = "/x").
func javaFirstStringArg(args *sitter.Node, src []byte) string {
	var walk func(n *sitter.Node) string
	walk = func(n *sitter.Node) string {
		if n.Type() == "string_literal" {
			return javaStringLiteralValue(n, src)
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

// javaExtractMembers returns method names and static field names from a type body.
func javaExtractMembers(body *sitter.Node, src []byte) (methods, staticState []string) {
	for i := 0; i < int(body.ChildCount()); i++ {
		member := body.Child(i)
		switch member.Type() {
		case "method_declaration":
			if name := javaChildFieldText(member, src, "name"); name != "" {
				methods = append(methods, name)
			}
		case "field_declaration":
			if javaHasStaticModifier(member) {
				staticState = append(staticState, javaFieldDeclaratorNames(member, src)...)
			}
		}
	}
	return
}

// javaHasStaticModifier reports whether a declaration's modifiers child contains
// a `static` keyword.
func javaHasStaticModifier(node *sitter.Node) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "modifiers" {
			for j := 0; j < int(c.ChildCount()); j++ {
				if c.Child(j).Type() == "static" {
					return true
				}
			}
		}
	}
	return false
}

// javaFieldDeclaratorNames returns the variable names declared in a field_declaration.
func javaFieldDeclaratorNames(node *sitter.Node, src []byte) []string {
	var names []string
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "variable_declarator" {
			if n := javaChildFieldText(c, src, "name"); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}

// javaExtractRoutes maps method-level Spring MVC annotations to javaRoute entries.
// classPrefix is the class-level @RequestMapping path, prepended to each route.
func javaExtractRoutes(body *sitter.Node, src []byte, classPrefix string) []javaRoute {
	var routes []javaRoute
	for i := 0; i < int(body.ChildCount()); i++ {
		member := body.Child(i)
		if member.Type() != "method_declaration" {
			continue
		}
		handler := javaChildFieldText(member, src, "name")
		for _, a := range javaTypeAnnotations(member, src) {
			method, ok := javaMappingMethod(a.name)
			if !ok {
				continue
			}
			routes = append(routes, javaRoute{
				Method:  method,
				Path:    javaJoinPath(classPrefix, a.firstStringArg),
				Handler: handler,
			})
		}
	}
	return routes
}

// javaMappingMethod maps a Spring mapping annotation name to its HTTP method.
// Returns ("", false) for non-mapping annotations.
func javaMappingMethod(name string) (string, bool) {
	switch name {
	case "GetMapping":
		return "GET", true
	case "PostMapping":
		return "POST", true
	case "PutMapping":
		return "PUT", true
	case "DeleteMapping":
		return "DELETE", true
	case "PatchMapping":
		return "PATCH", true
	case "RequestMapping":
		// Method-level @RequestMapping without an explicit method matches all verbs.
		return "ANY", true
	}
	return "", false
}

// javaJoinPath concatenates a class-level path prefix with a method-level path,
// normalising slashes. Either side may be empty.
func javaJoinPath(prefix, path string) string {
	prefix = strings.TrimRight(prefix, "/")
	if path == "" {
		if prefix == "" {
			return "/"
		}
		return prefix
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return prefix + path
}

// javaShortName returns the last dotted segment of an annotation name
// (e.g. "org.springframework.web.bind.annotation.GetMapping" → "GetMapping").
func javaShortName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// javaStringLiteralValue returns the inner text of a Java string_literal node,
// stripping the surrounding double quotes. The inner content node is string_fragment.
func javaStringLiteralValue(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "string_fragment" {
			return javaText(c, src)
		}
	}
	return strings.Trim(javaText(node, src), `"`)
}

// javaChildFieldText returns the text of the named field child, or "".
func javaChildFieldText(node *sitter.Node, src []byte, field string) string {
	if c := node.ChildByFieldName(field); c != nil {
		return javaText(c, src)
	}
	return ""
}

// javaText returns the raw source bytes for the span of node.
func javaText(node *sitter.Node, src []byte) string {
	return string(src[node.StartByte():node.EndByte()])
}

// javaCountLOC counts non-blank, non-comment lines in Java source.
// Handles // and /* ... */ comment forms.
func javaCountLOC(src []byte) uint32 {
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
