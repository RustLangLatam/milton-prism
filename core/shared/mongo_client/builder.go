// Package mongo_client provides a MongoDB client builder that initialises
// connection pooling, authentication, and TLS from service configuration.
package mongo_client

import (
	"context"
	"fmt"
	"net/url"
	"milton_prism/pkg/config"
	"milton_prism/pkg/log"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoClient struct {
	config *config.MongoDbCfg
	client *mongo.Client
	once   sync.Once
}

// NewClient creates and returns a configured MongoDB client
func NewClient(cfg *config.MongoDbCfg) (*MongoClient, error) {
	log.Info("MongoDB initializing client")
	defer log.Info("MongoDB initializing client - completed")

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid MongoDB config: %w", err)
	}

	c := &MongoClient{config: cfg}
	if err := c.connect(); err != nil {
		return nil, err
	}

	host, err := ExtractHostPortFromMongoURI(cfg.URI)
	if err != nil {
		log.Errorf("Warning: failed to extract host: %v", err)
	}

	// Logging each configuration parameter separately
	log.Infof("MongoDB Host: %s", host)
	log.Infof("MongoDB Database: %s", cfg.Database)
	log.Infof("MongoDB ConnectTimeout: %s", cfg.ConnectTimeout)
	log.Infof("MongoDB SocketTimeout: %s", cfg.SocketTimeout)
	log.Infof("MongoDB MaxPoolSize: %d", cfg.MaxPoolSize)
	log.Infof("MongoDB MinPoolSize: %d", cfg.MinPoolSize)
	log.Infof("MongoDB HeartbeatInterval: %s", cfg.HeartbeatInterval)
	log.Infof("MongoDB ServerSelectionTimeout: %s", cfg.ServerSelectionTimeout)
	log.Infof("MongoDB RetryWrites: %v", cfg.RetryWrites)
	log.Infof("MongoDB RetryReads: %v", cfg.RetryReads)
	log.Infof("MongoDB Monitor Enabled: %v", cfg.Monitor)

	return c, nil
}

func (c *MongoClient) connect() error {
	var err error
	c.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), c.config.ConnectTimeout)
		defer cancel()

		clientOpts := options.Client().
			ApplyURI(c.config.URI).
			SetMaxPoolSize(c.config.MaxPoolSize).
			SetMinPoolSize(c.config.MinPoolSize).
			SetSocketTimeout(c.config.SocketTimeout).
			SetConnectTimeout(c.config.ConnectTimeout).
			SetServerSelectionTimeout(c.config.ServerSelectionTimeout).
			SetRetryWrites(c.config.RetryWrites).
			SetRetryReads(c.config.RetryReads).
			SetHeartbeatInterval(c.config.HeartbeatInterval)

		//if c.config.Monitor {
		//	clientOpts.Monitor = &monitor
		//}

		c.client, err = mongo.Connect(ctx, clientOpts)
		if err != nil {
			err = fmt.Errorf("error connecting to MongoDB: %w", err)
			return
		}

		err = c.client.Ping(ctx, nil)
		if err != nil {
			err = fmt.Errorf("error pinging MongoDB: %w", err)
		}
	})
	return err
}

// GetDatabase returns the mongo.Database instance
func (c *MongoClient) GetDatabase() *mongo.Database {
	return c.client.Database(c.config.Database)
}

// GetClient returns the underlying *mongo.Client. It is used by infrastructure
// adapters (e.g. transaction managers) that need access to the session API.
func (c *MongoClient) GetClient() *mongo.Client {
	return c.client
}

// Disconnect closes the MongoDB connection
func (c *MongoClient) Disconnect(ctx context.Context) error {
	return c.client.Disconnect(ctx)
}

// ExtractHostPortFromMongoURI extracts "host:port" from a MongoDB URI.
// Example input:  mongodb://user:pass@host:port/db
// Output:         host:port
func ExtractHostPortFromMongoURI(uri string) (string, error) {
	// Replace mongodb:// with http:// to use net/url parsing
	parsedURI, err := url.Parse(strings.Replace(uri, "mongodb://", "http://", 1))
	if err != nil {
		return "", fmt.Errorf("invalid MongoDB URI: %w", err)
	}

	// Extract host:port
	if parsedURI.Host == "" {
		return "", fmt.Errorf("no host found in URI")
	}

	return parsedURI.Host, nil
}
