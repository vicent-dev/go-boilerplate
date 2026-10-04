package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go-boilerplate/pkg/repo"
)

const (
	// minPasswordLength and maxPasswordLength bound what bcrypt accepts; past
	// 72 bytes bcrypt silently truncates, so the limit is enforced up front.
	minPasswordLength = 8
	maxPasswordLength = 72

	maxUsernameLength = 30
)

// errTokenReused is internal to Refresh: it carries the reuse detection out
// of the transaction that found it, so the family revocation is not rolled
// back with it.
var errTokenReused = errors.New("auth: refresh token already used")

var (
	emailPattern    = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
)

// RegisterInput describes the account to create and the one time token that
// authorises it.
type RegisterInput struct {
	Email             string
	Username          string
	Password          string
	RegistrationToken string
	RequestMeta       RequestMeta
}

// LoginInput are the credentials presented to Login.
type LoginInput struct {
	Email       string
	Password    string
	RequestMeta RequestMeta
}

// RefreshInput is the refresh token presented to Refresh.
type RefreshInput struct {
	RefreshToken string
	RequestMeta  RequestMeta
}

// LogoutInput is the refresh token to revoke. It is optional: logging out
// without a session is a no-op.
type LogoutInput struct {
	RefreshToken string
}

// IssueRegistrationTokenInput describes a token to mint.
type IssueRegistrationTokenInput struct {
	IssuedBy string
	TTL      time.Duration
}

// Service holds the authentication use cases: it owns the rules, the ports and
// the token issuer, and nothing about how it is exposed over HTTP.
type Service struct {
	cfg    Config
	store  Store
	issuer *Issuer
	// now is swapped in tests to drive expiry and rotation without sleeping.
	now func() time.Time
}

// NewService returns a Service backed by store. A nil issuer is built from
// cfg, which is what callers that only mint registration tokens want.
func NewService(cfg Config, store Store, issuer *Issuer) *Service {
	if issuer == nil {
		issuer = NewIssuer(cfg)
	}
	return &Service{
		cfg:    cfg.withDefaults(),
		store:  store,
		issuer: issuer,
		now:    time.Now,
	}
}

// Register consumes a registration token and creates the account it
// authorises, then starts a session for it.
//
// The whole thing runs in one transaction: a registration either creates the
// account, burns the token and stores the session, or leaves no trace. That is
// also what makes a one time token one time, since ConsumeRegistrationToken
// decides the race inside the database rather than in a read followed by a
// write.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*Session, error) {
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}

	username, err := normalizeUsername(in.Username)
	if err != nil {
		return nil, err
	}

	if err := validatePassword(in.Password); err != nil {
		return nil, err
	}

	rawToken := strings.TrimSpace(in.RegistrationToken)
	if rawToken == "" {
		return nil, fmt.Errorf("%w: registration_token is required", ErrInvalidInput)
	}

	now := s.now()
	var session *Session

	err = s.store.WithinTx(ctx, func(tx Store) error {
		regToken, err := tx.ByTokenHash(ctx, HashToken(rawToken))
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return ErrRegistrationTokenInvalid
			}
			return err
		}
		if regToken.IsUsed() {
			return ErrRegistrationTokenInvalid
		}
		if regToken.IsExpired(now) {
			return ErrRegistrationTokenExpired
		}

		if _, err := tx.ByEmail(ctx, email); err == nil {
			return ErrEmailTaken
		} else if !errors.Is(err, repo.ErrNotFound) {
			return err
		}

		hash, err := HashPassword(in.Password, s.cfg.BcryptCost)
		if err != nil {
			return err
		}

		u := &User{
			Email:        email,
			Username:     username,
			PasswordHash: hash,
			Role:         RoleUser,
			IsActive:     true,
		}
		if err := tx.CreateUser(ctx, u); err != nil {
			// The ByEmail check above is not a lock, so two registrations for
			// the same address can both pass it and collide on the unique
			// index. That is the same outcome as an address already taken, and
			// the caller should hear about it the same way.
			if errors.Is(err, repo.ErrDuplicate) {
				return ErrEmailTaken
			}
			return err
		}

		consumed, err := tx.ConsumeRegistrationToken(ctx, regToken.ID, u.ID, now)
		if err != nil {
			return err
		}
		if !consumed {
			return ErrRegistrationTokenInvalid
		}

		session, _, err = s.issue(ctx, tx, u.ID, in.RequestMeta, now)
		if err != nil {
			return err
		}
		session.User = u
		return nil
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

// Login verifies credentials and starts a session.
//
// An unknown email and a wrong password return the same error on purpose: a
// different one would turn login into an account enumeration oracle.
func (s *Service) Login(ctx context.Context, in LoginInput) (*Session, error) {
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return nil, err
	}
	if in.Password == "" {
		return nil, fmt.Errorf("%w: password is required", ErrInvalidInput)
	}

	u, err := s.store.ByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if !u.CanAuthenticate() {
		return nil, ErrUserInactive
	}
	if !u.VerifyPassword(in.Password) {
		return nil, ErrInvalidCredentials
	}

	session, _, err := s.issue(ctx, s.store, u.ID, in.RequestMeta, s.now())
	if err != nil {
		return nil, err
	}
	session.User = u
	return session, nil
}

// Refresh rotates a refresh token, revoking the presented one and returning a
// new pair.
//
// Presenting a token that was already rotated means either a replay or a stolen
// token. Since the server cannot tell which, it answers both the same way: the
// user's remaining sessions are revoked and ErrTokenReused is returned.
func (s *Service) Refresh(ctx context.Context, in RefreshInput) (*Session, error) {
	raw := strings.TrimSpace(in.RefreshToken)
	if raw == "" {
		return nil, fmt.Errorf("%w: refresh_token is required", ErrInvalidInput)
	}

	claims, err := s.issuer.Parse(raw, KindRefresh)
	if err != nil {
		return nil, err
	}

	now := s.now()
	stored, err := s.store.ByJTI(ctx, claims.ID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrTokenRevoked
		}
		return nil, err
	}

	if stored.TokenHash != HashToken(raw) {
		return nil, ErrInvalidToken
	}
	if stored.IsRevoked() {
		return nil, s.revokeFamily(ctx, stored.UserID, now)
	}
	if stored.IsExpired(now) {
		return nil, ErrTokenExpired
	}

	subject, err := claims.UserID()
	if err != nil {
		return nil, err
	}
	if stored.UserID != subject {
		return nil, ErrInvalidToken
	}

	// A disabled account keeps its accounts but loses its sessions.
	u, err := s.store.ByID(ctx, subject)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	if !u.CanAuthenticate() {
		return nil, ErrUserInactive
	}

	var session *Session
	err = s.store.WithinTx(ctx, func(tx Store) error {
		current, err := tx.ByJTI(ctx, claims.ID)
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return errTokenReused
			}
			return err
		}
		if current.IsRevoked() {
			return errTokenReused
		}

		issued, stored, err := s.issue(ctx, tx, subject, in.RequestMeta, now)
		if err != nil {
			return err
		}
		session = issued

		current.Revoke(now, stored.JTI)
		return tx.UpdateRefreshToken(ctx, current)
	})
	if errors.Is(err, errTokenReused) {
		// Lost a rotation race: another request already consumed this token,
		// so treat it as a replay of the user's token.
		return nil, s.revokeFamily(ctx, stored.UserID, now)
	}
	if err != nil {
		return nil, err
	}
	session.User = u
	return session, nil
}

// revokeFamily drops every session a user still holds and reports reuse. It
// runs outside any transaction, because the transaction that discovered the
// reuse is rolled back.
func (s *Service) revokeFamily(ctx context.Context, userID uint, now time.Time) error {
	if err := s.store.RevokeAllByUser(ctx, userID, now); err != nil {
		return err
	}
	return ErrTokenReused
}

// Logout revokes the given refresh token. It is idempotent: revoking an
// already revoked token, or logging out without one, is not an error.
func (s *Service) Logout(ctx context.Context, in LogoutInput) error {
	raw := strings.TrimSpace(in.RefreshToken)
	if raw == "" {
		return nil
	}

	claims, err := s.issuer.Parse(raw, KindRefresh)
	if err != nil {
		return err
	}

	stored, err := s.store.ByJTI(ctx, claims.ID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrTokenRevoked
		}
		return err
	}
	if stored.TokenHash != HashToken(raw) {
		return ErrInvalidToken
	}
	if stored.IsRevoked() {
		return nil
	}

	now := s.now()
	stored.Revoke(now, "")
	return s.store.UpdateRefreshToken(ctx, stored)
}

// IssueRegistrationToken mints a one time token that authorises a single
// registration, and returns the raw value, which is never recoverable later.
func (s *Service) IssueRegistrationToken(ctx context.Context, in IssueRegistrationTokenInput) (string, error) {
	ttl := in.TTL
	if ttl <= 0 {
		ttl = s.cfg.RegistrationTokenTTL
	}

	raw, err := NewOpaqueToken()
	if err != nil {
		return "", err
	}

	token := &RegistrationToken{
		TokenHash: HashToken(raw),
		IssuedBy:  in.IssuedBy,
		ExpiresAt: s.now().Add(ttl),
	}
	if err := s.store.CreateRegistrationToken(ctx, token); err != nil {
		return "", err
	}
	return raw, nil
}

// Authenticate verifies an access token and returns the caller it identifies.
// It hits no storage: an access token is its own proof.
func (s *Service) Authenticate(bearer string) (*Principal, error) {
	claims, err := s.issuer.Parse(bearer, KindAccess)
	if err != nil {
		return nil, err
	}

	userID, err := claims.UserID()
	if err != nil {
		return nil, err
	}

	return &Principal{
		UserID:    userID,
		TokenID:   claims.ID,
		ExpiresAt: claims.ExpiresAt.Time,
	}, nil
}

// User returns the account behind an authenticated caller, so a handler can
// render "me" without reaching into the store itself.
//
// A principal is what the token proved; this is the record it referred to. The
// two can disagree — a deactivated account still has a valid access token until
// it expires — which is why this reads the user rather than assembling one from
// the claims.
func (s *Service) User(ctx context.Context, userID uint) (*User, error) {
	user, err := s.store.ByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	return user, nil
}

// issue mints a session and persists its refresh token through store, which
// may be a transactional one.
func (s *Service) issue(ctx context.Context, store Store, userID uint, meta RequestMeta, now time.Time) (*Session, *RefreshToken, error) {
	session, refresh, err := s.issuer.Issue(userID, meta, now)
	if err != nil {
		return nil, nil, err
	}
	if err := store.CreateRefreshToken(ctx, refresh); err != nil {
		return nil, nil, err
	}
	return session, refresh, nil
}

func normalizeEmail(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return "", fmt.Errorf("%w: email is required", ErrInvalidInput)
	}
	if !emailPattern.MatchString(normalized) {
		return "", fmt.Errorf("%w: email is malformed", ErrInvalidInput)
	}
	return normalized, nil
}

// normalizeUsername returns nil for an omitted alias, which the model stores as
// SQL NULL. A unique index ignores NULLs in both postgres and sqlite, so any
// number of accounts may leave it unset.
func normalizeUsername(username string) (*string, error) {
	trimmed := strings.TrimSpace(username)
	if trimmed == "" {
		return nil, nil
	}
	if len(trimmed) > maxUsernameLength {
		return nil, fmt.Errorf("%w: username must be at most %d characters", ErrInvalidInput, maxUsernameLength)
	}
	if !usernamePattern.MatchString(trimmed) {
		return nil, fmt.Errorf("%w: username may only contain letters, digits, dot, dash and underscore", ErrInvalidInput)
	}
	return &trimmed, nil
}

func validatePassword(password string) error {
	switch {
	case len(password) < minPasswordLength:
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidInput, minPasswordLength)
	case len(password) > maxPasswordLength:
		return fmt.Errorf("%w: password must be at most %d characters", ErrInvalidInput, maxPasswordLength)
	}
	return nil
}
