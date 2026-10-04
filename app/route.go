package app

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"go-boilerplate/pkg/auth"
	"go-boilerplate/pkg/log"
)

// routes wires every endpoint of the application.
//
// There are three tiers, and where a handler is registered decides what it
// requires:
//
//   - public routes, registered on the root router;
//   - the /auth subrouter, which is public because it is how a caller obtains a
//     token in the first place, and rate limited because it is the only part of
//     the application worth guessing at;
//   - the /api subrouter, which requires a verified access token.
//
// Domain handlers belong on the /api subrouter. That is the whole extension
// point of this boilerplate: write the use case in pkg/, then register it here
// and it arrives already authenticated.
func (s *server) routes() {
	s.r.Use(loggingMiddleware)

	// Public. A liveness probe that needs no token and no database.
	s.r.HandleFunc("/ping", s.pingHandler()).Methods(http.MethodGet)

	// Public, rate limited: register, login, refresh, logout.
	authR := s.r.PathPrefix("/auth").Subrouter()
	authR.Use(jsonMiddleware, rateLimitMiddleware(s.c.RateLimit))
	authR.HandleFunc("/register", s.registerHandler()).Methods(http.MethodPost)
	authR.HandleFunc("/login", s.loginHandler()).Methods(http.MethodPost)
	authR.HandleFunc("/logout", s.logoutHandler()).Methods(http.MethodPost)
	authR.HandleFunc("/refresh", s.refreshHandler()).Methods(http.MethodPost)

	// Authenticated. Everything a project adds goes here.
	api := s.r.PathPrefix("/api").Subrouter()
	api.Use(jsonMiddleware, s.authMiddleware)
	api.HandleFunc("/ping", s.protectedPingHandler()).Methods(http.MethodGet)
	api.HandleFunc("/me", s.meHandler()).Methods(http.MethodGet)

	s.registerDomainRoutes(api)
}

// registerDomainRoutes is where a project adds its own endpoints.
//
// It is a separate method so the wiring above stays readable, and it receives
// the authenticated subrouter: a handler registered here can rely on there being
// a principal in its request context.
func (s *server) registerDomainRoutes(api *mux.Router) {}

// pingHandler answers a liveness probe. It also publishes a message when a queue
// named "ping" is configured, so a fresh clone can see the broker round trip
// without writing any code.
func (s *server) pingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, configured := s.c.Rabbit.Queues["ping"]; configured {
			if err := s.publish(r.Context(), "ping", []byte("ping "+time.Now().String())); err != nil {
				// The broker being unavailable is not the caller's problem, so
				// the probe still answers and the reason goes to the log.
				log.LogWarn(r.Context(), "publishing ping: "+err.Error())
			}
		}

		writeResponse(w, map[string]any{"ping": "pong pong"})
	}
}

// protectedPingHandler is the example of a route that requires a token.
func (s *server) protectedPingHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeResponse(w, map[string]any{"ping": "pong pong", "authenticated": true})
	}
}

// meHandler returns the account behind the access token. It is the quickest way
// for a client to confirm who it is signed in as, and the shape a profile
// endpoint wants.
func (s *server) meHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromRequest(r)
		if !ok {
			// Unreachable: the route sits behind the auth middleware, so this
			// only fires if that middleware is ever removed.
			writeErrorResponse(w, map[string]any{"error": "unauthorized"}, http.StatusUnauthorized)
			return
		}

		user, err := s.authService().User(r.Context(), principal.UserID)
		if err != nil {
			writeAuthError(w, r, err)
			return
		}

		writeResponse(w, map[string]any{"user": user})
	}
}

// requestMeta describes the caller for the session records the auth domain
// stores, so a session can be traced back to the client that created it.
func requestMeta(r *http.Request) auth.RequestMeta {
	return auth.RequestMeta{
		UserAgent: r.UserAgent(),
		IP:        clientIP(r),
	}
}

// writeResponse renders a successful JSON body.
func writeResponse(w http.ResponseWriter, response map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	byteResponse, _ := json.Marshal(response)
	_, _ = w.Write(byteResponse)
}
