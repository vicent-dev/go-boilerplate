package queue

import (
	"context"
	"errors"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// The fakes below stand in for the broker. amqp091-go exposes Connection and
// Channel as structs, so the implementation declares the interfaces it needs and
// these satisfy them; that is the whole reason the package can be tested without
// a running rabbit.

// fakeChannel records what the implementation asked the broker to do.
type fakeChannel struct {
	mu sync.Mutex

	// declared collects the arguments of every QueueDeclare.
	declared []declareCall
	// published collects every PublishWithContext.
	published []publishCall
	// prefetch is the value of the last Qos call.
	prefetch int
	// consumerTags is the value of the last Consume call.
	consumerTags []string
	// autoAck is what the last Consume call asked for. Manual acknowledgement
	// is the whole point of the settle logic, so the tests read it back.
	autoAck bool

	acked  []uint64
	nacked []nackCall

	// deliveries is handed to the caller of Consume.
	deliveries chan amqp.Delivery

	// failures are returned by the matching operation when set.
	declareErr error
	publishErr error
	consumeErr error
	ackErr     error
	nackErr    error
	channelErr error

	closed bool
}

type declareCall struct {
	name       string
	durable    bool
	autoDelete bool
	exclusive  bool
	noWait     bool
}

type publishCall struct {
	exchange string
	key      string
	msg      amqp.Publishing
}

type nackCall struct {
	tag     uint64
	requeue bool
}

func newFakeChannel() *fakeChannel {
	return &fakeChannel{deliveries: make(chan amqp.Delivery, 16)}
}

func (c *fakeChannel) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.declareErr != nil {
		return amqp.Queue{}, c.declareErr
	}
	c.declared = append(c.declared, declareCall{name, durable, autoDelete, exclusive, noWait})
	return amqp.Queue{Name: name}, nil
}

func (c *fakeChannel) Qos(prefetchCount, prefetchSize int, global bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prefetch = prefetchCount
	return nil
}

func (c *fakeChannel) Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.consumeErr != nil {
		return nil, c.consumeErr
	}
	c.consumerTags = append(c.consumerTags, consumer)
	c.autoAck = autoAck
	return c.deliveries, nil
}

func (c *fakeChannel) PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.publishErr != nil {
		return c.publishErr
	}
	c.published = append(c.published, publishCall{exchange: exchange, key: key, msg: msg})
	return nil
}

func (c *fakeChannel) Ack(tag uint64, multiple bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ackErr != nil {
		return c.ackErr
	}
	c.acked = append(c.acked, tag)
	return nil
}

func (c *fakeChannel) Nack(tag uint64, multiple, requeue bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.nackErr != nil {
		return c.nackErr
	}
	c.nacked = append(c.nacked, nackCall{tag: tag, requeue: requeue})
	return nil
}

// Close reports an error once closed, which is how the driver signals that a
// channel is no longer usable.
func (c *fakeChannel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return amqp.ErrClosed
	}
	c.closed = true
	return nil
}

// deliver hands a message to a waiting consumer.
func (c *fakeChannel) deliver(body string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.deliveries <- amqp.Delivery{
		DeliveryTag: 1,
		Body:        []byte(body),
	}
}

// closeDeliveries simulates the broker closing the delivery channel.
func (c *fakeChannel) closeDeliveries() {
	c.mu.Lock()
	defer c.mu.Unlock()
	close(c.deliveries)
}

// snapshot accessors, so a test can read the recorded calls without holding the
// lock itself.
func (c *fakeChannel) declaredCalls() []declareCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]declareCall(nil), c.declared...)
}

func (c *fakeChannel) publishedCalls() []publishCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]publishCall(nil), c.published...)
}

func (c *fakeChannel) ackedTags() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]uint64(nil), c.acked...)
}

func (c *fakeChannel) nackedCalls() []nackCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]nackCall(nil), c.nacked...)
}

func (c *fakeChannel) lastPrefetch() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prefetch
}

func (c *fakeChannel) lastAutoAck() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.autoAck
}

func (c *fakeChannel) lastConsumerTag() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.consumerTags) == 0 {
		return ""
	}
	return c.consumerTags[len(c.consumerTags)-1]
}

func (c *fakeChannel) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// fakeConn hands out fake channels and records whether it was closed.
type fakeConn struct {
	mu        sync.Mutex
	channels  []*fakeChannel
	issued    int
	closed    bool
	channelFn func() (*fakeChannel, error)
}

func newFakeConn() *fakeConn {
	return &fakeConn{}
}

func (c *fakeConn) Channel() (amqpChannel, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, amqp.ErrClosed
	}
	if c.channelFn != nil {
		return c.channelFn()
	}

	c.issued++
	ch := newFakeChannel()
	c.channels = append(c.channels, ch)
	return ch, nil
}

func (c *fakeConn) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return amqp.ErrClosed
	}
	c.closed = true
	return nil
}

// channels returns the channels handed out so far.
func (c *fakeConn) issuedChannels() []*fakeChannel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*fakeChannel(nil), c.channels...)
}

// errDial stands in for a broker that is not there.
var errDial = errors.New("connection refused")
