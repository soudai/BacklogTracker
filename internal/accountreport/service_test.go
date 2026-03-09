package accountreport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/soudai/BacklogTracker/internal/backlogclient"
	"github.com/soudai/BacklogTracker/internal/config"
	"github.com/soudai/BacklogTracker/internal/llm"
	"github.com/soudai/BacklogTracker/internal/migrations"
	notificationslack "github.com/soudai/BacklogTracker/internal/notifications/slack"
	"github.com/soudai/BacklogTracker/internal/prompts"
	"github.com/soudai/BacklogTracker/internal/storage/sqlite"
)

func TestServiceRunDryRunPersistsArtifactsAndUsesLatestComments(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store := openStore(t, baseDir)
	defer store.Close()

	promptDir := filepath.Join(baseDir, "prompts")
	previewDir := filepath.Join(baseDir, "data", "prompt-previews")
	writePromptFixtures(t, promptDir)

	now := time.Date(2026, 3, 10, 11, 0, 0, 0, time.UTC)
	comments := []backlogclient.IssueComment{
		{Content: "oldest comment", CreatedAt: now.Add(-3 * time.Hour), CreatedUser: &backlogclient.User{UserID: "alice"}},
		{Content: "newer comment", CreatedAt: now.Add(-2 * time.Hour), CreatedUser: &backlogclient.User{UserID: "bob"}},
		{Content: "latest comment", CreatedAt: now.Add(-time.Hour), CreatedUser: &backlogclient.User{UserID: "carol"}},
	}
	llmProvider := &fakeLLMProvider{
		result: llm.GenerateResult{
			Output: llm.AccountReportOutput{
				ReportType: "account_report",
				Account:    llm.AccountReportAccount{ID: "yamada", DisplayName: "山田 太郎"},
				Summary:    "summary",
				Issues: []llm.AccountReportIssue{
					{
						IssueKey: "PROJ-1",
						Title:    "Issue title",
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
			OutputJSON:  []byte(`{"reportType":"account_report","account":{"id":"yamada","displayName":"山田 太郎"},"summary":"summary","issues":[{"issueKey":"PROJ-1","title":"Issue title","status":"Open","summary":"Issue summary","responseSuggestion":{"message":"Reply","confidence":"high","needsConfirmation":false}}]}`),
			RawResponse: []byte(`{"provider":"stub"}`),
		},
	}
	service := Service{
		BaseDir: baseDir,
		Config: config.Config{
			BacklogProjectKey:           "PROJ",
			LLMProvider:                 config.ProviderGemini,
			ReportDir:                   "./data/reports",
			RawResponseDir:              "./data/raw",
			PromptPreviewDir:            "./data/prompt-previews",
			PromptArtifactRetentionDays: 30,
		},
		Collector: &fakeCollector{
			user: backlogclient.User{ID: 10, UserID: "yamada", Name: "山田 太郎"},
			issues: []backlogclient.Issue{
				{
					IssueKey:    "PROJ-1",
					Summary:     "Issue title",
					Description: "Private body",
					Status:      &backlogclient.Status{Name: "Open"},
					UpdatedAt:   now,
				},
			},
		},
		Comments: &fakeCommentLister{
			commentsByIssue: map[string][]backlogclient.IssueComment{
				"PROJ-1": comments,
			},
		},
		PromptManager: prompts.Manager{
			PromptDir:     promptDir,
			PreviewDir:    previewDir,
			RetentionDays: 30,
			Now:           func() time.Time { return now },
		},
		LLMProvider:     llmProvider,
		Store:           store,
		SaveRawResponse: llm.SaveRawResponse,
		Now:             func() time.Time { return now },
	}

	result, err := service.Run(context.Background(), Input{
		Account:     "yamada",
		From:        time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC),
		MaxComments: 2,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.NotificationSent {
		t.Fatalf("NotificationSent = true, want false")
	}
	if strings.Contains(llmProvider.lastRequest.UserPrompt, "oldest comment") {
		t.Fatalf("user prompt unexpectedly contains trimmed comment: %q", llmProvider.lastRequest.UserPrompt)
	}
	if !strings.Contains(llmProvider.lastRequest.UserPrompt, "newer comment") || !strings.Contains(llmProvider.lastRequest.UserPrompt, "latest comment") {
		t.Fatalf("user prompt = %q, want latest comments", llmProvider.lastRequest.UserPrompt)
	}
	if _, err := os.Stat(result.PreviewPath); err != nil {
		t.Fatalf("preview file missing: %v", err)
	}
	if _, err := os.Stat(result.RawResponsePath); err != nil {
		t.Fatalf("raw response file missing: %v", err)
	}
	if _, err := os.Stat(result.ReportPath); err != nil {
		t.Fatalf("report file missing: %v", err)
	}

	jobRun, err := store.JobRuns().GetByJobID(context.Background(), result.JobID)
	if err != nil {
		t.Fatalf("GetByJobID returned error: %v", err)
	}
	if got, want := jobRun.Status, "completed"; got != want {
		t.Fatalf("jobRun.Status = %q, want %q", got, want)
	}
	if jobRun.TargetAccount == nil || *jobRun.TargetAccount != "yamada" {
		t.Fatalf("jobRun.TargetAccount = %v, want yamada", jobRun.TargetAccount)
	}
}

func TestServiceRunSendsSanitizedSlackMessage(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store := openStore(t, baseDir)
	defer store.Close()

	promptDir := filepath.Join(baseDir, "prompts")
	previewDir := filepath.Join(baseDir, "data", "prompt-previews")
	writePromptFixtures(t, promptDir)

	now := time.Date(2026, 3, 10, 11, 30, 0, 0, time.UTC)
	notifier := &fakeNotifier{}
	service := Service{
		BaseDir: baseDir,
		Config: config.Config{
			BacklogProjectKey:           "PROJ",
			LLMProvider:                 config.ProviderGemini,
			ReportDir:                   "./data/reports",
			RawResponseDir:              "./data/raw",
			PromptPreviewDir:            "./data/prompt-previews",
			PromptArtifactRetentionDays: 30,
			SlackChannel:                "#alerts",
		},
		Collector: &fakeCollector{
			user: backlogclient.User{ID: 10, UserID: "yamada", Name: "山田 太郎"},
			issues: []backlogclient.Issue{
				{
					IssueKey:    "PROJ-1",
					Summary:     "Issue title",
					Description: "Private backlog body",
				},
			},
		},
		Comments: &fakeCommentLister{
			commentsByIssue: map[string][]backlogclient.IssueComment{
				"PROJ-1": {
					{Content: "Raw comment body"},
				},
			},
		},
		PromptManager: prompts.Manager{
			PromptDir:     promptDir,
			PreviewDir:    previewDir,
			RetentionDays: 30,
			Now:           func() time.Time { return now },
		},
		LLMProvider: &fakeLLMProvider{
			result: llm.GenerateResult{
				Output: llm.AccountReportOutput{
					ReportType: "account_report",
					Account:    llm.AccountReportAccount{ID: "yamada", DisplayName: "山田 太郎"},
					Summary:    "summary alice@example.com +81 90-1234-5678",
					Issues: []llm.AccountReportIssue{
						{
							IssueKey: "PROJ-1",
							Title:    "Issue title",
							Status:   "Open",
							Summary:  "Issue summary alice@example.com",
							ResponseSuggestion: llm.AccountReportResponseSuggestion{
								Message:           "Reply to alice@example.com and call +81 90-1234-5678",
								Confidence:        "medium",
								NeedsConfirmation: true,
							},
						},
					},
				},
				OutputJSON:  []byte(`{"reportType":"account_report","account":{"id":"yamada","displayName":"山田 太郎"},"summary":"summary alice@example.com +81 90-1234-5678","issues":[{"issueKey":"PROJ-1","title":"Issue title","status":"Open","summary":"Issue summary alice@example.com","responseSuggestion":{"message":"Reply to alice@example.com and call +81 90-1234-5678","confidence":"medium","needsConfirmation":true}}]}`),
				RawResponse: []byte(`{"provider":"stub"}`),
			},
		},
		Notifier:        notifier,
		Store:           store,
		SaveRawResponse: llm.SaveRawResponse,
		Now:             func() time.Time { return now },
	}

	result, err := service.Run(context.Background(), Input{Account: "yamada"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.NotificationSent {
		t.Fatalf("NotificationSent = false, want true")
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier.calls = %d, want 1", notifier.calls)
	}

	payload, err := json.Marshal(notifier.message)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	serialized := string(payload)
	if strings.Contains(serialized, "Private backlog body") || strings.Contains(serialized, "Raw comment body") {
		t.Fatalf("slack payload leaked backlog content: %s", serialized)
	}
	if strings.Contains(serialized, "alice@example.com") || strings.Contains(serialized, "+81 90-1234-5678") {
		t.Fatalf("slack payload leaked personal data: %s", serialized)
	}
	if !strings.Contains(serialized, "[redacted-email]") || !strings.Contains(serialized, "[redacted-phone]") {
		t.Fatalf("slack payload = %s, want masked personal data", serialized)
	}
}

func TestServiceRunLogsWebhookDestinationOnSlackFailure(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store := openStore(t, baseDir)
	defer store.Close()

	promptDir := filepath.Join(baseDir, "prompts")
	previewDir := filepath.Join(baseDir, "data", "prompt-previews")
	writePromptFixtures(t, promptDir)

	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	service := Service{
		BaseDir: baseDir,
		Config: config.Config{
			BacklogProjectKey:           "PROJ",
			LLMProvider:                 config.ProviderGemini,
			ReportDir:                   "./data/reports",
			RawResponseDir:              "./data/raw",
			PromptPreviewDir:            "./data/prompt-previews",
			PromptArtifactRetentionDays: 30,
			SlackWebhookURL:             "https://hooks.slack.com/services/T000/B000/SECRET",
		},
		Collector: &fakeCollector{
			user: backlogclient.User{ID: 10, UserID: "yamada", Name: "山田 太郎"},
			issues: []backlogclient.Issue{
				{IssueKey: "PROJ-1", Summary: "Issue title"},
			},
		},
		Comments: &fakeCommentLister{},
		PromptManager: prompts.Manager{
			PromptDir:     promptDir,
			PreviewDir:    previewDir,
			RetentionDays: 30,
			Now:           func() time.Time { return now },
		},
		LLMProvider: &fakeLLMProvider{
			result: llm.GenerateResult{
				Output: llm.AccountReportOutput{
					ReportType: "account_report",
					Account:    llm.AccountReportAccount{ID: "yamada", DisplayName: "山田 太郎"},
					Summary:    "summary",
					Issues:     []llm.AccountReportIssue{},
				},
				OutputJSON:  []byte(`{"reportType":"account_report","account":{"id":"yamada","displayName":"山田 太郎"},"summary":"summary","issues":[]}`),
				RawResponse: []byte(`{"provider":"stub"}`),
			},
		},
		Notifier:        &fakeNotifier{err: errors.New("slack failed")},
		Store:           store,
		SaveRawResponse: llm.SaveRawResponse,
		Now:             func() time.Time { return now },
	}

	_, err := service.Run(context.Background(), Input{Account: "yamada"})
	var appErr *Error
	if !errors.As(err, &appErr) {
		t.Fatalf("Run error = %v, want *Error", err)
	}
	if got, want := appErr.Kind, KindSlack; got != want {
		t.Fatalf("appErr.Kind = %q, want %q", got, want)
	}

	logs, err := store.NotificationLogs().ListByJobID(context.Background(), buildJobID(now))
	if err != nil {
		t.Fatalf("ListByJobID returned error: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("notification log count = %d, want 1", len(logs))
	}
	if logs[0].Destination == nil || *logs[0].Destination != "incoming-webhook" {
		t.Fatalf("notification destination = %v, want incoming-webhook", logs[0].Destination)
	}
}

func TestServiceRunReturnsInputKindWhenDependenciesMissing(t *testing.T) {
	t.Parallel()

	_, err := (Service{}).Run(context.Background(), Input{})
	var appErr *Error
	if !errors.As(err, &appErr) {
		t.Fatalf("Run error = %v, want *Error", err)
	}
	if got, want := appErr.Kind, KindInput; got != want {
		t.Fatalf("appErr.Kind = %q, want %q", got, want)
	}
}

type fakeCollector struct {
	user      backlogclient.User
	userErr   error
	issues    []backlogclient.Issue
	issuesErr error
}

func (f *fakeCollector) ResolveAssignee(ctx context.Context, projectIDOrKey, account string) (backlogclient.User, error) {
	return f.user, f.userErr
}

func (f *fakeCollector) CollectPeriodIssues(ctx context.Context, input backlogclient.IssueListInput) ([]backlogclient.Issue, error) {
	return append([]backlogclient.Issue(nil), f.issues...), f.issuesErr
}

type fakeCommentLister struct {
	commentsByIssue map[string][]backlogclient.IssueComment
	err             error
}

func (f *fakeCommentLister) ListIssueComments(ctx context.Context, issueIDOrKey string) ([]backlogclient.IssueComment, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]backlogclient.IssueComment(nil), f.commentsByIssue[issueIDOrKey]...), nil
}

type fakeLLMProvider struct {
	lastRequest llm.GenerateRequest
	result      llm.GenerateResult
	err         error
}

func (f *fakeLLMProvider) Generate(ctx context.Context, req llm.GenerateRequest) (llm.GenerateResult, error) {
	f.lastRequest = req
	return f.result, f.err
}

type fakeNotifier struct {
	calls    int
	message  notificationslack.Message
	response notificationslack.Response
	err      error
}

func (f *fakeNotifier) Send(ctx context.Context, message notificationslack.Message) (notificationslack.Response, error) {
	f.calls++
	f.message = message
	if f.response == (notificationslack.Response{}) {
		f.response = notificationslack.Response{Destination: "#alerts", Summary: "ok"}
	}
	return f.response, f.err
}

func openStore(t *testing.T, baseDir string) *sqlite.Store {
	t.Helper()

	dbPath := filepath.Join(baseDir, "data", "tracker.sqlite3")
	migrationDir := filepath.Join(baseDir, "migrations")
	if err := os.MkdirAll(migrationDir, 0o755); err != nil {
		t.Fatalf("create migration dir: %v", err)
	}

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve current file path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	migrationPath := filepath.Join(repoRoot, "migrations", "0001_initial.sql")
	content, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrationDir, "0001_initial.sql"), content, 0o644); err != nil {
		t.Fatalf("write migration file: %v", err)
	}
	if err := migrations.ApplyAll(context.Background(), dbPath, migrationDir); err != nil {
		t.Fatalf("ApplyAll returned error: %v", err)
	}
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return store
}

func writePromptFixtures(t *testing.T, promptDir string) {
	t.Helper()

	taskDir := filepath.Join(promptDir, "account_report")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("create prompt dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "system.tmpl"), []byte("system account report"), 0o644); err != nil {
		t.Fatalf("write system template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "user.tmpl"), []byte("issues={{ .IssuesJSON }}"), 0o644); err != nil {
		t.Fatalf("write user template: %v", err)
	}
}
