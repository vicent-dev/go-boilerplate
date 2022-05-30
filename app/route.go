package app

import "net/http"

func (s *server) routes() {
	s.r.Use(loggingMiddleware)
	s.r.Use(jsonMiddleware)

	//ping example
	s.r.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{\"ping\": \"pong\"}"))
	}).Methods("GET")
}
