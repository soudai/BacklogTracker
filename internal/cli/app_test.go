package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/soudai/BacklogTracker/internal/accountreport"
	"github.com/soudai/BacklogTracker/internal/config"
	"github.com/soudai/BacklogTracker/internal/llm"
	"github.com/soudai/BacklogTracker/internal/migrations"
	notificationslack "github.com/soudai/BacklogTracker/internal/notifications/slack"
	"github.com/soudai/BacklogTracker/internal/periodsummary"
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
	restoreProvider := stubLLMProvider(t, llm.GenerateResult{
		Output: llm.PeriodSummaryOutput{
			ReportType: "period_summary",
			Headline:   "headline",
			Overview:   "overview",
			KeyPoints:  []string{"point"},
			RiskItems:  []llm.PeriodSummaryRiskItem{},
			Counts:     llm.PeriodSummaryCounts{Total: 1},
		},
		OutputJSON:  []byte(`{"reportType":"period_summary","headline":"headline","overview":"overview","keyPoints":["point"],"riskItems":[],"counts":{"total":1}}`),
		RawResponse: []byte(`{"provider":"stub"}`),
	}, nil)
	defer restoreProvider()

	baseDir := t.TempDir()
	envFile := filepath.Join(baseDir, ".env.local")
	dbPath := filepath.Join(baseDir, "data", "tracker.sqlite3")
	previewDir := filepath.Join(baseDir, "data", "prompt-previews")
	rawDir := filepath.Join(baseDir, "data", "raw")
	reportDir := filepath.Join(baseDir, "data", "reports")
	promptDir := filepath.Join(baseDir, "prompts")
	backlogServer := startBacklogTestServer(t)
	defer backlogServer.Close()

	writePromptFixtures(t, promptDir)
	writeEnvFile(t, envFile, map[string]string{
		"BACKLOG_BASE_URL":               backlogServer.URL,
		"BACKLOG_API_KEY":                "test-backlog-key",
		"BACKLOG_PROJECT_KEY":            "PROJ",
		"LLM_PROVIDER":                   "gemini",
		"GEMINI_API_KEY":                 "test-gemini-key",
		"GEMINI_MODEL":                   "gemini-2.5-pro",
		"SQLITE_DB_PATH":                 dbPath,
		"REPORT_DIR":                     reportDir,
		"RAW_RESPONSE_DIR":               rawDir,
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
	if !strings.Contains(stdout.String(), "issue_count: 1") {
		t.Fatalf("stdout = %q, want issue count", stdout.String())
	}

	jobID := extractPrefixedValue(t, stdout.String(), "job_id: ")
	previewPath := extractPrefixedValue(t, stdout.String(), "preview_path: ")
	rawResponsePath := extractPrefixedValue(t, stdout.String(), "raw_response_path: ")
	reportPath := extractPrefixedValue(t, stdout.String(), "report_path: ")
	if _, err := os.Stat(previewPath); err != nil {
		t.Fatalf("preview path missing: %v", err)
	}
	if !strings.HasPrefix(previewPath, previewDir) {
		t.Fatalf("previewPath = %q, want prefix %q", previewPath, previewDir)
	}
	if _, err := os.Stat(rawResponsePath); err != nil {
		t.Fatalf("raw response path missing: %v", err)
	}
	if !strings.HasPrefix(rawResponsePath, rawDir) {
		t.Fatalf("rawResponsePath = %q, want prefix %q", rawResponsePath, rawDir)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("report path missing: %v", err)
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
	if jobRun.RawResponsePath == nil || *jobRun.RawResponsePath != rawResponsePath {
		t.Fatalf("jobRun.RawResponsePath = %v, want %q", jobRun.RawResponsePath, rawResponsePath)
	}
	if jobRun.ReportPath == nil || *jobRun.ReportPath != reportPath {
		t.Fatalf("jobRun.ReportPath = %v, want %q", jobRun.ReportPath, reportPath)
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

	previewContent, err := os.ReadFile(previewPath)
	if err != nil {
		t.Fatalf("ReadFile preview returned error: %v", err)
	}
	if !strings.Contains(string(previewContent), "Issue summary") {
		t.Fatalf("preview content = %q, want backlog issue data", string(previewContent))
	}
}

func TestRunAccountReportDryRunRequiresAccount(t *testing.T) {
	restoreProvider := stubLLMProvider(t, llm.GenerateResult{}, nil)
	defer restoreProvider()

	baseDir := t.TempDir()
	envFile := filepath.Join(baseDir, ".env.local")
	promptDir := filepath.Join(baseDir, "prompts")
	writePromptFixtures(t, promptDir)
	writeEnvFile(t, envFile, map[string]string{
		"BACKLOG_BASE_URL":    "https://example.backlog.com",
		"BACKLOG_API_KEY":     "test-backlog-key",
		"BACKLOG_PROJECT_KEY": "PROJ",
		"LLM_PROVIDER":        "gemini",
		"GEMINI_API_KEY":      "test-gemini-key",
		"GEMINI_MODEL":        "gemini-2.5-pro",
		"SQLITE_DB_PATH":      filepath.Join(baseDir, "data", "tracker.sqlite3"),
		"RAW_RESPONSE_DIR":    filepath.Join(baseDir, "data", "raw"),
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
	if !strings.Contains(stderr.String(), "account is required") {
		t.Fatalf("stderr = %q, want account validation", stderr.String())
	}
}

func TestRunAccountReportDryRunPersistsArtifacts(t *testing.T) {
	restoreProvider := stubLLMProvider(t, llm.GenerateResult{
		Output: llm.AccountReportOutput{
			ReportType: "account_report",
			Account:    llm.AccountReportAccount{ID: "alice", DisplayName: "Alice"},
			Summary:    "summary",
			Issues: []llm.AccountReportIssue{
				{
					IssueKey: "PROJ-1",
					Title:    "Issue summary",
					Status:   "Open",
					Summary:  "Issue summary",
					ResponseSuggestion: llm.AccountReportResponseSuggestion{
						Message:           "Reply",
						Confidence:        "high",
						NeedsConfirmation: false,
					},
				},
			},
		},
		OutputJSON:  []byte(`{"reportType":"account_report","account":{"id":"alice","displayName":"Alice"},"summary":"summary","issues":[{"issueKey":"PROJ-1","title":"Issue summary","status":"Open","summary":"Issue summary","responseSuggestion":{"message":"Reply","confidence":"high","needsConfirmation":false}}]}`),
		RawResponse: []byte(`{"provider":"stub"}`),
	}, nil)
	defer restoreProvider()

	baseDir := t.TempDir()
	envFile := filepath.Join(baseDir, ".env.local")
	dbPath := filepath.Join(baseDir, "data", "tracker.sqlite3")
	previewDir := filepath.Join(baseDir, "data", "prompt-previews")
	rawDir := filepath.Join(baseDir, "data", "raw")
	reportDir := filepath.Join(baseDir, "data", "reports")
	promptDir := filepath.Join(baseDir, "prompts")
	backlogServer := startBacklogTestServer(t)
	defer backlogServer.Close()

	writePromptFixtures(t, promptDir)
	writeEnvFile(t, envFile, map[string]string{
		"BACKLOG_BASE_URL":               backlogServer.URL,
		"BACKLOG_API_KEY":                "test-backlog-key",
		"BACKLOG_PROJECT_KEY":            "PROJ",
		"LLM_PROVIDER":                   "gemini",
		"GEMINI_API_KEY":                 "test-gemini-key",
		"GEMINI_MODEL":                   "gemini-2.5-pro",
		"SQLITE_DB_PATH":                 dbPath,
		"REPORT_DIR":                     reportDir,
		"RAW_RESPONSE_DIR":               rawDir,
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
		[]string{"account-report", "--dry-run", "--project", "PROJ", "--account", "alice", "--max-comments", "1", "--env-file", envFile},
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
	if !strings.Contains(stdout.String(), "issue_count: 1") {
		t.Fatalf("stdout = %q, want issue count", stdout.String())
	}

	jobID := extractPrefixedValue(t, stdout.String(), "job_id: ")
	previewPath := extractPrefixedValue(t, stdout.String(), "preview_path: ")
	rawResponsePath := extractPrefixedValue(t, stdout.String(), "raw_response_path: ")
	reportPath := extractPrefixedValue(t, stdout.String(), "report_path: ")
	if _, err := os.Stat(previewPath); err != nil {
		t.Fatalf("preview path missing: %v", err)
	}
	if !strings.HasPrefix(previewPath, previewDir) {
		t.Fatalf("previewPath = %q, want prefix %q", previewPath, previewDir)
	}
	if _, err := os.Stat(rawResponsePath); err != nil {
		t.Fatalf("raw response path missing: %v", err)
	}
	if !strings.HasPrefix(rawResponsePath, rawDir) {
		t.Fatalf("rawResponsePath = %q, want prefix %q", rawResponsePath, rawDir)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("report path missing: %v", err)
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
	if jobRun.TargetAccount == nil || *jobRun.TargetAccount != "alice" {
		t.Fatalf("jobRun.TargetAccount = %v, want alice", jobRun.TargetAccount)
	}
	if jobRun.ReportPath == nil || *jobRun.ReportPath != reportPath {
		t.Fatalf("jobRun.ReportPath = %v, want %q", jobRun.ReportPath, reportPath)
	}

	previewContent, err := os.ReadFile(previewPath)
	if err != nil {
		t.Fatalf("ReadFile preview returned error: %v", err)
	}
	if !strings.Contains(string(previewContent), "Latest comment") {
		t.Fatalf("preview content = %q, want collected comment", string(previewContent))
	}
}

func TestRunPeriodSummaryDryRunSkipsNotifierSetup(t *testing.T) {
	restoreProvider := stubLLMProvider(t, llm.GenerateResult{
		Output: llm.PeriodSummaryOutput{
			ReportType: "period_summary",
			Headline:   "headline",
			Overview:   "overview",
			KeyPoints:  []string{"point"},
			RiskItems:  []llm.PeriodSummaryRiskItem{},
			Counts:     llm.PeriodSummaryCounts{Total: 1},
		},
		OutputJSON:  []byte(`{"reportType":"period_summary","headline":"headline","overview":"overview","keyPoints":["point"],"riskItems":[],"counts":{"total":1}}`),
		RawResponse: []byte(`{"provider":"stub"}`),
	}, nil)
	defer restoreProvider()

	restoreNotifier := stubSlackNotifier(t, nil, errors.New("notifier should not be initialized"))
	defer restoreNotifier()

	baseDir := t.TempDir()
	envFile := filepath.Join(baseDir, ".env.local")
	dbPath := filepath.Join(baseDir, "data", "tracker.sqlite3")
	promptDir := filepath.Join(baseDir, "prompts")
	backlogServer := startBacklogTestServer(t)
	defer backlogServer.Close()
	writePromptFixtures(t, promptDir)
	writeEnvFile(t, envFile, map[string]string{
		"BACKLOG_BASE_URL":    backlogServer.URL,
		"BACKLOG_API_KEY":     "test-backlog-key",
		"BACKLOG_PROJECT_KEY": "PROJ",
		"LLM_PROVIDER":        "gemini",
		"GEMINI_API_KEY":      "test-gemini-key",
		"GEMINI_MODEL":        "gemini-2.5-pro",
		"SQLITE_DB_PATH":      dbPath,
		"REPORT_DIR":          filepath.Join(baseDir, "data", "reports"),
		"RAW_RESPONSE_DIR":    filepath.Join(baseDir, "data", "raw"),
		"PROMPT_DIR":          promptDir,
		"PROMPT_PREVIEW_DIR":  filepath.Join(baseDir, "data", "prompt-previews"),
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
	if !strings.Contains(stdout.String(), "notification: skipped (dry-run)") {
		t.Fatalf("stdout = %q, want dry-run notification status", stdout.String())
	}
}

func TestRunAccountReportDryRunSkipsNotifierSetup(t *testing.T) {
	restoreProvider := stubLLMProvider(t, llm.GenerateResult{
		Output: llm.AccountReportOutput{
			ReportType: "account_report",
			Account:    llm.AccountReportAccount{ID: "alice", DisplayName: "Alice"},
			Summary:    "summary",
			Issues: []llm.AccountReportIssue{
				{
					IssueKey: "PROJ-1",
					Title:    "Issue summary",
					Status:   "Open",
					Summary:  "Issue summary",
					ResponseSuggestion: llm.AccountReportResponseSuggestion{
						Message:           "Reply",
						Confidence:        "high",
						NeedsConfirmation: false,
					},
				},
			},
		},
		OutputJSON:  []byte(`{"reportType":"account_report","account":{"id":"alice","displayName":"Alice"},"summary":"summary","issues":[{"issueKey":"PROJ-1","title":"Issue summary","status":"Open","summary":"Issue summary","responseSuggestion":{"message":"Reply","confidence":"high","needsConfirmation":false}}]}`),
		RawResponse: []byte(`{"provider":"stub"}`),
	}, nil)
	defer restoreProvider()

	restoreNotifier := stubSlackNotifier(t, nil, errors.New("notifier should not be initialized"))
	defer restoreNotifier()

	baseDir := t.TempDir()
	envFile := filepath.Join(baseDir, ".env.local")
	dbPath := filepath.Join(baseDir, "data", "tracker.sqlite3")
	promptDir := filepath.Join(baseDir, "prompts")
	backlogServer := startBacklogTestServer(t)
	defer backlogServer.Close()
	writePromptFixtures(t, promptDir)
	writeEnvFile(t, envFile, map[string]string{
		"BACKLOG_BASE_URL":    backlogServer.URL,
		"BACKLOG_API_KEY":     "test-backlog-key",
		"BACKLOG_PROJECT_KEY": "PROJ",
		"LLM_PROVIDER":        "gemini",
		"GEMINI_API_KEY":      "test-gemini-key",
		"GEMINI_MODEL":        "gemini-2.5-pro",
		"SQLITE_DB_PATH":      dbPath,
		"REPORT_DIR":          filepath.Join(baseDir, "data", "reports"),
		"RAW_RESPONSE_DIR":    filepath.Join(baseDir, "data", "raw"),
		"PROMPT_DIR":          promptDir,
		"PROMPT_PREVIEW_DIR":  filepath.Join(baseDir, "data", "prompt-previews"),
	})
	if err := migrations.ApplyAll(context.Background(), dbPath, repoMigrationDir(t)); err != nil {
		t.Fatalf("ApplyAll returned error: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Run(
		context.Background(),
		[]string{"account-report", "--dry-run", "--project", "PROJ", "--account", "alice", "--env-file", envFile},
		strings.NewReader(""),
		stdout,
		stderr,
	)

	if exitCode != ExitCodeOK {
		t.Fatalf("Run exit code = %d, want %d; stderr=%q", exitCode, ExitCodeOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "notification: skipped (dry-run)") {
		t.Fatalf("stdout = %q, want dry-run notification status", stdout.String())
	}
}

func TestRunPeriodSummaryDryRunReturnsExitCodeLLMOnProviderFailure(t *testing.T) {
	restoreProvider := stubLLMProvider(t, llm.GenerateResult{}, errors.New("provider failed"))
	defer restoreProvider()

	baseDir := t.TempDir()
	envFile := filepath.Join(baseDir, ".env.local")
	promptDir := filepath.Join(baseDir, "prompts")
	backlogServer := startBacklogTestServer(t)
	defer backlogServer.Close()
	writePromptFixtures(t, promptDir)
	writeEnvFile(t, envFile, map[string]string{
		"BACKLOG_BASE_URL":    backlogServer.URL,
		"BACKLOG_API_KEY":     "test-backlog-key",
		"BACKLOG_PROJECT_KEY": "PROJ",
		"LLM_PROVIDER":        "gemini",
		"GEMINI_API_KEY":      "test-gemini-key",
		"GEMINI_MODEL":        "gemini-2.5-pro",
		"SQLITE_DB_PATH":      filepath.Join(baseDir, "data", "tracker.sqlite3"),
		"REPORT_DIR":          filepath.Join(baseDir, "data", "reports"),
		"RAW_RESPONSE_DIR":    filepath.Join(baseDir, "data", "raw"),
		"PROMPT_DIR":          promptDir,
		"PROMPT_PREVIEW_DIR":  filepath.Join(baseDir, "data", "prompt-previews"),
	})
	if err := migrations.ApplyAll(context.Background(), filepath.Join(baseDir, "data", "tracker.sqlite3"), repoMigrationDir(t)); err != nil {
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

	if exitCode != ExitCodeLLM {
		t.Fatalf("Run exit code = %d, want %d", exitCode, ExitCodeLLM)
	}
	if !strings.Contains(stderr.String(), "provider failed") {
		t.Fatalf("stderr = %q, want provider failure", stderr.String())
	}
}

func TestRunPeriodSummaryReturnsExitCodeSlackOnNotifierFailure(t *testing.T) {
	restoreProvider := stubLLMProvider(t, llm.GenerateResult{
		Output: llm.PeriodSummaryOutput{
			ReportType: "period_summary",
			Headline:   "headline",
			Overview:   "overview",
			KeyPoints:  []string{"point"},
			RiskItems:  []llm.PeriodSummaryRiskItem{},
			Counts:     llm.PeriodSummaryCounts{Total: 1},
		},
		OutputJSON:  []byte(`{"reportType":"period_summary","headline":"headline","overview":"overview","keyPoints":["point"],"riskItems":[],"counts":{"total":1}}`),
		RawResponse: []byte(`{"provider":"stub"}`),
	}, nil)
	defer restoreProvider()

	restoreNotifier := stubSlackNotifier(t, fakeSlackNotifier{err: errors.New("slack failed")}, nil)
	defer restoreNotifier()

	baseDir := t.TempDir()
	envFile := filepath.Join(baseDir, ".env.local")
	dbPath := filepath.Join(baseDir, "data", "tracker.sqlite3")
	promptDir := filepath.Join(baseDir, "prompts")
	backlogServer := startBacklogTestServer(t)
	defer backlogServer.Close()
	writePromptFixtures(t, promptDir)
	writeEnvFile(t, envFile, map[string]string{
		"BACKLOG_BASE_URL":    backlogServer.URL,
		"BACKLOG_API_KEY":     "test-backlog-key",
		"BACKLOG_PROJECT_KEY": "PROJ",
		"LLM_PROVIDER":        "gemini",
		"GEMINI_API_KEY":      "test-gemini-key",
		"GEMINI_MODEL":        "gemini-2.5-pro",
		"SQLITE_DB_PATH":      dbPath,
		"REPORT_DIR":          filepath.Join(baseDir, "data", "reports"),
		"RAW_RESPONSE_DIR":    filepath.Join(baseDir, "data", "raw"),
		"PROMPT_DIR":          promptDir,
		"PROMPT_PREVIEW_DIR":  filepath.Join(baseDir, "data", "prompt-previews"),
	})
	if err := migrations.ApplyAll(context.Background(), dbPath, repoMigrationDir(t)); err != nil {
		t.Fatalf("ApplyAll returned error: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Run(
		context.Background(),
		[]string{"period-summary", "--project", "PROJ", "--from", "2026-03-01", "--to", "2026-03-07", "--env-file", envFile},
		strings.NewReader(""),
		stdout,
		stderr,
	)

	if exitCode != ExitCodeSlack {
		t.Fatalf("Run exit code = %d, want %d", exitCode, ExitCodeSlack)
	}
	if !strings.Contains(stderr.String(), "slack failed") {
		t.Fatalf("stderr = %q, want slack failure", stderr.String())
	}
}

func TestRunAccountReportReturnsExitCodeSlackOnNotifierFailure(t *testing.T) {
	restoreProvider := stubLLMProvider(t, llm.GenerateResult{
		Output: llm.AccountReportOutput{
			ReportType: "account_report",
			Account:    llm.AccountReportAccount{ID: "alice", DisplayName: "Alice"},
			Summary:    "summary",
			Issues: []llm.AccountReportIssue{
				{
					IssueKey: "PROJ-1",
					Title:    "Issue summary",
					Status:   "Open",
					Summary:  "Issue summary",
					ResponseSuggestion: llm.AccountReportResponseSuggestion{
						Message:           "Reply",
						Confidence:        "high",
						NeedsConfirmation: false,
					},
				},
			},
		},
		OutputJSON:  []byte(`{"reportType":"account_report","account":{"id":"alice","displayName":"Alice"},"summary":"summary","issues":[{"issueKey":"PROJ-1","title":"Issue summary","status":"Open","summary":"Issue summary","responseSuggestion":{"message":"Reply","confidence":"high","needsConfirmation":false}}]}`),
		RawResponse: []byte(`{"provider":"stub"}`),
	}, nil)
	defer restoreProvider()

	restoreNotifier := stubSlackNotifier(t, fakeSlackNotifier{err: errors.New("slack failed")}, nil)
	defer restoreNotifier()

	baseDir := t.TempDir()
	envFile := filepath.Join(baseDir, ".env.local")
	dbPath := filepath.Join(baseDir, "data", "tracker.sqlite3")
	promptDir := filepath.Join(baseDir, "prompts")
	backlogServer := startBacklogTestServer(t)
	defer backlogServer.Close()
	writePromptFixtures(t, promptDir)
	writeEnvFile(t, envFile, map[string]string{
		"BACKLOG_BASE_URL":    backlogServer.URL,
		"BACKLOG_API_KEY":     "test-backlog-key",
		"BACKLOG_PROJECT_KEY": "PROJ",
		"LLM_PROVIDER":        "gemini",
		"GEMINI_API_KEY":      "test-gemini-key",
		"GEMINI_MODEL":        "gemini-2.5-pro",
		"SQLITE_DB_PATH":      dbPath,
		"REPORT_DIR":          filepath.Join(baseDir, "data", "reports"),
		"RAW_RESPONSE_DIR":    filepath.Join(baseDir, "data", "raw"),
		"PROMPT_DIR":          promptDir,
		"PROMPT_PREVIEW_DIR":  filepath.Join(baseDir, "data", "prompt-previews"),
	})
	if err := migrations.ApplyAll(context.Background(), dbPath, repoMigrationDir(t)); err != nil {
		t.Fatalf("ApplyAll returned error: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := Run(
		context.Background(),
		[]string{"account-report", "--project", "PROJ", "--account", "alice", "--env-file", envFile},
		strings.NewReader(""),
		stdout,
		stderr,
	)

	if exitCode != ExitCodeSlack {
		t.Fatalf("Run exit code = %d, want %d", exitCode, ExitCodeSlack)
	}
	if !strings.Contains(stderr.String(), "slack failed") {
		t.Fatalf("stderr = %q, want slack failure", stderr.String())
	}
}

func TestExitCodeForPeriodSummaryError(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want int
	}{
		{name: "non app error", err: errors.New("boom"), want: ExitCodeInput},
		{name: "input", err: &periodsummary.Error{Kind: periodsummary.KindInput, Err: errors.New("boom")}, want: ExitCodeInput},
		{name: "backlog", err: &periodsummary.Error{Kind: periodsummary.KindBacklog, Err: errors.New("boom")}, want: ExitCodeBacklog},
		{name: "llm", err: &periodsummary.Error{Kind: periodsummary.KindLLM, Err: errors.New("boom")}, want: ExitCodeLLM},
		{name: "slack", err: &periodsummary.Error{Kind: periodsummary.KindSlack, Err: errors.New("boom")}, want: ExitCodeSlack},
		{name: "storage", err: &periodsummary.Error{Kind: periodsummary.KindStorage, Err: errors.New("boom")}, want: ExitCodeStorage},
		{name: "unknown kind", err: &periodsummary.Error{Kind: "other", Err: errors.New("boom")}, want: ExitCodeInput},
	}

	for _, testCase := range testCases {
		if got := exitCodeForPeriodSummaryError(testCase.err); got != testCase.want {
			t.Fatalf("%s: exitCodeForPeriodSummaryError(%v) = %d, want %d", testCase.name, testCase.err, got, testCase.want)
		}
	}
}

func TestExitCodeForAccountReportError(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want int
	}{
		{name: "non app error", err: errors.New("boom"), want: ExitCodeInput},
		{name: "input", err: &accountreport.Error{Kind: accountreport.KindInput, Err: errors.New("boom")}, want: ExitCodeInput},
		{name: "backlog", err: &accountreport.Error{Kind: accountreport.KindBacklog, Err: errors.New("boom")}, want: ExitCodeBacklog},
		{name: "llm", err: &accountreport.Error{Kind: accountreport.KindLLM, Err: errors.New("boom")}, want: ExitCodeLLM},
		{name: "slack", err: &accountreport.Error{Kind: accountreport.KindSlack, Err: errors.New("boom")}, want: ExitCodeSlack},
		{name: "storage", err: &accountreport.Error{Kind: accountreport.KindStorage, Err: errors.New("boom")}, want: ExitCodeStorage},
		{name: "unknown kind", err: &accountreport.Error{Kind: "other", Err: errors.New("boom")}, want: ExitCodeInput},
	}

	for _, testCase := range testCases {
		if got := exitCodeForAccountReportError(testCase.err); got != testCase.want {
			t.Fatalf("%s: exitCodeForAccountReportError(%v) = %d, want %d", testCase.name, testCase.err, got, testCase.want)
		}
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
	if err := os.WriteFile(filepath.Join(promptDir, "period_summary", "user.tmpl"), []byte("projectKey: {{ .ProjectKey }}\nfrom: {{ .DateFrom }}\nto: {{ .DateTo }}\nissues: {{ .IssuesJSON }}\nschema: {{ .OutputSchemaJSON }}"), 0o644); err != nil {
		t.Fatalf("write period_summary user template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "account_report", "system.tmpl"), []byte("system account report"), 0o644); err != nil {
		t.Fatalf("write account_report system template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "account_report", "user.tmpl"), []byte("account: {{ .AccountID }}\nissues: {{ .IssuesJSON }}"), 0o644); err != nil {
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

func stubLLMProvider(t *testing.T, result llm.GenerateResult, err error) func() {
	t.Helper()

	previous := newLLMProvider
	newLLMProvider = func(cfg config.Config, optionFns ...llm.Option) (llm.Provider, error) {
		return fakeLLMProvider{
			result: result,
			err:    err,
		}, nil
	}
	return func() {
		newLLMProvider = previous
	}
}

func stubSlackNotifier(t *testing.T, notifier notificationslack.Notifier, err error) func() {
	t.Helper()

	previous := newSlackNotifier
	newSlackNotifier = func(cfg config.Config, optionFns ...notificationslack.Option) (notificationslack.Notifier, error) {
		if err != nil {
			return nil, err
		}
		return notifier, nil
	}
	return func() {
		newSlackNotifier = previous
	}
}

type fakeLLMProvider struct {
	result llm.GenerateResult
	err    error
}

func (f fakeLLMProvider) Generate(ctx context.Context, req llm.GenerateRequest) (llm.GenerateResult, error) {
	return f.result, f.err
}

type fakeSlackNotifier struct {
	err error
}

func (f fakeSlackNotifier) Send(ctx context.Context, message notificationslack.Message) (notificationslack.Response, error) {
	return notificationslack.Response{}, f.err
}

func startBacklogTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v2/projects/PROJ":
			if got, want := r.URL.Query().Get("apiKey"), "test-backlog-key"; got != want {
				t.Fatalf("apiKey = %q, want %q", got, want)
			}
			writeAPIJSON(t, w, map[string]any{
				"id":         1,
				"projectKey": "PROJ",
				"name":       "Project",
			})
		case "/api/v2/projects/PROJ/users":
			writeAPIJSON(t, w, []map[string]any{
				{
					"id":          10,
					"userId":      "alice",
					"name":        "Alice",
					"mailAddress": "alice@example.com",
				},
			})
		case "/api/v2/issues":
			writeAPIJSON(t, w, []map[string]any{
				{
					"id":          1001,
					"projectId":   1,
					"issueKey":    "PROJ-1",
					"summary":     "Issue summary",
					"description": "Issue description",
					"status": map[string]any{
						"id":        2,
						"projectId": 1,
						"name":      "Open",
					},
					"assignee": map[string]any{
						"id":     10,
						"userId": "alice",
						"name":   "Alice",
					},
					"created": "2026-03-01T00:00:00Z",
					"updated": "2026-03-02T00:00:00Z",
				},
			})
		case "/api/v2/issues/PROJ-1/comments":
			writeAPIJSON(t, w, []map[string]any{
				{
					"id":      5001,
					"content": "Old comment",
					"createdUser": map[string]any{
						"id":     11,
						"userId": "bob",
						"name":   "Bob",
					},
					"created": "2026-03-01T09:00:00Z",
					"updated": "2026-03-01T09:00:00Z",
				},
				{
					"id":      5002,
					"content": "Latest comment",
					"createdUser": map[string]any{
						"id":     12,
						"userId": "carol",
						"name":   "Carol",
					},
					"created": "2026-03-02T09:00:00Z",
					"updated": "2026-03-02T09:00:00Z",
				},
			})
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
}

func writeAPIJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json: %v", err)
	}
}
