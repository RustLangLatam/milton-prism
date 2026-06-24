package main

import (
	"example.com/app/internal/service"
	"example.com/app/web"
)

func main() {
	var _ service.UserService
	var _ web.Server
}
