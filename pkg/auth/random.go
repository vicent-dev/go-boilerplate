package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// tokenBytes is the entropy of an opaque registration token.
const tokenBytes = 32

// jtiBytes is the entropy of a JWT ID.
const jtiBytes = 16

// NewOpaqueToken returns a random token safe to hand to a human. It is hex
// encoded, so it survives query strings, headers and copy and paste.
func NewOpaqueToken() (string, error) {
	return randomHex(tokenBytes)
}

// NewJTI returns a random JWT ID, used to identify a single token both in the
// token itself and in the persisted session.
func NewJTI() (string, error) {
	return randomHex(jtiBytes)
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// HashToken returns the hex encoded SHA-256 of a token. Only hashes are
// persisted, so a stolen database cannot be replayed against the service.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
