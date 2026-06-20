package ports

import "context"

// TransactionManager is the driven port for atomic units of work.
type TransactionManager interface {
	WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}
