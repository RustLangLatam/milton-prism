package service

import (
	"fmt"

	"example.com/app/internal/model"
	repository "example.com/app/internal/repo" // aliased import

	"github.com/google/uuid"
)

// UserService coordinates user use-cases.
type UserService struct {
	store repository.PostgresRepo
}

func (s *UserService) Create(name string) (model.User, error) {
	u := model.New(name)
	u.ID = uuid.NewString()
	fmt.Println("created", u.ID)
	return u, nil
}
