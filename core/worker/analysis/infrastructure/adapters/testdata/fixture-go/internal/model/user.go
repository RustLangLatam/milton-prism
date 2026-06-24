// Package model holds the core domain entities.
// Second line of the package doc.
package model

import "time"

// User is the primary aggregate.
type User struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// Role is a named string type (not a struct/interface).
type Role string

// Store describes how users are persisted.
type Store interface {
	Save(u User) error
}

func (u User) Display() string {
	return u.Name
}

func New(name string) User {
	return User{Name: name}
}
