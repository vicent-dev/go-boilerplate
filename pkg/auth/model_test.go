package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse", bcryptMinCost)
	require.NoError(t, err)

	u := &User{PasswordHash: hash}

	assert.True(t, u.VerifyPassword("correct horse"))
	assert.False(t, u.VerifyPassword("Correct horse"))
	assert.False(t, u.VerifyPassword(""))
	assert.False(t, u.VerifyPassword("not a hash"))
}

func TestUserCanAuthenticate(t *testing.T) {
	assert.True(t, (&User{IsActive: true}).CanAuthenticate())
	assert.False(t, (&User{IsActive: false}).CanAuthenticate())

	var nilUser *User
	assert.False(t, nilUser.CanAuthenticate())
}

func TestRefreshTokenLifecycle(t *testing.T) {
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	rt := &RefreshToken{ExpiresAt: now.Add(time.Hour)}

	assert.False(t, rt.IsRevoked())
	assert.False(t, rt.IsExpired(now))
	assert.False(t, rt.IsExpired(now.Add(time.Hour-time.Nanosecond)))
	assert.True(t, rt.IsExpired(now.Add(time.Hour)), "expiring exactly now is over")

	rt.Revoke(now.Add(time.Minute), "next-jti")

	assert.True(t, rt.IsRevoked())
	assert.Equal(t, "next-jti", rt.ReplacedByJTI)
	assert.Equal(t, now.Add(time.Minute), *rt.RevokedAt)
}

func TestRegistrationTokenLifecycle(t *testing.T) {
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	token := &RegistrationToken{ExpiresAt: now.Add(time.Hour)}

	assert.False(t, token.IsUsed())
	assert.False(t, token.IsExpired(now))
	assert.False(t, token.IsExpired(now.Add(time.Hour-time.Nanosecond)))
	assert.True(t, token.IsExpired(now.Add(time.Hour)), "expiring exactly now is over")

	used := now.Add(time.Minute)
	token.UsedAt = &used

	assert.True(t, token.IsUsed())
}

func TestRefreshTokenExpiryIsHalfOpen(t *testing.T) {
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

	// A token expiring exactly now is already gone: one second earlier it is
	// still valid.
	assert.True(t, (&RefreshToken{ExpiresAt: now}).IsExpired(now))
	assert.False(t, (&RefreshToken{ExpiresAt: now.Add(time.Nanosecond)}).IsExpired(now))
}
