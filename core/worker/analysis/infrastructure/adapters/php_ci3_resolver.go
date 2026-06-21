package adapters

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	workerdomain "milton_prism/core/worker/analysis/domain"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/php"
)

// CI3 lineage is resolved by convention, not by PSR-4. A CodeIgniter 3 project
// ships its framework under system/ and its application code under
// application/{controllers,models,libraries,core}, with files that declare no
// namespace (f.NS == ""). The PSR-4 resolver gates exactly those files out, so
// a parallel convention path is required to recover their lineage.
//
// This path is deliberately separate from BuildGraphEdges (PSR-4) so that
// BookStack/Laravel graphs stay byte-identical. It is only entered when
// isCI3Workspace reports the CI3 marker.

// ci3ModuleRoots are the application subdirectories that hold convention modules.
// A file's identity is its base name (without .php); its directory selects which
// loader call / extension can reach it.
var ci3ModuleRoots = []string{"controllers", "models", "libraries", "core"}

// ci3Module is one discovered convention module.
type ci3Module struct {
	name      string // base filename without ".php" (CI3 module identity)
	dir       string // one of ci3ModuleRoots
	relPath   string // workspace-relative path
	className string // first declared class name (for extends resolution), may be ""
	loc       uint32
	methods   []string
	state     []string
}

// isCI3Workspace reports whether workspacePath is a CodeIgniter 3 project that
// must be resolved by convention rather than PSR-4. The marker mirrors the
// structural framework detector's CI3 rule: the framework bootstrap file
// system/core/CodeIgniter.php and an application/ directory both present, AND
// composer.json declaring no autoload.psr-4 map (a CI3 app vendored without a
// PSR-4 autoloader is exactly the blind spot this path covers; a CI3 app that
// also wires PSR-4 keeps using the PSR-4 path for its namespaced classes).
func isCI3Workspace(workspacePath string) bool {
	bootstrap := filepath.Join(workspacePath, "system", "core", "CodeIgniter.php")
	if info, err := os.Stat(bootstrap); err != nil || info.IsDir() {
		return false
	}
	appDir := filepath.Join(workspacePath, "application")
	if info, err := os.Stat(appDir); err != nil || !info.IsDir() {
		return false
	}
	// If composer.json declares a non-empty PSR-4 map, the project autoloads via
	// PSR-4 and the existing resolver already covers its namespaced classes.
	if r, err := NewPHPModuleResolver(workspacePath); err == nil && len(r.internalPrefixes) > 0 {
		return false
	}
	return true
}

// ci3DiscoverModules indexes the extracted PHP files into CI3 convention modules.
// Only files physically under application/{controllers,models,libraries,core}
// become modules. A file with no namespace and a class declaration is the common
// case; namespace presence is NOT required (this is the f.NS=="" gate removal).
func ci3DiscoverModules(files []phpRawFile) []ci3Module {
	var mods []ci3Module
	for _, f := range files {
		dir, ok := ci3DirOf(f.RelPath)
		if !ok {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(f.RelPath), filepath.Ext(f.RelPath))
		mods = append(mods, ci3Module{
			name:      base,
			dir:       dir,
			relPath:   f.RelPath,
			className: f.Class,
			loc:       f.Loc,
			methods:   f.Methods,
			state:     f.StaticProps,
		})
	}
	return mods
}

// ci3DirOf returns the CI3 module root that relPath sits directly or transitively
// under (e.g. "application/controllers/admin/Users.php" → "controllers"), and
// whether it sits under one at all.
func ci3DirOf(relPath string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] != "application" {
			continue
		}
		root := parts[i+1]
		for _, r := range ci3ModuleRoots {
			if root == r {
				return r, true
			}
		}
	}
	return "", false
}

// ci3Index keys modules for the existence gate. CI3 resolves loader names and
// class extensions case-insensitively on the first letter (it ucfirst's the file
// name), so the gate is keyed by the lower-cased base name within each root.
type ci3Index struct {
	byDir   map[string]map[string]ci3Module // root -> lower(name) -> module
	byClass map[string]ci3Module            // declared class name -> module (for extends)
}

func buildCI3Index(mods []ci3Module) ci3Index {
	idx := ci3Index{
		byDir:   map[string]map[string]ci3Module{},
		byClass: map[string]ci3Module{},
	}
	for _, m := range mods {
		if idx.byDir[m.dir] == nil {
			idx.byDir[m.dir] = map[string]ci3Module{}
		}
		idx.byDir[m.dir][strings.ToLower(m.name)] = m
		if m.className != "" {
			idx.byClass[m.className] = m
		}
	}
	return idx
}

// ci3moduleID is the node identifier used in the dependency graph for a CI3
// module. It is the workspace-relative path so two modules with the same base
// name in different roots (e.g. a model and a library both named "User") never
// collide, while still being human-readable and traceable to a file.
func ci3moduleID(m ci3Module) string { return m.relPath }

// ci3BootstrapNode is the synthetic source node for globally autoloaded modules.
// application/config/autoload.php lists models/libraries the framework loads at
// every request; those loads are not written in any single consumer, so the
// honest model is an edge from this bootstrap node to each globally-loaded
// module (an explicit "the framework wires this in at boot" signal) rather than
// inventing edges into every controller.
const ci3BootstrapNode = "application/config/autoload.php"

// ci3ResolvedEdges produces the convention dependency edges plus the per-module
// cards, both gated by existence (an edge or extends target must resolve to a
// real file under application/). Edge sources:
//   - extends MY_Foo / extends Foo where Foo is a repo class → edge to its file.
//     extends CI_* (framework base) yields no edge.
//   - $this->load->model('x')   → application/models/X.php   (existence-gated).
//   - $this->load->library('y') → application/libraries/Y.php (existence-gated).
//   - application/config/autoload.php arrays of models/libraries → edge from the
//     bootstrap node to each globally-loaded module (existence-gated).
//
// Out of scope (left as honest islands, never faked): get_instance(), load->helper,
// load->view, and dynamic/variable/array/subfolder loads.
func ci3ResolvedEdges(files []phpRawFile, workspacePath string) ([]workerdomain.ResolvedImport, []ci3Module) {
	mods := ci3DiscoverModules(files)
	idx := buildCI3Index(mods)

	seen := make(map[[2]string]bool)
	var out []workerdomain.ResolvedImport
	add := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		k := [2]string{from, to}
		if !seen[k] {
			seen[k] = true
			out = append(out, workerdomain.ResolvedImport{FromModule: from, ToModule: to})
		}
	}

	for _, f := range files {
		if _, ok := ci3DirOf(f.RelPath); !ok {
			continue
		}
		from := f.RelPath

		// extends → edge to the parent class file when it is a repo class.
		// CI_* parents are framework bases and intentionally produce no edge.
		for _, parent := range f.CI3Extends {
			if strings.HasPrefix(parent, "CI_") {
				continue
			}
			if target, ok := idx.byClass[parent]; ok {
				add(from, ci3moduleID(target))
			}
		}

		// load->model('x') / load->library('y') with a string literal.
		for _, ld := range f.CI3Loads {
			root := ""
			switch ld.kind {
			case "model":
				root = "models"
			case "library":
				root = "libraries"
			default:
				continue
			}
			// Subfolder loads (e.g. 'sub/Foo') are out of v1 — skip, never fake.
			if strings.ContainsAny(ld.name, "/\\") {
				continue
			}
			if target, ok := idx.byDir[root][strings.ToLower(ld.name)]; ok {
				add(from, ci3moduleID(target))
			}
		}
	}

	// autoload.php — global model/library registrations.
	for _, name := range ci3ParseAutoload(workspacePath) {
		if target, ok := idx.byDir["models"][strings.ToLower(name)]; ok {
			add(ci3BootstrapNode, ci3moduleID(target))
		}
		if target, ok := idx.byDir["libraries"][strings.ToLower(name)]; ok {
			add(ci3BootstrapNode, ci3moduleID(target))
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].FromModule != out[j].FromModule {
			return out[i].FromModule < out[j].FromModule
		}
		return out[i].ToModule < out[j].ToModule
	})
	return out, mods
}

// ci3ParseAutoload reads application/config/autoload.php and returns the model
// and library names listed in the $autoload['model'] / $autoload['libraries']
// arrays. These are loaded at every request by the framework bootstrap, so each
// is a global dependency. Only plain string-literal array elements are returned;
// dynamic entries are skipped (out of v1 scope). A missing file yields no names.
func ci3ParseAutoload(workspacePath string) []string {
	path := filepath.Join(workspacePath, "application", "config", "autoload.php")
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	parser := sitter.NewParser()
	parser.SetLanguage(php.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil
	}

	var names []string
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		// Match  $autoload['model'] = array(...)  /  $autoload['libraries'] = [...]
		if n.Type() == "assignment_expression" {
			if key := ci3SubscriptKey(n.ChildByFieldName("left"), src); key == "model" || key == "libraries" {
				right := n.ChildByFieldName("right")
				names = append(names, ci3ArrayStringElements(right, src)...)
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())
	return names
}

// ci3SubscriptKey returns the literal string key of a $var['key'] subscript
// expression, or "" when left is not a string-keyed subscript.
func ci3SubscriptKey(left *sitter.Node, src []byte) string {
	if left == nil || left.Type() != "subscript_expression" {
		return ""
	}
	for i := 0; i < int(left.ChildCount()); i++ {
		c := left.Child(i)
		if c.Type() == "string" || c.Type() == "encapsed_string" {
			return phpStringLiteralValue(c, src)
		}
	}
	return ""
}

// ci3ArrayStringElements returns the string-literal elements of an array creation
// expression (both array(...) and [...] forms). Non-string elements are skipped.
func ci3ArrayStringElements(node *sitter.Node, src []byte) []string {
	if node == nil {
		return nil
	}
	var out []string
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Type() == "array_element_initializer" {
			for i := 0; i < int(n.ChildCount()); i++ {
				c := n.Child(i)
				if c.Type() == "string" || c.Type() == "encapsed_string" {
					if v := phpStringLiteralValue(c, src); v != "" {
						out = append(out, v)
					}
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(node)
	return out
}
