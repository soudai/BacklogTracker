package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/soudai/BacklogTracker/internal/migrations"
)

func TestStoreRepositoriesPersistRecords(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "tracker.sqlite3")
	migrationDir := filepath.Join(baseDir, "migrations")

	writeInitialMigration(t, migrationDir)
	if err := migrations.ApplyAll(ctx, dbPath, migrationDir); err != nil {
		t.Fatalf("ApplyAll returned error: %v", err)
	}

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	jobRepo := store.JobRuns()
	notificationRepo := store.NotificationLogs()
	promptRepo := store.PromptRuns()

	targetAccount := "yamada"
	promptName := "period_summary"
	promptHash := "prompt-hash"
	issueCount := 3
	reportPath := filepath.Join(baseDir, "report.md")
	rawResponsePath := filepath.Join(baseDir, "raw.json")
	startedAt := time.Date(2026, 3, 9, 10, 0, 0, 0, time.UTC)

	if err := jobRepo.Save(ctx, JobRun{
		JobID:           "job-1",
		JobType:         "period_summary",
		Provider:        "gemini",
		ProjectKey:      "PROJ",
		TargetAccount:   &targetAccount,
		Status:          "running",
		PromptName:      &promptName,
		PromptHash:      &promptHash,
		IssueCount:      &issueCount,
		ReportPath:      &reportPath,
		RawResponsePath: &rawResponsePath,
		StartedAt:       startedAt,
	}); err != nil {
		t.Fatalf("Save job run returned error: %v", err)
	}

	finishedAt := startedAt.Add(5 * time.Minute)
	if err := jobRepo.UpdateStatus(ctx, "job-1", JobRunStatusUpdate{
		Status:     "completed",
		FinishedAt: &finishedAt,
	}); err != nil {
		t.Fatalf("UpdateStatus returned error: %v", err)
	}

	run, err := jobRepo.GetByJobID(ctx, "job-1")
	if err != nil {
		t.Fatalf("GetByJobID returned error: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("run.Status = %q, want completed", run.Status)
	}
	if run.FinishedAt == nil || !run.FinishedAt.Equal(finishedAt) {
		t.Fatalf("run.FinishedAt = %v, want %v", run.FinishedAt, finishedAt)
	}
	if run.TargetAccount == nil || *run.TargetAccount != targetAccount {
		t.Fatalf("run.TargetAccount = %v, want %q", run.TargetAccount, targetAccount)
	}

	destination := "slack://incoming-webhook"
	responseSummary := "ok"
	sentAt := finishedAt.Add(time.Minute)
	if err := notificationRepo.Save(ctx, NotificationLog{
		JobID:           "job-1",
		ChannelType:     "slack",
		Destination:     &destination,
		Status:          "sent",
		ResponseSummary: &responseSummary,
		SentAt:          &sentAt,
	}); err != nil {
		t.Fatalf("Save notification log returned error: %v", err)
	}

	notificationLogs, err := notificationRepo.ListByJobID(ctx, "job-1")
	if err != nil {
		t.Fatalf("ListByJobID notification logs returned error: %v", err)
	}
	if len(notificationLogs) != 1 {
		t.Fatalf("notification log count = %d, want 1", len(notificationLogs))
	}
	if notificationLogs[0].Destination == nil || *notificationLogs[0].Destination != destination {
		t.Fatalf("notification destination = %v, want %q", notificationLogs[0].Destination, destination)
	}

	renderedPromptPath := filepath.Join(baseDir, "prompt.txt")
	createdAt := startedAt.Add(30 * time.Second)
	if err := promptRepo.Save(ctx, PromptRun{
		JobID:              "job-1",
		TaskType:           "period_summary",
		SystemTemplate:     "system template",
		UserTemplate:       "user template",
		PromptHash:         "prompt-hash",
		RenderedPromptPath: &renderedPromptPath,
		CreatedAt:          createdAt,
	}); err != nil {
		t.Fatalf("Save prompt run returned error: %v", err)
	}

	promptRuns, err := promptRepo.ListByJobID(ctx, "job-1")
	if err != nil {
		t.Fatalf("ListByJobID prompt runs returned error: %v", err)
	}
	if len(promptRuns) != 1 {
		t.Fatalf("prompt run count = %d, want 1", len(promptRuns))
	}
	if promptRuns[0].RenderedPromptPath == nil || *promptRuns[0].RenderedPromptPath != renderedPromptPath {
		t.Fatalf("prompt rendered path = %v, want %q", promptRuns[0].RenderedPromptPath, renderedPromptPath)
	}
}

func TestInspectReturnsAppliedAndPendingVersions(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	dbPath := filepath.Join(baseDir, "tracker.sqlite3")
	migrationDir := filepath.Join(baseDir, "migrations")

	writeInitialMigration(t, migrationDir)

	statuses, err := migrations.Inspect(ctx, dbPath, migrationDir)
	if err != nil {
		t.Fatalf("Inspect returned error before apply: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("status count = %d, want 1", len(statuses))
	}
	if statuses[0].Applied {
		t.Fatalf("status unexpectedly marked as applied before ApplyAll")
	}

	if err := migrations.ApplyAll(ctx, dbPath, migrationDir); err != nil {
		t.Fatalf("ApplyAll returned error: %v", err)
	}

	statuses, err = migrations.Inspect(ctx, dbPath, migrationDir)
	if err != nil {
		t.Fatalf("Inspect returned error after apply: %v", err)
	}
	if !statuses[0].Applied {
		t.Fatalf("status not marked as applied after ApplyAll")
	}
	if statuses[0].AppliedAt == nil {
		t.Fatalf("status missing applied_at after ApplyAll")
	}
}

func TestGetByJobIDRejectsEmptyJobID(t *testing.T) {
	repo := &JobRunRepository{}

	_, err := repo.GetByJobID(context.Background(), "   ")
	if err == nil {
		t.Fatalf("GetByJobID expected validation error")
	}
	if !strings.Contains(err.Error(), "job_id is required") {
		t.Fatalf("GetByJobID error = %q, want validation message", err.Error())
	}
}

func TestScanJobRunWrapsScanError(t *testing.T) {
	_, err := scanJobRun(scanStub{err: errors.New("boom")})
	if err == nil {
		t.Fatalf("scanJobRun expected error")
	}
	if !strings.Contains(err.Error(), "scan job_run: boom") {
		t.Fatalf("scanJobRun error = %q, want wrapped scan error", err.Error())
	}
}

func writeInitialMigration(t *testing.T, migrationDir string) {
	t.Helper()

	if err := os.MkdirAll(migrationDir, 0o755); err != nil {
		t.Fatalf("create migration dir: %v", err)
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve current file path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	migrationPath := filepath.Join(repoRoot, "migrations", "0001_initial.sql")
	content, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrationDir, "0001_initial.sql"), content, 0o644); err != nil {
		t.Fatalf("write migration file: %v", err)
	}
}

type scanStub struct {
	err error
}

func (s scanStub) Scan(_ ...any) error {
	return s.err
}
