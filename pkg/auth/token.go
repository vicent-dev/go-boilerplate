package auth

import (
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenKind tells an access token from a refresh token. Each kind is signed
// with its own secret and rejected when the other kind is asked for.
type TokenKind string

const (
	// KindAccess identifies short lived tokens that authenticate callers.
	KindAccess TokenKind = "access"
	// KindRefresh identifies the tokens that can mint new sessions.
	KindRefresh TokenKind = "refresh"
)

// BearerScheme is the Authorization scheme of the tokens this package issues.
const BearerScheme = "Bearer"

// Claims are the claims carried by the tokens issued by the Issuer.
type Claims struct {
	jwt.RegisteredClaims
	// Kind is the token kind, kept under the typ claim name.
	Kind TokenKind `json:"typ"`
}

// UserID returns the subject of the claims as a user id.
func (c *Claims) UserID() (uint, error) {
	id, err := strconv.ParseUint(c.Subject, 10, 64)
	if err != nil {
		return 0, ErrInvalidToken
	}
	return uint(id), nil
}

// RequestMeta is the client metadata stored alongside an issued session.
type RequestMeta struct {
	UserAgent string
	IP        string
}

// Session is the credential pair handed to a client, plus what the service
// needs to reason about it later.
type Session struct {
	AccessToken  string
	RefreshToken string
	Scheme       string
	// AccessExpiresAt and RefreshExpiresAt are wall clock hints for clients.
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	// User is set when the session was started for a known account.
	User *User
}

// Issuer signs and verifies the JWTs this package issues. It owns no persistence: turning
// a refresh token into a stored session is the service's job, which keeps the
// signing code a pure function of its configuration.
type Issuer struct {
	cfg Config
}

// NewIssuer returns an Issuer using the given configuration with defaults
// applied.
func NewIssuer(cfg Config) *Issuer {
	return &Issuer{cfg: cfg.withDefaults()}
}

// Issue mints a new access and refresh token pair for userID at now. The
// returned RefreshToken is the session record to persist; it is not stored
// here, so callers decide in which transaction it belongs.
func (i *Issuer) Issue(userID uint, meta RequestMeta, now time.Time) (*Session, *RefreshToken, error) {
	accessExpiresAt := now.Add(i.cfg.AccessTTL)
	accessToken, _, err := i.sign(userID, KindAccess, i.cfg.AccessSecret, accessExpiresAt, now)
	if err != nil {
		return nil, nil, err
	}

	refreshExpiresAt := now.Add(i.cfg.RefreshTTL)
	refreshToken, refreshJTI, err := i.sign(userID, KindRefresh, i.cfg.RefreshSecret, refreshExpiresAt, now)
	if err != nil {
		return nil, nil, err
	}

	session := &Session{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		Scheme:           BearerScheme,
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	}

	stored := &RefreshToken{
		JTI:       refreshJTI,
		UserID:    userID,
		TokenHash: HashToken(refreshToken),
		ExpiresAt: refreshExpiresAt,
		UserAgent: meta.UserAgent,
		IP:        meta.IP,
	}

	return session, stored, nil
}

// Parse verifies a token of the given kind and returns its claims. Every
// failure mode, malformed, foreign, tampered, expired or wrong kind, is
// reported as one of the package sentinels.
func (i *Issuer) Parse(raw string, kind TokenKind) (*Claims, error) {
	if raw == "" {
		return nil, ErrMissingToken
	}

	secret := i.cfg.AccessSecret
	if kind == KindRefresh {
		secret = i.cfg.RefreshSecret
	}

	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims,
		func(*jwt.Token) (any, error) { return []byte(secret), nil },
		// Pinning the method keeps a token signed with another algorithm from
		// ever reaching the key function.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithLeeway(i.cfg.ClockSkew),
		jwt.WithIssuer(i.cfg.Issuer),
		jwt.WithAudience(i.cfg.Audience),
	)
	if err != nil {
		return nil, parseError(err)
	}
	if claims.Kind != kind {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// ParseUserID verifies a token of the given kind and returns its subject.
func (i *Issuer) ParseUserID(raw string, kind TokenKind) (uint, error) {
	claims, err := i.Parse(raw, kind)
	if err != nil {
		return 0, err
	}
	return claims.UserID()
}

func (i *Issuer) sign(userID uint, kind TokenKind, secret string, expiresAt, now time.Time) (raw, jti string, err error) {
	jti, err = NewJTI()
	if err != nil {
		return "", "", err
	}

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatUint(uint64(userID), 10),
			Issuer:    i.cfg.Issuer,
			Audience:  jwt.ClaimStrings{i.cfg.Audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			// NotBefore is deliberately left unset: verification has no way to
			// know the issuer's clock, so a not before claim would only make
			// tokens unusable on a host whose clock runs behind. Expiry, with
			// the configured leeway, is the whole lifetime story.
			ID: jti,
		},
		Kind: kind,
	}

	raw, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		return "", "", err
	}
	return raw, jti, nil
}

// parseError collapses the JWT library errors into the package sentinels, so
// that no caller ever has to import the library to classify a failure.
func parseError(err error) error {
	if errors.Is(err, jwt.ErrTokenExpired) {
		return ErrTokenExpired
	}
	return ErrInvalidToken
}
