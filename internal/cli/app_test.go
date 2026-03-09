package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/soudai/BacklogTracker/internal/migrations"
	"github.com/soudai/BacklogTracker/internal/storage/sqlite"
)

func TestRunCommandRouting(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		args           []string
		wantExitCode   int
		wantStdout     string
		wantStderr     string
		stderrContains string
	}{
		{
			name:           "no args",
			args:           nil,
			wantExitCode:   ExitCodeInput,
			stderrContains: "Usage:",
		},
		{
			name:           "unknown command",
			args:           []string{"unknown"},
			wantExitCode:   ExitCodeInput,
			stderrContains: "unknown subcommand",
		},
		{
			name:         "root help",
			args:         []string{"help"},
			wantExitCode: ExitCodeOK,
			wantStdout:   "Usage:",
		},
		{
			name:           "init help",
			args:           []string{"init", "--help"},
			wantExitCode:   ExitCodeOK,
			stderrContains: "Usage of init:",
		},
		{
			name:           "period summary help",
			args:           []string{"period-summary", "--help"},
			wantExitCode:   ExitCodeOK,
			stderrContains: "Usage of period-summary:",
		},
		{
			name:           "account report help",
			args:           []string{"account-report", "--help"},
			wantExitCode:   ExitCodeOK,
			stderrContains: "Usage of account-report:",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}

			got := Run(context.Background(), testCase.args, strings.NewReader(""), stdout, stderr)
			if got != testCase.wantExitCode {
				t.Fatalf("Run exit code = %d, want %d", got, testCase.wantExitCode)
			}

			if testCase.wantStdout != "" && !strings.Contains(stdout.String(), testCase.wantStdout) {
				t.Fatalf("stdout = %q, want substring %q", stdout.String(), testCase.wantStdout)
			}
			if testCase.wantStderr != "" && !strings.Contains(stderr.String(), testCase.wantStderr) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), testCase.wantStderr)
			}
			if testCase.stderrContains != "" && !strings.Contains(stderr.String(), testCase.stderrContains) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), testCase.stderrContains)
			}
		})
	}
}

func TestRunInitReturnsExitCodeInitOnMigrationError(t *testing.T) {
	t.Parallel()

	envFile := filepath.Join(t.TempDir(), ".env.test")
	if err := os.WriteFile(envFile, []byte("MIGRATION_DIR=/path/that/does/not/exist\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Run(
		context.Background(),
		[]string{"init", "--migrate-only", "--db-path", filepath.Join(t.TempDir(), "tracker.sqlite3"), "--env-file", envFile},
		strings.NewReader(""),
		stdout,
		stderr,
	)

	if exitCode != ExitCodeInit {
		t.Fatalf("Run exit code = %d, want %d", exitCode, ExitCodeInit)
	}
	if !strings.Contains(stderr.String(), "init failed:") {
		t.Fatalf("stderr = %q, want init failure message", stderr.String())
	}
}

func TestRunPeriodSummaryDryRunRendersPromptAndPersistsArtifacts(t *testing.T) {
	baseDir := t.TempDir()
	envFile := filepath.Join(baseDir, ".env.local")
	dbPath := filepath.Join(baseDir, "data", "tracker.sqlite3")
	previewDir := filepath.Join(baseDir, "data", "prompt-previews")
	promptDir := filepath.Join(baseDir, "prompts")

	writePromptFixtures(t, promptDir)
	writeEnvFile(t, envFile, map[string]string{
		"BACKLOG_BASE_URL":               "https://example.backlog.com",
		"BACKLOG_API_KEY":                "test-backlog-key",
		"BACKLOG_PROJECT_KEY":            "PROJ",
		"SQLITE_DB_PATH":                 dbPath,
		"PROMPT_DIR":                     promptDir,
		"PROMPT_PREVIEW_DIR":             previewDir,
		"PROMPT_ARTIFACT_RETENTION_DAYS": "30",
	})
	if err := migrations.ApplyAll(context.Background(), dbPath, repoMigrationDir(t)); err != nil {
		t.Fatalf("ApplyAll returned error: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Run(
		context.Background(),
		[]string{"period-summary", "--dry-run", "--project", "PROJ", "--from", "2026-03-01", "--to", "2026-03-07", "--env-file", envFile},
		strings.NewReader(""),
		stdout,
		stderr,
	)

	if exitCode != ExitCodeOK {
		t.Fatalf("Run exit code = %d, want %d; stderr=%q", exitCode, ExitCodeOK, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "--- SYSTEM ---") {
		t.Fatalf("stdout = %q, want rendered system prompt", stdout.String())
	}
	if !strings.Contains(stdout.String(), "projectKey: PROJ") {
		t.Fatalf("stdout = %q, want rendered project key", stdout.String())
	}

	jobID := extractPrefixedValue(t, stdout.String(), "job_id: ")
	previewPath := extractPrefixedValue(t, stdout.String(), "preview_path: ")
	if _, err := os.Stat(previewPath); err != nil {
		t.Fatalf("preview path missing: %v", err)
	}
	if !strings.HasPrefix(previewPath, previewDir) {
		t.Fatalf("previewPath = %q, want prefix %q", previewPath, previewDir)
	}

	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	jobRun, err := store.JobRuns().GetByJobID(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetByJobID returned error: %v", err)
	}
	if jobRun.PromptHash == nil || *jobRun.PromptHash == "" {
		t.Fatalf("jobRun.PromptHash = %v, want a saved prompt hash", jobRun.PromptHash)
	}

	promptRuns, err := store.PromptRuns().ListByJobID(context.Background(), jobID)
	if err != nil {
		t.Fatalf("ListByJobID returned error: %v", err)
	}
	if len(promptRuns) != 1 {
		t.Fatalf("prompt run count = %d, want 1", len(promptRuns))
	}
	if promptRuns[0].RenderedPromptPath == nil || *promptRuns[0].RenderedPromptPath != previewPath {
		t.Fatalf("RenderedPromptPath = %v, want %q", promptRuns[0].RenderedPromptPath, previewPath)
	}
}

func TestRunAccountReportDryRunRequiresAccount(t *testing.T) {
	baseDir := t.TempDir()
	envFile := filepath.Join(baseDir, ".env.local")
	promptDir := filepath.Join(baseDir, "prompts")
	writePromptFixtures(t, promptDir)
	writeEnvFile(t, envFile, map[string]string{
		"BACKLOG_BASE_URL":    "https://example.backlog.com",
		"BACKLOG_API_KEY":     "test-backlog-key",
		"BACKLOG_PROJECT_KEY": "PROJ",
		"SQLITE_DB_PATH":      filepath.Join(baseDir, "data", "tracker.sqlite3"),
		"PROMPT_DIR":          promptDir,
		"PROMPT_PREVIEW_DIR":  filepath.Join(baseDir, "data", "prompt-previews"),
	})

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Run(
		context.Background(),
		[]string{"account-report", "--dry-run", "--project", "PROJ", "--env-file", envFile},
		strings.NewReader(""),
		stdout,
		stderr,
	)

	if exitCode != ExitCodeInput {
		t.Fatalf("Run exit code = %d, want %d", exitCode, ExitCodeInput)
	}
	if !strings.Contains(stderr.String(), "--account is required") {
		t.Fatalf("stderr = %q, want account validation", stderr.String())
	}
}

func writePromptFixtures(t *testing.T, promptDir string) {
	t.Helper()

	for _, task := range []string{"period_summary", "account_report"} {
		taskDir := filepath.Join(promptDir, task)
		if err := os.MkdirAll(taskDir, 0o755); err != nil {
			t.Fatalf("create prompt dir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(promptDir, "period_summary", "system.tmpl"), []byte("system period summary"), 0o644); err != nil {
		t.Fatalf("write period_summary system template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "period_summary", "user.tmpl"), []byte("projectKey: {{ .ProjectKey }}\nfrom: {{ .DateFrom }}\nto: {{ .DateTo }}\nschema: {{ .OutputSchemaJSON }}"), 0o644); err != nil {
		t.Fatalf("write period_summary user template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "account_report", "system.tmpl"), []byte("system account report"), 0o644); err != nil {
		t.Fatalf("write account_report system template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "account_report", "user.tmpl"), []byte("account: {{ .AccountID }}"), 0o644); err != nil {
		t.Fatalf("write account_report user template: %v", err)
	}
}

func writeEnvFile(t *testing.T, path string, values map[string]string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create env dir: %v", err)
	}
	var builder strings.Builder
	for key, value := range values {
		builder.WriteString(key)
		builder.WriteString("=")
		builder.WriteString(value)
		builder.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}
}

func extractPrefixedValue(t *testing.T, output, prefix string) string {
	t.Helper()

	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	t.Fatalf("output missing prefix %q: %q", prefix, output)
	return ""
}

func repoMigrationDir(t *testing.T) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve current file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "migrations"))
}
