package ports

import "context"

// TransactionManager wraps a unit of work in a storage transaction.
// When the underlying store does not support transactions the implementation
// must run fn in the caller's context without error.
type TransactionManager interface {
	WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}
