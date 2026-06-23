package application

import (
	"testing"

	migrationv1 "milton_prism/pkg/pb/gen/milton_prism/types/migration/v1"
)

// TestOutputProfileLabel_Node proves the Node target language maps to the "node"
// output profile (in lockstep with domain.IsGenerableLanguage and the assembler),
// and that Go/Python/default behaviour is unchanged.
func TestOutputProfileLabel_Node(t *testing.T) {
	cases := []struct {
		name string
		tc   *migrationv1.TargetConfig
		want string
	}{
		{"nil", nil, "go"},
		{"go", &migrationv1.TargetConfig{Language: migrationv1.TargetLanguage_TARGET_LANGUAGE_GO}, "go"},
		{"python", &migrationv1.TargetConfig{Language: migrationv1.TargetLanguage_TARGET_LANGUAGE_PYTHON}, "python"},
		{"node", &migrationv1.TargetConfig{Language: migrationv1.TargetLanguage_TARGET_LANGUAGE_NODE}, "node"},
		{"rust", &migrationv1.TargetConfig{Language: migrationv1.TargetLanguage_TARGET_LANGUAGE_RUST}, "rust"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := outputProfileLabel(tc.tc); got != tc.want {
				t.Errorf("outputProfileLabel(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestGeneratorPromptRef_Node proves the "node" profile resolves to the Node
// generator prompt document, and Go/Python are unchanged.
func TestGeneratorPromptRef_Node(t *testing.T) {
	cases := map[string]string{
		"go":      "docs/prism/milton-prism-service-generator-prompt.md",
		"python":  "docs/prism/milton-prism-service-generator-prompt-python.md",
		"node":    "docs/prism/milton-prism-service-generator-prompt-node.md",
		"rust":    "docs/prism/milton-prism-service-generator-prompt-rust.md",
		"unknown": "docs/prism/milton-prism-service-generator-prompt.md",
	}
	for profile, want := range cases {
		t.Run(profile, func(t *testing.T) {
			if got := generatorPromptRef(profile, migrationv1.Transport_TRANSPORT_GRPC); got != want {
				t.Errorf("generatorPromptRef(%q) = %q, want %q", profile, got, want)
			}
		})
	}
}

// TestProfileSourceRoot proves the viewer's source-root rename mapping is in
// lockstep with the assembler: python→python, node→node, everything else "".
func TestProfileSourceRoot(t *testing.T) {
	cases := map[string]string{
		"python": "python",
		"node":   "node",
		"rust":   "rust",
		"go":     "",
		"":       "",
	}
	for profile, want := range cases {
		if got := profileSourceRoot(profile); got != want {
			t.Errorf("profileSourceRoot(%q) = %q, want %q", profile, got, want)
		}
	}
}

// TestSourceRootToCorePath proves the artifact-viewer path rename rewrites the
// node/ (and python/) source root to core/, mirroring the assembler step 3b, and
// leaves protos/docs and Go paths untouched.
func TestSourceRootToCorePath(t *testing.T) {
	cases := []struct {
		path, root, want string
	}{
		{"node/services/user/index.ts", "node", "core/services/user/index.ts"},
		{"node", "node", "core"},
		{"rust/services/user/src/main.rs", "rust", "core/services/user/src/main.rs"},
		{"rust", "rust", "core"},
		{"python/services/user/main.py", "python", "core/services/user/main.py"},
		{"protobuf/proto/user/v1/user.proto", "node", "protobuf/proto/user/v1/user.proto"},
		{"docs/openapi.yaml", "node", "docs/openapi.yaml"},
		{"core/services/user/wire.go", "", "core/services/user/wire.go"},
		// A path that merely starts with the root name but is not the root segment.
		{"nodexyz/x.ts", "node", "nodexyz/x.ts"},
	}
	for _, tc := range cases {
		if got := sourceRootToCorePath(tc.path, tc.root); got != tc.want {
			t.Errorf("sourceRootToCorePath(%q, %q) = %q, want %q", tc.path, tc.root, got, tc.want)
		}
	}
}
