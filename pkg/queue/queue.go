// Package queue is the message broker boundary: it declares the configured
// queues, publishes to them and consumes from them. Nothing here knows about
// RabbitMQ beyond the file named rabbit.go, so a project can swap the broker by
// implementing Client.
//
// The port is deliberately small. A consumer is handed a Handler and returns
// when the context is cancelled or the handler fails irrecoverably; the caller
// decides what to run and when to stop.
package queue

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// QueueConfig is the configuration of a single declared queue. It mirrors the
// amqp091 queue arguments one to one, so nothing has to be translated between
// the configuration file and the broker.
type QueueConfig struct {
	Name string `yaml:"name"`
	// Exchange is the routing key used when publishing to this queue. Exchanges
	// themselves are not declared: see Known issues in AGENTS.md.
	Exchange   string `yaml:"exchange"`
	Durable    bool   `yaml:"durable"`
	AutoDelete bool   `yaml:"autoDelete"`
	Exclusive  bool   `yaml:"exclusive"`
	NoWait     bool   `yaml:"noWait"`
	// ContentType is the MIME type stamped on published messages. It defaults
	// to application/json, which is what a consumer of this boilerplate is
	// almost always handling.
	ContentType string `yaml:"contentType"`
	// Prefetch is how many unacknowledged messages the broker hands a consumer
	// at a time. It bounds how much work is in flight per handler and stops a
	// fast consumer from hoarding the queue while a slow one still has to
	// finish. Zero leaves it at the broker default.
	Prefetch int `yaml:"prefetch"`
}

// Config is the broker connection configuration.
type Config struct {
	User   string                 `yaml:"user"`
	Pwd    string                 `yaml:"pwd"`
	Port   string                 `yaml:"port"`
	Host   string                 `yaml:"host"`
	VHost  string                 `yaml:"vhost"`
	Queues map[string]QueueConfig `yaml:"queues"`
}

// DSN renders the amqp connection string.
//
// The vhost is the URL path, and a bare "/" is how the broker is told to use the
// default vhost. Credentials are escaped because a password containing an "@"
// would otherwise produce a URL pointing at the wrong host.
func (c Config) DSN() string {
	path := "/"
	if vhost := strings.Trim(c.VHost, "/"); vhost != "" {
		path = "/" + vhost
	}

	return "amqp://" + url.QueryEscape(c.User) + ":" + url.QueryEscape(c.Pwd) +
		"@" + c.Host + ":" + c.Port + path
}

// withDefaults fills in what the configuration may leave out. The map key is
// the handle used by Publish and Consume, and doubles as the broker side queue
// name when none is given, so the minimal configuration is a single field.
func (q QueueConfig) withDefaults(handle string) QueueConfig {
	if q.Name == "" {
		q.Name = handle
	}
	return q
}

// contentTypeOrDefault resolves the media type stamped on published messages.
func (q QueueConfig) contentTypeOrDefault() string {
	if q.ContentType == "" {
		return "application/json"
	}
	return q.ContentType
}

// consumerTag identifies one consumer of one queue. It is derived from the
// queue name so that a restarted worker reuses the same tag, which keeps the
// broker side of the queue readable.
func (q QueueConfig) consumerTag() string {
	return "boilerplate-" + q.Name
}

// Delivery is one message handed to a Handler. It carries the envelope fields a
// handler usually needs without having to know the driver.
type Delivery struct {
	// Queue is the queue the message came from.
	Queue string
	Body  []byte
	// ContentType and MessageID come from the amqp properties when the
	// publisher set them.
	ContentType string
	MessageID   string
	// DeliveredAt is stamped when the message reached this process, not when it
	// was published, so it is a measure of how long a message waited.
	DeliveredAt time.Time
}

// Handler processes one delivery. Returning nil acknowledges the message;
// returning an error requeues it, so a handler that always fails needs a
// bounded retry strategy of its own (see Known issues in AGENTS.md).
type Handler func(ctx context.Context, d Delivery) error

// Client is the broker port. It is the only thing app/ and cmd/ depend on.
type Client interface {
	// Declare creates every configured queue that does not exist yet. It is
	// safe to call on every start, and must be called before publishing or
	// consuming so the queues exist.
	Declare(ctx context.Context) error
	// Publish sends body to the named queue, which must be one of the
	// configured queues: an unconfigured name is ErrQueueNotConfigured rather
	// than a silent publish to nowhere.
	Publish(ctx context.Context, queue string, body []byte) error
	// Consume delivers messages from the named queue to h until ctx is
	// cancelled. It returns nil on a clean stop.
	Consume(ctx context.Context, queue string, h Handler) error
	// Close releases the connection and every channel opened on it. It is safe
	// to call more than once.
	Close() error
}
