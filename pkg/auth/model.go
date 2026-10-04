package auth

import (
	"time"

	"gorm.io/gorm"
)

// Role is the authorization role stored on a user. Roles are not part of the
// issued tokens, so a role change only applies to tokens issued afterwards.
type Role string

const (
	// RoleUser is the role given to every self registered account.
	RoleUser Role = "user"
	// RoleAdmin is reserved for accounts allowed to manage other accounts.
	RoleAdmin Role = "admin"
)

// User is an account that can authenticate against the service.
type User struct {
	ID    uint   `gorm:"primarykey" json:"id"`
	Email string `gorm:"type:varchar(255);uniqueIndex;not null" json:"email"`
	// Username is an optional alias, so it is nullable rather than an empty
	// string. A unique index over "" admits exactly one row, which made every
	// registration after the first fail on the constraint.
	Username     *string        `gorm:"type:varchar(100);uniqueIndex" json:"username,omitempty"`
	PasswordHash string         `gorm:"type:varchar(255);not null" json:"-"`
	Role         Role           `gorm:"type:varchar(50);default:'user';not null" json:"role"`
	IsActive     bool           `gorm:"default:true;not null" json:"is_active"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// VerifyPassword reports whether the plain password matches the stored hash.
func (u *User) VerifyPassword(plain string) bool {
	return VerifyPassword(u.PasswordHash, plain)
}

// CanAuthenticate reports whether the account is allowed to start a session.
func (u *User) CanAuthenticate() bool {
	return u != nil && u.IsActive
}

// RefreshToken is the persisted half of a session: the JWT itself is never
// stored, only its identifier and its hash, so a database leak cannot be
// replayed against the service.
type RefreshToken struct {
	ID            uint       `gorm:"primarykey"`
	JTI           string     `gorm:"type:varchar(64);uniqueIndex;not null"`
	UserID        uint       `gorm:"index;not null"`
	TokenHash     string     `gorm:"type:varchar(255);index;not null"`
	ExpiresAt     time.Time  `gorm:"index;not null"`
	RevokedAt     *time.Time `gorm:"index"`
	ReplacedByJTI string     `gorm:"type:varchar(64);index"`
	UserAgent     string     `gorm:"type:varchar(255)"`
	IP            string     `gorm:"type:varchar(45)"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     gorm.DeletedAt `gorm:"index"`
}

// IsRevoked reports whether the token has been rotated or logged out.
func (t *RefreshToken) IsRevoked() bool {
	return t.RevokedAt != nil
}

// IsExpired reports whether the token is past its expiry at now.
func (t *RefreshToken) IsExpired(now time.Time) bool {
	return !t.ExpiresAt.After(now)
}

// Revoke marks the token as unusable, recording the token that replaced it.
func (t *RefreshToken) Revoke(now time.Time, replacedBy string) {
	t.RevokedAt = &now
	t.ReplacedByJTI = replacedBy
}

// RegistrationToken authorises exactly one account creation.
type RegistrationToken struct {
	ID           uint       `gorm:"primarykey"`
	TokenHash    string     `gorm:"type:varchar(255);uniqueIndex;not null"`
	IssuedBy     string     `gorm:"type:varchar(100)"`
	ExpiresAt    time.Time  `gorm:"not null"`
	UsedAt       *time.Time `gorm:"index"`
	UsedByUserID *uint      `gorm:"index"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    gorm.DeletedAt `gorm:"index"`
}

// IsUsed reports whether the token has already authorised a registration.
func (t *RegistrationToken) IsUsed() bool {
	return t.UsedAt != nil
}

// IsExpired reports whether the token is past its expiry at now.
func (t *RegistrationToken) IsExpired(now time.Time) bool {
	return !t.ExpiresAt.After(now)
}
