package app

import (
	"encoding/json"
	"errors"
	"net/http"

	"go-boilerplate/pkg/auth"
	"go-boilerplate/pkg/log"
	"go-boilerplate/pkg/repo"
)

// The handlers below only translate between HTTP and the auth domain: decode,
// delegate, render. Every rule about what is valid lives in pkg/auth and comes
// back as one of its sentinel errors, which writeAuthError maps to a status.
//
// Each factory builds the auth service before returning its handler, the same
// way defaultRouteHandler builds the response cache and the client.

type registerRequest struct {
	Email             string `json:"email"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	RegistrationToken string `json:"registration_token"`
}

func (s *server) registerHandler() http.HandlerFunc {
	svc := s.authService()

	return func(w http.ResponseWriter, r *http.Request) {
		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorResponse(w, map[string]any{"error": "invalid request body"}, http.StatusBadRequest)
			return
		}

		session, err := svc.Register(r.Context(), auth.RegisterInput{
			Email:             req.Email,
			Username:          req.Username,
			Password:          req.Password,
			RegistrationToken: req.RegistrationToken,
			RequestMeta:       requestMeta(r),
		})
		if err != nil {
			writeAuthError(w, r, err)
			return
		}

		writeSession(w, http.StatusCreated, session)
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *server) loginHandler() http.HandlerFunc {
	svc := s.authService()

	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorResponse(w, map[string]any{"error": "invalid request body"}, http.StatusBadRequest)
			return
		}

		session, err := svc.Login(r.Context(), auth.LoginInput{
			Email:       req.Email,
			Password:    req.Password,
			RequestMeta: requestMeta(r),
		})
		if err != nil {
			writeAuthError(w, r, err)
			return
		}

		writeSession(w, http.StatusOK, session)
	}
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *server) refreshHandler() http.HandlerFunc {
	svc := s.authService()

	return func(w http.ResponseWriter, r *http.Request) {
		var req refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorResponse(w, map[string]any{"error": "invalid request body"}, http.StatusBadRequest)
			return
		}

		session, err := svc.Refresh(r.Context(), auth.RefreshInput{
			RefreshToken: req.RefreshToken,
			RequestMeta:  requestMeta(r),
		})
		if err != nil {
			writeAuthError(w, r, err)
			return
		}

		writeSession(w, http.StatusOK, session)
	}
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *server) logoutHandler() http.HandlerFunc {
	svc := s.authService()

	return func(w http.ResponseWriter, r *http.Request) {
		// Logging out is idempotent from the client's point of view: whatever
		// happens to the token, the session is gone.
		var req logoutRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		if err := svc.Logout(r.Context(), auth.LogoutInput{RefreshToken: req.RefreshToken}); err != nil {
			log.LogInfo(r.Context(), "logout: "+err.Error())
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// writeSession renders a session. The user is included only when the domain
// resolved one, which is not the case on a plain token refresh.
func writeSession(w http.ResponseWriter, status int, session *auth.Session) {
	payload := map[string]any{
		"access_token":  session.AccessToken,
		"refresh_token": session.RefreshToken,
		"token_type":    session.Scheme,
		"expires_at":    session.AccessExpiresAt,
	}
	if session.User != nil {
		payload["user"] = session.User
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeAuthError maps a domain error to the status that describes it, keeping
// the http layer from having to know the rules behind it.
func writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidInput):
		// The domain explains which field is wrong, and the client sent it.
		writeErrorResponse(w, map[string]any{"error": err.Error()}, http.StatusBadRequest)

	case errors.Is(err, auth.ErrEmailTaken):
		writeErrorResponse(w, map[string]any{"error": err.Error()}, http.StatusConflict)

	case errors.Is(err, auth.ErrUserInactive):
		writeErrorResponse(w, map[string]any{"error": err.Error()}, http.StatusForbidden)

	case errors.Is(err, repo.ErrNotFound):
		// The token was valid but the account behind it is gone, so there is
		// nothing to return. It is not an internal error, and saying so would
		// send the caller looking for one.
		writeErrorResponse(w, map[string]any{"error": "not found"}, http.StatusNotFound)

	case errors.Is(err, auth.ErrInvalidCredentials),
		errors.Is(err, auth.ErrMissingToken),
		errors.Is(err, auth.ErrInvalidToken),
		errors.Is(err, auth.ErrTokenExpired),
		errors.Is(err, auth.ErrTokenRevoked),
		errors.Is(err, auth.ErrTokenReused),
		errors.Is(err, auth.ErrRegistrationTokenInvalid),
		errors.Is(err, auth.ErrRegistrationTokenExpired):
		// Deliberately vague: the client learns that it is not allowed in, and
		// nothing about why.
		log.LogInfo(r.Context(), "auth rejected: "+err.Error())
		writeErrorResponse(w, map[string]any{"error": "unauthorized"}, http.StatusUnauthorized)

	default:
		// A storage or programming failure is ours, not the caller's.
		log.LogError(r.Context(), err.Error())
		writeErrorResponse(w, map[string]any{"error": "internal error"}, http.StatusInternalServerError)
	}
}
