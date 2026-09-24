package auth

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	testEmail = "user@example.com"
	testPass  = "password123"
)

func TestGenerateTokenAndHash(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken returned error: %v", err)
	}
	if token == "" {
		t.Fatal("GenerateToken returned empty token")
	}
	if HashToken(token) == token {
		t.Fatal("HashToken returned raw token")
	}
	if HashToken(token) != HashToken(token) {
		t.Fatal("HashToken is not stable")
	}
}

func TestSetupCreatesAdminSession(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	result, err := svc.Setup(context.Background(), " USER@example.COM ", testPass)
	if err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}

	if result.User.Email != testEmail {
		t.Fatalf("email = %q, want %q", result.User.Email, testEmail)
	}
	if result.User.Role != "admin" {
		t.Fatalf("role = %q, want admin", result.User.Role)
	}
	if result.User.PasswordHash == testPass {
		t.Fatal("password stored in plaintext")
	}
	if bcrypt.CompareHashAndPassword([]byte(result.User.PasswordHash), []byte(testPass)) != nil {
		t.Fatal("password hash does not verify")
	}
	if result.Token == "" {
		t.Fatal("session token is empty")
	}
	if result.Session.TokenHash != HashToken(result.Token) {
		t.Fatal("session token hash mismatch")
	}
}

func TestSetupRejectsExistingUser(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	if _, err := svc.Setup(context.Background(), testEmail, testPass); err != nil {
		t.Fatalf("first Setup returned error: %v", err)
	}
	_, err := svc.Setup(context.Background(), "other@example.com", testPass)
	if !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("second Setup error = %v, want %v", err, ErrSetupComplete)
	}
}

func TestLogin(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	if _, err := svc.Setup(context.Background(), testEmail, testPass); err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}
	result, err := svc.Login(context.Background(), " USER@example.COM ", testPass)
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}

	if result.User.Email != testEmail {
		t.Fatalf("email = %q, want %q", result.User.Email, testEmail)
	}
	if result.Token == "" {
		t.Fatal("session token is empty")
	}
}

func TestLoginRejectsBadPassword(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	if _, err := svc.Setup(context.Background(), testEmail, testPass); err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}
	_, err := svc.Login(context.Background(), testEmail, "wrong-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login error = %v, want %v", err, ErrInvalidCredentials)
	}
}

func TestAuthenticateToken(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	setup, err := svc.Setup(context.Background(), testEmail, testPass)
	if err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}
	user, session, err := svc.AuthenticateToken(context.Background(), setup.Token)
	if err != nil {
		t.Fatalf("AuthenticateToken returned error: %v", err)
	}

	if user.ID != setup.User.ID {
		t.Fatalf("user ID = %q, want %q", user.ID, setup.User.ID)
	}
	if session.ID != setup.Session.ID {
		t.Fatalf("session ID = %q, want %q", session.ID, setup.Session.ID)
	}
	if store.sessions[session.ID].LastSeenAt == nil {
		t.Fatal("session was not touched")
	}
}

func TestAuthenticateTokenRejectsMissing(t *testing.T) {
	svc := NewService(newFakeStore())

	_, _, err := svc.AuthenticateToken(context.Background(), "")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("AuthenticateToken error = %v, want %v", err, ErrSessionNotFound)
	}
}

func TestLogoutDeletesSession(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store)

	setup, err := svc.Setup(context.Background(), testEmail, testPass)
	if err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}
	if err := svc.Logout(context.Background(), setup.Token); err != nil {
		t.Fatalf("Logout returned error: %v", err)
	}
	_, _, err = svc.AuthenticateToken(context.Background(), setup.Token)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("AuthenticateToken error = %v, want %v", err, ErrSessionNotFound)
	}
}

func TestPasswordResetFlow(t *testing.T) {
	store := newFakeStore()
	sender := &fakeResetSender{}
	svc := NewServiceWithPasswordReset(store, sender)
	setup, err := svc.Setup(context.Background(), testEmail, testPass)
	if err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}

	if err := svc.RequestPasswordReset(context.Background(), " USER@example.COM "); err != nil {
		t.Fatalf("RequestPasswordReset returned error: %v", err)
	}
	if sender.email != testEmail || sender.token == "" {
		t.Fatalf("reset delivery = (%q, %q), want email and token", sender.email, sender.token)
	}
	if store.reset.TokenHash == sender.token {
		t.Fatal("reset token stored in plaintext")
	}
	if err := svc.ResetPassword(context.Background(), sender.token, "new-password"); err != nil {
		t.Fatalf("ResetPassword returned error: %v", err)
	}
	if _, _, err := svc.AuthenticateToken(context.Background(), setup.Token); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("AuthenticateToken error = %v, want %v", err, ErrSessionNotFound)
	}
	if _, err := svc.Login(context.Background(), testEmail, testPass); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password Login error = %v, want %v", err, ErrInvalidCredentials)
	}
	if _, err := svc.Login(context.Background(), testEmail, "new-password"); err != nil {
		t.Fatalf("new password Login returned error: %v", err)
	}
	if err := svc.ResetPassword(context.Background(), sender.token, "another-password"); !errors.Is(err, ErrInvalidResetToken) {
		t.Fatalf("reused ResetPassword error = %v, want %v", err, ErrInvalidResetToken)
	}
}

func TestPasswordResetRequestDoesNotRevealUnknownEmail(t *testing.T) {
	sender := &fakeResetSender{}
	svc := NewServiceWithPasswordReset(newFakeStore(), sender)

	if err := svc.RequestPasswordReset(context.Background(), "missing@example.com"); err != nil {
		t.Fatalf("RequestPasswordReset returned error: %v", err)
	}
	if sender.token != "" {
		t.Fatal("reset sent for unknown email")
	}
}

type fakeResetSender struct {
	email string
	token string
}

func (s *fakeResetSender) SendPasswordReset(ctx context.Context, email, token string) error {
	s.email = email
	s.token = token
	return nil
}

type fakePasswordReset struct {
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

type fakeStore struct {
	mu           sync.Mutex
	usersByEmail map[string]User
	sessions     map[string]Session
	reset        fakePasswordReset
	nextID       int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		usersByEmail: make(map[string]User),
		sessions:     make(map[string]Session),
	}
}

func (s *fakeStore) SetupRequired(ctx context.Context) (bool, error) {
	return len(s.usersByEmail) == 0, nil
}

func (s *fakeStore) CreateFirstUser(ctx context.Context, email, passwordHash, role string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.usersByEmail) != 0 {
		return User{}, ErrSetupComplete
	}
	return s.createUser(email, passwordHash, role)
}

func (s *fakeStore) createUser(email, passwordHash, role string) (User, error) {
	now := time.Now().UTC()
	user := User{
		ID:           s.id(),
		Email:        email,
		PasswordHash: passwordHash,
		Role:         role,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.usersByEmail[email] = user
	return user, nil
}

func (s *fakeStore) FindUserByEmail(ctx context.Context, email string) (User, error) {
	user, ok := s.usersByEmail[email]
	if !ok {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (s *fakeStore) FindUserBySessionTokenHash(ctx context.Context, tokenHash string, now time.Time) (User, Session, error) {
	for _, session := range s.sessions {
		if session.TokenHash != tokenHash || !session.ExpiresAt.After(now) {
			continue
		}
		for _, user := range s.usersByEmail {
			if user.ID == session.UserID {
				return user, session, nil
			}
		}
	}
	return User{}, Session{}, ErrSessionNotFound
}

func (s *fakeStore) CreateSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time) (Session, error) {
	session := Session{
		ID:        s.id(),
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now().UTC(),
	}
	s.sessions[session.ID] = session
	return session, nil
}

func (s *fakeStore) CreatePasswordReset(ctx context.Context, userID, tokenHash string, rejectAfter, expiresAt time.Time) error {
	s.reset = fakePasswordReset{UserID: userID, TokenHash: tokenHash, ExpiresAt: expiresAt}
	return nil
}

func (s *fakeStore) ResetPassword(ctx context.Context, tokenHash, passwordHash string, now time.Time) error {
	if s.reset.TokenHash != tokenHash || !s.reset.ExpiresAt.After(now) || s.reset.UsedAt != nil {
		return ErrInvalidResetToken
	}
	usedAt := now
	s.reset.UsedAt = &usedAt
	for email, user := range s.usersByEmail {
		if user.ID == s.reset.UserID {
			user.PasswordHash = passwordHash
			s.usersByEmail[email] = user
		}
	}
	for id, session := range s.sessions {
		if session.UserID == s.reset.UserID {
			delete(s.sessions, id)
		}
	}
	return nil
}

func (s *fakeStore) DeleteSessionByTokenHash(ctx context.Context, tokenHash string) error {
	for id, session := range s.sessions {
		if session.TokenHash == tokenHash {
			delete(s.sessions, id)
		}
	}
	return nil
}

func (s *fakeStore) TouchSession(ctx context.Context, id string, seenAt time.Time) error {
	session, ok := s.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	session.LastSeenAt = &seenAt
	s.sessions[id] = session
	return nil
}

func (s *fakeStore) id() string {
	s.nextID++
	return "id-" + strconv.Itoa(s.nextID)
}
