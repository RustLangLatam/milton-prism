// Package web wires the HTTP surface.
package web

import (
	"net/http"

	"example.com/app/internal/model"
	"example.com/app/internal/service"

	"github.com/gin-gonic/gin"

	. "github.com/google/uuid" // dot import
)

// activeRequests is mutable module-level state.
var activeRequests int

type Server struct {
	svc service.UserService
}

func (s *Server) Register(r *gin.Engine) {
	r.GET("/users/:id", s.getUser)
	r.POST("/users", s.createUser)
	r.DELETE("/users/:id", s.deleteUser)
}

func (s *Server) getUser(c *gin.Context)    {}
func (s *Server) createUser(c *gin.Context) { _ = model.User{} }
func (s *Server) deleteUser(c *gin.Context) {}

// RegisterStdlib uses the net/http mux form.
func RegisterStdlib(mux *http.ServeMux) {
	mux.HandleFunc("/health", healthHandler)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	_ = New() // dot-imported uuid.New
}
