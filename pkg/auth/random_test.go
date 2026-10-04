package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOpaqueToken(t *testing.T) {
	first, err := NewOpaqueToken()
	require.NoError(t, err)
	second, err := NewOpaqueToken()
	require.NoError(t, err)

	// 32 bytes hex encoded.
	assert.Len(t, first, tokenBytes*2)
	assert.NotEqual(t, first, second)
	assert.Regexp(t, "^[0-9a-f]+$", first)
}

func TestNewJTI(t *testing.T) {
	first, err := NewJTI()
	require.NoError(t, err)
	second, err := NewJTI()
	require.NoError(t, err)

	// 16 bytes hex encoded, short enough for the varchar(64) column.
	assert.Len(t, first, jtiBytes*2)
	assert.NotEqual(t, first, second)
}

func TestHashTokenIsStableSHA256(t *testing.T) {
	// Known SHA-256 of "abc", so a change in the hashing scheme is caught
	// here instead of invalidating every stored session.
	assert.Equal(t,
		"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		HashToken("abc"),
	)
	assert.Equal(t, HashToken("token"), HashToken("token"))
	assert.NotEqual(t, HashToken("token"), HashToken("Token"))
}

func TestHashTokenDoesNotLeakTheToken(t *testing.T) {
	assert.NotContains(t, HashToken("super-secret"), "secret")
}
