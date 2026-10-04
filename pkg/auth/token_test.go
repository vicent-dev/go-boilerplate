package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig() Config {
	return Config{
		AccessSecret:  "access-secret",
		RefreshSecret: "refresh-secret",
		Issuer:        "boilerplate-test",
		Audience:      "boilerplate-test-clients",
	}
}

func TestIssuerIssueReturnsUsablePair(t *testing.T) {
	issuer := NewIssuer(testConfig())
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

	session, stored, err := issuer.Issue(42, RequestMeta{UserAgent: "curl", IP: "127.0.0.1"}, now)

	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.NotEmpty(t, session.AccessToken)
	assert.NotEmpty(t, session.RefreshToken)
	assert.Equal(t, BearerScheme, session.Scheme)
	assert.Equal(t, now.Add(15*time.Minute), session.AccessExpiresAt)
	assert.Equal(t, now.Add(7*24*time.Hour), session.RefreshExpiresAt)

	// The stored record is what a session needs to be rotated or revoked, and
	// it never contains the raw token.
	assert.Equal(t, uint(42), stored.UserID)
	assert.Equal(t, "curl", stored.UserAgent)
	assert.Equal(t, "127.0.0.1", stored.IP)
	assert.Equal(t, session.RefreshExpiresAt, stored.ExpiresAt)
	assert.Equal(t, HashToken(session.RefreshToken), stored.TokenHash)
	assert.NotEmpty(t, stored.JTI)
	assert.False(t, stored.IsRevoked())
}

func TestIssuerGivesEachTokenItsOwnID(t *testing.T) {
	issuer := NewIssuer(testConfig())

	session, stored, err := issuer.Issue(1, RequestMeta{}, time.Now())

	require.NoError(t, err)
	accessClaims, err := issuer.Parse(session.AccessToken, KindAccess)
	require.NoError(t, err)
	refreshClaims, err := issuer.Parse(session.RefreshToken, KindRefresh)
	require.NoError(t, err)

	// A shared JTI would make revocation ambiguous, which is why they differ.
	assert.NotEqual(t, accessClaims.ID, refreshClaims.ID)
	assert.Equal(t, refreshClaims.ID, stored.JTI)
}

func TestIssuerParseSubject(t *testing.T) {
	issuer := NewIssuer(testConfig())

	session, _, err := issuer.Issue(1234, RequestMeta{}, time.Now())
	require.NoError(t, err)

	userID, err := issuer.ParseUserID(session.AccessToken, KindAccess)

	require.NoError(t, err)
	assert.Equal(t, uint(1234), userID)
}

func TestIssuerRejectsWrongKind(t *testing.T) {
	issuer := NewIssuer(testConfig())

	session, _, err := issuer.Issue(1, RequestMeta{}, time.Now())
	require.NoError(t, err)

	_, err = issuer.Parse(session.RefreshToken, KindAccess)
	assert.ErrorIs(t, err, ErrInvalidToken)

	_, err = issuer.Parse(session.AccessToken, KindRefresh)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestIssuerRejectsForeignSecret(t *testing.T) {
	issued := NewIssuer(testConfig())
	other := NewIssuer(Config{
		AccessSecret:  "another-access-secret",
		RefreshSecret: "another-refresh-secret",
		Issuer:        "boilerplate-test",
		Audience:      "boilerplate-test-clients",
	})

	session, _, err := issued.Issue(1, RequestMeta{}, time.Now())
	require.NoError(t, err)

	_, err = other.Parse(session.AccessToken, KindAccess)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestIssuerRejectsForeignIssuerAndAudience(t *testing.T) {
	issued := NewIssuer(testConfig())
	elsewhere := NewIssuer(Config{
		AccessSecret:  "access-secret",
		RefreshSecret: "refresh-secret",
		Issuer:        "someone-else",
		Audience:      "boilerplate-test-clients",
	})
	otherAudience := NewIssuer(Config{
		AccessSecret:  "access-secret",
		RefreshSecret: "refresh-secret",
		Issuer:        "boilerplate-test",
		Audience:      "another-api",
	})

	session, _, err := issued.Issue(1, RequestMeta{}, time.Now())
	require.NoError(t, err)

	_, err = elsewhere.Parse(session.AccessToken, KindAccess)
	assert.ErrorIs(t, err, ErrInvalidToken)

	_, err = otherAudience.Parse(session.AccessToken, KindAccess)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestIssuerRejectsTamperedToken(t *testing.T) {
	issuer := NewIssuer(testConfig())

	session, _, err := issuer.Issue(1, RequestMeta{}, time.Now())
	require.NoError(t, err)

	tampered := session.AccessToken[:len(session.AccessToken)-4] + "abcd"

	_, err = issuer.Parse(tampered, KindAccess)
	assert.ErrorIs(t, err, ErrInvalidToken)

	_, err = issuer.Parse("not.a.token", KindAccess)
	assert.ErrorIs(t, err, ErrInvalidToken)

	_, err = issuer.Parse("", KindAccess)
	assert.ErrorIs(t, err, ErrMissingToken)
}

func TestIssuerRejectsAnotherSigningMethod(t *testing.T) {
	cfg := testConfig()
	issuer := NewIssuer(cfg)
	now := time.Now()

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			Issuer:    cfg.Issuer,
			Audience:  jwt.ClaimStrings{cfg.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			ID:        "attacker",
		},
		Kind: KindAccess,
	}
	// A token signed with a different method must never reach the key.
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS512, claims).SignedString([]byte(cfg.AccessSecret))
	require.NoError(t, err)

	_, err = issuer.Parse(forged, KindAccess)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestIssuerRejectsUnsignedToken(t *testing.T) {
	issuer := NewIssuer(testConfig())

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Kind: KindAccess,
	}
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = issuer.Parse(unsigned, KindAccess)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestIssuerRejectsExpiredToken(t *testing.T) {
	cfg := testConfig()
	cfg.AccessTTL = time.Minute
	cfg.ClockSkew = 0
	issuer := NewIssuer(cfg)

	session, _, err := issuer.Issue(1, RequestMeta{}, time.Now().Add(-2*time.Minute))
	require.NoError(t, err)

	_, err = issuer.Parse(session.AccessToken, KindAccess)
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestIssuerAcceptsTokenWithinClockSkew(t *testing.T) {
	cfg := testConfig()
	cfg.AccessTTL = time.Minute
	cfg.ClockSkew = 30 * time.Second
	issuer := NewIssuer(cfg)

	session, _, err := issuer.Issue(1, RequestMeta{}, time.Now().Add(-45*time.Second))
	require.NoError(t, err)

	_, err = issuer.Parse(session.AccessToken, KindAccess)
	assert.NoError(t, err)
}

func TestClaimsUserIDRejectsGarbageSubject(t *testing.T) {
	_, err := (&Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "not-a-number"}}).UserID()

	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr error
	}{
		{
			name: "valid",
			cfg:  Config{AccessSecret: "a", RefreshSecret: "b"},
		},
		{
			name:    "missing access secret",
			cfg:     Config{RefreshSecret: "b"},
			wantErr: ErrMissingAccessSecret,
		},
		{
			name:    "missing refresh secret",
			cfg:     Config{AccessSecret: "a"},
			wantErr: ErrMissingRefreshSecret,
		},
		{
			name:    "shared secret",
			cfg:     Config{AccessSecret: "same", RefreshSecret: "same"},
			wantErr: ErrSharedTokenSecret,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ErrorIs(t, tt.cfg.Validate(), tt.wantErr)
		})
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{AccessSecret: "a", RefreshSecret: "b"}.withDefaults()

	assert.Equal(t, 15*time.Minute, cfg.AccessTTL)
	assert.Equal(t, 7*24*time.Hour, cfg.RefreshTTL)
	assert.Equal(t, 24*time.Hour, cfg.RegistrationTokenTTL)
	assert.Equal(t, defaultBcryptCost, cfg.BcryptCost)
	assert.Equal(t, defaultIssuer, cfg.Issuer)
	assert.Equal(t, defaultAudience, cfg.Audience)
	assert.Equal(t, 5*time.Second, cfg.ClockSkew)
}
