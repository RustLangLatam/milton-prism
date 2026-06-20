package adapters

import (
	"context"
	"errors"

	workerdomain "milton_prism/core/worker/decomposition/domain"
	"milton_prism/core/worker/decomposition/ports"
)

var _ ports.SemanticClusterer = (*LLMClustererStub)(nil)

// ErrLLMClustererNotImplemented is returned by LLMClustererStub.Cluster. The
// pipeline detects this error and falls back to the deterministic adapter with
// a low-confidence flag (spec §3 / stage-3 hole pattern).
var ErrLLMClustererNotImplemented = errors.New("LLM clusterer: not implemented in v1")

// LLMClustererStub is the hole adapter for the LLM-based clustering path.
// It unconditionally returns ErrLLMClustererNotImplemented. Wire it when you
// want to test the fallback path; production wires LouvainClusterer directly.
type LLMClustererStub struct{}

// NewLLMClustererStub returns the no-op LLM clusterer stub.
func NewLLMClustererStub() *LLMClustererStub { return &LLMClustererStub{} }

// Cluster always returns ErrLLMClustererNotImplemented.
func (l *LLMClustererStub) Cluster(
	_ context.Context,
	_ ports.ClusterInput,
) (*workerdomain.ClusteringResult, error) {
	return nil, ErrLLMClustererNotImplemented
}
