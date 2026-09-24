package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	CookieName            = "spool_session"
	DefaultSessionTTL     = 30 * 24 * time.Hour
	passwordResetTTL      = time.Hour
	passwordResetCooldown = time.Minute
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSessionNotFound    = errors.New("session not found")
	ErrSetupComplete      = errors.New("setup is already complete")
	ErrInvalidResetToken  = errors.New("invalid or expired password reset token")
	ErrPasswordTooShort   = errors.New("password must be at least 8 characters")

	errPasswordResetThrottled = errors.New("password reset requested too recently")
)

type User struct {
	ID           string
	Email        string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	ID         string
	UserID     string
	TokenHash  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastSeenAt *time.Time
}

type PasswordResetSender interface {
	SendPasswordReset(ctx context.Context, email, token string) error
}

type Store interface {
	SetupRequired(ctx context.Context) (bool, error)
	CreateFirstUser(ctx context.Context, email, passwordHash, role string) (User, error)
	FindUserByEmail(ctx context.Context, email string) (User, error)
	FindUserBySessionTokenHash(ctx context.Context, tokenHash string, now time.Time) (User, Session, error)
	CreateSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time) (Session, error)
	CreatePasswordReset(ctx context.Context, userID, tokenHash string, rejectAfter, expiresAt time.Time) error
	ResetPassword(ctx context.Context, tokenHash, passwordHash string, now time.Time) error
	DeleteSessionByTokenHash(ctx context.Context, tokenHash string) error
	TouchSession(ctx context.Context, id string, seenAt time.Time) error
}

type Service struct {
	store                 Store
	resetSender           PasswordResetSender
	sessionTTL            time.Duration
	passwordResetTTL      time.Duration
	passwordResetCooldown time.Duration
}

type LoginResult struct {
	User    User
	Session Session
	Token   string
}

func NewService(store Store) *Service {
	return &Service{
		store:                 store,
		sessionTTL:            DefaultSessionTTL,
		passwordResetTTL:      passwordResetTTL,
		passwordResetCooldown: passwordResetCooldown,
	}
}

func NewServiceWithPasswordReset(store Store, sender PasswordResetSender) *Service {
	service := NewService(store)
	service.resetSender = sender
	return service
}

func (s *Service) PasswordResetEnabled() bool {
	return s.resetSender != nil
}

func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	return s.store.SetupRequired(ctx)
}

func (s *Service) Setup(ctx context.Context, email, password string) (LoginResult, error) {
	user, err := s.createFirstUser(ctx, email, password, "admin")
	if err != nil {
		return LoginResult{}, err
	}
	return s.createSession(ctx, user)
}

func (s *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	user, err := s.store.FindUserByEmail(ctx, normalizeEmail(email))
	if err != nil {
		return LoginResult{}, ErrInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return LoginResult{}, ErrInvalidCredentials
	}
	return s.createSession(ctx, user)
}

func (s *Service) AuthenticateToken(ctx context.Context, token string) (User, Session, error) {
	if strings.TrimSpace(token) == "" {
		return User{}, Session{}, ErrSessionNotFound
	}
	user, session, err := s.store.FindUserBySessionTokenHash(ctx, HashToken(token), time.Now().UTC())
	if err != nil {
		return User{}, Session{}, ErrSessionNotFound
	}
	_ = s.store.TouchSession(ctx, session.ID, time.Now().UTC())
	return user, session, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return s.store.DeleteSessionByTokenHash(ctx, HashToken(token))
}

func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	if s.resetSender == nil {
		return nil
	}
	user, err := s.store.FindUserByEmail(ctx, normalizeEmail(email))
	if errors.Is(err, ErrInvalidCredentials) {
		return nil
	}
	if err != nil {
		return err
	}
	token, err := GenerateToken()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.store.CreatePasswordReset(ctx, user.ID, HashToken(token), now.Add(-s.passwordResetCooldown), now.Add(s.passwordResetTTL)); err != nil {
		if errors.Is(err, errPasswordResetThrottled) {
			return nil
		}
		return err
	}
	return s.resetSender.SendPasswordReset(ctx, user.Email, token)
}

func (s *Service) ResetPassword(ctx context.Context, token, password string) error {
	if strings.TrimSpace(token) == "" {
		return ErrInvalidResetToken
	}
	hash, err := passwordHash(password)
	if err != nil {
		return err
	}
	return s.store.ResetPassword(ctx, HashToken(token), hash, time.Now().UTC())
}

func (s *Service) createFirstUser(ctx context.Context, email, password, role string) (User, error) {
	email = normalizeEmail(email)
	if email == "" {
		return User{}, fmt.Errorf("email is required")
	}
	hash, err := passwordHash(password)
	if err != nil {
		return User{}, err
	}
	return s.store.CreateFirstUser(ctx, email, hash, role)
}

func (s *Service) createSession(ctx context.Context, user User) (LoginResult, error) {
	token, err := GenerateToken()
	if err != nil {
		return LoginResult{}, err
	}
	session, err := s.store.CreateSession(ctx, user.ID, HashToken(token), time.Now().UTC().Add(s.sessionTTL))
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{User: user, Session: session, Token: token}, nil
}

func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func passwordHash(password string) (string, error) {
	if len(password) < 8 {
		return "", ErrPasswordTooShort
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
