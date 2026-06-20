package repositories

import (
	"context"

	"milton_prism/core/services/articles/ports"

	"go.mongodb.org/mongo-driver/mongo"
)

var _ ports.TransactionManager = (*MongoTransactionManager)(nil)

// MongoTransactionManager wraps Mongo sessions to implement TransactionManager.
// A nil receiver degrades to running the work without a transactional wrapper.
type MongoTransactionManager struct {
	client *mongo.Client
}

func NewMongoTransactionManager(client *mongo.Client) *MongoTransactionManager {
	if client == nil {
		return nil
	}
	return &MongoTransactionManager{client: client}
}

func (m *MongoTransactionManager) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if m == nil || m.client == nil {
		return fn(ctx)
	}
	return m.client.UseSession(ctx, func(sc mongo.SessionContext) error { return fn(sc) })
}
