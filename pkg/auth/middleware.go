package auth

import "net/http"

// Authenticator turns a bearer token into the caller it identifies. *Service
// implements it, and tests can substitute a stub.
type Authenticator interface {
	Authenticate(bearer string) (*Principal, error)
}

// ErrorHandler renders the response for a request that could not be
// authenticated.
type ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

// RequireAuth returns middleware that rejects any request without a valid
// access token, and injects the Principal into the context of the requests it
// lets through.
//
// It decides who the caller is, not how a rejection looks: status codes and
// payload shapes belong to the caller of this package, which passes them in
// as onError.
func RequireAuth(authenticator Authenticator, onError ErrorHandler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bearer, err := BearerToken(r.Header)
			if err == nil {
				var principal *Principal
				principal, err = authenticator.Authenticate(bearer)
				if err == nil {
					next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
					return
				}
			}
			onError(w, r, err)
		})
	}
}
