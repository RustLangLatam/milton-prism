package pkg

// This file lives under vendor/ and must be skipped by the walker. If the
// walker fails to skip vendor/, the import below would create a spurious edge.
import "example.com/app/internal/model"

var _ = model.User{}
