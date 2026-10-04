package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    string
		wantErr error
	}{
		{name: "no header", header: "", wantErr: ErrMissingToken},
		{name: "only whitespace", header: "   ", wantErr: ErrMissingToken},
		{name: "no scheme", header: "token123", wantErr: ErrMissingToken},
		{name: "wrong scheme", header: "Basic dXNlcjpwYXNz", wantErr: ErrMissingToken},
		{name: "scheme without token", header: "Bearer", wantErr: ErrMissingToken},
		{name: "scheme with empty token", header: "Bearer ", wantErr: ErrMissingToken},
		{name: "bearer token", header: "Bearer token123", want: "token123"},
		{name: "lowercase scheme", header: "bearer token123", want: "token123"},
		{name: "mixed case scheme", header: "BeArEr token123", want: "token123"},
		{name: "padded value", header: "Bearer   token123  ", want: "token123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			if tt.header != "" {
				header.Set(AuthorizationHeader, tt.header)
			}

			got, err := BearerToken(header)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Empty(t, got)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBearerTokenReadsTheAuthorizationHeader(t *testing.T) {
	header := http.Header{}
	header.Set(AuthorizationHeader, "Bearer abc")

	token, err := BearerToken(header)

	assert.NoError(t, err)
	assert.Equal(t, "abc", token)
}

func TestPrincipalRoundTrip(t *testing.T) {
	want := &Principal{UserID: 42, TokenID: "jti", ExpiresAt: time.Now().Add(time.Minute)}

	got, ok := PrincipalFromContext(WithPrincipal(t.Context(), want))

	assert.True(t, ok)
	assert.Equal(t, want, got)
}

func TestPrincipalFromContextWithoutPrincipal(t *testing.T) {
	principal, ok := PrincipalFromContext(t.Context())

	assert.False(t, ok)
	assert.Nil(t, principal)
}

func TestPrincipalFromContextIgnoresNilPrincipal(t *testing.T) {
	ctx := WithPrincipal(t.Context(), nil)

	_, ok := PrincipalFromContext(ctx)

	assert.False(t, ok)
}

func TestPrincipalFromRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/internal/resource", nil)
	request = request.WithContext(WithPrincipal(request.Context(), &Principal{UserID: 7}))

	principal, ok := PrincipalFromRequest(request)

	assert.True(t, ok)
	assert.Equal(t, uint(7), principal.UserID)
}

func TestPrincipalContextKeyIsNotForgeableOutsideThePackage(t *testing.T) {
	// The key type is unexported, so a plain string key cannot collide with it.
	ctx := WithPrincipal(t.Context(), &Principal{UserID: 1})
	ctx = context.WithValue(ctx, "principal", &Principal{UserID: 99}) //nolint:staticcheck // deliberate

	principal, ok := PrincipalFromContext(ctx)

	assert.True(t, ok)
	assert.Equal(t, uint(1), principal.UserID)
}
