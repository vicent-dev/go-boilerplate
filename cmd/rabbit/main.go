// Command rabbit runs the queue consumers: it declares the configured queues and
// processes their messages until it is signalled to stop.
//
// It is a separate binary from the API on purpose. A worker scales differently
// from a web server, and a worker should not need a listening port.
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"go-boilerplate/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.RunConsumer(ctx); err != nil {
		log.Fatalf("consumer stopped: %v", err)
	}
}
