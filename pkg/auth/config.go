package auth

import (
	"time"
)

const (
	defaultBcryptCost = 12
	defaultIssuer     = "boilerplate"
	defaultAudience   = "boilerplate-clients"
)

// Config holds everything the auth domain needs to sign and verify tokens.
// It is built by the configuration layer and never read from the environment
// here, so the domain stays testable.
type Config struct {
	// AccessSecret and RefreshSecret sign the two token kinds. They must
	// differ, otherwise a refresh token would validate as an access token.
	AccessSecret  string
	RefreshSecret string
	// AccessTTL, RefreshTTL and RegistrationTokenTTL bound token lifetimes.
	AccessTTL            time.Duration
	RefreshTTL           time.Duration
	RegistrationTokenTTL time.Duration
	// Issuer and Audience are checked on every verification.
	Issuer   string
	Audience string
	// BcryptCost is the cost of new password hashes.
	BcryptCost int
	// ClockSkew is the leeway allowed on not before and expiry checks.
	ClockSkew time.Duration
}

// withDefaults returns a copy with every unset field filled in.
func (c Config) withDefaults() Config {
	if c.AccessTTL == 0 {
		c.AccessTTL = 15 * time.Minute
	}
	if c.RefreshTTL == 0 {
		c.RefreshTTL = 7 * 24 * time.Hour
	}
	if c.RegistrationTokenTTL == 0 {
		c.RegistrationTokenTTL = 24 * time.Hour
	}
	if c.BcryptCost == 0 {
		c.BcryptCost = defaultBcryptCost
	}
	if c.Issuer == "" {
		c.Issuer = defaultIssuer
	}
	if c.Audience == "" {
		c.Audience = defaultAudience
	}
	if c.ClockSkew == 0 {
		c.ClockSkew = 5 * time.Second
	}
	return c
}

// Validate reports whether the configuration can issue tokens at all. Failing
// fast at startup beats signing tokens nobody can verify.
func (c Config) Validate() error {
	switch {
	case c.AccessSecret == "":
		return ErrMissingAccessSecret
	case c.RefreshSecret == "":
		return ErrMissingRefreshSecret
	case c.AccessSecret == c.RefreshSecret:
		return ErrSharedTokenSecret
	}
	return nil
}
