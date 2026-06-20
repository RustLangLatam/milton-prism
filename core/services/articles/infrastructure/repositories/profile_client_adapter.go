package repositories

import (
	"context"

	"milton_prism/core/services/articles/ports"
)

var _ ports.ProfileClient = (*NoOpProfileClient)(nil)

// NoOpProfileClient satisfies the ProfileClient port without a live profile service.
// Replace with a real gRPC adapter once the profile service is deployed.
type NoOpProfileClient struct{}

func NewNoOpProfileClient() *NoOpProfileClient { return &NoOpProfileClient{} }

func (*NoOpProfileClient) ValidateProfileExists(_ context.Context, _ uint64) error { return nil }
