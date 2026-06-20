package ports

import "context"

// TransactionManager wraps a function in a unit of work.
type TransactionManager interface {
	WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}
