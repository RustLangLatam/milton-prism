package repo

import (
	"database/sql"

	"example.com/app/internal/model"

	_ "github.com/lib/pq" // blank import: driver registration side effect
)

// connCount is file-scope mutable state (a shared-state signal).
var connCount int

type PostgresRepo struct {
	db *sql.DB
}

func (r *PostgresRepo) Save(u model.User) error {
	connCount++
	return nil
}

func NewPostgresRepo(db *sql.DB) *PostgresRepo {
	return &PostgresRepo{db: db}
}
