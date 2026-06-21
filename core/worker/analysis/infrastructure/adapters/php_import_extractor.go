package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/php"
)

// PHPImportExtractor extracts namespace declarations, use statements, and class
// metadata from PHP source files using tree-sitter.
//
// Known limitations:
//   - Dynamic requires (require $path, include $path) are not tracked.
//   - use function / use const declarations are skipped (not class dependencies).
//   - Files with multiple namespace blocks (uncommon in PSR-4) use only the
//     first declared namespace.
type PHPImportExtractor struct{}

// NewPHPImportExtractor returns a new PHPImportExtractor.
func NewPHPImportExtractor() *PHPImportExtractor {
	return &PHPImportExtractor{}
}

// phpRawFile holds the data extracted from a single PHP source file.
// All namespace strings use the PHP separator (backslash, 0x5C).
type phpRawFile struct {
	RelPath     string            // path relative to the workspace root
	NS          string            // declared namespace, e.g. "BookStack\Entities\Controllers"
	Class       string            // first class / interface / trait / enum name in the file
	Kind        string            // "class" | "interface" | "trait" | "enum"
	Uses        []string          // FQNs from top-level use declarations (classes only)
	UseAliases  map[string]string // alias (last segment or explicit `as`) → FQN, for name resolution
	Refs        []string          // Tier-A in-body class references (type-hints, new, ::, ::class) as written
	Methods     []string          // method names declared in Class
	Props       []string          // all property names declared in Class ($ stripped)
	StaticProps []string          // subset of Props that carry static modifier (state signals)
	Loc         uint32            // non-blank, non-comment line count

	// CI3 convention fields — populated for every file regardless of namespace,
	// since CodeIgniter 3 classes declare none. They are unused by the PSR-4
	// resolver (which gates on f.NS != "") and consumed only by the CI3 path.
	CI3Extends []string  // parent class name(s) written in `extends` (verbatim)
	CI3Loads   []ci3Load // $this->load->model/library('literal') calls in the body
}

// ci3Load is one CodeIgniter loader call with a string-literal argument:
// $this->load->model('foo') or $this->load->library('bar'). Calls with a
// variable/array/dynamic argument are not captured (out of v1 scope).
type ci3Load struct {
	kind string // "model" | "library"
	name string // the literal argument, quotes stripped
}

// ExtractFiles walks workspacePath for .php files, parses each with tree-sitter,
// and returns one phpRawFile per file. vendor/, node_modules/, .git/, storage/
// and bootstrap/ are skipped.
//
// Context cancellation aborts the walk. Per-file parse errors are skipped silently.
func (e *PHPImportExtractor) ExtractFiles(ctx context.Context, workspacePath string) ([]phpRawFile, error) {
	lang := php.GetLanguage()
	var files []phpRawFile

	err := filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", "node_modules", ".git", "storage", "bootstrap":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".php") {
			return nil
		}

		relPath, _ := filepath.Rel(workspacePath, path)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		parser := sitter.NewParser()
		parser.SetLanguage(lang)
		tree, err := parser.ParseCtx(ctx, nil, src)
		if err != nil {
			return nil
		}

		f := extractPHPFile(tree.RootNode(), src, relPath)
		f.Loc = phpCountLOC(src)
		files = append(files, f)
		return nil
	})
	return files, err
}

// extractPHPFile walks the top-level nodes of a parsed PHP program and assembles
// a phpRawFile. Only the first namespace declaration and first type declaration
// are captured (PSR-4: one namespace + one class per file).
func extractPHPFile(root *sitter.Node, src []byte, relPath string) phpRawFile {
	f := phpRawFile{RelPath: relPath, UseAliases: map[string]string{}}
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "namespace_definition":
			if f.NS == "" {
				f.NS = phpNamespaceDeclText(child, src)
			}
		case "namespace_use_declaration":
			for _, u := range phpExtractUseEntries(child, src) {
				f.Uses = append(f.Uses, u.fqn)
				if u.alias != "" {
					f.UseAliases[u.alias] = u.fqn
				}
			}
		case "class_declaration":
			if f.Class == "" {
				f.Class = phpChildText(child, src, "name")
				f.Kind = "class"
				f.Methods, f.Props, f.StaticProps = phpExtractClassMembers(child, src)
				f.Refs = phpCollectRefs(child, src)
				f.CI3Extends = phpExtendsNames(child, src)
			}
		case "interface_declaration":
			if f.Class == "" {
				f.Class = phpChildText(child, src, "name")
				f.Kind = "interface"
				f.Refs = phpCollectRefs(child, src)
			}
		case "trait_declaration":
			if f.Class == "" {
				f.Class = phpChildText(child, src, "name")
				f.Kind = "trait"
				f.Refs = phpCollectRefs(child, src)
			}
		case "enum_declaration":
			if f.Class == "" {
				f.Class = phpChildText(child, src, "name")
				f.Kind = "enum"
				f.Refs = phpCollectRefs(child, src)
			}
		}
	}
	// CI3 loader calls can appear anywhere in any method body, so collect them by
	// walking the whole program once. Cheap and only consumed by the CI3 path.
	f.CI3Loads = phpCollectCI3Loads(root, src)
	return f
}

// phpExtendsNames returns the parent class name(s) written in the base_clause of
// a class_declaration (the `extends X` clause). Used by the CI3 convention
// resolver, where classes carry no namespace and extends is the primary lineage
// signal (extends MY_Foo / extends Foo). PHP single inheritance means at most
// one entry, but the walk is list-tolerant.
func phpExtendsNames(classNode *sitter.Node, src []byte) []string {
	var names []string
	for i := 0; i < int(classNode.ChildCount()); i++ {
		c := classNode.Child(i)
		if c.Type() != "base_clause" {
			continue
		}
		for j := 0; j < int(c.ChildCount()); j++ {
			if g := c.Child(j); g.Type() == "name" || g.Type() == "qualified_name" {
				names = append(names, phpText(g, src))
			}
		}
	}
	return names
}

// phpCollectCI3Loads walks the program for CodeIgniter loader calls of the form
// $this->load->model('literal') and $this->load->library('literal'), returning
// one ci3Load per call with a string-literal argument. The tree-sitter shape is
// a member_call_expression whose object is itself a member access ending in the
// `load` property, whose method name is `model`/`library`, and whose first
// argument is a string. Variable/array/dynamic arguments produce no entry.
func phpCollectCI3Loads(root *sitter.Node, src []byte) []ci3Load {
	var loads []ci3Load
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Type() == "member_call_expression" {
			if ld, ok := phpMatchCI3Load(n, src); ok {
				loads = append(loads, ld)
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return loads
}

// phpMatchCI3Load tests one member_call_expression for the loader shape and
// extracts (kind, literal-name) when it matches. It does not require the object
// chain to literally be $this->load — matching the method name (model|library)
// on a `load`-rooted access is enough and keeps the matcher resilient to
// get_instance()->load and $CI->load variants, which still denote a CI3 load.
func phpMatchCI3Load(call *sitter.Node, src []byte) (ci3Load, bool) {
	method := phpChildText(call, src, "name")
	if method != "model" && method != "library" {
		return ci3Load{}, false
	}
	// The object must be a `->load` member access (the CodeIgniter loader handle).
	obj := call.ChildByFieldName("object")
	if obj == nil || obj.Type() != "member_access_expression" {
		return ci3Load{}, false
	}
	if phpChildText(obj, src, "name") != "load" {
		return ci3Load{}, false
	}
	// First string-literal argument is the loaded module name.
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return ci3Load{}, false
	}
	for i := 0; i < int(args.ChildCount()); i++ {
		a := args.Child(i)
		if a.Type() != "argument" {
			continue
		}
		for j := 0; j < int(a.ChildCount()); j++ {
			lit := a.Child(j)
			switch lit.Type() {
			case "string", "encapsed_string":
				return ci3Load{kind: method, name: phpStringLiteralValue(lit, src)}, true
			}
		}
		// A non-string first argument (variable, array) is out of v1 scope.
		return ci3Load{}, false
	}
	return ci3Load{}, false
}

// phpStringLiteralValue returns the inner text of a PHP string literal node,
// stripping the surrounding single/double quotes. tree-sitter exposes the raw
// span including quotes; the inner string_content child carries the value.
func phpStringLiteralValue(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "string_content" {
			return phpText(c, src)
		}
	}
	raw := phpText(node, src)
	return strings.Trim(raw, "'\"")
}

// phpUseEntry is one resolved use clause: its canonical FQN and the alias under
// which it is referenced in code (explicit `as` alias, else the last segment).
type phpUseEntry struct {
	fqn   string
	alias string
}

// phpCollectRefs walks a type-declaration subtree and returns the class references
// written in its body, across all three tiers:
//   - Tier A: type-hints (named_type), object creation (new X), static access
//     (X::method / X::CONST / X::class).
//   - Tier B: extends (base_clause) and implements (class_interface_clause).
//   - Tier C: trait use in the class body (use_declaration — distinct from the
//     top-level namespace_use_declaration handled as imports).
//
// Names are returned verbatim (short, qualified, or fully-qualified) for the
// resolver to resolve against the file's use-aliases and namespace.
func phpCollectRefs(node *sitter.Node, src []byte) []string {
	var refs []string
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Type() {
		case "named_type", "object_creation_expression":
			if name := phpFirstNameChild(n, src); name != "" {
				refs = append(refs, name)
			}
		case "class_constant_access_expression", "scoped_call_expression", "scoped_property_access_expression":
			// The scope is the first child; collect it only when it is a written
			// class name (skip $var::, self::, static::, parent::).
			if n.ChildCount() > 0 {
				if c := n.Child(0); c.Type() == "name" || c.Type() == "qualified_name" {
					refs = append(refs, phpText(c, src))
				}
			}
		case "base_clause", "class_interface_clause", "use_declaration":
			// Tier B/C: every written name child is a parent class, interface, or
			// trait. Each can list several (multiple interfaces / multiple traits).
			for i := 0; i < int(n.ChildCount()); i++ {
				if c := n.Child(i); c.Type() == "name" || c.Type() == "qualified_name" {
					refs = append(refs, phpText(c, src))
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(node)
	return refs
}

// phpFirstNameChild returns the text of the first child that is a `name` or
// `qualified_name`, or "" when there is none (e.g. `new $dynamic`).
func phpFirstNameChild(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		if c := node.Child(i); c.Type() == "name" || c.Type() == "qualified_name" {
			return phpText(c, src)
		}
	}
	return ""
}

// phpNamespaceDeclText returns the namespace string from a namespace_definition node.
func phpNamespaceDeclText(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		if child := node.Child(i); child.Type() == "namespace_name" {
			return phpText(child, src)
		}
	}
	return ""
}

// phpExtractUseEntries extracts (FQN, alias) pairs from a namespace_use_declaration
// node. Both standard and grouped forms are handled. use function / use const
// declarations produce no entries. The alias is the explicit `as` name when
// present, otherwise the last backslash-segment of the FQN.
func phpExtractUseEntries(node *sitter.Node, src []byte) []phpUseEntry {
	// Detect and skip 'use function' / 'use const' modifiers.
	for i := 0; i < int(node.ChildCount()); i++ {
		t := node.Child(i).Type()
		if t == "function" || t == "const" {
			return nil
		}
	}

	var prefix string
	var entries []phpUseEntry

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "namespace_use_clause":
			// Standard form: use App\Services\UserService [as Alias];
			if e := phpUseClauseEntry(child, src, ""); e.fqn != "" {
				entries = append(entries, e)
			}
		case "namespace_name":
			// Grouped-use prefix: the namespace_name before the '{' group.
			prefix = phpText(child, src)
		case "namespace_use_group":
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == "namespace_use_group_clause" {
					if e := phpUseClauseEntry(gc, src, prefix); e.fqn != "" {
						entries = append(entries, e)
					}
				}
			}
		}
	}
	return entries
}

// phpUseClauseEntry builds the (FQN, alias) pair from a namespace_use_clause or
// namespace_use_group_clause node. prefix is the grouped-use prefix ("" for the
// standard form). The FQN text lives in a qualified_name child (standard form)
// or a namespace_name child (grouped form); an explicit alias is the name inside
// a namespace_aliasing_clause, else the last segment of the FQN.
func phpUseClauseEntry(node *sitter.Node, src []byte, prefix string) phpUseEntry {
	var fqn, alias string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "qualified_name", "namespace_name":
			fqn = phpText(child, src)
			if prefix != "" {
				fqn = prefix + `\` + fqn
			}
		case "namespace_aliasing_clause":
			alias = phpChildText(child, src, "name")
		}
	}
	if fqn == "" {
		return phpUseEntry{}
	}
	if alias == "" {
		if i := strings.LastIndex(fqn, `\`); i >= 0 {
			alias = fqn[i+1:]
		} else {
			alias = fqn
		}
	}
	return phpUseEntry{fqn: fqn, alias: alias}
}

// phpExtractClassMembers returns method names, all property names, and the
// subset of properties declared with the static modifier from a class_declaration node.
// Static properties indicate class-level mutable state (singletons, registries).
func phpExtractClassMembers(classNode *sitter.Node, src []byte) (methods, props, staticProps []string) {
	for i := 0; i < int(classNode.ChildCount()); i++ {
		body := classNode.Child(i)
		if body.Type() != "declaration_list" {
			continue
		}
		for j := 0; j < int(body.ChildCount()); j++ {
			member := body.Child(j)
			switch member.Type() {
			case "method_declaration":
				if name := phpChildText(member, src, "name"); name != "" {
					methods = append(methods, name)
				}
			case "property_declaration":
				isStatic := false
				var varNames []string
				for k := 0; k < int(member.ChildCount()); k++ {
					child := member.Child(k)
					switch child.Type() {
					case "static_modifier":
						isStatic = true
					case "property_element":
						for l := 0; l < int(child.ChildCount()); l++ {
							varNode := child.Child(l)
							if varNode.Type() == "variable_name" {
								varNames = append(varNames, strings.TrimPrefix(phpText(varNode, src), "$"))
							}
						}
					}
				}
				props = append(props, varNames...)
				if isStatic {
					staticProps = append(staticProps, varNames...)
				}
			}
		}
		break // only one declaration_list per class
	}
	return
}

// phpCountLOC counts non-blank, non-comment lines in PHP source.
// Handles //, #, and /* ... */ comment forms.
func phpCountLOC(src []byte) uint32 {
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
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "/**") {
			if !strings.Contains(trimmed[2:], "*/") {
				inBlock = true
			}
			continue
		}
		count++
	}
	return count
}

// phpChildText returns the raw text of the first child of node with type childType.
func phpChildText(node *sitter.Node, src []byte, childType string) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		if child := node.Child(i); child.Type() == childType {
			return phpText(child, src)
		}
	}
	return ""
}

// phpText returns the raw source bytes for the span of node.
func phpText(node *sitter.Node, src []byte) string {
	return string(src[node.StartByte():node.EndByte()])
}
