package ports

import "context"

// ProtoArtifact is one generated .proto file destined for the deliverable's
// protobuf tree. Path is workspace-relative (e.g.
// "protobuf/proto/milton_prism/services/articles/v1/articles_service.proto");
// Content is the raw UTF-8 proto source.
type ProtoArtifact struct {
	Path    string
	Content []byte
}

// OpenAPIGenerator produces a single, merged OpenAPI 3 document from a set of
// generated service protos. It is language-agnostic: the spec is derived from
// the protos alone, so the same document is emitted regardless of the
// migration's OutputProfile.
//
// Implementations run `buf generate` with the deliverable OpenAPI template
// (protobuf/buf.deliverable.openapi.yaml) inside the generation-agent image,
// which carries the protoc-gen-openapi plugin on PATH.
type OpenAPIGenerator interface {
	// Generate writes protoArtifacts into a fresh workspace copied from
	// workspaceBase (the monorepo root on the host), runs buf to produce
	// docs/openapi.yaml, and returns the generated document's bytes.
	//
	// workspaceBase is the same monorepo root passed to AgentInvoker.Invoke.
	// protoArtifacts are the newly generated service/type protos for the
	// migration; they overwrite any same-path file copied from the base.
	Generate(ctx context.Context, workspaceBase string, protoArtifacts []ProtoArtifact) ([]byte, error)
}
