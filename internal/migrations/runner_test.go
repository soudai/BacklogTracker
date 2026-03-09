package migrations

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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

func TestInspectReportsAppliedAndPendingMigrations(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "tracker.sqlite3")
	migrationDir := filepath.Join(baseDir, "migrations")
	if err := os.MkdirAll(migrationDir, 0o755); err != nil {
		t.Fatalf("create migration dir: %v", err)
	}

	firstMigration := `
CREATE TABLE IF NOT EXISTS first_items (
    id INTEGER PRIMARY KEY
);
`
	secondMigration := `
CREATE TABLE IF NOT EXISTS second_items (
    id INTEGER PRIMARY KEY
);
`
	if err := os.WriteFile(filepath.Join(migrationDir, "0001_first.sql"), []byte(firstMigration), 0o644); err != nil {
		t.Fatalf("write first migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrationDir, "0002_second.sql"), []byte(secondMigration), 0o644); err != nil {
		t.Fatalf("write second migration: %v", err)
	}

	statuses, err := Inspect(context.Background(), dbPath, migrationDir)
	if err != nil {
		t.Fatalf("Inspect returned error before apply: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("status count = %d, want 2", len(statuses))
	}
	for _, status := range statuses {
		if status.Applied {
			t.Fatalf("status %s unexpectedly marked as applied", status.Version)
		}
		if status.AppliedAt != nil {
			t.Fatalf("status %s has applied_at before apply", status.Version)
		}
	}

	if err := ApplyAll(context.Background(), dbPath, migrationDir); err != nil {
		t.Fatalf("ApplyAll returned error: %v", err)
	}

	statuses, err = Inspect(context.Background(), dbPath, migrationDir)
	if err != nil {
		t.Fatalf("Inspect returned error after apply: %v", err)
	}
	for _, status := range statuses {
		if !status.Applied {
			t.Fatalf("status %s not marked as applied", status.Version)
		}
		if status.AppliedAt == nil {
			t.Fatalf("status %s missing applied_at", status.Version)
		}
	}
}

func TestApplyAllEnforcesForeignKeys(t *testing.T) {
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "tracker.sqlite3")
	migrationDir := filepath.Join(baseDir, "migrations")
	if err := os.MkdirAll(migrationDir, 0o755); err != nil {
		t.Fatalf("create migration dir: %v", err)
	}

	migrationSQL := `
CREATE TABLE IF NOT EXISTS parents (
    id INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS children (
    id INTEGER PRIMARY KEY,
    parent_id INTEGER NOT NULL,
    FOREIGN KEY(parent_id) REFERENCES parents(id)
);

INSERT INTO children(id, parent_id) VALUES (1, 999);
`
	if err := os.WriteFile(filepath.Join(migrationDir, "0001_fk.sql"), []byte(migrationSQL), 0o644); err != nil {
		t.Fatalf("write migration file: %v", err)
	}

	err := ApplyAll(context.Background(), dbPath, migrationDir)
	if err == nil {
		t.Fatalf("ApplyAll expected foreign key failure")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("ApplyAll error = %q, want foreign key failure", err.Error())
	}
}
