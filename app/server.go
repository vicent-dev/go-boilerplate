package app

import (
	"net/http"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
)

type server struct {
	r  *mux.Router
	c  *config
	db *gorm.DB
}

func NewServer() *server {
	s := server{
		c: loadConfig(),
		r: mux.NewRouter(),
	}

	s.database()
	s.routes()

	return &s
}

func (s *server) Run() error {
	return http.ListenAndServe(":"+s.c.Server.Port, handlers.RecoveryHandler()(s.r))
}
