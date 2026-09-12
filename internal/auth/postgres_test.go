package auth

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	dbkit "github.com/spool-reader/spool/internal/db"
)

const testDBEnv = "SPOOL_TEST_DATABASE_URL"

func TestPostgresStoreUserSessionFlow(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, testEmail, "hash", "admin")
	if err != nil {
		t.Fatalf("CreateUser returned error: %v", err)
	}
	if user.ID == "" {
		t.Fatal("user ID is empty")
	}

	count, err := store.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	found, err := store.FindUserByEmail(ctx, testEmail)
	if err != nil {
		t.Fatalf("FindUserByEmail returned error: %v", err)
	}
	if found.ID != user.ID {
		t.Fatalf("user ID = %q, want %q", found.ID, user.ID)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	session, err := store.CreateSession(ctx, user.ID, "token-hash", expiresAt)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	if session.UserID != user.ID {
		t.Fatalf("session user ID = %q, want %q", session.UserID, user.ID)
	}

	authedUser, authedSession, err := store.FindUserBySessionTokenHash(ctx, "token-hash", time.Now().UTC())
	if err != nil {
		t.Fatalf("FindUserBySessionTokenHash returned error: %v", err)
	}
	if authedUser.ID != user.ID {
		t.Fatalf("authed user ID = %q, want %q", authedUser.ID, user.ID)
	}
	if authedSession.ID != session.ID {
		t.Fatalf("authed session ID = %q, want %q", authedSession.ID, session.ID)
	}

	seenAt := time.Now().UTC()
	if err := store.TouchSession(ctx, session.ID, seenAt); err != nil {
		t.Fatalf("TouchSession returned error: %v", err)
	}

	_, touched, err := store.FindUserBySessionTokenHash(ctx, "token-hash", time.Now().UTC())
	if err != nil {
		t.Fatalf("FindUserBySessionTokenHash returned error: %v", err)
	}
	if touched.LastSeenAt == nil {
		t.Fatal("last seen was not set")
	}

	if err := store.DeleteSessionByTokenHash(ctx, "token-hash"); err != nil {
		t.Fatalf("DeleteSessionByTokenHash returned error: %v", err)
	}
	_, _, err = store.FindUserBySessionTokenHash(ctx, "token-hash", time.Now().UTC())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("FindUserBySessionTokenHash error = %v, want %v", err, ErrSessionNotFound)
	}
}

func TestPostgresStoreDuplicateUser(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()

	if _, err := store.CreateUser(ctx, testEmail, "hash", "admin"); err != nil {
		t.Fatalf("CreateUser returned error: %v", err)
	}
	_, err := store.CreateUser(ctx, testEmail, "hash", "admin")
	if !errors.Is(err, ErrUserExists) {
		t.Fatalf("CreateUser error = %v, want %v", err, ErrUserExists)
	}
}

func TestPostgresStoreExpiredSession(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, testEmail, "hash", "admin")
	if err != nil {
		t.Fatalf("CreateUser returned error: %v", err)
	}
	_, err = store.CreateSession(ctx, user.ID, "expired-token", time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}

	_, _, err = store.FindUserBySessionTokenHash(ctx, "expired-token", time.Now().UTC())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("FindUserBySessionTokenHash error = %v, want %v", err, ErrSessionNotFound)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	url := os.Getenv(testDBEnv)
	if url == "" {
		t.Skip(testDBEnv + " not set")
	}

	database, err := dbkit.Open(url)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	ctx := context.Background()
	if err := dbkit.Migrate(ctx, database); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	if _, err := database.ExecContext(ctx, `TRUNCATE sessions, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate auth tables: %v", err)
	}

	return database
}
