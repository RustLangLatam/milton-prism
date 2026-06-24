package assembler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveProtoImportClosure proves the deliverable's proto set is made
// self-contained: transitive imports of the shipped protos (here user_service →
// pagination → openapiv3/annotations) are pulled from the canonical skeleton tree,
// while imports already present and imports with no canonical source are left as-is.
func TestResolveProtoImportClosure(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, "protobuf", "proto", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Canonical skeleton tree carries the transitive deps.
	write("milton_prism/types/pagination/v1/pagination.proto", `import "openapiv3/annotations.proto";`)
	write("openapiv3/annotations.proto", `import "openapiv3/OpenAPIv3.proto";`)
	write("openapiv3/OpenAPIv3.proto", `syntax="proto3";`)

	// merged ships only the service proto, which imports pagination (transitively
	// pulling openapiv3) and a google WKT that is already vendored.
	merged := map[string][]byte{
		"protobuf/proto/milton_prism/services/user/v1/user_service.proto": []byte(
			`import "milton_prism/types/pagination/v1/pagination.proto";
import "google/protobuf/timestamp.proto";`),
		"protobuf/proto/google/protobuf/timestamp.proto": []byte(`syntax="proto3";`),
	}
	resolveProtoImportClosure(merged, root)

	for _, want := range []string{
		"protobuf/proto/milton_prism/types/pagination/v1/pagination.proto",
		"protobuf/proto/openapiv3/annotations.proto",
		"protobuf/proto/openapiv3/OpenAPIv3.proto",
	} {
		if _, ok := merged[want]; !ok {
			t.Errorf("import closure missing %q", want)
		}
	}
	// The already-vendored google WKT is untouched, and nothing fabricated for it.
	if _, ok := merged["protobuf/proto/google/protobuf/timestamp.proto"]; !ok {
		t.Error("present proto wrongly dropped")
	}
}

// TestIsCargoBuildArtifact_CargoHomeVariants proves the DEFECT 4b fix: a
// workspace-local cargo home named .cargo-home (or cargo-home) — the convention
// mig67 used via CARGO_HOME=$workspace/.cargo-home, which persisted 12983 registry
// files — is recognised as cargo build output and dropped at assembly, alongside
// the established .cargo / target / .rustup / lockfile cases.
func TestIsCargoBuildArtifact_CargoHomeVariants(t *testing.T) {
	drop := []string{
		".cargo-home/registry/src/index.crates.io-1949cf8c6b5b557f/anyhow-1.0.102/src/lib.rs",
		".cargo-home/registry/index/index.crates.io-1949cf8c6b5b557f/.cache/2/cc",
		"rust/.cargo-home/registry/cache/x.crate",
		"cargo-home/registry/src/foo/lib.rs",
		// established cases stay covered.
		".cargo/registry/src/abc/tokio/lib.rs",
		"rust/target/debug/user",
		"Cargo.lock",
		"rust/services/user/target/debug/deps/user.rlib",
	}
	for _, p := range drop {
		if !isCargoBuildArtifact(p) {
			t.Errorf("isCargoBuildArtifact(%q) = false, want true (cargo bloat must be dropped)", p)
		}
	}
	keep := []string{
		"rust/services/user/src/main.rs",
		"rust/services/registry/src/main.rs", // a service legitimately named "registry"
		"rust/services/user/Cargo.toml",
		"rust/services/user/build.rs",
	}
	for _, p := range keep {
		if isCargoBuildArtifact(p) {
			t.Errorf("isCargoBuildArtifact(%q) = true, want false (real source must survive)", p)
		}
	}
}

// TestStripProtoIncludeFromBuildRs_VendorMarkers proves the build.rs rewriter
// drops the per-service vendored-proto include path for all three conventions the
// agent has used — proto_include/, third_party/ (mig68) and proto_vendor/ (mig67) —
// so tonic-build resolves the google deps via the canonical protobuf/proto include
// root alone after the vendored copies are relocated.
func TestStripProtoIncludeFromBuildRs_VendorMarkers(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{
			name: "mig68 third_party PathBuf binding",
			in: `fn main() -> Result<(), Box<dyn std::error::Error>> {
    let proto_root = PathBuf::from("../../../protobuf/proto");
    let service_proto = proto_root.join("milton_prism/services/user/v1/user_service.proto");
    let third_party = PathBuf::from("third_party");
    tonic_build::configure()
        .compile_protos(
            &[service_proto.to_str().unwrap()],
            &[proto_root.to_str().unwrap(), third_party.to_str().unwrap()],
        )?;
    Ok(())
}`,
		},
		{
			name: "mig67 proto_vendor str binding",
			in: `fn main() -> Result<(), Box<dyn std::error::Error>> {
    let proto_root = "../../../protobuf/proto";
    let wkt_include = "../../proto_vendor";
    tonic_build::configure()
        .compile_protos(
            &["../../../protobuf/proto/milton_prism/services/user/v1/user_service.proto"],
            &[proto_root, wkt_include],
        )?;
    Ok(())
}`,
		},
		{
			name: "legacy proto_include inline literal",
			in: `fn main() {
    tonic_build::configure()
        .compile_protos(&["x.proto"], &[proto_root, "proto_include"]).unwrap();
}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, changed := stripProtoIncludeFromBuildRs(tc.in)
			if !changed {
				t.Fatalf("expected build.rs to be rewritten")
			}
			for _, marker := range []string{"third_party", "proto_vendor", "proto_include"} {
				if strings.Contains(out, marker) {
					t.Errorf("rewritten build.rs still references vendored include %q:\n%s", marker, out)
				}
			}
			// proto_root (canonical include) must survive.
			if !strings.Contains(out, "proto_root") {
				t.Errorf("rewritten build.rs lost the canonical proto_root include:\n%s", out)
			}
		})
	}
}

// TestRelocateRustVendoredProtos_AllMarkers proves vendored .proto under any of the
// three marker dirs are relocated to canonical protobuf/proto/<import-path>, the
// per-service copies are dropped, and build.rs is rewritten — for both the
// per-service (rust/services/user/third_party/) and workspace-root
// (rust/proto_vendor/) layouts.
func TestRelocateRustVendoredProtos_AllMarkers(t *testing.T) {
	merged := map[string][]byte{
		"rust/services/user/third_party/google/api/http.proto":        []byte("syntax=\"proto3\";\n"),
		"rust/proto_vendor/google/protobuf/timestamp.proto":           []byte("syntax=\"proto3\";\n"),
		"rust/services/user/proto_include/google/api/annotations.proto": []byte("syntax=\"proto3\";\n"),
		"rust/services/user/build.rs": []byte(`fn main() {
    let proto_root = PathBuf::from("../../../protobuf/proto");
    let third_party = PathBuf::from("third_party");
    tonic_build::configure().compile_protos(&["x"], &[proto_root.to_str().unwrap(), third_party.to_str().unwrap()]).unwrap();
}`),
		"rust/services/user/src/main.rs": []byte("fn main() {}\n"),
	}
	relocateRustVendoredProtos(merged)

	for p := range merged {
		if strings.HasPrefix(p, "rust/") && strings.HasSuffix(p, ".proto") {
			t.Errorf("vendored proto still under rust/: %q", p)
		}
	}
	want := []string{
		"protobuf/proto/google/api/http.proto",
		"protobuf/proto/google/protobuf/timestamp.proto",
		"protobuf/proto/google/api/annotations.proto",
	}
	for _, w := range want {
		if _, ok := merged[w]; !ok {
			t.Errorf("vendored proto not relocated to canonical path %q", w)
		}
	}
	if strings.Contains(string(merged["rust/services/user/build.rs"]), "third_party") {
		t.Errorf("build.rs still references third_party:\n%s", merged["rust/services/user/build.rs"])
	}
}

// TestIsNodeVendoredProto proves the Node third_party/proto leak detector matches
// vendored protos (both the node/ and core/ rooted forms) while leaving canonical
// protobuf/proto and committed TS untouched.
func TestIsNodeVendoredProto(t *testing.T) {
	drop := []string{
		"node/third_party/proto/google/api/http.proto",
		"node/third_party/proto/milton_prism/services/billing/v1/billing_service.proto",
		"core/third_party/proto/openapiv3/OpenAPIv3.proto",
		// mig21 vendored under node/proto/ (a different dir name) — must also drop.
		"node/proto/google/api/http.proto",
		"node/proto/milton_prism/services/user/v1/user_service.proto",
		"core/proto/openapiv3/OpenAPIv3.proto",
	}
	for _, p := range drop {
		if !isNodeVendoredProto(p) {
			t.Errorf("isNodeVendoredProto(%q) = false, want true", p)
		}
	}
	keep := []string{
		// Only the canonical protobuf/proto tree survives.
		"protobuf/proto/milton_prism/services/user/v1/user_service.proto",
		"protobuf/proto/google/api/http.proto",
		"node/gen/google/api/Http.ts",
		"node/package.json",
	}
	for _, p := range keep {
		if isNodeVendoredProto(p) {
			t.Errorf("isNodeVendoredProto(%q) = true, want false", p)
		}
	}
}

// TestRewriteNodeGenProtoScript proves the gen:proto npm script's include path is
// repointed from the dropped third_party/proto tree to the canonical
// ../protobuf/proto (relative to core/).
func TestRewriteNodeGenProtoScript(t *testing.T) {
	cases := []string{
		// mig65 form: -I third_party/proto + third_party/proto/… file arg.
		`{"scripts":{"gen:proto":"proto-loader-gen-types --outDir=gen/ third_party/proto/milton_prism/services/user/v1/user_service.proto -I third_party/proto"}}`,
		// mig21 form: --includeDirs=proto + bare proto/… file arg.
		`{"scripts":{"gen:user":"proto-loader-gen-types --includeDirs=proto --outDir=gen proto/milton_prism/services/user/v1/user_service.proto"}}`,
	}
	for _, in := range cases {
		out, changed := rewriteNodeGenProtoScript(in)
		if !changed {
			t.Fatalf("expected gen script to be rewritten: %s", in)
		}
		if strings.Contains(out, "third_party/proto") {
			t.Errorf("gen script still references third_party/proto: %s", out)
		}
		// No standalone vendored `proto` include should remain (only ../protobuf/proto).
		if strings.Contains(out, "=proto ") || strings.Contains(out, " proto/") || strings.Contains(out, "-I proto") {
			t.Errorf("gen script still references bare proto dir: %s", out)
		}
		if !strings.Contains(out, "../protobuf/proto") {
			t.Errorf("gen script not repointed to canonical protobuf/proto: %s", out)
		}
	}
}

// TestPrunePyprojectMotorDep proves the motor dependency line is removed from
// [tool.poetry.dependencies] while other deps survive.
func TestPrunePyprojectMotorDep(t *testing.T) {
	merged := map[string][]byte{
		"python/pyproject.toml": []byte(`[tool.poetry.dependencies]
python = "^3.12"
motor = "^3.6.0"
pydantic = "^2.10.0"
`),
	}
	prunePyprojectMotorDep(merged)
	out := string(merged["python/pyproject.toml"])
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "motor ") {
			t.Errorf("motor dep not removed: %q", line)
		}
	}
	if !strings.Contains(out, `pydantic = "^2.10.0"`) {
		t.Error("pydantic dep wrongly removed")
	}
}
