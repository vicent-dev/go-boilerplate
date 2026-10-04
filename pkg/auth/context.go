package auth

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// AuthorizationHeader is the header carrying the bearer token.
const AuthorizationHeader = "Authorization"

const bearerPrefix = "bearer"

// Principal is the authenticated caller, as proven by an access token.
type Principal struct {
	UserID uint
	// TokenID identifies the access token that was presented.
	TokenID string
	// ExpiresAt is when the access token stops being accepted.
	ExpiresAt time.Time
}

// principalContextKey is unexported on purpose: an unexported key type cannot
// be produced outside this package, so no other package can overwrite the
// principal carried by a context.
type principalContextKey struct{}

// WithPrincipal returns a context carrying p as the authenticated caller.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// PrincipalFromContext returns the authenticated caller, if any. Downstream
// code asks this instead of looking at headers.
func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(*Principal)
	return p, ok && p != nil
}

// PrincipalFromRequest returns the authenticated caller of an HTTP request.
func PrincipalFromRequest(r *http.Request) (*Principal, bool) {
	return PrincipalFromContext(r.Context())
}

// BearerToken extracts the token from an Authorization header. It takes an
// http.Header rather than a request because that is all it needs, which keeps
// it testable on its own.
func BearerToken(header http.Header) (string, error) {
	raw := strings.TrimSpace(header.Get(AuthorizationHeader))
	if raw == "" {
		return "", ErrMissingToken
	}

	scheme, token, found := strings.Cut(raw, " ")
	if !found || !strings.EqualFold(scheme, bearerPrefix) {
		return "", ErrMissingToken
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return "", ErrMissingToken
	}
	return token, nil
}
