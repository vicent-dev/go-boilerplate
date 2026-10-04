package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubAuthenticator lets a test decide what the middleware sees as a valid
// token, independently of the JWT layer.
type stubAuthenticator struct {
	principal *Principal
	err       error
	seen      string
}

func (s *stubAuthenticator) Authenticate(bearer string) (*Principal, error) {
	s.seen = bearer
	if s.err != nil {
		return nil, s.err
	}
	return s.principal, nil
}

func serveWithRequireAuth(t *testing.T, authenticator Authenticator, header string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()

	var captured *http.Request
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		w.WriteHeader(http.StatusOK)
	})

	onError := func(w http.ResponseWriter, r *http.Request, err error) {
		w.WriteHeader(http.StatusTeapot)
	}

	request := httptest.NewRequest(http.MethodGet, "/internal/resource", nil)
	if header != "" {
		request.Header.Set(AuthorizationHeader, header)
	}
	recorder := httptest.NewRecorder()

	RequireAuth(authenticator, onError)(next).ServeHTTP(recorder, request)

	return recorder, captured
}

func requireRequest(t *testing.T, r *http.Request) {
	t.Helper()
	require.NotNil(t, r, "the next handler should have run")
}

func TestRequireAuthAllowsValidToken(t *testing.T) {
	authenticator := &stubAuthenticator{principal: &Principal{UserID: 42}}

	recorder, captured := serveWithRequireAuth(t, authenticator, "Bearer good-token")

	assert.Equal(t, http.StatusOK, recorder.Code)
	requireRequest(t, captured)
	assert.Equal(t, "good-token", authenticator.seen)

	principal, ok := PrincipalFromContext(captured.Context())
	assert.True(t, ok)
	assert.Equal(t, uint(42), principal.UserID)
}

func TestRequireAuthRejectsMissingHeader(t *testing.T) {
	authenticator := &stubAuthenticator{principal: &Principal{UserID: 42}}

	recorder, captured := serveWithRequireAuth(t, authenticator, "")

	// onError decides the status; here it answers 418 to prove the middleware
	// does not impose one.
	assert.Equal(t, http.StatusTeapot, recorder.Code)
	assert.Nil(t, captured, "the next handler must not run")
	assert.Empty(t, authenticator.seen, "the authenticator is not consulted without a token")
}

func TestRequireAuthRejectsMalformedHeader(t *testing.T) {
	authenticator := &stubAuthenticator{principal: &Principal{UserID: 42}}

	recorder, captured := serveWithRequireAuth(t, authenticator, "Basic dXNlcjpwYXNz")

	assert.Equal(t, http.StatusTeapot, recorder.Code)
	assert.Nil(t, captured)
}

func TestRequireAuthRejectsInvalidToken(t *testing.T) {
	authenticator := &stubAuthenticator{err: ErrTokenExpired}

	recorder, captured := serveWithRequireAuth(t, authenticator, "Bearer expired-token")

	assert.Equal(t, http.StatusTeapot, recorder.Code)
	assert.Nil(t, captured)
	assert.Equal(t, "expired-token", authenticator.seen)
}

func TestRequireAuthPassesTheErrorToTheHandler(t *testing.T) {
	var handled error
	authenticator := &stubAuthenticator{err: errors.New("boom")}

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	onError := func(_ http.ResponseWriter, _ *http.Request, err error) { handled = err }

	request := httptest.NewRequest(http.MethodGet, "/internal", nil)
	request.Header.Set(AuthorizationHeader, "Bearer whatever")
	RequireAuth(authenticator, onError)(next).ServeHTTP(httptest.NewRecorder(), request)

	assert.EqualError(t, handled, "boom")
}

func TestRequireAuthMatchesAuthenticator(t *testing.T) {
	var _ Authenticator = (*Service)(nil)
}
