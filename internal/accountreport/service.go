package accountreport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/soudai/BacklogTracker/internal/backlogclient"
	"github.com/soudai/BacklogTracker/internal/config"
	"github.com/soudai/BacklogTracker/internal/llm"
	notificationslack "github.com/soudai/BacklogTracker/internal/notifications/slack"
	"github.com/soudai/BacklogTracker/internal/prompts"
	"github.com/soudai/BacklogTracker/internal/storage/sqlite"
)

const (
	KindInput   Kind = "input"
	KindBacklog Kind = "backlog"
	KindLLM     Kind = "llm"
	KindSlack   Kind = "slack"
	KindStorage Kind = "storage"
)

var (
	emailPattern = regexp.MustCompile(`(?i)\b[0-9a-z._%+\-]+@[0-9a-z.\-]+\.[a-z]{2,}\b`)
	phonePattern = regexp.MustCompile(`\+?\d[\d\s().-]{7,}\d`)
)

type Kind string

type Error struct {
	Kind Kind
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type issueCollector interface {
	ResolveAssignee(ctx context.Context, projectIDOrKey, account string) (backlogclient.User, error)
	CollectPeriodIssues(ctx context.Context, input backlogclient.IssueListInput) ([]backlogclient.Issue, error)
}

type commentLister interface {
	ListIssueComments(ctx context.Context, issueIDOrKey string) ([]backlogclient.IssueComment, error)
}

type promptManager interface {
	Render(task prompts.Task, data any) (prompts.RenderedPrompt, error)
	SavePreview(jobID string, rendered prompts.RenderedPrompt) (string, error)
}

type llmProvider interface {
	Generate(ctx context.Context, req llm.GenerateRequest) (llm.GenerateResult, error)
}

type notifier interface {
	Send(ctx context.Context, message notificationslack.Message) (notificationslack.Response, error)
}

type rawResponseSaver func(baseDir, rawResponseDir, jobID string, provider config.Provider, task prompts.Task, payload []byte, now time.Time) (string, error)

type Service struct {
	BaseDir         string
	Config          config.Config
	Collector       issueCollector
	Comments        commentLister
	PromptManager   promptManager
	LLMProvider     llmProvider
	Notifier        notifier
	Store           *sqlite.Store
	SaveRawResponse rawResponseSaver
	Now             func() time.Time
}

type Input struct {
	Account     string
	From        time.Time
	To          time.Time
	MaxComments int
	DryRun      bool
}

type Result struct {
	JobID                string
	IssueCount           int
	PreviewPath          string
	RawResponsePath      string
	ReportPath           string
	NotificationSent     bool
	NotificationResponse string
	Output               llm.AccountReportOutput
}

type promptIssue struct {
	IssueKey    string          `json:"issueKey"`
	Title       string          `json:"title"`
	Status      string          `json:"status,omitempty"`
	Description string          `json:"description,omitempty"`
	CreatedAt   string          `json:"createdAt,omitempty"`
	UpdatedAt   string          `json:"updatedAt,omitempty"`
	Comments    []promptComment `json:"comments,omitempty"`
}

type promptComment struct {
	Author    string `json:"author,omitempty"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt,omitempty"`
}

func (s Service) Run(ctx context.Context, input Input) (result Result, err error) {
	if err := s.validate(); err != nil {
		return Result{}, &Error{Kind: KindInput, Err: err}
	}
	if strings.TrimSpace(s.Config.BacklogProjectKey) == "" {
		return Result{}, &Error{Kind: KindInput, Err: fmt.Errorf("BACKLOG_PROJECT_KEY is required")}
	}
	if strings.TrimSpace(input.Account) == "" {
		return Result{}, &Error{Kind: KindInput, Err: fmt.Errorf("account is required")}
	}
	if input.MaxComments < 0 {
		return Result{}, &Error{Kind: KindInput, Err: fmt.Errorf("max-comments must be greater than or equal to 0")}
	}
	if !input.From.IsZero() && !input.To.IsZero() && input.To.Before(input.From) {
		return Result{}, &Error{Kind: KindInput, Err: fmt.Errorf("to must not be before from")}
	}

	startedAt := s.now().UTC()
	result.JobID = buildJobID(startedAt)
	targetAccount := stringPointer(input.Account)

	if err := s.Store.JobRuns().Save(ctx, sqlite.JobRun{
		JobID:         result.JobID,
		JobType:       string(prompts.TaskAccountReport),
		Provider:      string(s.Config.LLMProvider),
		ProjectKey:    s.Config.BacklogProjectKey,
		TargetAccount: targetAccount,
		Status:        "running",
		StartedAt:     startedAt,
	}); err != nil {
		return Result{}, &Error{Kind: KindStorage, Err: fmt.Errorf("save job_run: %w", err)}
	}

	fail := func(kind Kind, cause error) (Result, error) {
		finishedAt := s.now().UTC()
		message := cause.Error()
		if updateErr := s.Store.JobRuns().UpdateStatus(ctx, result.JobID, sqlite.JobRunStatusUpdate{
			Status:       "failed",
			FinishedAt:   &finishedAt,
			ErrorMessage: &message,
		}); updateErr != nil {
			return result, &Error{Kind: KindStorage, Err: fmt.Errorf("update failed job_run %s: %w", result.JobID, updateErr)}
		}
		return result, &Error{Kind: kind, Err: cause}
	}

	account, err := s.Collector.ResolveAssignee(ctx, s.Config.BacklogProjectKey, input.Account)
	if err != nil {
		if errors.Is(err, backlogclient.ErrAssigneeNotFound) {
			return fail(KindInput, err)
		}
		return fail(KindBacklog, err)
	}
	if err := s.Store.JobRuns().UpdateArtifacts(ctx, result.JobID, sqlite.JobRunArtifactUpdate{
		TargetAccount: stringPointer(account.UserID),
	}); err != nil {
		return fail(KindStorage, fmt.Errorf("update target_account: %w", err))
	}

	issues, err := s.Collector.CollectPeriodIssues(ctx, backlogclient.IssueListInput{
		ProjectIDOrKey: s.Config.BacklogProjectKey,
		AssigneeIDs:    []int{account.ID},
		DateField:      backlogclient.IssueDateFieldUpdated,
		From:           input.From,
		To:             input.To,
	})
	if err != nil {
		return fail(KindBacklog, err)
	}
	result.IssueCount = len(issues)
	if err := s.Store.JobRuns().UpdateArtifacts(ctx, result.JobID, sqlite.JobRunArtifactUpdate{
		IssueCount: intPointer(result.IssueCount),
	}); err != nil {
		return fail(KindStorage, fmt.Errorf("update issue_count: %w", err))
	}

	issuesJSON, err := s.buildIssuesJSON(ctx, issues, input.MaxComments)
	if err != nil {
		var appErr *Error
		if errors.As(err, &appErr) {
			return fail(appErr.Kind, appErr.Err)
		}
		return fail(KindStorage, err)
	}

	outputSchemaJSON, err := prompts.OutputSchemaJSON(prompts.TaskAccountReport)
	if err != nil {
		return fail(KindStorage, fmt.Errorf("load output schema: %w", err))
	}

	promptData := map[string]any{
		"AccountID":        account.UserID,
		"AccountName":      displayName(account),
		"DateFrom":         formatOptionalDate(input.From),
		"DateTo":           formatOptionalDate(input.To),
		"IssuesJSON":       issuesJSON,
		"OutputSchemaJSON": outputSchemaJSON,
		"Language":         "ja",
	}

	rendered, err := s.PromptManager.Render(prompts.TaskAccountReport, promptData)
	if err != nil {
		return fail(KindStorage, fmt.Errorf("render prompt: %w", err))
	}
	result.PreviewPath, err = s.PromptManager.SavePreview(result.JobID, rendered)
	if err != nil {
		return fail(KindStorage, fmt.Errorf("save preview: %w", err))
	}
	if err := s.Store.JobRuns().UpdateArtifacts(ctx, result.JobID, sqlite.JobRunArtifactUpdate{
		PromptName: stringPointer(string(rendered.Task)),
		PromptHash: stringPointer(rendered.Hash),
	}); err != nil {
		return fail(KindStorage, fmt.Errorf("update prompt artifacts: %w", err))
	}
	if err := s.Store.PromptRuns().Save(ctx, sqlite.PromptRun{
		JobID:              result.JobID,
		TaskType:           string(prompts.TaskAccountReport),
		SystemTemplate:     rendered.SystemTemplate,
		UserTemplate:       rendered.UserTemplate,
		PromptHash:         rendered.Hash,
		RenderedPromptPath: stringPointer(result.PreviewPath),
		CreatedAt:          startedAt,
	}); err != nil {
		return fail(KindStorage, fmt.Errorf("save prompt_run: %w", err))
	}

	llmResult, err := s.LLMProvider.Generate(ctx, llm.GenerateRequest{
		Task:         prompts.TaskAccountReport,
		SystemPrompt: rendered.System,
		UserPrompt:   rendered.User,
		SchemaJSON:   outputSchemaJSON,
	})
	if err != nil {
		return fail(KindLLM, err)
	}

	output, ok := llmResult.Output.(llm.AccountReportOutput)
	if !ok {
		return fail(KindLLM, fmt.Errorf("unexpected llm output type %T", llmResult.Output))
	}
	result.Output = output

	result.RawResponsePath, err = s.SaveRawResponse(s.BaseDir, s.Config.RawResponseDir, result.JobID, s.Config.LLMProvider, prompts.TaskAccountReport, llmResult.RawResponse, startedAt)
	if err != nil {
		return fail(KindStorage, fmt.Errorf("save raw response: %w", err))
	}
	if err := s.Store.JobRuns().UpdateArtifacts(ctx, result.JobID, sqlite.JobRunArtifactUpdate{
		RawResponsePath: stringPointer(result.RawResponsePath),
	}); err != nil {
		return fail(KindStorage, fmt.Errorf("update raw_response_path: %w", err))
	}

	result.ReportPath, err = saveReport(s.BaseDir, s.Config.ReportDir, result.JobID, s.Config, input, account, output, startedAt)
	if err != nil {
		return fail(KindStorage, fmt.Errorf("save report: %w", err))
	}
	if err := s.Store.JobRuns().UpdateArtifacts(ctx, result.JobID, sqlite.JobRunArtifactUpdate{
		ReportPath: stringPointer(result.ReportPath),
	}); err != nil {
		return fail(KindStorage, fmt.Errorf("update report_path: %w", err))
	}

	if !input.DryRun {
		if s.Notifier == nil {
			return fail(KindSlack, fmt.Errorf("slack notifier is required"))
		}
		message := BuildSlackMessage(s.Config, input, output)
		response, notifyErr := s.Notifier.Send(ctx, message)
		if notifyErr != nil {
			saveErr := s.Store.NotificationLogs().Save(ctx, sqlite.NotificationLog{
				JobID:           result.JobID,
				ChannelType:     "slack",
				Destination:     stringPointer(notificationDestination(s.Config)),
				Status:          "failed",
				ResponseSummary: stringPointer(notifyErr.Error()),
				SentAt:          timePointer(s.now().UTC()),
			})
			if saveErr != nil {
				return fail(KindStorage, fmt.Errorf("save failed notification_log: %w", saveErr))
			}
			return fail(KindSlack, notifyErr)
		}
		result.NotificationSent = true
		result.NotificationResponse = response.Summary
		if err := s.Store.NotificationLogs().Save(ctx, sqlite.NotificationLog{
			JobID:           result.JobID,
			ChannelType:     "slack",
			Destination:     stringPointer(response.Destination),
			Status:          "sent",
			ResponseSummary: stringPointer(response.Summary),
			SentAt:          timePointer(s.now().UTC()),
		}); err != nil {
			return fail(KindStorage, fmt.Errorf("save notification_log: %w", err))
		}
	}

	finishedAt := s.now().UTC()
	if err := s.Store.JobRuns().UpdateStatus(ctx, result.JobID, sqlite.JobRunStatusUpdate{
		Status:     "completed",
		FinishedAt: &finishedAt,
	}); err != nil {
		return Result{}, &Error{Kind: KindStorage, Err: fmt.Errorf("update completed job_run %s: %w", result.JobID, err)}
	}

	return result, nil
}

func (s Service) buildIssuesJSON(ctx context.Context, issues []backlogclient.Issue, maxComments int) (string, error) {
	snapshots := make([]promptIssue, 0, len(issues))
	for _, issue := range issues {
		comments, err := s.Comments.ListIssueComments(ctx, issue.IssueKey)
		if err != nil {
			return "", &Error{Kind: KindBacklog, Err: err}
		}

		snapshots = append(snapshots, promptIssue{
			IssueKey:    issue.IssueKey,
			Title:       issue.Summary,
			Status:      issueStatus(issue),
			Description: issue.Description,
			CreatedAt:   formatTimestamp(issue.CreatedAt),
			UpdatedAt:   formatTimestamp(issue.UpdatedAt),
			Comments:    mapComments(selectLatestComments(comments, maxComments)),
		})
	}

	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal account report issues JSON: %w", err)
	}
	return string(data), nil
}

func BuildSlackMessage(cfg config.Config, input Input, output llm.AccountReportOutput) notificationslack.Message {
	accountLabel := output.Account.DisplayName
	if strings.TrimSpace(accountLabel) == "" {
		accountLabel = output.Account.ID
	}
	summary := sanitizeSlackText(output.Summary)
	textLines := []string{
		fmt.Sprintf("[%s] %s の担当課題レポート", cfg.BacklogProjectKey, accountLabel),
		fmt.Sprintf("対象件数: %d", len(output.Issues)),
		fmt.Sprintf("期間: %s - %s", formatOptionalDate(input.From), formatOptionalDate(input.To)),
		fmt.Sprintf("概要: %s", summary),
	}

	blocks := []notificationslack.Block{
		{
			"type": "header",
			"text": map[string]any{
				"type":  "plain_text",
				"text":  truncateSlackPlainText(fmt.Sprintf("[%s] %s の担当課題レポート", cfg.BacklogProjectKey, accountLabel), 150),
				"emoji": true,
			},
		},
		{
			"type": "section",
			"fields": []map[string]any{
				{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*アカウント*\n%s (`%s`)", truncateSlackMarkdown(sanitizeSlackText(accountLabel), 80), sanitizeSlackText(output.Account.ID)),
				},
				{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*対象件数*\n%d", len(output.Issues)),
				},
			},
		},
		{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": "*概要*\n" + truncateSlackMarkdown(summary, 2800),
			},
		},
	}

	displayCount := min(len(output.Issues), 5)
	for index := 0; index < displayCount; index++ {
		issue := output.Issues[index]
		issueBlockText := buildIssueSlackText(issue)
		blocks = append(blocks, notificationslack.Block{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": truncateSlackMarkdown(issueBlockText, 2800),
			},
		})
		textLines = append(textLines, fmt.Sprintf("- %s %s: %s", issue.IssueKey, sanitizeSlackText(issue.Status), sanitizeSlackText(issue.Summary)))
	}
	if len(output.Issues) > displayCount {
		remaining := len(output.Issues) - displayCount
		blocks = append(blocks, notificationslack.Block{
			"type": "context",
			"elements": []map[string]any{
				{
					"type": "mrkdwn",
					"text": fmt.Sprintf("%d 件の課題は保存レポートを参照", remaining),
				},
			},
		})
		textLines = append(textLines, fmt.Sprintf("ほか %d 件は保存レポートを参照", remaining))
	}

	return notificationslack.Message{
		Text:   strings.Join(textLines, "\n"),
		Blocks: blocks,
	}
}

func buildIssueSlackText(issue llm.AccountReportIssue) string {
	title := sanitizeSlackText(issue.Title)
	status := sanitizeSlackText(issue.Status)
	summary := sanitizeSlackText(issue.Summary)
	message := sanitizeSlackText(issue.ResponseSuggestion.Message)
	needsConfirmation := "no"
	if issue.ResponseSuggestion.NeedsConfirmation {
		needsConfirmation = "yes"
	}

	lines := []string{
		fmt.Sprintf("*%s* %s", issue.IssueKey, truncateSlackMarkdown(title, 120)),
		fmt.Sprintf("status: %s", truncateSlackMarkdown(status, 80)),
		fmt.Sprintf("summary: %s", truncateSlackMarkdown(summary, 280)),
		fmt.Sprintf("suggestion: %s", truncateSlackMarkdown(message, 500)),
		fmt.Sprintf("confidence: %s / needsConfirmation: %s", issue.ResponseSuggestion.Confidence, needsConfirmation),
	}
	return strings.Join(lines, "\n")
}

func saveReport(baseDir, reportDir, jobID string, cfg config.Config, input Input, account backlogclient.User, output llm.AccountReportOutput, now time.Time) (string, error) {
	targetDir := filepath.Join(config.ResolvePath(baseDir, reportDir), string(prompts.TaskAccountReport))
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("create report directory: %w", err)
	}

	filePath := filepath.Join(targetDir, fmt.Sprintf("%s-%s.md", jobID, now.UTC().Format("20060102T150405Z")))
	if err := os.WriteFile(filePath, []byte(buildReportMarkdown(cfg, input, account, output, now)), 0o600); err != nil {
		return "", fmt.Errorf("write report file: %w", err)
	}
	return filePath, nil
}

func buildReportMarkdown(cfg config.Config, input Input, account backlogclient.User, output llm.AccountReportOutput, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Account Report\n\n")
	builder.WriteString(fmt.Sprintf("- project: %s\n", cfg.BacklogProjectKey))
	builder.WriteString(fmt.Sprintf("- provider: %s\n", cfg.LLMProvider))
	builder.WriteString(fmt.Sprintf("- generatedAt: %s\n", generatedAt.UTC().Format(time.RFC3339)))
	builder.WriteString(fmt.Sprintf("- accountId: %s\n", account.UserID))
	builder.WriteString(fmt.Sprintf("- accountName: %s\n", displayName(account)))
	builder.WriteString(fmt.Sprintf("- from: %s\n", formatOptionalDate(input.From)))
	builder.WriteString(fmt.Sprintf("- to: %s\n", formatOptionalDate(input.To)))
	builder.WriteString(fmt.Sprintf("- issueCount: %d\n\n", len(output.Issues)))
	builder.WriteString("## Summary\n\n")
	builder.WriteString(output.Summary)
	builder.WriteString("\n\n## Issues\n\n")
	if len(output.Issues) == 0 {
		builder.WriteString("- none\n")
		return builder.String()
	}
	for _, issue := range output.Issues {
		builder.WriteString(fmt.Sprintf("### %s %s\n\n", issue.IssueKey, issue.Title))
		builder.WriteString(fmt.Sprintf("- status: %s\n", issue.Status))
		builder.WriteString(fmt.Sprintf("- summary: %s\n", issue.Summary))
		builder.WriteString(fmt.Sprintf("- confidence: %s\n", issue.ResponseSuggestion.Confidence))
		builder.WriteString(fmt.Sprintf("- needsConfirmation: %t\n\n", issue.ResponseSuggestion.NeedsConfirmation))
		builder.WriteString(issue.ResponseSuggestion.Message)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

func mapComments(comments []backlogclient.IssueComment) []promptComment {
	mapped := make([]promptComment, 0, len(comments))
	for _, comment := range comments {
		mapped = append(mapped, promptComment{
			Author:    commentAuthor(comment),
			Content:   comment.Content,
			CreatedAt: formatTimestamp(comment.CreatedAt),
		})
	}
	return mapped
}

func selectLatestComments(comments []backlogclient.IssueComment, maxComments int) []backlogclient.IssueComment {
	if len(comments) == 0 {
		return nil
	}

	sorted := append([]backlogclient.IssueComment(nil), comments...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})
	if maxComments > 0 && len(sorted) > maxComments {
		sorted = sorted[len(sorted)-maxComments:]
	}
	return sorted
}

func sanitizeSlackText(value string) string {
	sanitized := strings.Join(strings.Fields(value), " ")
	sanitized = emailPattern.ReplaceAllString(sanitized, "[redacted-email]")
	sanitized = phonePattern.ReplaceAllString(sanitized, "[redacted-phone]")
	return strings.TrimSpace(sanitized)
}

func truncateSlackMarkdown(value string, limit int) string {
	return truncateSlackText(value, limit)
}

func truncateSlackPlainText(value string, limit int) string {
	return truncateSlackText(value, limit)
}

func truncateSlackText(value string, limit int) string {
	if limit <= 0 {
		return value
	}

	runeCount := 0
	for index := range value {
		if runeCount == limit-1 {
			return strings.TrimSpace(value[:index]) + "…"
		}
		runeCount++
	}
	return value
}

func commentAuthor(comment backlogclient.IssueComment) string {
	if comment.CreatedUser == nil {
		return ""
	}
	if strings.TrimSpace(comment.CreatedUser.UserID) != "" {
		return comment.CreatedUser.UserID
	}
	return comment.CreatedUser.Name
}

func issueStatus(issue backlogclient.Issue) string {
	if issue.Status == nil {
		return ""
	}
	return issue.Status.Name
}

func displayName(user backlogclient.User) string {
	if strings.TrimSpace(user.Name) != "" {
		return user.Name
	}
	return user.UserID
}

func formatOptionalDate(value time.Time) string {
	if value.IsZero() {
		return "(not specified)"
	}
	return value.Format("2006-01-02")
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func buildJobID(now time.Time) string {
	return fmt.Sprintf("%s-%s", prompts.TaskAccountReport, now.UTC().Format("20060102T150405.000000000"))
}

func notificationDestination(cfg config.Config) string {
	if strings.TrimSpace(cfg.SlackWebhookURL) != "" {
		return "incoming-webhook"
	}
	if strings.TrimSpace(cfg.SlackChannel) != "" {
		return cfg.SlackChannel
	}
	return "slack"
}

func (s Service) validate() error {
	switch {
	case s.Collector == nil:
		return fmt.Errorf("collector is required")
	case s.Comments == nil:
		return fmt.Errorf("comment lister is required")
	case s.PromptManager == nil:
		return fmt.Errorf("prompt manager is required")
	case s.LLMProvider == nil:
		return fmt.Errorf("llm provider is required")
	case s.Store == nil:
		return fmt.Errorf("sqlite store is required")
	case s.SaveRawResponse == nil:
		return fmt.Errorf("raw response saver is required")
	}
	return nil
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func stringPointer(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func intPointer(value int) *int {
	return &value
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
