package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-boilerplate/pkg/repo"
)

// testNow is the time injected into the service under test. It starts at the
// real current time, because the JWT library validates against its own clock,
// and tests move it forward explicitly when they need to expire something.
var testNow = time.Now().Truncate(time.Second)

// newTestService wires a Service over an in memory store with a frozen clock,
// so token lifetimes are exercised without sleeping.
func newTestService(t *testing.T) (*Service, *fakeStore) {
	t.Helper()

	cfg := testConfig()
	cfg.BcryptCost = bcryptMinCost
	store := newFakeStore()
	svc := NewService(cfg, store, nil)
	svc.now = func() time.Time { return testNow }

	return svc, store
}

// issueRegistrationToken mints a token through the service, so tests exercise
// the same path an admin CLI would.
func issueRegistrationToken(t *testing.T, svc *Service) string {
	t.Helper()

	raw, err := svc.IssueRegistrationToken(context.Background(), IssueRegistrationTokenInput{IssuedBy: "test"})
	require.NoError(t, err)

	return raw
}

func registerUser(t *testing.T, svc *Service, email, password string) *Session {
	t.Helper()

	session, err := svc.Register(context.Background(), RegisterInput{
		Email:             email,
		Password:          password,
		RegistrationToken: issueRegistrationToken(t, svc),
	})
	require.NoError(t, err)

	return session
}

func TestRegisterCreatesTheAccountAndASession(t *testing.T) {
	svc, store := newTestService(t)
	regToken := issueRegistrationToken(t, svc)

	session, err := svc.Register(context.Background(), RegisterInput{
		Email:             "  Ada@Example.COM ",
		Username:          "ada",
		Password:          "supersecret",
		RegistrationToken: regToken,
		RequestMeta:       RequestMeta{UserAgent: "curl", IP: "10.0.0.1"},
	})

	require.NoError(t, err)
	require.NotNil(t, session.User)
	assert.Equal(t, "ada@example.com", session.User.Email, "the email is normalised")
	require.NotNil(t, session.User.Username)
	assert.Equal(t, "ada", *session.User.Username)
	assert.Equal(t, RoleUser, session.User.Role)
	assert.True(t, session.User.IsActive)
	assert.NotEqual(t, "supersecret", session.User.PasswordHash)
	assert.True(t, session.User.VerifyPassword("supersecret"))
	assert.Equal(t, BearerScheme, session.Scheme)
	assert.NotEmpty(t, session.AccessToken)
	assert.NotEmpty(t, session.RefreshToken)

	// The session is usable straight away.
	principal, err := svc.Authenticate(session.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, session.User.ID, principal.UserID)

	// The one time token is burnt and points at the account it created.
	stored := store.registrationToken(1)
	require.NotNil(t, stored)
	require.NotNil(t, stored.UsedAt)
	assert.Equal(t, testNow, *stored.UsedAt)
	require.NotNil(t, stored.UsedByUserID)
	assert.Equal(t, session.User.ID, *stored.UsedByUserID)

	// The session is persisted with its metadata, never in raw form.
	claims, err := svc.issuer.Parse(session.RefreshToken, KindRefresh)
	require.NoError(t, err)
	persisted := store.storedRefreshToken(claims.ID)
	require.NotNil(t, persisted)
	assert.Equal(t, "curl", persisted.UserAgent)
	assert.Equal(t, "10.0.0.1", persisted.IP)
	assert.Equal(t, HashToken(session.RefreshToken), persisted.TokenHash)
}

func TestRegisterRejectsAnAlreadyUsedRegistrationToken(t *testing.T) {
	svc, _ := newTestService(t)
	regToken := issueRegistrationToken(t, svc)

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:             "first@example.com",
		Password:          "supersecret",
		RegistrationToken: regToken,
	})
	require.NoError(t, err)

	// The same token cannot authorise a second account.
	_, err = svc.Register(context.Background(), RegisterInput{
		Email:             "second@example.com",
		Password:          "supersecret",
		RegistrationToken: regToken,
	})

	assert.ErrorIs(t, err, ErrRegistrationTokenInvalid)
}

func TestRegisterRejectsAnUnknownRegistrationToken(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:             "ada@example.com",
		Password:          "supersecret",
		RegistrationToken: "not-a-real-token",
	})

	assert.ErrorIs(t, err, ErrRegistrationTokenInvalid)
}

func TestRegisterRejectsAnExpiredRegistrationToken(t *testing.T) {
	svc, _ := newTestService(t)

	raw, err := svc.IssueRegistrationToken(context.Background(), IssueRegistrationTokenInput{
		IssuedBy: "test",
		TTL:      time.Hour,
	})
	require.NoError(t, err)

	svc.now = func() time.Time { return testNow.Add(2 * time.Hour) }

	_, err = svc.Register(context.Background(), RegisterInput{
		Email:             "ada@example.com",
		Password:          "supersecret",
		RegistrationToken: raw,
	})

	assert.ErrorIs(t, err, ErrRegistrationTokenExpired)
}

func TestRegisterRejectsADuplicateEmail(t *testing.T) {
	svc, _ := newTestService(t)
	registerUser(t, svc, "ada@example.com", "supersecret")

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:             "ADA@example.com",
		Password:          "othersecret",
		RegistrationToken: issueRegistrationToken(t, svc),
	})

	assert.ErrorIs(t, err, ErrEmailTaken)
}

func TestRegisterValidatesItsInput(t *testing.T) {
	tests := []struct {
		name  string
		input RegisterInput
	}{
		{name: "no email", input: RegisterInput{Password: "supersecret", RegistrationToken: "t"}},
		{name: "malformed email", input: RegisterInput{Email: "ada@", Password: "supersecret", RegistrationToken: "t"}},
		{name: "no password", input: RegisterInput{Email: "ada@example.com", RegistrationToken: "t"}},
		{name: "short password", input: RegisterInput{Email: "ada@example.com", Password: "short", RegistrationToken: "t"}},
		{name: "long password", input: RegisterInput{Email: "ada@example.com", Password: strings.Repeat("a", 73), RegistrationToken: "t"}},
		{name: "no registration token", input: RegisterInput{Email: "ada@example.com", Password: "supersecret"}},
		{name: "blank registration token", input: RegisterInput{Email: "ada@example.com", Password: "supersecret", RegistrationToken: "   "}},
		{name: "bad username", input: RegisterInput{Email: "ada@example.com", Username: "ada lovelace", Password: "supersecret", RegistrationToken: "t"}},
		{name: "long username", input: RegisterInput{Email: "ada@example.com", Username: strings.Repeat("a", 31), Password: "supersecret", RegistrationToken: "t"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store := newTestService(t)

			_, err := svc.Register(context.Background(), tt.input)

			assert.ErrorIs(t, err, ErrInvalidInput)
			assert.Empty(t, store.users, "a rejected registration must not create anything")
		})
	}
}

func TestRegisterRollsBackWhenTheSessionCannotBeStored(t *testing.T) {
	svc, store := newTestService(t)
	store.failCreateRefresh = errors.New("redis is on fire")

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:             "ada@example.com",
		Password:          "supersecret",
		RegistrationToken: issueRegistrationToken(t, svc),
	})

	assert.EqualError(t, err, "redis is on fire")
	assert.Empty(t, store.users, "the account is rolled back with the session")
	assert.Nil(t, store.registrationToken(1).UsedAt, "the registration token is not burnt")
	assert.False(t, store.committed)
}

func TestRegisterDoesNotBurnTheTokenWhenTheAccountExists(t *testing.T) {
	svc, store := newTestService(t)
	registerUser(t, svc, "ada@example.com", "supersecret")
	// A second token is minted, so the duplicate email is what fails.
	regToken := issueRegistrationToken(t, svc)

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:             "ada@example.com",
		Password:          "supersecret",
		RegistrationToken: regToken,
	})

	assert.ErrorIs(t, err, ErrEmailTaken)
	assert.Nil(t, store.registrationToken(2).UsedAt)
}

func TestLoginStartsASession(t *testing.T) {
	svc, store := newTestService(t)
	registered := registerUser(t, svc, "ada@example.com", "supersecret")

	session, err := svc.Login(context.Background(), LoginInput{
		Email:       "  ADA@example.com ",
		Password:    "supersecret",
		RequestMeta: RequestMeta{UserAgent: "curl", IP: "10.0.0.2"},
	})

	require.NoError(t, err)
	assert.Equal(t, registered.User.ID, session.User.ID)
	assert.NotEqual(t, registered.RefreshToken, session.RefreshToken)

	claims, err := svc.issuer.Parse(session.RefreshToken, KindRefresh)
	require.NoError(t, err)
	persisted := store.storedRefreshToken(claims.ID)
	require.NotNil(t, persisted)
	assert.Equal(t, "curl", persisted.UserAgent)
	assert.Equal(t, "10.0.0.2", persisted.IP)
	assert.Equal(t, 2, store.sessionCount(), "the session from Register is kept")
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	svc, _ := newTestService(t)
	registerUser(t, svc, "ada@example.com", "supersecret")

	_, err := svc.Login(context.Background(), LoginInput{Email: "ada@example.com", Password: "wrongpassword"})

	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestLoginRejectsAnUnknownEmailWithTheSameError(t *testing.T) {
	svc, _ := newTestService(t)
	registerUser(t, svc, "ada@example.com", "supersecret")

	_, err := svc.Login(context.Background(), LoginInput{Email: "nobody@example.com", Password: "supersecret"})

	_, err2 := svc.Login(context.Background(), LoginInput{Email: "ada@example.com", Password: "wrongpassword"})

	// Identical errors, so login cannot be used to find out which accounts exist.
	assert.ErrorIs(t, err, ErrInvalidCredentials)
	assert.Equal(t, err, err2)
}

func TestLoginRejectsADisabledAccount(t *testing.T) {
	svc, store := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	stored, err := store.ByID(context.Background(), session.User.ID)
	require.NoError(t, err)
	stored.IsActive = false
	require.NoError(t, store.UpdateUser(stored))

	_, err = svc.Login(context.Background(), LoginInput{Email: "ada@example.com", Password: "supersecret"})

	assert.ErrorIs(t, err, ErrUserInactive)
}

func TestLoginValidatesItsInput(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.Login(context.Background(), LoginInput{Password: "supersecret"})
	assert.ErrorIs(t, err, ErrInvalidInput)

	_, err = svc.Login(context.Background(), LoginInput{Email: "ada@example.com"})
	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestRefreshRotatesTheToken(t *testing.T) {
	svc, store := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	original, err := svc.issuer.Parse(session.RefreshToken, KindRefresh)
	require.NoError(t, err)

	svc.now = func() time.Time { return testNow.Add(time.Hour) }

	refreshed, err := svc.Refresh(context.Background(), RefreshInput{
		RefreshToken: session.RefreshToken,
		RequestMeta:  RequestMeta{UserAgent: "curl"},
	})

	require.NoError(t, err)
	assert.NotEqual(t, session.RefreshToken, refreshed.RefreshToken)
	assert.NotEmpty(t, refreshed.AccessToken)
	require.NotNil(t, refreshed.User)
	assert.Equal(t, session.User.ID, refreshed.User.ID)

	// The presented token is revoked and remembers its replacement, so the
	// session history stays auditable.
	rotated := store.storedRefreshToken(original.ID)
	require.NotNil(t, rotated)
	require.NotNil(t, rotated.RevokedAt)
	assert.Equal(t, testNow.Add(time.Hour), *rotated.RevokedAt)

	replacement, err := svc.issuer.Parse(refreshed.RefreshToken, KindRefresh)
	require.NoError(t, err)
	assert.Equal(t, replacement.ID, rotated.ReplacedByJTI)

	fresh := store.storedRefreshToken(replacement.ID)
	require.NotNil(t, fresh)
	assert.False(t, fresh.IsRevoked())
	assert.False(t, fresh.IsExpired(testNow.Add(time.Hour)))
}

func TestRefreshRevokesTheFamilyOnReuse(t *testing.T) {
	svc, store := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	// A second device logs in, so the user has another session to lose.
	other, err := svc.Login(context.Background(), LoginInput{Email: "ada@example.com", Password: "supersecret"})
	require.NoError(t, err)

	first, err := svc.Refresh(context.Background(), RefreshInput{RefreshToken: session.RefreshToken})
	require.NoError(t, err)

	// Replaying the rotated token is the signal that it leaked.
	_, err = svc.Refresh(context.Background(), RefreshInput{RefreshToken: session.RefreshToken})
	assert.ErrorIs(t, err, ErrTokenReused)

	// Both the fresh session and the other device are dropped.
	claims, err := svc.issuer.Parse(first.RefreshToken, KindRefresh)
	require.NoError(t, err)
	assert.True(t, store.storedRefreshToken(claims.ID).IsRevoked())

	otherClaims, err := svc.issuer.Parse(other.RefreshToken, KindRefresh)
	require.NoError(t, err)
	assert.True(t, store.storedRefreshToken(otherClaims.ID).IsRevoked())

	_, err = svc.Refresh(context.Background(), RefreshInput{RefreshToken: other.RefreshToken})
	assert.ErrorIs(t, err, ErrTokenReused)
}

func TestRefreshRejectsAnAccessToken(t *testing.T) {
	svc, _ := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	_, err := svc.Refresh(context.Background(), RefreshInput{RefreshToken: session.AccessToken})

	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestRefreshRejectsAnExpiredSession(t *testing.T) {
	svc, store := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	// The stored record expires the session regardless of what the token
	// itself claims, e.g. after the tokens were rotated out of band.
	expireStoredSession(t, store, svc, session.RefreshToken)

	_, err := svc.Refresh(context.Background(), RefreshInput{RefreshToken: session.RefreshToken})

	assert.ErrorIs(t, err, ErrTokenExpired)
}

// expireStoredSession backdates the stored record of a refresh token.
func expireStoredSession(t *testing.T, store *fakeStore, svc *Service, refreshToken string) {
	t.Helper()

	claims, err := svc.issuer.Parse(refreshToken, KindRefresh)
	require.NoError(t, err)
	stored := store.storedRefreshToken(claims.ID)
	require.NotNil(t, stored)
	stored.ExpiresAt = testNow.Add(-time.Hour)
	require.NoError(t, store.UpdateRefreshToken(context.Background(), stored))
}

func TestRefreshRejectsATamperedToken(t *testing.T) {
	svc, _ := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	_, err := svc.Refresh(context.Background(), RefreshInput{
		RefreshToken: session.RefreshToken[:len(session.RefreshToken)-4] + "abcd",
	})

	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestRefreshRejectsAnUnknownToken(t *testing.T) {
	svc, _ := newTestService(t)
	registerUser(t, svc, "ada@example.com", "supersecret")

	session, _, err := svc.issuer.Issue(1, RequestMeta{}, testNow)
	require.NoError(t, err)

	// Signed by this issuer, but no session was ever stored for it.
	_, err = svc.Refresh(context.Background(), RefreshInput{RefreshToken: session.RefreshToken})

	assert.ErrorIs(t, err, ErrTokenRevoked)
}

func TestRefreshRejectsADisabledAccount(t *testing.T) {
	svc, store := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	stored, err := store.ByID(context.Background(), session.User.ID)
	require.NoError(t, err)
	stored.IsActive = false
	require.NoError(t, store.UpdateUser(stored))

	_, err = svc.Refresh(context.Background(), RefreshInput{RefreshToken: session.RefreshToken})

	assert.ErrorIs(t, err, ErrUserInactive)
}

func TestRefreshRequiresAToken(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.Refresh(context.Background(), RefreshInput{})

	assert.ErrorIs(t, err, ErrInvalidInput)
}

func TestLogoutRevokesTheSession(t *testing.T) {
	svc, store := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	require.NoError(t, svc.Logout(context.Background(), LogoutInput{RefreshToken: session.RefreshToken}))

	claims, err := svc.issuer.Parse(session.RefreshToken, KindRefresh)
	require.NoError(t, err)
	revoked := store.storedRefreshToken(claims.ID)
	require.NotNil(t, revoked.RevokedAt)
	assert.Equal(t, testNow, *revoked.RevokedAt)

	_, err = svc.Refresh(context.Background(), RefreshInput{RefreshToken: session.RefreshToken})
	assert.ErrorIs(t, err, ErrTokenReused)
}

func TestLogoutIsIdempotent(t *testing.T) {
	svc, _ := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	require.NoError(t, svc.Logout(context.Background(), LogoutInput{RefreshToken: session.RefreshToken}))
	assert.NoError(t, svc.Logout(context.Background(), LogoutInput{RefreshToken: session.RefreshToken}))
	assert.NoError(t, svc.Logout(context.Background(), LogoutInput{}))
	assert.NoError(t, svc.Logout(context.Background(), LogoutInput{RefreshToken: "   "}))
}

func TestLogoutRejectsAForeignToken(t *testing.T) {
	svc, _ := newTestService(t)
	registerUser(t, svc, "ada@example.com", "supersecret")

	assert.ErrorIs(t, svc.Logout(context.Background(), LogoutInput{RefreshToken: "garbage"}), ErrInvalidToken)
}

func TestIssueRegistrationTokenStoresOnlyTheHash(t *testing.T) {
	svc, store := newTestService(t)

	raw, err := svc.IssueRegistrationToken(context.Background(), IssueRegistrationTokenInput{
		IssuedBy: "admin@example.com",
		TTL:      time.Hour,
	})

	require.NoError(t, err)
	assert.Len(t, raw, tokenBytes*2)

	stored := store.registrationToken(1)
	require.NotNil(t, stored)
	assert.Equal(t, HashToken(raw), stored.TokenHash)
	assert.NotEqual(t, raw, stored.TokenHash)
	assert.Equal(t, "admin@example.com", stored.IssuedBy)
	assert.Equal(t, testNow.Add(time.Hour), stored.ExpiresAt)
	assert.False(t, stored.IsUsed())
}

func TestIssueRegistrationTokenDefaultsTheTTL(t *testing.T) {
	svc, store := newTestService(t)

	_, err := svc.IssueRegistrationToken(context.Background(), IssueRegistrationTokenInput{IssuedBy: "admin"})

	require.NoError(t, err)
	assert.Equal(t, testNow.Add(svc.cfg.RegistrationTokenTTL), store.registrationToken(1).ExpiresAt)
}

func TestAuthenticate(t *testing.T) {
	svc, _ := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	principal, err := svc.Authenticate(session.AccessToken)

	require.NoError(t, err)
	assert.Equal(t, session.User.ID, principal.UserID)
	assert.NotEmpty(t, principal.TokenID)
	assert.True(t, session.AccessExpiresAt.Equal(principal.ExpiresAt),
		"the principal expiry matches the session, got %v want %v", principal.ExpiresAt, session.AccessExpiresAt)
}

func TestAuthenticateRejectsBadTokens(t *testing.T) {
	svc, _ := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "supersecret")

	_, err := svc.Authenticate("")
	assert.ErrorIs(t, err, ErrMissingToken)

	_, err = svc.Authenticate("garbage")
	assert.ErrorIs(t, err, ErrInvalidToken)

	_, err = svc.Authenticate(session.RefreshToken)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestServicePropagatesStoreFailures(t *testing.T) {
	boom := errors.New("database is unreachable")

	t.Run("registration token lookup", func(t *testing.T) {
		svc, store := newTestService(t)
		regToken := issueRegistrationToken(t, svc)
		store.failByTokenHash = boom

		_, err := svc.Register(context.Background(), RegisterInput{
			Email:             "ada@example.com",
			Password:          "supersecret",
			RegistrationToken: regToken,
		})

		// A storage failure is never disguised as a bad token.
		assert.ErrorIs(t, err, boom)
		assert.False(t, errors.Is(err, ErrRegistrationTokenInvalid))
	})

	t.Run("user creation", func(t *testing.T) {
		svc, store := newTestService(t)
		regToken := issueRegistrationToken(t, svc)
		store.failCreateUser = boom

		_, err := svc.Register(context.Background(), RegisterInput{
			Email:             "ada@example.com",
			Password:          "supersecret",
			RegistrationToken: regToken,
		})

		assert.ErrorIs(t, err, boom)
		assert.Nil(t, store.registrationToken(1).UsedAt, "the token survives a failed registration")
	})

	t.Run("consume", func(t *testing.T) {
		svc, store := newTestService(t)
		regToken := issueRegistrationToken(t, svc)
		store.failConsumeRegToken = boom

		_, err := svc.Register(context.Background(), RegisterInput{
			Email:             "ada@example.com",
			Password:          "supersecret",
			RegistrationToken: regToken,
		})

		assert.ErrorIs(t, err, boom)
		assert.Empty(t, store.users, "the account is rolled back")
	})
}

func TestServiceWorksWithAGormShapedStore(t *testing.T) {
	// The service is written against Store only; make sure the production
	// adapter is the one satisfying it.
	var _ Store = (*GormStore)(nil)
}

func TestNotFoundFromTheStoreIsNotAnError(t *testing.T) {
	// Guards the assumption the service makes about lookups: they report the
	// repo sentinel, never a driver error.
	svc, _ := newTestService(t)

	_, err := svc.Login(context.Background(), LoginInput{Email: "ghost@example.com", Password: "supersecret"})

	assert.ErrorIs(t, err, ErrInvalidCredentials)
	assert.NotErrorIs(t, err, repo.ErrNotFound)
}

func TestUserReturnsTheAccountBehindAPrincipal(t *testing.T) {
	svc, _ := newTestService(t)
	session := registerUser(t, svc, "ada@example.com", "correct horse battery staple")

	principal, err := svc.Authenticate(session.AccessToken)
	require.NoError(t, err)

	user, err := svc.User(context.Background(), principal.UserID)
	require.NoError(t, err)

	assert.Equal(t, "ada@example.com", user.Email)
	assert.Equal(t, session.User.ID, user.ID)
}

// A token can outlive its account, and callers need to tell "gone" apart from
// "broken". The sentinel has to survive the trip through the service, or the
// handler answering it can only fall through to a 500.
func TestUserReportsAMissingAccountAsNotFound(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.User(context.Background(), 404)

	require.Error(t, err)
	assert.ErrorIs(t, err, repo.ErrNotFound)
}

// The ByEmail pre-check is not a lock, so two registrations for one address can
// both pass it and collide on the unique index. The loser has to hear the same
// answer as the loser of the pre-check, or the race surfaces as a 500 that tells
// the caller nothing. The store hook stands in for the driver's collision,
// which the in-memory fake does not enforce on its own.
func TestRegisterReportsADuplicateEmailAsTaken(t *testing.T) {
	svc, store := newTestService(t)
	store.failCreateUser = fmt.Errorf("%w: users_email_key", repo.ErrDuplicate)

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:             "ada@example.com",
		Password:          "supersecret",
		RegistrationToken: issueRegistrationToken(t, svc),
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmailTaken)
	// The storage sentinel is for the domain to translate, not to leak past it.
	assert.NotErrorIs(t, err, repo.ErrDuplicate)
}

// A duplicate that is not an email collision is still ours, not the caller's.
func TestRegisterDoesNotSwallowOtherStorageFailures(t *testing.T) {
	svc, store := newTestService(t)
	store.failCreateUser = errors.New("connection reset by peer")

	_, err := svc.Register(context.Background(), RegisterInput{
		Email:             "ada@example.com",
		Password:          "supersecret",
		RegistrationToken: issueRegistrationToken(t, svc),
	})

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrEmailTaken)
}
