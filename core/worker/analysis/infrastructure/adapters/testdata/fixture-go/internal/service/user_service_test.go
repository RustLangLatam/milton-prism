package service

import (
	"testing"

	"example.com/app/internal/model" // same intra-repo import from a _test.go file
)

func TestCreate(t *testing.T) {
	_ = model.User{}
}
