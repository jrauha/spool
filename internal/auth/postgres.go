package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

const postgresUniqueViolation = "23505"

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count)
	return count, err
}

func (s *PostgresStore) CreateUser(ctx context.Context, email, passwordHash, role string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id::text, email, password_hash, role, created_at, updated_at
	`, email, passwordHash, role).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.CreatedAt, &user.UpdatedAt)
	if isUniqueViolation(err) {
		return User{}, ErrUserExists
	}
	return user, err
}

func (s *PostgresStore) FindUserByEmail(ctx context.Context, email string) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, email, password_hash, role, created_at, updated_at
		FROM users
		WHERE email = $1
	`, email).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	return user, err
}

func (s *PostgresStore) FindUserBySessionTokenHash(ctx context.Context, tokenHash string, now time.Time) (User, Session, error) {
	var user User
	var session Session
	err := s.db.QueryRowContext(ctx, `
		SELECT
			u.id::text, u.email, u.password_hash, u.role, u.created_at, u.updated_at,
			s.id::text, s.user_id::text, s.token_hash, s.expires_at, s.created_at, s.last_seen_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2
	`, tokenHash, now).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.CreatedAt, &user.UpdatedAt,
		&session.ID, &session.UserID, &session.TokenHash, &session.ExpiresAt, &session.CreatedAt, &session.LastSeenAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, Session{}, ErrSessionNotFound
	}
	return user, session, err
}

func (s *PostgresStore) CreateSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time) (Session, error) {
	var session Session
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id::text, user_id::text, token_hash, expires_at, created_at, last_seen_at
	`, userID, tokenHash, expiresAt).Scan(&session.ID, &session.UserID, &session.TokenHash, &session.ExpiresAt, &session.CreatedAt, &session.LastSeenAt)
	return session, err
}

func (s *PostgresStore) DeleteSessionByTokenHash(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *PostgresStore) TouchSession(ctx context.Context, id string, seenAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE id = $1`, id, seenAt)
	return err
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == postgresUniqueViolation
}
