package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// bcryptMinCost keeps the tests fast; production cost comes from Config.
const bcryptMinCost = bcrypt.MinCost

func TestHashPasswordUsesGivenCost(t *testing.T) {
	hash, err := HashPassword("supersecret", bcryptMinCost)

	require.NoError(t, err)
	cost, err := bcrypt.Cost([]byte(hash))
	require.NoError(t, err)
	assert.Equal(t, bcryptMinCost, cost)
}

func TestHashPasswordDefaultsToBcryptDefault(t *testing.T) {
	hash, err := HashPassword("supersecret", 0)

	require.NoError(t, err)
	cost, err := bcrypt.Cost([]byte(hash))
	require.NoError(t, err)
	assert.Equal(t, bcrypt.DefaultCost, cost)
}

func TestHashPasswordIsSalted(t *testing.T) {
	first, err := HashPassword("supersecret", bcryptMinCost)
	require.NoError(t, err)
	second, err := HashPassword("supersecret", bcryptMinCost)
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "two hashes of the same password must differ")
	assert.True(t, VerifyPassword(first, "supersecret"))
	assert.True(t, VerifyPassword(second, "supersecret"))
}

func TestVerifyPassword(t *testing.T) {
	hash, err := HashPassword("supersecret", bcryptMinCost)
	require.NoError(t, err)

	assert.True(t, VerifyPassword(hash, "supersecret"))
	assert.False(t, VerifyPassword(hash, "supersecret "))
	assert.False(t, VerifyPassword("", "supersecret"))
	assert.False(t, VerifyPassword("plaintext", "supersecret"))
}

func TestHashPasswordRejectsTooLongPassword(t *testing.T) {
	// bcrypt truncates past 72 bytes, so the domain rejects it beforehand with
	// ErrInvalidInput; the primitive itself reports the error too.
	_, err := HashPassword(strings.Repeat("a", 73), bcryptMinCost)

	assert.Error(t, err)
}
