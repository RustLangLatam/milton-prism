package agent

// Test exports — exposes unexported helpers for white-box unit tests.
// This file is compiled only during `go test`.

var (
	CaptureArtifacts      = captureArtifacts
	CopyMonorepo          = copyMonorepo
	IsRootLevelBinary     = isRootLevelBinary
	WriteCombinedPrompt   = writeCombinedPrompt
	PromptProfileBindings = promptProfileBindings
)
