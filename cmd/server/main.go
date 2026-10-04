// Command server runs the http API.
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"go-boilerplate/app"
)

func main() {
	// A context cancelled by SIGINT or SIGTERM, so startup can be abandoned and
	// shutdown drains in flight requests instead of dropping them.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	s, err := app.NewServer(ctx)
	if err != nil {
		log.Fatalf("could not start: %v", err)
	}

	if err := s.Run(ctx); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
