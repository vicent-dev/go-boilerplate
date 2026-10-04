package log

import (
	"context"
	"fmt"

	"github.com/en-vee/alog"
)

type contextLogKey string

const METHOD_CTX_LOG_KEY = contextLogKey("method")
const PATH_CTX_LOG_KEY = contextLogKey("path")

// entry renders the request a log line belongs to, or the empty string when
// there is no request to name.
func entry(ctx context.Context) string {
	method := ctx.Value(METHOD_CTX_LOG_KEY)
	path := ctx.Value(PATH_CTX_LOG_KEY)

	if method == nil && path == nil {
		return ""
	}

	return fmt.Sprintf("[%v] - %v", method, path)
}

// line prefixes a message with its request when there is one.
//
// Without this every line outside a request — startup, shutdown, and all of a
// worker's output, since a worker never has one — opened with "<nil> - <nil>:",
// which buries the message and reads like a bug.
func line(ctx context.Context, l string) string {
	if e := entry(ctx); e != "" {
		return fmt.Sprintf("%s: %s", e, l)
	}

	return l
}

func LogError(ctx context.Context, l string) {
	alog.Error(line(ctx, l))
}

func LogWarn(ctx context.Context, l string) {
	alog.Warn(line(ctx, l))
}

func LogInfo(ctx context.Context, l string) {
	alog.Info(line(ctx, l))
}

// LogRequest writes the access log. It has no message of its own: the entry is
// the whole line.
func LogRequest(ctx context.Context) {
	if e := entry(ctx); e != "" {
		alog.Info(e)
	}
}
