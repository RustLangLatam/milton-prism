package repositories

import (
	"context"

	"milton_prism/core/services/identity/ports"

	"go.mongodb.org/mongo-driver/mongo"
)

// MongoTransactionManager satisfies ports.TransactionManager.
type MongoTransactionManager struct {
	client *mongo.Client
}

var _ ports.TransactionManager = (*MongoTransactionManager)(nil)

// NewMongoTransactionManager builds a transaction manager bound to the mongo client.
func NewMongoTransactionManager(client *mongo.Client) *MongoTransactionManager {
	return &MongoTransactionManager{client: client}
}

// WithTransaction starts a session and runs fn within a transaction. When
// sessions are unsupported (standalone clusters) it falls back to running fn
// in the caller's context.
func (m *MongoTransactionManager) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if m == nil || m.client == nil {
		return fn(ctx)
	}
	session, err := m.client.StartSession()
	if err != nil {
		return fn(ctx)
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(sctx mongo.SessionContext) (any, error) {
		return nil, fn(sctx)
	})
	return err
}
