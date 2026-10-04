package auth

import "errors"

// Sentinel errors returned by Service. Callers map them to their own transport
// semantics with errors.Is; nothing here mentions HTTP.
var (
	// ErrInvalidInput marks a payload the domain refuses, and wraps the
	// offending field detail, e.g. fmt.Errorf("%w: email is required", ...).
	ErrInvalidInput = errors.New("auth: invalid input")
	// ErrEmailTaken is returned when an account already uses the email.
	ErrEmailTaken = errors.New("auth: email already registered")
	// ErrInvalidCredentials covers both unknown email and wrong password, so
	// that login cannot be used to enumerate accounts.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrUserInactive is returned when the account exists but is disabled.
	ErrUserInactive = errors.New("auth: user is not active")

	// ErrMissingToken is returned when no bearer token was presented.
	ErrMissingToken = errors.New("auth: missing token")
	// ErrInvalidToken covers malformed, foreign and tampered tokens.
	ErrInvalidToken = errors.New("auth: invalid token")
	// ErrTokenExpired is returned for tokens past their expiry.
	ErrTokenExpired = errors.New("auth: token expired")
	// ErrTokenRevoked is returned when a refresh token is valid but no longer
	// backed by a stored session.
	ErrTokenRevoked = errors.New("auth: token revoked")
	// ErrTokenReused is returned when an already rotated refresh token is
	// presented again, which is treated as a stolen token.
	ErrTokenReused = errors.New("auth: refresh token already used")

	// ErrRegistrationTokenInvalid covers unknown, already used and lost race
	// registration tokens.
	ErrRegistrationTokenInvalid = errors.New("auth: invalid registration token")
	// ErrRegistrationTokenExpired is returned for expired registration tokens.
	ErrRegistrationTokenExpired = errors.New("auth: registration token expired")

	// ErrMissingAccessSecret and ErrMissingRefreshSecret come from Config
	// validation, and ErrSharedTokenSecret guards against signing both token
	// kinds with the same key.
	ErrMissingAccessSecret  = errors.New("auth: access secret is not configured")
	ErrMissingRefreshSecret = errors.New("auth: refresh secret is not configured")
	ErrSharedTokenSecret    = errors.New("auth: access and refresh secrets must differ")
)
