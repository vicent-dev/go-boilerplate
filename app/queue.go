package app

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"go-boilerplate/pkg/log"
	"go-boilerplate/pkg/queue"
)

// queueClient returns the shared broker client, connecting on first use.
//
// It is lazy and it retries on the next call after a failure: the http server is
// expected to come up before its broker does during a compose start, and caching
// the failure would keep the application from publishing for the rest of its
// life.
func (s *server) queueClient() (queue.Client, error) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()

	if s.queue != nil {
		return s.queue, nil
	}

	client, err := queue.NewRabbit(s.c.Rabbit)
	if err != nil {
		return nil, err
	}
	s.queue = client

	return client, nil
}

// publish sends a message to a configured queue from a request handler.
func (s *server) publish(ctx context.Context, handle string, body []byte) error {
	client, err := s.queueClient()
	if err != nil {
		return err
	}
	return client.Publish(ctx, handle, body)
}

// closeQueue releases the broker client if one was ever opened.
func (s *server) closeQueue() error {
	s.queueMu.Lock()
	client := s.queue
	s.queue = nil
	s.queueMu.Unlock()

	if client == nil {
		return nil
	}
	return client.Close()
}

// RunConsumer is the worker entry point: it declares the configured queues and
// consumes from all of them until ctx is cancelled.
//
// It is deliberately separate from NewServer. The worker needs a broker and a
// database, but not an http listener, and booting the whole server to consume a
// queue means the worker also opens a port it never serves.
//
// A message that cannot be handled is reported and requeued, which keeps the
// consumer alive: one bad message must not take the worker down.
func RunConsumer(ctx context.Context) error {
	c, err := LoadConfig()
	if err != nil {
		return err
	}

	client, err := queue.NewRabbit(c.Rabbit)
	if err != nil {
		return err
	}
	defer client.Close()

	client.OnError = func(name string, d queue.Delivery, err error) {
		log.LogError(ctx, fmt.Sprintf("handler failed on queue %s (message %s): %v",
			name, d.MessageID, err))
	}

	if err := client.Declare(ctx); err != nil {
		return fmt.Errorf("declare queues: %w", err)
	}

	// The database is opened for the same reason the http server opens it: the
	// use cases a worker runs need persistence. A worker that only transforms
	// messages can drop this block and the CloseDB below.
	db, err := OpenDB(c.DB)
	if err != nil {
		return err
	}
	defer CloseDB(db)

	return runConsumers(ctx, client, c, newConsumerHandlers(db))
}

// newConsumerHandlers is where a project's worker logic plugs in.
//
// It returns one handler per configured queue, keyed by queue handle. A queue
// with no handler is an error rather than a silent skip, because a declared queue
// nobody reads is either a typo or an unfinished feature.
func newConsumerHandlers(db *gorm.DB) map[string]queue.Handler {
	byQueue := make(map[string]queue.Handler)

	// Example: log whatever arrives on the "ping" queue that the /ping route
	// publishes to. Replace this with the domain use cases of your project, and
	// use db to reach the repositories.
	byQueue["ping"] = func(ctx context.Context, d queue.Delivery) error {
		log.LogInfo(ctx, "ping queue received: "+string(d.Body))
		return nil
	}

	return byQueue
}

// runConsumers starts one goroutine per configured queue and returns once they
// have all stopped. The first failure stops the others, since a dead broker or a
// dead connection is not something an individual consumer can recover from.
func runConsumers(ctx context.Context, client queue.Client, c *Config, byQueue map[string]queue.Handler) error {
	if len(c.Rabbit.Queues) == 0 {
		return queue.ErrNoQueuesConfigured
	}

	consumerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(c.Rabbit.Queues))
	var running int

	for handle := range c.Rabbit.Queues {
		handler, ok := byQueue[handle]
		if !ok {
			return fmt.Errorf("queue %q is declared but has no handler", handle)
		}

		running++
		go func(handle string, handler queue.Handler) {
			// Stopping here rather than at the end matters: the delivery loop
			// only unblocks when its channel is closed, and a cancelled context
			// is what closes it.
			defer cancel()

			log.LogInfo(ctx, "consuming queue: "+handle)
			err := client.Consume(consumerCtx, handle, handler)
			// One result per consumer, always: a clean stop has to be reported
			// too, or a cancelled parent context would leave this loop waiting
			// on a result that never comes.
			errCh <- err
		}(handle, handler)
	}

	var firstErr error
	for i := 0; i < running; i++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}
