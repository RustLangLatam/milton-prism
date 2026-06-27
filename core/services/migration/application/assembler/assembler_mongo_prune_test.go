package assembler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadSkeleton reads python/pyproject.toml + python/poetry.lock from the canonical
// monorepo root so the prune is validated against the REAL shipped files.
func skeletonRoot(t *testing.T) string {
	// test runs from the package dir; walk up to repo root (has go.mod).
	dir, _ := os.Getwd()
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate repo root")
	return ""
}

func TestPrunePoetryLockMongo_RealSkeleton(t *testing.T) {
	root := skeletonRoot(t)
	lock, err := os.ReadFile(filepath.Join(root, "python/poetry.lock"))
	if err != nil {
		t.Skipf("no skeleton poetry.lock: %v", err)
	}
	merged := map[string][]byte{"python/poetry.lock": lock}
	prunePoetryLockMongo(merged)
	got := string(merged["python/poetry.lock"])
	for _, pkg := range []string{`name = "motor"`, `name = "pymongo"`, `name = "dnspython"`, `name = "mongomock"`} {
		if strings.Contains(got, pkg) {
			t.Errorf("poetry.lock still contains %q after prune", pkg)
		}
	}
	// Sanity: a non-mongo package must survive and metadata must remain.
	if !strings.Contains(got, "[metadata]") {
		t.Error("metadata table dropped")
	}
	if !strings.Contains(got, `name = "fastapi"`) {
		t.Error("unrelated package fastapi was removed")
	}
}

func TestPrunePoetryLockMongo_KeepsWhenImported(t *testing.T) {
	root := skeletonRoot(t)
	lock, err := os.ReadFile(filepath.Join(root, "python/poetry.lock"))
	if err != nil {
		t.Skip()
	}
	merged := map[string][]byte{
		"python/poetry.lock":        lock,
		"python/services/x/repo.py": []byte("import motor.motor_asyncio\n"),
	}
	prunePoetryLockMongo(merged)
	got := string(merged["python/poetry.lock"])
	if !strings.Contains(got, `name = "motor"`) {
		t.Error("motor removed despite a source import (safety belt failed)")
	}
}

func TestPrunePythonMongoMypyOverrides_RealSkeleton(t *testing.T) {
	root := skeletonRoot(t)
	py, err := os.ReadFile(filepath.Join(root, "python/pyproject.toml"))
	if err != nil {
		t.Skip()
	}
	merged := map[string][]byte{"python/pyproject.toml": py}
	// Production order: the motor dependency line is dropped first, then the mypy
	// overrides. After both, no mongo token (not even in a comment) must remain.
	prunePyprojectMotorDep(merged)
	prunePythonMongoMypyOverrides(merged)
	got := strings.ToLower(string(merged["python/pyproject.toml"]))
	for _, tok := range []string{"motor", "mongomock", "mongo_client"} {
		if strings.Contains(got, tok) {
			t.Errorf("pyproject still contains mongo token %q after prune", tok)
		}
	}
	// Non-mongo overrides preserved.
	if !strings.Contains(got, "grpc.*") || !strings.Contains(got, "shared.interceptors.interceptors") {
		t.Error("non-mongo mypy overrides were lost")
	}
}

func TestPruneNodeMongoDeps(t *testing.T) {
	pkg := `{
  "dependencies": {
    "@grpc/grpc-js": "1.10.9",
    "mongodb": "6.12.0",
    "@prisma/client": "5.22.0"
  },
  "devDependencies": {
    "@types/mongodb": "4.0.0",
    "typescript": "5.5.4"
  }
}
`
	merged := map[string][]byte{"node/package.json": []byte(pkg)}
	pruneNodeMongoDeps(merged)
	got := string(merged["node/package.json"])
	if strings.Contains(got, "mongodb") {
		t.Errorf("node package.json still references mongodb:\n%s", got)
	}
	if !strings.Contains(got, "@prisma/client") || !strings.Contains(got, "@grpc/grpc-js") {
		t.Error("non-mongo deps lost")
	}
	// Must remain valid JSON: no trailing comma before a closing brace.
	if strings.Contains(got, ",\n  }") || strings.Contains(got, ",\n}") {
		t.Errorf("dangling trailing comma left, invalid JSON:\n%s", got)
	}
}

func TestPruneNodeMongoDeps_KeepsWhenImported(t *testing.T) {
	pkg := `{
  "dependencies": {
    "mongoose": "8.0.0"
  }
}
`
	merged := map[string][]byte{
		"node/package.json":       []byte(pkg),
		"node/services/x/repo.ts": []byte("import mongoose from 'mongoose';\n"),
	}
	pruneNodeMongoDeps(merged)
	if !strings.Contains(string(merged["node/package.json"]), "mongoose") {
		t.Error("mongoose removed despite source import")
	}
}

func TestPruneCargoMongoDeps(t *testing.T) {
	ws := `[workspace.dependencies]
tonic = "0.12.3"
mongodb = "3"
sea-orm = { version = "1.1" }
`
	member := `[dependencies]
shared = { path = "../../shared" }
mongodb.workspace = true
sea-orm.workspace = true
`
	merged := map[string][]byte{
		"rust/Cargo.toml":               []byte(ws),
		"rust/services/user/Cargo.toml": []byte(member),
	}
	pruneCargoMongoDeps(merged)
	for p, c := range merged {
		if strings.Contains(string(c), "mongodb") {
			t.Errorf("%s still references mongodb:\n%s", p, c)
		}
	}
	if !strings.Contains(string(merged["rust/Cargo.toml"]), "sea-orm") {
		t.Error("sea-orm workspace dep lost")
	}
	if !strings.Contains(string(merged["rust/services/user/Cargo.toml"]), "sea-orm.workspace") {
		t.Error("member sea-orm dep lost")
	}
}

func TestPruneCargoMongoDeps_KeepsWhenImported(t *testing.T) {
	merged := map[string][]byte{
		"rust/Cargo.toml":           []byte("[workspace.dependencies]\nmongodb = \"3\"\n"),
		"rust/shared/src/client.rs": []byte("use mongodb::Client;\n"),
	}
	pruneCargoMongoDeps(merged)
	if !strings.Contains(string(merged["rust/Cargo.toml"]), "mongodb") {
		t.Error("mongodb crate removed despite a .rs using it")
	}
}
