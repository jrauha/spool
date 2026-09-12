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

type fakeStore struct {
	mu           sync.Mutex
	usersByEmail map[string]User
	sessions     map[string]Session
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
