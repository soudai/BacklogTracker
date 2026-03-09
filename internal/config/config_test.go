package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveValuesPrecedence(t *testing.T) {
	t.Helper()

	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, ".env"), []byte("BACKLOG_PROJECT_KEY=env-project\nAPP_TIMEZONE=UTC\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, ".env.local"), []byte("BACKLOG_PROJECT_KEY=local-project\nPROMPT_DIR=./custom-prompts\n"), 0o644); err != nil {
		t.Fatalf("write .env.local: %v", err)
	}

	values, err := ResolveValues(baseDir, ".env.local", []string{
		"BACKLOG_PROJECT_KEY=os-project",
		"SQLITE_DB_PATH=/tmp/from-env.sqlite3",
	}, map[string]string{
		"BACKLOG_PROJECT_KEY": "flag-project",
		"APP_TIMEZONE":        "Asia/Tokyo",
	})
	if err != nil {
		t.Fatalf("ResolveValues returned error: %v", err)
	}

	if got, want := values["BACKLOG_PROJECT_KEY"], "flag-project"; got != want {
		t.Fatalf("BACKLOG_PROJECT_KEY = %q, want %q", got, want)
	}
	if got, want := values["SQLITE_DB_PATH"], "/tmp/from-env.sqlite3"; got != want {
		t.Fatalf("SQLITE_DB_PATH = %q, want %q", got, want)
	}
	if got, want := values["PROMPT_DIR"], "./custom-prompts"; got != want {
		t.Fatalf("PROMPT_DIR = %q, want %q", got, want)
	}
	if got, want := values["APP_TIMEZONE"], "Asia/Tokyo"; got != want {
		t.Fatalf("APP_TIMEZONE = %q, want %q", got, want)
	}
}

func TestValidateProviderCredentials(t *testing.T) {
	cfg, err := New(map[string]string{
		"LLM_PROVIDER": "gemini",
		"GEMINI_MODEL": "gemini-2.5-pro",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cfg.ValidateProviderCredentials(); err == nil {
		t.Fatalf("ValidateProviderCredentials expected error")
	}

	cfg, err = New(map[string]string{
		"LLM_PROVIDER":    "chatgpt",
		"OPENAI_API_KEY":  "token",
		"OPENAI_MODEL":    "gpt-4.1",
		"BACKLOG_BASE_URL": "https://example.backlog.com",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cfg.ValidateProviderCredentials(); err != nil {
		t.Fatalf("ValidateProviderCredentials returned error: %v", err)
	}
}

func TestValidateForInitRejectsPlaceholderValues(t *testing.T) {
	cfg, err := New(map[string]string{
		"BACKLOG_BASE_URL":               "https://your-space.backlog.com",
		"BACKLOG_API_KEY":                "replace-with-local-value",
		"BACKLOG_PROJECT_KEY":            "PROJ",
		"APP_TIMEZONE":                   "Asia/Tokyo",
		"SQLITE_DB_PATH":                 "./data/backlog-tracker.sqlite3",
		"MIGRATION_DIR":                  "./migrations",
		"REPORT_DIR":                     "./data/reports",
		"RAW_RESPONSE_DIR":               "./data/raw",
		"PROMPT_PREVIEW_DIR":             "./data/prompt-previews",
		"PROMPT_DIR":                     "./prompts",
		"LLM_PROVIDER":                   "gemini",
		"GEMINI_API_KEY":                 "replace-with-local-value",
		"GEMINI_MODEL":                   "gemini-2.5-pro",
		"SLACK_WEBHOOK_URL":              "https://hooks.slack.com/services/replace/me",
		"PROMPT_ARTIFACT_RETENTION_DAYS": "30",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := cfg.ValidateForInit(); err == nil {
		t.Fatalf("ValidateForInit expected placeholder rejection")
	}
}
