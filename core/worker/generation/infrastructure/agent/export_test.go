package agent

import "time"

// Test exports — exposes unexported helpers for white-box unit tests.
// This file is compiled only during `go test`.

var (
	CaptureArtifacts      = captureArtifacts
	CopyMonorepo          = copyMonorepo
	IsRootLevelBinary     = isRootLevelBinary
	PromptProfileBindings = promptProfileBindings
	StoreSection          = storeSection
	FrameworkSection      = frameworkSection
	VerifyCommandFor      = verifyCommandFor
	SourceToPortSection   = sourceToPortSection
)

// WriteCombinedPrompt preserves the pre-source-to-port arity for existing tests,
// defaulting the new (sourceToPort, previousVerifyStderr) parameters to the empty
// case. Tests that exercise the source-to-port block call writeCombinedPrompt-backed
// helpers directly via SourceToPortSection.
func WriteCombinedPrompt(workspaceDir, generatorPromptRef, serviceName, errorPrefix, outputProfile, protocol, framework, authScheme, authSigAlg, store, boundarySpec, protoContent string) (string, error) {
	return writeCombinedPrompt(workspaceDir, generatorPromptRef, serviceName, errorPrefix, outputProfile, protocol, framework, authScheme, authSigAlg, store, boundarySpec, protoContent, nil, "")
}

// Resource-tier (#6e) test exports.
var (
	HeavyAgentCPUQuota   = int64(heavyAgentCPUQuota)
	HeavyAgentMemory     = heavyAgentMemory
	HeavyAgentTimeout    = heavyAgentTimeout
	DefaultAgentCPUQuota = int64(defaultAgentCPUQuota)
	DefaultAgentMemory   = defaultAgentMemory
	DefaultAgentTimeout  = defaultAgentTimeout
)

// ResourceTierFor exposes resourceTierFor's limits and heavy flag for tests.
func ResourceTierFor(profile string) (cpuQuota, memoryBytes int64, timeout time.Duration, heavy bool) {
	t, h := resourceTierFor(profile)
	return t.cpuQuota, t.memoryBytes, t.timeout, h
}
