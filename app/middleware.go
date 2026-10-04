package app

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"go-boilerplate/pkg/auth"
	"go-boilerplate/pkg/log"
)

// loggingMiddleware stamps the method and path onto the request context, so
// every log line written downstream can name the request it belongs to, and
// emits the access log once the handler has returned.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), log.METHOD_CTX_LOG_KEY, r.Method)
		ctx = context.WithValue(ctx, log.PATH_CTX_LOG_KEY, r.URL.Path)

		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)

		log.LogRequest(ctx)
	})
}

// jsonMiddleware declares the response type of a subrouter.
func jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// authMiddleware guards a subrouter with a verified access token.
//
// The authentication itself lives in pkg/auth, which decides who the caller is;
// this package only decides how a rejection is rendered.
func (s *server) authMiddleware(next http.Handler) http.Handler {
	return auth.RequireAuth(s.authService(), unauthorizedResponse)(next)
}

func unauthorizedResponse(w http.ResponseWriter, r *http.Request, err error) {
	log.LogInfo(r.Context(), "unauthorized: "+err.Error())
	writeErrorResponse(w, map[string]any{"error": "unauthorized"}, http.StatusUnauthorized)
}

// rateLimitMiddleware throttles requests per client address, which is what makes
// it a defence against credential stuffing rather than a global speed bump.
//
// It is applied to the auth endpoints only: those are the ones worth guessing
// against, and a login form is also the request a user is most likely to retry.
func rateLimitMiddleware(cfg RateLimitConfig) func(http.Handler) http.Handler {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}

	limiter := newIPRateLimiter(cfg)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.allow(clientIP(r)) {
				log.LogWarn(r.Context(), "rate limited: "+r.URL.Path)
				writeErrorResponse(w, map[string]any{"error": "too many requests"}, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ipRateLimiter holds one token bucket per client address.
//
// Buckets are created on first sight and swept once they have been idle for
// IdleTTL, so an attacker rotating source addresses cannot pin memory for ever.
type ipRateLimiter struct {
	limit rate.Limit
	burst int
	ttl   time.Duration

	mu      sync.Mutex
	buckets map[string]*ipBucket

	// now is swapped in tests so sweeping does not depend on wall clock time.
	now func() time.Time
	// lastSweep is when the bucket map was last pruned.
	lastSweep time.Time
}

type ipBucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

// sweepEvery bounds how often the map is walked. Pruning on every request would
// make the limiter as expensive as the handler it guards.
const sweepEvery = time.Minute

func newIPRateLimiter(cfg RateLimitConfig) *ipRateLimiter {
	cfg = cfg.withDefaults()
	return &ipRateLimiter{
		limit:     rate.Limit(cfg.RPS),
		burst:     cfg.Burst,
		ttl:       cfg.IdleTTL,
		buckets:   make(map[string]*ipBucket),
		now:       time.Now,
		lastSweep: time.Now(),
	}
}

func (l *ipRateLimiter) allow(ip string) bool {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) >= sweepEvery {
		l.sweepLocked(now)
		l.lastSweep = now
	}

	bucket, ok := l.buckets[ip]
	if !ok {
		bucket = &ipBucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[ip] = bucket
	}
	bucket.seen = now

	return bucket.limiter.Allow()
}

// sweepLocked drops buckets that have not been seen for longer than the ttl.
func (l *ipRateLimiter) sweepLocked(now time.Time) {
	for ip, bucket := range l.buckets {
		if now.Sub(bucket.seen) > l.ttl {
			delete(l.buckets, ip)
		}
	}
}

// size reports how many buckets are held, for tests.
func (l *ipRateLimiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// clientIP is the address the request came from, without the port.
//
// The values of the usual forwarding headers are deliberately not consulted: a
// rate limiter keyed on a client supplied header can be defeated by sending a
// different one on every request. Run behind a proxy that sets a trusted one, or
// replace this function, before relying on it against a determined attacker.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
