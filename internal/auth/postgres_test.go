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

	user, err := store.CreateFirstUser(ctx, testEmail, "hash", "admin")
	if err != nil {
		t.Fatalf("CreateFirstUser returned error: %v", err)
	}
	if user.ID == "" {
		t.Fatal("user ID is empty")
	}

	setupRequired, err := store.SetupRequired(ctx)
	if err != nil {
		t.Fatalf("SetupRequired returned error: %v", err)
	}
	if setupRequired {
		t.Fatal("setup is still required after user creation")
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

func TestPostgresStorePasswordReset(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()
	user, err := store.CreateFirstUser(ctx, testEmail, "old-hash", "admin")
	if err != nil {
		t.Fatalf("CreateFirstUser returned error: %v", err)
	}
	if _, err := store.CreateSession(ctx, user.ID, "session-hash", time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	if err := store.CreatePasswordReset(ctx, user.ID, "reset-hash", time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("CreatePasswordReset returned error: %v", err)
	}
	if err := store.ResetPassword(ctx, "reset-hash", "new-hash", time.Now().UTC()); err != nil {
		t.Fatalf("ResetPassword returned error: %v", err)
	}
	found, err := store.FindUserByEmail(ctx, testEmail)
	if err != nil || found.PasswordHash != "new-hash" {
		t.Fatalf("user = %#v, error = %v", found, err)
	}
	if _, _, err := store.FindUserBySessionTokenHash(ctx, "session-hash", time.Now().UTC()); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("session lookup error = %v, want %v", err, ErrSessionNotFound)
	}
	if err := store.ResetPassword(ctx, "reset-hash", "other-hash", time.Now().UTC()); !errors.Is(err, ErrInvalidResetToken) {
		t.Fatalf("reused ResetPassword error = %v, want %v", err, ErrInvalidResetToken)
	}
}

func TestPostgresStoreCreateFirstUserRejectsSetupComplete(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()

	if _, err := store.CreateFirstUser(ctx, testEmail, "hash", "admin"); err != nil {
		t.Fatalf("CreateFirstUser returned error: %v", err)
	}
	_, err := store.CreateFirstUser(ctx, "other@example.com", "hash", "admin")
	if !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("CreateFirstUser error = %v, want %v", err, ErrSetupComplete)
	}
}

func TestPostgresStoreExpiredSession(t *testing.T) {
	database := openTestDB(t)
	store := NewPostgresStore(database)
	ctx := context.Background()

	user, err := store.CreateFirstUser(ctx, testEmail, "hash", "admin")
	if err != nil {
		t.Fatalf("CreateFirstUser returned error: %v", err)
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
