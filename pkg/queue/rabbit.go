package queue

import (
	"context"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// amqpChannel is the slice of *amqp.Channel this package uses.
//
// amqp091-go exposes Connection and Channel as structs, not interfaces, so they
// cannot be faked directly. Declaring the methods here is what lets the whole
// package be tested without a running broker, and it keeps the surface small
// enough to read in one screen.
type amqpChannel interface {
	QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error)
	Qos(prefetchCount, prefetchSize int, global bool) error
	Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error)
	PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error
	Ack(tag uint64, multiple bool) error
	Nack(tag uint64, multiple, requeue bool) error
	Close() error
}

// amqpConn is the slice of a broker connection this package uses.
type amqpConn interface {
	Channel() (amqpChannel, error)
	IsClosed() bool
	Close() error
}

// amqpConnection adapts *amqp.Connection to amqpConn: the driver's Channel
// returns a concrete *amqp.Channel, while the pool wants an interface it can
// wrap.
type amqpConnection struct {
	*amqp.Connection
}

func (c amqpConnection) Channel() (amqpChannel, error) {
	ch, err := c.Connection.Channel()
	if err != nil {
		return nil, err
	}
	return ch, nil
}

// dialFunc opens a connection. It is a field so a test can connect without a
// broker.
type dialFunc func(dsn string) (amqpConn, error)

// Rabbit is the amqp091 implementation of Client.
//
// A connection is opened once and never re-established: if it drops, operations
// return ErrNotConnected and the process is left to be restarted by whatever
// supervises it. That simplification is deliberate, and recorded in AGENTS.md.
type Rabbit struct {
	cfg Config

	// OnError is called when a handler fails, after the message has been
	// requeued. This package does not log: what a failed message means is the
	// application's business, so it is injected. A nil OnError is silent.
	OnError func(queue string, d Delivery, err error)

	mu     sync.Mutex
	conn   amqpConn
	closed bool

	pool channelPool
}

// compile time proof that the implementation satisfies the port.
var _ Client = (*Rabbit)(nil)

// timeNow is swapped in tests to make delivery timestamps predictable.
var timeNow = func() time.Time { return time.Now() }

// NewRabbit dials the broker described by cfg. A dial failure is returned
// rather than panicked on, so a worker started before its broker fails to start
// instead of taking the whole process down.
func NewRabbit(cfg Config) (*Rabbit, error) {
	return newRabbit(cfg, func(dsn string) (amqpConn, error) {
		conn, err := amqp.Dial(dsn)
		if err != nil {
			return nil, err
		}
		return amqpConnection{conn}, nil
	})
}

func newRabbit(cfg Config, dial dialFunc) (*Rabbit, error) {
	conn, err := dial(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("dial broker: %w", err)
	}

	return &Rabbit{
		cfg:  cfg,
		conn: conn,
		pool: newChannelPool(conn),
	}, nil
}

// Declare creates every configured queue that is missing. Declaring a queue that
// already exists with the same arguments is a no-op on the broker, so this is
// safe to call on every start.
func (r *Rabbit) Declare(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if len(r.cfg.Queues) == 0 {
		return ErrNoQueuesConfigured
	}

	conn, err := r.live()
	if err != nil {
		return err
	}

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDeclare, err)
	}
	defer ch.Close()

	for name, q := range r.cfg.Queues {
		q = q.withDefaults(name)
		if _, err := ch.QueueDeclare(q.Name, q.Durable, q.AutoDelete, q.Exclusive, q.NoWait, nil); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrDeclare, q.Name, err)
		}
	}

	return nil
}

// Publish sends body to a configured queue.
//
// It borrows a channel from a pool rather than declaring the queue again on
// every message: opening a channel is a round trip to the broker, and a single
// channel cannot be used by concurrent publishers.
func (r *Rabbit) Publish(ctx context.Context, name string, body []byte) error {
	cfg, err := r.configured(name)
	if err != nil {
		return err
	}

	// Checked here as well as in configured: a connection can drop between the
	// two, and without this the pool would surface the driver's own error and a
	// lost connection would be indistinguishable from a rejected message.
	if _, err := r.live(); err != nil {
		return err
	}

	ch, err := r.pool.acquire()
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrPublish, name, err)
	}
	defer r.pool.release(ch)

	if err := ch.PublishWithContext(ctx, "", cfg.Exchange, false, false, amqp.Publishing{
		ContentType: cfg.contentTypeOrDefault(),
		Timestamp:   timeNow(),
		Body:        body,
	}); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrPublish, name, err)
	}

	return nil
}

// Consume delivers messages from a configured queue to h until ctx is cancelled.
//
// Acknowledgement is manual: a message is acked only after h has returned
// successfully, and requeued when h fails. The tradeoff is that a handler which
// always fails requeues its message forever, so a handler that can fail
// permanently needs a bounded retry of its own.
func (r *Rabbit) Consume(ctx context.Context, name string, h Handler) error {
	cfg, err := r.configured(name)
	if err != nil {
		return err
	}

	conn, err := r.live()
	if err != nil {
		return err
	}

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrConsume, name, err)
	}
	defer ch.Close()

	if cfg.Prefetch > 0 {
		if err := ch.Qos(cfg.Prefetch, 0, false); err != nil {
			return fmt.Errorf("%w: %s: prefetch: %w", ErrConsume, name, err)
		}
	}

	msgs, err := ch.Consume(cfg.Name, cfg.consumerTag(), false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrConsume, name, err)
	}

	// Only a channel close unblocks the loop below, since the delivery channel
	// stays open until then. So a cancelled context has to close the channel
	// for the shutdown to actually reach us.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			_ = ch.Close()
		case <-stopped:
		}
	}()

	for {
		select {
		case <-ctx.Done():
			// In flight deliveries are left unacked on purpose: the broker hands
			// them to whoever picks the queue up next, which beats acking work
			// that was interrupted half way.
			return nil

		case msg, open := <-msgs:
			if !open {
				// The broker closed the delivery channel. A cancelled context is
				// the expected reason; anything else means the connection went
				// away underneath us.
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("%w: %s: delivery channel closed", ErrConsume, name)
			}

			if err := r.handle(ctx, name, ch, msg, h); err != nil {
				return err
			}
		}
	}
}

// handle runs h for one delivery and settles the message according to the
// outcome. Only settlement problems are returned as errors: a failing handler is
// an expected outcome that has already been requeued and reported.
func (r *Rabbit) handle(ctx context.Context, name string, ch amqpChannel, msg amqp.Delivery, h Handler) error {
	d := Delivery{
		Queue:       name,
		Body:        msg.Body,
		ContentType: msg.ContentType,
		MessageID:   msg.MessageId,
		DeliveredAt: timeNow(),
	}

	if err := h(ctx, d); err != nil {
		if nackErr := ch.Nack(msg.DeliveryTag, false, true); nackErr != nil {
			return fmt.Errorf("%w: %s: requeue after handler error (%v): %w",
				ErrAck, name, err, nackErr)
		}
		if r.OnError != nil {
			r.OnError(name, d, err)
		}
		return nil
	}

	if err := ch.Ack(msg.DeliveryTag, false); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrAck, name, err)
	}
	return nil
}

// Close releases the connection and every pooled channel. It is idempotent.
func (r *Rabbit) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	conn := r.conn
	r.mu.Unlock()

	r.pool.drain()
	if conn == nil || conn.IsClosed() {
		return nil
	}
	return conn.Close()
}

// configured looks a queue up by its handle, filling in the broker side name
// from the handle so a minimal configuration needs only one field.
func (r *Rabbit) configured(name string) (QueueConfig, error) {
	if err := r.closedState(); err != nil {
		return QueueConfig{}, err
	}

	cfg, ok := r.cfg.Queues[name]
	if !ok {
		return QueueConfig{}, fmt.Errorf("%w: %s", ErrQueueNotConfigured, name)
	}
	return cfg.withDefaults(name), nil
}

// live returns the connection, refusing to hand out a closed one.
func (r *Rabbit) live() (amqpConn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, ErrClosed
	}
	if r.conn == nil || r.conn.IsClosed() {
		return nil, ErrNotConnected
	}
	return r.conn, nil
}

func (r *Rabbit) closedState() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	return nil
}

// maxPooledChannels bounds how many channels are kept alive between publishes.
const maxPooledChannels = 8

// channelPool hands out one channel per concurrent caller.
//
// Opening a channel costs a round trip, so healthy channels are recycled; a
// single channel cannot be shared by concurrent publishers, which is what makes
// a pool necessary rather than one long lived channel behind a mutex.
type channelPool struct {
	conn amqpConn
	free chan *trackedChannel
}

// trackedChannel remembers whether it has been closed, because the driver offers
// no non destructive way to ask a channel if it is still usable.
type trackedChannel struct {
	amqpChannel
	mu     sync.Mutex
	closed bool
}

func newChannelPool(conn amqpConn) channelPool {
	return channelPool{
		conn: conn,
		free: make(chan *trackedChannel, maxPooledChannels),
	}
}

func (p *channelPool) acquire() (amqpChannel, error) {
	select {
	case ch := <-p.free:
		if ch.isOpen() {
			return ch, nil
		}
		// Dropped by a previous release as broken: open a replacement.
		return p.open()
	default:
		return p.open()
	}
}

// release keeps a healthy channel for the next publisher and closes a broken
// one, so a channel the broker tore down is never handed out again.
func (p *channelPool) release(ch amqpChannel) {
	tracked, ok := ch.(*trackedChannel)
	if !ok || !tracked.isOpen() {
		return
	}

	select {
	case p.free <- tracked:
	default:
		// The pool is full, so this channel is surplus. Close it instead of
		// letting a burst grow the pool without bound.
		_ = tracked.Close()
	}
}

func (p *channelPool) open() (*trackedChannel, error) {
	ch, err := p.conn.Channel()
	if err != nil {
		return nil, err
	}
	return &trackedChannel{amqpChannel: ch}, nil
}

// drain closes every pooled channel.
func (p *channelPool) drain() {
	for {
		select {
		case ch := <-p.free:
			_ = ch.Close()
		default:
			return
		}
	}
}

func (c *trackedChannel) isOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed
}

// Close is idempotent, so the pool can close a channel it already dropped
// without the driver reporting a spurious error.
func (c *trackedChannel) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	return c.amqpChannel.Close()
}
