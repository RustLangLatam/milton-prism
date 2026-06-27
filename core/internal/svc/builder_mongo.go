package services

import (
	"milton_prism/core/shared/mongo_client"
)

// builder_mongo.go is the ONLY file in this package that imports
// core/shared/mongo_client. It carries the entire compile-time dependency on the
// MongoDB driver for the store-agnostic Services builder: the Mongo wiring (via the
// mongoInit hook in builder.go) and the typed Mongo() accessor live here.
//
// Every MongoDB deliverable (and the platform monorepo) ships this file, so
// mongoInit is registered and Mongo() is available. A Go+SQL (GORM) deliverable
// PRUNES this file together with core/shared/mongo_client/ and drops the
// go.mongodb.org/mongo-driver require from go.mod (see the assembler): with this
// file gone, builder.go references no mongo type, so the deliverable compiles with
// zero mongo footprint. The GORM repos use their own gorm_client and never call
// Mongo(), so removing the accessor is safe for that cell.
func init() {
	mongoInit = buildMongoClient
}

// buildMongoClient constructs the MongoDB client from config and stores it on the
// Services when MongoDB persistence is configured. It is the body of the former
// inline Mongo branch in initServices, registered into mongoInit by init().
func buildMongoClient(s *Services) error {
	if s.config.Mongo == nil {
		return nil
	}
	client, err := mongo_client.NewClient(s.config.Mongo)
	if err != nil {
		return err
	}
	s.mongo = client
	return nil
}

// Mongo returns the MongoDB client wrapper, or nil when MongoDB persistence is not
// configured. The field is stored as `any` on Services (so builder.go stays
// mongo-free); this accessor performs the typed assertion back to
// *mongo_client.MongoClient for the platform services and Mongo deliverables that
// call it.
func (s *Services) Mongo() *mongo_client.MongoClient {
	if s.mongo == nil {
		return nil
	}
	return s.mongo.(*mongo_client.MongoClient)
}
