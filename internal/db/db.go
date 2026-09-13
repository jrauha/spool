package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"

	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrations embed.FS

const migrationLockID int64 = 810314792318951140

func Open(databaseURL string) (*sql.DB, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("SPOOL_DATABASE_URL is required")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockID)

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}

	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
