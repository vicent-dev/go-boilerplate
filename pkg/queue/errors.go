package queue

import "errors"

// Sentinel errors returned by the broker implementation. Callers classify with
// errors.Is; none of them mention the driver, and the driver errors themselves
// are always wrapped around one of these rather than returned bare.
var (
	// ErrNotConnected is returned by every operation attempted before a
	// connection exists, or after it was lost. The connection is never
	// re-established: a retry is the caller's decision.
	ErrNotConnected = errors.New("queue: not connected")
	// ErrClosed is returned once Close has been called.
	ErrClosed = errors.New("queue: client is closed")
	// ErrQueueNotConfigured is returned when publishing to or consuming from a
	// name that is not in the configuration, which is how a typo is caught
	// instead of becoming a message sent nowhere.
	ErrQueueNotConfigured = errors.New("queue: queue is not configured")
	// ErrNoQueuesConfigured is returned by Declare when the configuration has
	// no queues at all, since a worker with nothing to consume is a
	// misconfiguration worth failing on rather than idling silently.
	ErrNoQueuesConfigured = errors.New("queue: no queues configured")
	// ErrDeclare is returned when the broker refuses to declare a queue.
	ErrDeclare = errors.New("queue: cannot declare queue")
	// ErrPublish is returned when a message could not be handed to the broker.
	ErrPublish = errors.New("queue: cannot publish message")
	// ErrAck is returned when a delivery could not be acknowledged or
	// requeued. The handler already ran, so the caller has to decide whether
	// the work should be repeated.
	ErrAck = errors.New("queue: cannot settle delivery")
	// ErrConsume is returned when a consumer could not be started.
	ErrConsume = errors.New("queue: cannot consume queue")
)
