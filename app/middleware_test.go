package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-boilerplate/pkg/auth"
	"go-boilerplate/pkg/log"
)

func TestLoggingMiddleware(t *testing.T) {
	var seenMethod, seenPath string

	handler := loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod, _ = r.Context().Value(log.METHOD_CTX_LOG_KEY).(string)
		seenPath, _ = r.Context().Value(log.PATH_CTX_LOG_KEY).(string)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/me", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// The point of the middleware: a log line written downstream can name the
	// request it belongs to.
	assert.Equal(t, http.MethodPost, seenMethod)
	assert.Equal(t, "/api/me", seenPath)
}

func TestJsonMiddleware(t *testing.T) {
	var contentType string

	handler := jsonMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = w.Header().Get("Content-Type")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))

	assert.Equal(t, "application/json", contentType)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestUnauthorizedResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	unauthorizedResponse(rec, httptest.NewRequest(http.MethodGet, "/api/me", nil),
		auth.ErrTokenExpired)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	// Deliberately vague: the client learns it is not allowed in, and nothing
	// about why.
	assert.JSONEq(t, `{"error":"unauthorized"}`, rec.Body.String())
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

// --- rate limiting ---

// requestFrom builds a request as if it came from addr.
func requestFrom(addr string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = addr
	return req
}

func okHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}

func TestRateLimitThrottlesOneAddress(t *testing.T) {
	// A sustained rate far below one request per second, so the burst is what is
	// being spent rather than the rate.
	handler := rateLimitMiddleware(RateLimitConfig{Enabled: true, RPS: 0.01, Burst: 3})(okHandler())

	for i := range 3 {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, requestFrom("10.0.0.1:1111"))
		assert.Equal(t, http.StatusOK, rec.Code, "request %d should pass", i)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestFrom("10.0.0.1:1111"))
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.JSONEq(t, `{"error":"too many requests"}`, rec.Body.String())
}

// TestRateLimitIsPerAddress is the regression that matters: one shared bucket
// would let a single client lock every other user out.
func TestRateLimitIsPerAddress(t *testing.T) {
	handler := rateLimitMiddleware(RateLimitConfig{Enabled: true, RPS: 0.01, Burst: 1})(okHandler())

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, requestFrom("10.0.0.1:1111"))
	assert.Equal(t, http.StatusOK, first.Code)

	// The same address is now throttled...
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, requestFrom("10.0.0.1:2222"))
	assert.Equal(t, http.StatusTooManyRequests, second.Code)

	// ...while a different one is untouched.
	other := httptest.NewRecorder()
	handler.ServeHTTP(other, requestFrom("10.0.0.2:3333"))
	assert.Equal(t, http.StatusOK, other.Code)
}

func TestRateLimitDisabledIsTransparent(t *testing.T) {
	called := false
	handler := rateLimitMiddleware(RateLimitConfig{Enabled: false})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestFrom("10.0.0.1:1111"))

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestIPRateLimiterSweepsIdleAddresses(t *testing.T) {
	limiter := newIPRateLimiter(RateLimitConfig{RPS: 1, Burst: 1, IdleTTL: time.Minute})

	now := time.Now()
	limiter.now = func() time.Time { return now }

	// Addresses that stop sending must not accumulate for ever, or rotating
	// source addresses becomes a way to exhaust memory.
	for i := range 100 {
		assert.True(t, limiter.allow(fmt.Sprintf("10.0.%d.%d:1111", i/256, i%256)))
	}
	assert.Equal(t, 100, limiter.size())

	// Far enough ahead for the ttl to have elapsed.
	limiter.now = func() time.Time { return now.Add(2 * time.Minute) }
	assert.True(t, limiter.allow("10.9.9.9:1111"))

	// The hundred old ones are gone; the new arrival is not.
	assert.Equal(t, 1, limiter.size())
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "address and port", addr: "10.0.0.1:5555", want: "10.0.0.1"},
		{name: "bare address", addr: "10.0.0.1", want: "10.0.0.1"},
		{name: "ipv6 with port", addr: "[::1]:5555", want: "::1"},
		{name: "ipv6 bare", addr: "::1", want: "::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, clientIP(requestFrom(tt.addr)))
		})
	}
}
