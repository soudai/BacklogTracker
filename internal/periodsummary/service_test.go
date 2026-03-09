package periodsummary

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/soudai/BacklogTracker/internal/backlogclient"
	"github.com/soudai/BacklogTracker/internal/config"
	"github.com/soudai/BacklogTracker/internal/llm"
	"github.com/soudai/BacklogTracker/internal/migrations"
	notificationslack "github.com/soudai/BacklogTracker/internal/notifications/slack"
	"github.com/soudai/BacklogTracker/internal/prompts"
	"github.com/soudai/BacklogTracker/internal/storage/sqlite"
)

func TestServiceRunDryRunPersistsArtifactsWithoutSlack(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store := openStore(t, baseDir)
	defer store.Close()

	promptDir := filepath.Join(baseDir, "prompts")
	previewDir := filepath.Join(baseDir, "data", "prompt-previews")
	writePromptFixtures(t, promptDir)

	now := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	llmProvider := &fakeLLMProvider{
		result: llm.GenerateResult{
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
			issues: []backlogclient.Issue{
				{
					IssueKey:    "PROJ-1",
					Summary:     "Issue summary",
					Description: "Private body alice@example.com",
					Status:      &backlogclient.Status{Name: "Open"},
					Assignee:    &backlogclient.User{UserID: "alice"},
					UpdatedAt:   now,
				},
			},
		},
		Statuses: &fakeStatusLister{},
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
		From:   time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		To:     time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC),
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.NotificationSent {
		t.Fatalf("NotificationSent = true, want false")
	}
	if !strings.Contains(llmProvider.lastRequest.UserPrompt, "Private body alice@example.com") {
		t.Fatalf("user prompt = %q, want backlog issue description", llmProvider.lastRequest.UserPrompt)
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
	if jobRun.IssueCount == nil || *jobRun.IssueCount != 1 {
		t.Fatalf("jobRun.IssueCount = %v, want 1", jobRun.IssueCount)
	}

	notificationLogs, err := store.NotificationLogs().ListByJobID(context.Background(), result.JobID)
	if err != nil {
		t.Fatalf("ListByJobID returned error: %v", err)
	}
	if len(notificationLogs) != 0 {
		t.Fatalf("notification log count = %d, want 0", len(notificationLogs))
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

	now := time.Date(2026, 3, 10, 9, 30, 0, 0, time.UTC)
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
			issues: []backlogclient.Issue{
				{
					IssueKey:    "PROJ-1",
					Summary:     "Issue summary",
					Description: "Sensitive backlog body",
					Status:      &backlogclient.Status{Name: "Open"},
				},
			},
		},
		Statuses: &fakeStatusLister{},
		PromptManager: prompts.Manager{
			PromptDir:     promptDir,
			PreviewDir:    previewDir,
			RetentionDays: 30,
			Now:           func() time.Time { return now },
		},
		LLMProvider: &fakeLLMProvider{
			result: llm.GenerateResult{
				Output: llm.PeriodSummaryOutput{
					ReportType: "period_summary",
					Headline:   "headline",
					Overview:   "contact alice@example.com or +81 90-1234-5678",
					KeyPoints:  []string{"Reach owner alice@example.com"},
					RiskItems: []llm.PeriodSummaryRiskItem{
						{IssueKey: "PROJ-1", Reason: "owner alice@example.com"},
					},
					Counts: llm.PeriodSummaryCounts{Total: 1},
				},
				OutputJSON:  []byte(`{"reportType":"period_summary","headline":"headline","overview":"contact alice@example.com or +81 90-1234-5678","keyPoints":["Reach owner alice@example.com"],"riskItems":[{"issueKey":"PROJ-1","reason":"owner alice@example.com"}],"counts":{"total":1}}`),
				RawResponse: []byte(`{"provider":"stub"}`),
			},
		},
		Notifier:        notifier,
		Store:           store,
		SaveRawResponse: llm.SaveRawResponse,
		Now:             func() time.Time { return now },
	}

	result, err := service.Run(context.Background(), Input{
		From: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.NotificationSent {
		t.Fatalf("NotificationSent = false, want true")
	}
	if notifier.calls != 1 {
		t.Fatalf("notifier calls = %d, want 1", notifier.calls)
	}

	serialized, err := json.Marshal(notifier.message)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	payload := string(serialized)
	if strings.Contains(payload, "Sensitive backlog body") {
		t.Fatalf("slack payload leaked backlog body: %s", payload)
	}
	if strings.Contains(payload, "alice@example.com") || strings.Contains(payload, "+81 90-1234-5678") {
		t.Fatalf("slack payload leaked personal data: %s", payload)
	}
	if !strings.Contains(payload, "[redacted-email]") || !strings.Contains(payload, "[redacted-phone]") {
		t.Fatalf("slack payload = %s, want masked personal data", payload)
	}

	notificationLogs, err := store.NotificationLogs().ListByJobID(context.Background(), result.JobID)
	if err != nil {
		t.Fatalf("ListByJobID returned error: %v", err)
	}
	if len(notificationLogs) != 1 {
		t.Fatalf("notification log count = %d, want 1", len(notificationLogs))
	}
	if got, want := notificationLogs[0].Status, "sent"; got != want {
		t.Fatalf("notificationLogs[0].Status = %q, want %q", got, want)
	}
}

func TestTruncateSlackTextPreservesUTF8(t *testing.T) {
	t.Parallel()

	value := "日本語の要約テキスト"
	truncated := truncateSlackMarkdown(value, 5)
	if !utf8.ValidString(truncated) {
		t.Fatalf("truncateSlackMarkdown produced invalid UTF-8: %q", truncated)
	}
	if !strings.HasSuffix(truncated, "…") {
		t.Fatalf("truncated = %q, want ellipsis suffix", truncated)
	}
}

func TestBuildSlackMessageTruncatesJoinedSections(t *testing.T) {
	t.Parallel()

	output := llm.PeriodSummaryOutput{
		ReportType: "period_summary",
		Headline:   "headline",
		Overview:   "overview",
		KeyPoints:  []string{strings.Repeat("あ", 2000), strings.Repeat("い", 2000)},
		RiskItems: []llm.PeriodSummaryRiskItem{
			{IssueKey: "PROJ-1", Reason: strings.Repeat("う", 2000)},
			{IssueKey: "PROJ-2", Reason: strings.Repeat("え", 2000)},
		},
		Counts: llm.PeriodSummaryCounts{Total: 2},
	}

	message := BuildSlackMessage(config.Config{BacklogProjectKey: "PROJ"}, Input{
		From: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC),
	}, 2, output)

	var sectionTexts []string
	for _, block := range message.Blocks {
		text, ok := block["text"].(map[string]any)
		if !ok {
			continue
		}
		value, _ := text["text"].(string)
		sectionTexts = append(sectionTexts, value)
	}
	if len(sectionTexts) < 3 {
		t.Fatalf("sectionTexts = %d, want at least 3 text sections", len(sectionTexts))
	}
	for _, text := range sectionTexts {
		if utf8.RuneCountInString(text) > 2800 {
			t.Fatalf("section text length = %d, want <= 2800", utf8.RuneCountInString(text))
		}
	}
}

type fakeCollector struct {
	resolveUser backlogclient.User
	resolveErr  error
	issues      []backlogclient.Issue
	issuesErr   error
}

func (f *fakeCollector) ResolveAssignee(ctx context.Context, projectIDOrKey, account string) (backlogclient.User, error) {
	return f.resolveUser, f.resolveErr
}

func (f *fakeCollector) CollectPeriodIssues(ctx context.Context, input backlogclient.IssueListInput) ([]backlogclient.Issue, error) {
	return append([]backlogclient.Issue(nil), f.issues...), f.issuesErr
}

type fakeStatusLister struct {
	statuses []backlogclient.Status
	err      error
}

func (f *fakeStatusLister) ListProjectStatuses(ctx context.Context, projectIDOrKey string) ([]backlogclient.Status, error) {
	return append([]backlogclient.Status(nil), f.statuses...), f.err
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

	taskDir := filepath.Join(promptDir, "period_summary")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("create prompt dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "system.tmpl"), []byte("system period summary"), 0o644); err != nil {
		t.Fatalf("write system template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "user.tmpl"), []byte("issues={{ .IssuesJSON }}"), 0o644); err != nil {
		t.Fatalf("write user template: %v", err)
	}
}
