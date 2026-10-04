package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"go-boilerplate/pkg/auth"
	"go-boilerplate/pkg/log"
	"go-boilerplate/pkg/queue"
)

// shutdownTimeout is how long in flight requests get to finish once a shutdown
// signal arrives.
const shutdownTimeout = 10 * time.Second

// server is the http side of the application: the router, the configuration and
// the infrastructure the handlers are built from.
//
// Its fields are deliberately the shared resources only. Anything a single
// handler needs is built where it is used, out of these, as the auth service is
// in authService.
type server struct {
	r  *mux.Router
	c  *Config
	db *gorm.DB

	// queue is the broker client, opened on first use by queueClient and closed
	// on shutdown. See app/queue.go.
	queue   queue.Client
	queueMu sync.Mutex

	httpServer http.Server

	// tlsCertFile and tlsKeyFile are the PEM paths the listener serves TLS
	// with. Both are empty when TLS is off, and neither can be empty on its own
	// when it is on: newServer refuses to build a server otherwise.
	tlsCertFile string
	tlsKeyFile  string
}

// NewServer loads the configuration and builds a fully wired application:
// postgres for persistence and the auth domain on top of it. ctx bounds the
// startup database check, so a hung database cannot keep the process in the
// starting state indefinitely.
func NewServer(ctx context.Context) (*server, error) {
	c, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	return newServer(ctx, c)
}

// newServer wires an application from an already loaded configuration. Tests use
// it to skip the embedded file.
func newServer(ctx context.Context, c *Config) (*server, error) {
	s := &server{
		r: mux.NewRouter(),
		c: c,
	}

	// Resolved before anything is opened: an environment asking for TLS without
	// shipping certificates has to fail here, not on the first connection.
	certFile, keyFile, err := c.TLSFiles()
	if err != nil {
		return nil, err
	}
	s.tlsCertFile = certFile
	s.tlsKeyFile = keyFile

	if err := s.openDatabase(ctx); err != nil {
		return nil, err
	}

	s.routes()

	s.httpServer = http.Server{
		Addr:              s.c.Server.Addr(),
		Handler:           handlers.RecoveryHandler()(s.r),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s, nil
}

// openDatabase validates the auth configuration and connects postgres. Failing
// here, rather than on the first request, is what keeps the server from coming up
// wired to nothing.
//
// It stops there on purpose: the auth service is built by each handler, out of
// the configuration and the connection this leaves behind.
func (s *server) openDatabase(ctx context.Context) error {
	if err := s.c.AuthConfig().Validate(); err != nil {
		return fmt.Errorf("auth configuration: %w", err)
	}

	db, err := OpenDB(s.c.DB)
	if err != nil {
		return err
	}
	s.db = db

	// gorm.Open is lazy: it does not wait for the database. Without this the
	// server would start cleanly and then fail the first request that happens to
	// touch a table.
	pingCtx, cancel := context.WithTimeout(ctx, dbPingTimeout)
	defer cancel()

	if err := pingDB(pingCtx, db); err != nil {
		_ = CloseDB(db)
		s.db = nil
		return fmt.Errorf("ping postgres at %s: %w", s.c.DB.Host, err)
	}

	return nil
}

// authService builds the auth domain for a handler. The service is stateless and
// the store is a view over s.db, so there is nothing to share between handlers.
func (s *server) authService() *auth.Service {
	return auth.NewService(s.c.AuthConfig(), auth.NewGormStore(s.db), nil)
}

// Run serves until ctx is cancelled, then drains in flight requests and closes
// the database.
func (s *server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		if err := s.serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	<-ctx.Done()
	log.LogInfo(ctx, "shutting down http server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	shutdownErr := s.httpServer.Shutdown(shutdownCtx)
	if err := s.closeQueue(); err != nil {
		log.LogWarn(shutdownCtx, "closing broker connection: "+err.Error())
	}
	if err := CloseDB(s.db); err != nil {
		log.LogWarn(shutdownCtx, "closing database: "+err.Error())
	}

	if shutdownErr != nil {
		return fmt.Errorf("http server shutdown: %w", shutdownErr)
	}

	return <-errCh
}

// serve blocks on the listener, over TLS when the environment says to. The
// certificate paths are already known to be set in that case: newServer refuses
// to build a server whose TLS configuration is incomplete, so there is nothing
// here that can quietly fall back to plaintext.
func (s *server) serve() error {
	if s.c.TLSEnabled() {
		return s.httpServer.ListenAndServeTLS(s.tlsCertFile, s.tlsKeyFile)
	}
	return s.httpServer.ListenAndServe()
}

// Router exposes the router, so a test can drive the application without
// binding a port.
func (s *server) Router() *mux.Router {
	return s.r
}

// writeErrorResponse renders a JSON error body. The Content-Type is set here
// rather than relying on a middleware, so an error is still JSON when a route
// was reached outside the json subrouter.
func writeErrorResponse(w http.ResponseWriter, response map[string]any, errorCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(errorCode)
	byteResponse, _ := json.Marshal(response)
	_, _ = w.Write(byteResponse)
}
