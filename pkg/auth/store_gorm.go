package auth

import (
	"context"
	"time"

	"gorm.io/gorm"

	"go-boilerplate/pkg/repo"
)

// GormStore is the GORM implementation of Store. It composes the generic
// repository for the CRUD operations and runs the domain specific queries
// itself, so no SQL leaks into the ports declared in store.go.
type GormStore struct {
	db           *gorm.DB
	users        *repo.GormRepository[User]
	refreshToken *repo.GormRepository[RefreshToken]
	regToken     *repo.GormRepository[RegistrationToken]
}

// NewGormStore returns a Store backed by db.
func NewGormStore(db *gorm.DB) *GormStore {
	return &GormStore{
		db:           db,
		users:        repo.NewGormRepository[User](db),
		refreshToken: repo.NewGormRepository[RefreshToken](db),
		regToken:     repo.NewGormRepository[RegistrationToken](db),
	}
}

// Models returns every entity owned by this package.
func Models() []any {
	return []any{&User{}, &RefreshToken{}, &RegistrationToken{}}
}

// Migrate creates or updates the tables backing the auth domain.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(Models()...)
}

// WithinTx runs fn in a transaction, giving it a Store bound to it.
func (s *GormStore) WithinTx(ctx context.Context, fn func(Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(NewGormStore(tx))
	})
}

func (s *GormStore) CreateUser(ctx context.Context, u *User) error {
	return s.users.Create(ctx, u)
}

func (s *GormStore) ByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	if err := s.users.DB().WithContext(ctx).Where("email = ?", email).First(&u).Error; err != nil {
		return nil, repo.NormalizeError(err)
	}
	return &u, nil
}

func (s *GormStore) ByID(ctx context.Context, id uint) (*User, error) {
	return s.users.GetByID(ctx, id)
}

func (s *GormStore) CreateRefreshToken(ctx context.Context, rt *RefreshToken) error {
	return s.refreshToken.Create(ctx, rt)
}

// ByJTI returns the stored session identified by jti.
func (s *GormStore) ByJTI(ctx context.Context, jti string) (*RefreshToken, error) {
	var rt RefreshToken
	if err := s.refreshToken.DB().WithContext(ctx).Where("jti = ?", jti).First(&rt).Error; err != nil {
		return nil, repo.NormalizeError(err)
	}
	return &rt, nil
}

// UpdateRefreshToken persists a session, typically to record a rotation.
func (s *GormStore) UpdateRefreshToken(ctx context.Context, rt *RefreshToken) error {
	return s.refreshToken.Update(ctx, rt)
}

func (s *GormStore) RevokeAllByUser(ctx context.Context, userID uint, at time.Time) error {
	return s.refreshToken.DB().WithContext(ctx).
		Model(&RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", at).Error
}

func (s *GormStore) CreateRegistrationToken(ctx context.Context, t *RegistrationToken) error {
	return s.regToken.Create(ctx, t)
}

// ByTokenHash looks a registration token up by the hash of its raw value, the
// raw value itself never being stored.
func (s *GormStore) ByTokenHash(ctx context.Context, hash string) (*RegistrationToken, error) {
	var t RegistrationToken
	if err := s.regToken.DB().WithContext(ctx).Where("token_hash = ?", hash).First(&t).Error; err != nil {
		return nil, repo.NormalizeError(err)
	}
	return &t, nil
}

// ConsumeRegistrationToken marks a registration token as used in a single
// conditional statement, so the winner of a race is the only caller that gets
// true.
func (s *GormStore) ConsumeRegistrationToken(ctx context.Context, id uint, usedBy uint, at time.Time) (bool, error) {
	result := s.regToken.DB().WithContext(ctx).
		Model(&RegistrationToken{}).
		Where("id = ? AND used_at IS NULL", id).
		Updates(map[string]any{"used_at": at, "used_by_user_id": usedBy})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}
