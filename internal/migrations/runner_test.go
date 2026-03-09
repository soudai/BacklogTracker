package migrations

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestApplyAllIsIdempotent(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "tracker.sqlite3")
	migrationDir := filepath.Join(baseDir, "migrations")
	if err := os.MkdirAll(migrationDir, 0o755); err != nil {
		t.Fatalf("create migration dir: %v", err)
	}

	migrationSQL := `
CREATE TABLE IF NOT EXISTS sample_items (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL
);
`
	if err := os.WriteFile(filepath.Join(migrationDir, "0001_sample.sql"), []byte(migrationSQL), 0o644); err != nil {
		t.Fatalf("write migration file: %v", err)
	}

	if err := ApplyAll(context.Background(), dbPath, migrationDir); err != nil {
		t.Fatalf("first ApplyAll returned error: %v", err)
	}
	if err := ApplyAll(context.Background(), dbPath, migrationDir); err != nil {
		t.Fatalf("second ApplyAll returned error: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	defer db.Close()

	var migrationCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, "0001_sample.sql").Scan(&migrationCount); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration count = %d, want 1", migrationCount)
	}

	var tableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'sample_items'`).Scan(&tableCount); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if tableCount != 1 {
		t.Fatalf("table count = %d, want 1", tableCount)
	}
}
