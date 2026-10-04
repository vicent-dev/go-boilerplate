package log

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContextKeys(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, METHOD_CTX_LOG_KEY, "GET")
	ctx = context.WithValue(ctx, PATH_CTX_LOG_KEY, "/api/test")

	method := ctx.Value(METHOD_CTX_LOG_KEY)
	path := ctx.Value(PATH_CTX_LOG_KEY)

	assert.Equal(t, "GET", method)
	assert.Equal(t, "/api/test", path)
}

// A log line has to name the request it came from, or two concurrent requests
// are indistinguishable after the fact.
func TestLineNamesTheRequest(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, METHOD_CTX_LOG_KEY, "POST")
	ctx = context.WithValue(ctx, PATH_CTX_LOG_KEY, "/auth/login")

	assert.Equal(t, "[POST] - /auth/login: handler failed", line(ctx, "handler failed"))
}

// Most log lines are not about a request at all: startup, shutdown, and every
// single line a worker writes, because a worker has no request to name. Printing
// "<nil> - <nil>" in front of those buries the message.
func TestLineWithoutARequestIsJustTheMessage(t *testing.T) {
	assert.Equal(t, "consumer stopped", line(context.Background(), "consumer stopped"))
	assert.Equal(t, "consumer stopped", line(t.Context(), "consumer stopped"))
}

func TestLineToleratesAHalfStampedContext(t *testing.T) {
	// The middleware stamps method then path, so a log line written in between
	// must not fall over or claim there is no request.
	ctx := context.WithValue(context.Background(), METHOD_CTX_LOG_KEY, "GET")

	assert.Equal(t, "[GET] - <nil>: started", line(ctx, "started"))
}

// The access log emitted by loggingMiddleware has no message of its own.
func TestLogRequestEmitsTheEntry(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, METHOD_CTX_LOG_KEY, "GET")
	ctx = context.WithValue(ctx, PATH_CTX_LOG_KEY, "/ping")

	assert.Equal(t, "[GET] - /ping", entry(ctx))
	assert.Equal(t, "", entry(context.Background()))
}
