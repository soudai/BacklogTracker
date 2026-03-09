package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type FileStatus struct {
	Version   string
	Applied   bool
	AppliedAt *time.Time
}

func ApplyAll(ctx context.Context, dbPath, dir string) error {
	if strings.TrimSpace(dbPath) == "" {
		return fmt.Errorf("db path is required")
	}
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("migration directory is required")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite database: %w", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}

	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	versions, err := migrationVersions(dir)
	if err != nil {
		return err
	}

	for _, version := range versions {
		applied, err := migrationApplied(ctx, db, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		content, err := os.ReadFile(filepath.Join(dir, version))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`, version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", version, err)
		}
	}

	return nil
}

func Inspect(ctx context.Context, dbPath, dir string) ([]FileStatus, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, fmt.Errorf("db path is required")
	}
	versions, err := migrationVersions(dir)
	if err != nil {
		return nil, err
	}

	appliedAtByVersion, err := appliedMigrations(ctx, dbPath)
	if err != nil {
		return nil, err
	}

	statuses := make([]FileStatus, 0, len(versions))
	for _, version := range versions {
		status := FileStatus{Version: version}
		if appliedAt, ok := appliedAtByVersion[version]; ok {
			status.Applied = true
			appliedAtCopy := appliedAt
			status.AppliedAt = &appliedAtCopy
		}
		statuses = append(statuses, status)
	}

	return statuses, nil
}

func migrationApplied(ctx context.Context, db *sql.DB, version string) (bool, error) {
	var existing string
	err := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE version = ?`, version).Scan(&existing)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("query migration %s: %w", version, err)
}

func migrationVersions(dir string) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("migration directory is required")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migration directory: %w", err)
	}

	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		versions = append(versions, entry.Name())
	}
	sort.Strings(versions)
	return versions, nil
}

func appliedMigrations(ctx context.Context, dbPath string) (map[string]time.Time, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return map[string]time.Time{}, nil
		}
		return nil, fmt.Errorf("inspect sqlite database: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	defer db.Close()

	exists, err := schemaMigrationsExists(ctx, db)
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]time.Time{}, nil
	}

	rows, err := db.QueryContext(ctx, `SELECT version, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[string]time.Time{}
	for rows.Next() {
		var version string
		var appliedAtRaw string
		if err := rows.Scan(&version, &appliedAtRaw); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}

		appliedAt, err := time.Parse(time.RFC3339, appliedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse schema_migrations applied_at for %s: %w", version, err)
		}
		applied[version] = appliedAt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}

	return applied, nil
}

func schemaMigrationsExists(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&count); err != nil {
		return false, fmt.Errorf("query sqlite_master: %w", err)
	}
	return count > 0, nil
}
