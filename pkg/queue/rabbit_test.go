package queue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testConfig is a configuration with one queue whose name defaults to its
// handle, which is the minimal shape a project actually writes.
func testConfig() Config {
	return Config{
		User: "guest",
		Pwd:  "guest",
		Host: "localhost",
		Port: "5672",
		Queues: map[string]QueueConfig{
			"ping": {Durable: true, Prefetch: 4},
		},
	}
}

// newTestRabbit returns a client wired to a fake broker.
func newTestRabbit(t *testing.T, cfg Config) (*Rabbit, *fakeConn) {
	t.Helper()

	conn := newFakeConn()
	r, err := newRabbit(cfg, func(dsn string) (amqpConn, error) { return conn, nil })
	require.NoError(t, err)

	t.Cleanup(func() { _ = r.Close() })
	return r, conn
}

func TestNewRabbitReportsADialFailure(t *testing.T) {
	// The old implementation panicked here, which took a worker down whenever it
	// was started before its broker.
	r, err := newRabbit(testConfig(), func(dsn string) (amqpConn, error) { return nil, errDial })

	require.Error(t, err)
	assert.Nil(t, r)
	assert.ErrorIs(t, err, errDial)
}

func TestConfigDSN(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "default vhost",
			cfg:  Config{User: "u", Pwd: "p", Host: "broker", Port: "5672"},
			want: "amqp://u:p@broker:5672/",
		},
		{
			name: "named vhost",
			cfg:  Config{User: "u", Pwd: "p", Host: "broker", Port: "5672", VHost: "staging"},
			want: "amqp://u:p@broker:5672/staging",
		},
		{
			name: "a vhost written with slashes",
			cfg:  Config{User: "u", Pwd: "p", Host: "broker", Port: "5672", VHost: "/staging/"},
			want: "amqp://u:p@broker:5672/staging",
		},
		{
			name: "credentials are escaped",
			cfg:  Config{User: "u@ser", Pwd: "p@ss:word", Host: "broker", Port: "5672"},
			want: "amqp://u%40ser:p%40ss%3Aword@broker:5672/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.DSN())
		})
	}
}

func TestDeclareCreatesEveryConfiguredQueue(t *testing.T) {
	cfg := testConfig()
	cfg.Queues["emails"] = QueueConfig{Name: "mail.outbound", Durable: true}
	cfg.Queues["volatile"] = QueueConfig{Name: "scratch", AutoDelete: true}

	r, conn := newTestRabbit(t, cfg)
	require.NoError(t, r.Declare(t.Context()))

	declared := conn.issuedChannels()[0].declaredCalls()
	require.Len(t, declared, 3)

	byName := map[string]declareCall{}
	for _, call := range declared {
		byName[call.name] = call
	}

	// The name defaults to the handle, and explicit names are respected.
	assert.Contains(t, byName, "ping")
	assert.Contains(t, byName, "mail.outbound")
	assert.Contains(t, byName, "scratch")
	assert.True(t, byName["ping"].durable)
	assert.True(t, byName["scratch"].autoDelete)
}

func TestDeclareRefusesAnEmptyConfiguration(t *testing.T) {
	cfg := testConfig()
	cfg.Queues = nil
	r, _ := newTestRabbit(t, cfg)

	// A worker with nothing to consume is a misconfiguration, not an idle worker.
	assert.ErrorIs(t, r.Declare(t.Context()), ErrNoQueuesConfigured)
}

func TestDeclareReportsABrokerRefusal(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())
	conn.channelFn = func() (*fakeChannel, error) {
		ch := newFakeChannel()
		ch.declareErr = errors.New("PRECONDITION_FAILED")
		return ch, nil
	}

	err := r.Declare(t.Context())

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDeclare)
	assert.Contains(t, err.Error(), "PRECONDITION_FAILED")
}

func TestDeclareRespectsACancelledContext(t *testing.T) {
	r, _ := newTestRabbit(t, testConfig())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	assert.ErrorIs(t, r.Declare(ctx), context.Canceled)
}

func TestPublishSendsToTheConfiguredQueue(t *testing.T) {
	cfg := testConfig()
	cfg.Queues["ping"] = QueueConfig{Exchange: "ping.exchange"}

	r, conn := newTestRabbit(t, cfg)
	require.NoError(t, r.Publish(t.Context(), "ping", []byte("hello")))

	published := conn.issuedChannels()[0].publishedCalls()
	require.Len(t, published, 1)
	assert.Equal(t, "ping.exchange", published[0].key, "the exchange field is the routing key")
	assert.Equal(t, "application/json", published[0].msg.ContentType)
	assert.Equal(t, []byte("hello"), published[0].msg.Body)
}

func TestPublishDefaultsTheContentType(t *testing.T) {
	cfg := testConfig()
	cfg.Queues["ping"] = QueueConfig{ContentType: "text/plain"}

	r, conn := newTestRabbit(t, cfg)
	require.NoError(t, r.Publish(t.Context(), "ping", nil))

	assert.Equal(t, "text/plain", conn.issuedChannels()[0].publishedCalls()[0].msg.ContentType)
}

// TestPublishDoesNotRedeclareTheQueue is the regression for the old behaviour,
// which declared the queue again on every single message.
func TestPublishDoesNotRedeclareTheQueue(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())

	for range 5 {
		require.NoError(t, r.Publish(t.Context(), "ping", []byte("x")))
	}

	for _, ch := range conn.issuedChannels() {
		assert.Empty(t, ch.declaredCalls(), "publishing must not declare anything")
	}
}

func TestPublishRejectsAnUnconfiguredQueue(t *testing.T) {
	r, _ := newTestRabbit(t, testConfig())

	// A typo must not become a message sent nowhere.
	err := r.Publish(t.Context(), "typo", []byte("x"))

	assert.ErrorIs(t, err, ErrQueueNotConfigured)
}

func TestPublishReusesAChannel(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())

	for range 4 {
		require.NoError(t, r.Publish(t.Context(), "ping", []byte("x")))
	}

	// Four messages, one channel: opening a channel is a round trip, so a healthy
	// one is kept rather than thrown away after each publish.
	assert.Len(t, conn.issuedChannels(), 1)
}

func TestPublishReportsABrokerRefusal(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())
	conn.channelFn = func() (*fakeChannel, error) {
		ch := newFakeChannel()
		ch.publishErr = errors.New("channel is closed")
		return ch, nil
	}

	err := r.Publish(t.Context(), "ping", []byte("x"))

	assert.ErrorIs(t, err, ErrPublish)
}

func TestPublishAfterCloseIsRefused(t *testing.T) {
	r, _ := newTestRabbit(t, testConfig())
	require.NoError(t, r.Close())

	assert.ErrorIs(t, r.Publish(t.Context(), "ping", []byte("x")), ErrClosed)
}

func TestPublishOnALostConnectionIsRefused(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())
	require.NoError(t, conn.Close())

	// The connection is never re-established, so the caller is told rather than
	// left to block on a dead socket.
	assert.ErrorIs(t, r.Publish(t.Context(), "ping", []byte("x")), ErrNotConnected)
}

// --- consuming ---

// consumeInBackground runs Consume and reports how it ended.
func consumeInBackground(t *testing.T, r *Rabbit, name string, h Handler) (stop context.CancelFunc, done <-chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)

	go func() { errCh <- r.Consume(ctx, name, h) }()

	return cancel, errCh
}

func TestConsumeAcknowledgesOnSuccess(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())

	// The handler runs on the consumer goroutine, so what it saw is captured
	// rather than asserted in place.
	var seen Delivery
	cancel, done := consumeInBackground(t, r, "ping", func(ctx context.Context, d Delivery) error {
		seen = d
		return nil
	})

	ch := waitForChannel(t, conn)
	ch.deliver("hello")
	waitFor(t, func() bool { return len(ch.ackedTags()) == 1 })

	cancel()
	assert.NoError(t, <-done)

	assert.Equal(t, "ping", seen.Queue)
	assert.Equal(t, []byte("hello"), seen.Body)

	// Manual acknowledgement is the point: a message the handler failed on must
	// not be acked away.
	assert.False(t, ch.lastAutoAck(), "consuming must not auto-acknowledge")
	assert.Equal(t, []uint64{1}, ch.ackedTags())
	assert.Empty(t, ch.nackedCalls())
}

func TestConsumeRequeuesWhenTheHandlerFails(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())

	var mu sync.Mutex
	var reported []error
	r.OnError = func(name string, d Delivery, err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, err)
	}
	boom := errors.New("cannot process")

	cancel, done := consumeInBackground(t, r, "ping", func(ctx context.Context, d Delivery) error {
		return boom
	})

	ch := waitForChannel(t, conn)
	ch.deliver("hello")

	waitFor(t, func() bool { return len(ch.nackedCalls()) == 1 })

	// Stopped before reading anything the consumer goroutine wrote.
	cancel()
	require.NoError(t, <-done)

	mu.Lock()
	defer mu.Unlock()

	// Requeued, not acked and not dropped.
	assert.Equal(t, []nackCall{{tag: 1, requeue: true}}, ch.nackedCalls())
	assert.Empty(t, ch.ackedTags())
	assert.Equal(t, []error{boom}, reported)
}

func TestConsumeKeepsGoingAfterAFailedMessage(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())

	var handled atomic.Int64
	cancel, done := consumeInBackground(t, r, "ping", func(ctx context.Context, d Delivery) error {
		handled.Add(1)
		return errors.New("always fails")
	})

	ch := waitForChannel(t, conn)
	ch.deliver("one")
	ch.deliver("two")

	// One bad message must not take the worker down.
	waitFor(t, func() bool { return handled.Load() == 2 })

	cancel()
	assert.NoError(t, <-done)
}

func TestConsumeAppliesPrefetch(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())
	cancel, done := consumeInBackground(t, r, "ping", func(ctx context.Context, d Delivery) error {
		return nil
	})

	ch := waitForChannel(t, conn)
	waitFor(t, func() bool { return ch.lastPrefetch() == 4 })
	assert.Equal(t, 4, ch.lastPrefetch())

	cancel()
	<-done
}

func TestConsumeSkipsPrefetchWhenUnset(t *testing.T) {
	cfg := testConfig()
	cfg.Queues["ping"] = QueueConfig{Durable: true}

	r, conn := newTestRabbit(t, cfg)
	cancel, done := consumeInBackground(t, r, "ping", func(ctx context.Context, d Delivery) error {
		return nil
	})
	defer func() { cancel(); <-done }()

	// Zero leaves it at the broker default rather than asking for zero.
	assert.Equal(t, 0, waitForChannel(t, conn).lastPrefetch())
}

func TestConsumeUsesAStableConsumerTag(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())
	cancel, done := consumeInBackground(t, r, "ping", func(ctx context.Context, d Delivery) error {
		return nil
	})
	defer func() { cancel(); <-done }()

	assert.Equal(t, "boilerplate-ping", waitForChannel(t, conn).lastConsumerTag())
}

func TestConsumeStopsOnACancelledContext(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())

	// A handler that blocks, so the consumer is genuinely mid flight when the
	// context is cancelled.
	release := make(chan struct{})
	cancel, done := consumeInBackground(t, r, "ping", func(ctx context.Context, d Delivery) error {
		<-release
		return nil
	})

	ch := waitForChannel(t, conn)
	ch.deliver("hello")
	waitFor(t, func() bool { return !ch.isClosed() })

	cancel()
	close(release)

	// A cancelled context closes the channel, which is what unblocks the loop.
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Consume did not return after its context was cancelled")
	}
}

func TestConsumeReportsTheDeliveryChannelClosing(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())

	errCh := make(chan error, 1)
	go func() { errCh <- r.Consume(t.Context(), "ping", func(context.Context, Delivery) error { return nil }) }()

	ch := waitForChannel(t, conn)
	ch.closeDeliveries()

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, ErrConsume)
	case <-time.After(2 * time.Second):
		t.Fatal("Consume did not return after the delivery channel closed")
	}
}

func TestConsumeRejectsAnUnconfiguredQueue(t *testing.T) {
	r, _ := newTestRabbit(t, testConfig())

	assert.ErrorIs(t, r.Consume(t.Context(), "nope", func(context.Context, Delivery) error {
		return nil
	}), ErrQueueNotConfigured)
}

func TestConsumeReportsAnAckFailure(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Consume(t.Context(), "ping", func(context.Context, Delivery) error { return nil })
	}()

	ch := waitForChannel(t, conn)
	ch.ackErr = errors.New("unknown delivery tag")
	ch.deliver("hello")

	select {
	case err := <-errCh:
		// The handler already ran, so the caller has to decide whether to repeat
		// the work.
		assert.ErrorIs(t, err, ErrAck)
	case <-time.After(2 * time.Second):
		t.Fatal("Consume did not return after the acknowledgement failed")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())

	require.NoError(t, r.Close())
	require.NoError(t, r.Close())

	assert.True(t, conn.IsClosed())
}

func TestCloseDrainsPooledChannels(t *testing.T) {
	r, conn := newTestRabbit(t, testConfig())

	// Leave a healthy channel in the pool.
	require.NoError(t, r.Publish(t.Context(), "ping", []byte("x")))
	ch := conn.issuedChannels()[0]

	require.NoError(t, r.Close())
	assert.True(t, ch.isClosed(), "a pooled channel must not outlive the client")
}

// --- the port itself ---

func TestRabbitSatisfiesClient(t *testing.T) {
	var _ Client = (*Rabbit)(nil)
}

func TestDriverErrorsNeverEscape(t *testing.T) {
	// Every driver failure has to be classifiable through a sentinel of this
	// package, so a caller never imports amqp091 to find out what went wrong.
	r, conn := newTestRabbit(t, testConfig())
	conn.channelFn = func() (*fakeChannel, error) {
		ch := newFakeChannel()
		ch.publishErr = amqp.ErrClosed
		return ch, nil
	}

	err := r.Publish(t.Context(), "ping", nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPublish)
	assert.NotErrorIs(t, err, ErrQueueNotConfigured)
}

// --- helpers ---

// waitFor polls until cond holds, so a test never depends on a fixed sleep.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for the condition")
}

// waitForChannel returns the channel the consumer opened, once it exists.
func waitForChannel(t *testing.T, conn *fakeConn) *fakeChannel {
	t.Helper()

	var found *fakeChannel
	waitFor(t, func() bool {
		channels := conn.issuedChannels()
		if len(channels) == 0 {
			return false
		}
		found = channels[0]
		return true
	})
	return found
}
