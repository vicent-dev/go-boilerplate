package auth

import "golang.org/x/crypto/bcrypt"

// HashPassword hashes a password with bcrypt. A cost of zero means the bcrypt
// default.
func HashPassword(password string, cost int) (string, error) {
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword reports whether password matches the bcrypt hash, following
// the argument order of bcrypt.CompareHashAndPassword.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
