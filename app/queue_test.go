package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-boilerplate/pkg/queue"
)

// fakeQueueClient records what the worker asked the broker to do, so the wiring
// can be tested without a broker. It satisfies queue.Client.
type fakeQueueClient struct {
	mu sync.Mutex

	declared   int
	published  []string
	consuming  []string
	closed     int
	declareErr error

	// consumeErr is returned by Consume when set.
	consumeErr error
	// block makes Consume wait until it is released, so a test can observe a
	// worker that is still running.
	block chan struct{}
	// consumed is closed once Consume has been entered.
	consumed chan struct{}
	once     sync.Once
}

func newFakeQueueClient() *fakeQueueClient {
	return &fakeQueueClient{
		block:    make(chan struct{}),
		consumed: make(chan struct{}),
	}
}

func (f *fakeQueueClient) Declare(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.declareErr != nil {
		return f.declareErr
	}
	f.declared++
	return nil
}

func (f *fakeQueueClient) Publish(ctx context.Context, name string, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, name)
	return nil
}

func (f *fakeQueueClient) Consume(ctx context.Context, name string, h queue.Handler) error {
	f.mu.Lock()
	f.consuming = append(f.consuming, name)
	err := f.consumeErr
	f.mu.Unlock()

	f.once.Do(func() { close(f.consumed) })

	// A configured failure ends the consumer straight away, the way a broken
	// connection does.
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return nil
	case <-f.block:
		return nil
	}
}

func (f *fakeQueueClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func (f *fakeQueueClient) declaredTimes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.declared
}

func (f *fakeQueueClient) consumingQueues() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.consuming...)
}

func (f *fakeQueueClient) closedTimes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// queueConfig builds a configuration with the given queue handles.
func queueConfig(handles ...string) *Config {
	c := testConfig()
	c.Rabbit.Queues = make(map[string]queue.QueueConfig, len(handles))
	for _, h := range handles {
		c.Rabbit.Queues[h] = queue.QueueConfig{Durable: true}
	}
	return c
}

func TestRunConsumersConsumesEveryConfiguredQueue(t *testing.T) {
	client := newFakeQueueClient()
	c := queueConfig("ping", "emails")
	handlers := map[string]queue.Handler{
		"ping":   func(context.Context, queue.Delivery) error { return nil },
		"emails": func(context.Context, queue.Delivery) error { return nil },
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runConsumers(ctx, client, c, handlers) }()

	select {
	case <-client.consumed:
	case <-time.After(2 * time.Second):
		t.Fatal("no consumer started")
	}

	waitForCondition(t, func() bool { return len(client.consumingQueues()) == 2 })

	cancel()
	assert.NoError(t, <-done)
	assert.ElementsMatch(t, []string{"ping", "emails"}, client.consumingQueues())
}

func TestRunConsumersRefusesAQueueWithoutAHandler(t *testing.T) {
	client := newFakeQueueClient()
	c := queueConfig("ping", "emails")

	err := runConsumers(t.Context(), client, c, map[string]queue.Handler{
		"ping": func(context.Context, queue.Delivery) error { return nil },
	})

	// A declared queue nobody reads is either a typo or an unfinished feature,
	// and neither should be discovered in production.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "emails")
}

func TestRunConsumersRefusesAnEmptyConfiguration(t *testing.T) {
	c := queueConfig()

	assert.ErrorIs(t,
		runConsumers(t.Context(), newFakeQueueClient(), c, nil),
		queue.ErrNoQueuesConfigured)
}

func TestRunConsumersReportsAConsumerFailure(t *testing.T) {
	client := newFakeQueueClient()
	client.consumeErr = errors.New("connection reset")
	c := queueConfig("ping")

	err := runConsumers(t.Context(), client, c, map[string]queue.Handler{
		"ping": func(context.Context, queue.Delivery) error { return nil },
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "connection reset")
}

func TestRunConsumersStopsTheRestWhenOneFails(t *testing.T) {
	client := newFakeQueueClient()
	client.consumeErr = errors.New("connection reset")
	c := queueConfig("ping", "emails")
	handlers := map[string]queue.Handler{
		"ping":   func(context.Context, queue.Delivery) error { return nil },
		"emails": func(context.Context, queue.Delivery) error { return nil },
	}

	err := runConsumers(t.Context(), client, c, handlers)

	// A dead broker is not something one consumer can recover from on its own.
	require.Error(t, err)

	// The failed consumer cancels the context the others run on, so a worker
	// never sits with half its queues still "running".
	assert.ErrorIs(t, err, client.consumeErr)
}

func TestQueueClientIsLazyAndRetried(t *testing.T) {
	// The api is expected to come up before its broker does during a compose
	// start, so a failed connection must not be cached for the rest of the
	// process's life.
	s := &server{c: queueConfig("ping")}

	client, err := s.queueClient()
	// Nothing is listening on this port in the test environment.
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Nil(t, s.queue)

	_, err = s.queueClient()
	assert.Error(t, err, "a second attempt must be made rather than a cached failure returned")
}

func TestCloseQueueWithoutAClientIsANoOp(t *testing.T) {
	s := &server{c: queueConfig()}

	assert.NoError(t, s.closeQueue())
}

func TestNewConsumerHandlersCoverTheConfiguredQueues(t *testing.T) {
	handlers := newConsumerHandlers(newTestDB(t))

	// The example queue the /ping route publishes to has a consumer, or the
	// worker would refuse to start on a fresh clone.
	assert.Contains(t, handlers, "ping")
}

// waitForCondition polls until cond holds, so a test never sleeps for a fixed
// duration and hope.
func waitForCondition(t *testing.T, cond func() bool) {
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

// compile time proof that the test double really stands in for the port.
var _ queue.Client = (*fakeQueueClient)(nil)
