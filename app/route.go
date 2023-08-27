package app

import (
	"github.com/en-vee/alog"
	"net/http"
	"time"
)

func (s *server) routes() {
	s.r.Use(loggingMiddleware)
	s.r.Use(jsonMiddleware)

	//ping example
	s.r.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		response := make(map[string]interface{})
		response["ping"] = "pong pong"

		err := s.produce("ping", []byte("Ping sent "+time.Now().String()))
		if err != nil {
			alog.Debug(err.Error())
		}

		s.writeResponse(w, response)
	}).Methods("GET")
}
