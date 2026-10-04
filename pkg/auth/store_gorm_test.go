package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-boilerplate/pkg/repo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTestGormStore(t *testing.T) *GormStore {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)

	for _, model := range Models() {
		require.NoError(t, db.Migrator().DropTable(model))
	}
	require.NoError(t, Migrate(db))

	return NewGormStore(db)
}

func TestMigrateCreatesTheAuthTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)

	require.NoError(t, Migrate(db))

	assert.True(t, db.Migrator().HasTable(&User{}))
	assert.True(t, db.Migrator().HasTable(&RefreshToken{}))
	assert.True(t, db.Migrator().HasTable(&RegistrationToken{}))
}

func TestGormStoreUserLookups(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()

	username := "ada"
	u := &User{Email: "ada@example.com", Username: &username, PasswordHash: "hash", Role: RoleUser, IsActive: true}
	require.NoError(t, store.CreateUser(ctx, u))
	assert.NotZero(t, u.ID)

	byEmail, err := store.ByEmail(ctx, "ada@example.com")
	require.NoError(t, err)
	assert.Equal(t, u.ID, byEmail.ID)

	byID, err := store.ByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "ada@example.com", byID.Email)
}

func TestGormStoreMissingRecordsReportRepoErrNotFound(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()

	_, err := store.ByEmail(ctx, "ghost@example.com")
	assert.ErrorIs(t, err, repo.ErrNotFound)

	_, err = store.ByID(ctx, 404)
	assert.ErrorIs(t, err, repo.ErrNotFound)

	_, err = store.ByJTI(ctx, "unknown-jti")
	assert.ErrorIs(t, err, repo.ErrNotFound)

	_, err = store.ByTokenHash(ctx, "unknown-hash")
	assert.ErrorIs(t, err, repo.ErrNotFound)
}

func TestGormStoreRefreshTokenLifecycle(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	rt := &RefreshToken{
		JTI:       "jti-1",
		UserID:    7,
		TokenHash: "hash-1",
		ExpiresAt: now.Add(time.Hour),
		UserAgent: "curl",
		IP:        "127.0.0.1",
	}
	require.NoError(t, store.CreateRefreshToken(ctx, rt))

	stored, err := store.ByJTI(ctx, "jti-1")
	require.NoError(t, err)
	assert.Equal(t, uint(7), stored.UserID)
	assert.False(t, stored.IsRevoked())

	stored.Revoke(now, "jti-2")
	require.NoError(t, store.UpdateRefreshToken(ctx, stored))

	revoked, err := store.ByJTI(ctx, "jti-1")
	require.NoError(t, err)
	assert.True(t, revoked.IsRevoked())
	assert.Equal(t, "jti-2", revoked.ReplacedByJTI)
}

func TestGormStoreRevokeAllByUser(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	for _, rt := range []*RefreshToken{
		{JTI: "u1-a", UserID: 1, ExpiresAt: now.Add(time.Hour)},
		{JTI: "u1-b", UserID: 1, ExpiresAt: now.Add(time.Hour)},
		{JTI: "u2-a", UserID: 2, ExpiresAt: now.Add(time.Hour)},
	} {
		require.NoError(t, store.CreateRefreshToken(ctx, rt))
	}

	require.NoError(t, store.RevokeAllByUser(ctx, 1, now))

	first, err := store.ByJTI(ctx, "u1-a")
	require.NoError(t, err)
	second, err := store.ByJTI(ctx, "u1-b")
	require.NoError(t, err)
	other, err := store.ByJTI(ctx, "u2-a")
	require.NoError(t, err)

	assert.True(t, first.IsRevoked())
	assert.True(t, second.IsRevoked())
	assert.False(t, other.IsRevoked(), "another user's session is untouched")
}

func TestGormStoreConsumeRegistrationTokenIsAtomic(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	token := &RegistrationToken{TokenHash: "hash", IssuedBy: "admin", ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, store.CreateRegistrationToken(ctx, token))

	consumed, err := store.ConsumeRegistrationToken(ctx, token.ID, 42, now)
	require.NoError(t, err)
	assert.True(t, consumed)

	// A second registration racing for the same token loses, and says so.
	consumed, err = store.ConsumeRegistrationToken(ctx, token.ID, 43, now)
	require.NoError(t, err)
	assert.False(t, consumed)

	stored, err := store.ByTokenHash(ctx, "hash")
	require.NoError(t, err)
	require.NotNil(t, stored.UsedAt)
	assert.True(t, now.Equal(*stored.UsedAt), "used_at is %v, want %v", stored.UsedAt, now)
	require.NotNil(t, stored.UsedByUserID)
	assert.Equal(t, uint(42), *stored.UsedByUserID, "the first caller keeps the attribution")
}

func TestGormStoreConsumeUnknownRegistrationToken(t *testing.T) {
	store := newTestGormStore(t)

	// A conditional update that matches nothing is not an error: the token is
	// simply not consumable, whether it is gone or already taken.
	consumed, err := store.ConsumeRegistrationToken(context.Background(), 999, 1, time.Now())

	assert.NoError(t, err)
	assert.False(t, consumed)
}

func TestGormStoreWithinTxCommits(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()

	err := store.WithinTx(ctx, func(tx Store) error {
		if err := tx.CreateUser(ctx, &User{Email: "ada@example.com", IsActive: true}); err != nil {
			return err
		}
		return tx.CreateRefreshToken(ctx, &RefreshToken{JTI: "jti-1", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)})
	})

	require.NoError(t, err)

	_, err = store.ByEmail(ctx, "ada@example.com")
	assert.NoError(t, err)
	_, err = store.ByJTI(ctx, "jti-1")
	assert.NoError(t, err)
}

func TestGormStoreWithinTxRollsBack(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()
	boom := errors.New("something went wrong")

	err := store.WithinTx(ctx, func(tx Store) error {
		if err := tx.CreateUser(ctx, &User{Email: "ada@example.com", IsActive: true}); err != nil {
			return err
		}
		if err := tx.CreateRefreshToken(ctx, &RefreshToken{JTI: "jti-1", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			return err
		}
		return boom
	})

	assert.ErrorIs(t, err, boom)

	_, err = store.ByEmail(ctx, "ada@example.com")
	assert.ErrorIs(t, err, repo.ErrNotFound)
	_, err = store.ByJTI(ctx, "jti-1")
	assert.ErrorIs(t, err, repo.ErrNotFound)
}

func TestServiceRunsAgainstTheGormStore(t *testing.T) {
	// The service is written against Store; this runs the happy path on the
	// real adapter rather than on the fake.
	store := newTestGormStore(t)
	svc := NewService(Config{AccessSecret: "a", RefreshSecret: "b", BcryptCost: bcryptMinCost}, store, nil)
	ctx := context.Background()

	raw, err := svc.IssueRegistrationToken(ctx, IssueRegistrationTokenInput{IssuedBy: "admin"})
	require.NoError(t, err)

	session, err := svc.Register(ctx, RegisterInput{
		Email:             "ada@example.com",
		Password:          "supersecret",
		RegistrationToken: raw,
	})
	require.NoError(t, err)
	require.NotNil(t, session.User)

	principal, err := svc.Authenticate(session.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, session.User.ID, principal.UserID)

	loggedIn, err := svc.Login(ctx, LoginInput{Email: "ada@example.com", Password: "supersecret"})
	require.NoError(t, err)

	refreshed, err := svc.Refresh(ctx, RefreshInput{RefreshToken: loggedIn.RefreshToken})
	require.NoError(t, err)

	// The rotated token is refused, and the reuse drops the whole family.
	_, err = svc.Refresh(ctx, RefreshInput{RefreshToken: loggedIn.RefreshToken})
	assert.ErrorIs(t, err, ErrTokenReused)
	_, err = svc.Refresh(ctx, RefreshInput{RefreshToken: refreshed.RefreshToken})
	assert.ErrorIs(t, err, ErrTokenReused)

	require.NoError(t, svc.Logout(ctx, LogoutInput{}))
}

// A username is optional, so the common case is many accounts with none at all.
// Storing that as "" put them all in competition for one unique index, and every
// registration after the first failed on the constraint — which is why the
// column is nullable and the test asserts against the real schema.
func TestManyUsersCanLeaveTheUsernameUnset(t *testing.T) {
	store := newTestGormStore(t)
	ctx := context.Background()

	for _, email := range []string{"one@example.com", "two@example.com", "three@example.com"} {
		u := &User{Email: email, PasswordHash: "hash", Role: RoleUser, IsActive: true}
		require.NoError(t, store.CreateUser(ctx, u), "creating %s", email)
		assert.Nil(t, u.Username)
	}

	// And the alias still refuses a duplicate when it is supplied.
	first, second := "ada", "grace"
	require.NoError(t, store.CreateUser(ctx, &User{
		Email: "four@example.com", Username: &first, PasswordHash: "h", Role: RoleUser, IsActive: true,
	}))

	err := store.CreateUser(ctx, &User{
		Email: "five@example.com", Username: &second, PasswordHash: "h", Role: RoleUser, IsActive: true,
	})
	require.NoError(t, err, "distinct usernames are fine")

	duplicate := "ada"
	err = store.CreateUser(ctx, &User{
		Email: "six@example.com", Username: &duplicate, PasswordHash: "h", Role: RoleUser, IsActive: true,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, repo.ErrDuplicate)
}
