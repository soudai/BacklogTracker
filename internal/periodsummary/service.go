package periodsummary

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

type statusLister interface {
	ListProjectStatuses(ctx context.Context, projectIDOrKey string) ([]backlogclient.Status, error)
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
	Statuses        statusLister
	PromptManager   promptManager
	LLMProvider     llmProvider
	Notifier        notifier
	Store           *sqlite.Store
	SaveRawResponse rawResponseSaver
	Now             func() time.Time
}

type Input struct {
	From      time.Time
	To        time.Time
	DateField backlogclient.IssueDateField
	Assignee  string
	Statuses  []string
	DryRun    bool
}

type Result struct {
	JobID                string
	IssueCount           int
	PreviewPath          string
	RawResponsePath      string
	ReportPath           string
	NotificationSent     bool
	NotificationResponse string
	Output               llm.PeriodSummaryOutput
}

type reportIssue struct {
	IssueKey    string `json:"issueKey"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	Assignee    string `json:"assignee,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

func (s Service) Run(ctx context.Context, input Input) (result Result, err error) {
	if err := s.validate(); err != nil {
		return Result{}, &Error{Kind: KindStorage, Err: err}
	}
	if strings.TrimSpace(s.Config.BacklogProjectKey) == "" {
		return Result{}, &Error{Kind: KindInput, Err: fmt.Errorf("BACKLOG_PROJECT_KEY is required")}
	}
	if input.From.IsZero() || input.To.IsZero() {
		return Result{}, &Error{Kind: KindInput, Err: fmt.Errorf("from and to are required")}
	}
	if input.To.Before(input.From) {
		return Result{}, &Error{Kind: KindInput, Err: fmt.Errorf("to must not be before from")}
	}
	if input.DateField == "" {
		input.DateField = backlogclient.IssueDateFieldUpdated
	}
	switch input.DateField {
	case backlogclient.IssueDateFieldUpdated, backlogclient.IssueDateFieldCreated:
	default:
		return Result{}, &Error{Kind: KindInput, Err: fmt.Errorf("dateField must be %q or %q", backlogclient.IssueDateFieldUpdated, backlogclient.IssueDateFieldCreated)}
	}

	startedAt := s.now().UTC()
	result.JobID = buildJobID(startedAt)
	targetAccount := stringPointer(input.Assignee)

	if err := s.Store.JobRuns().Save(ctx, sqlite.JobRun{
		JobID:         result.JobID,
		JobType:       string(prompts.TaskPeriodSummary),
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

	statusIDs, err := s.resolveStatusIDs(ctx, input.Statuses)
	if err != nil {
		var appErr *Error
		if errors.As(err, &appErr) {
			return fail(appErr.Kind, appErr.Err)
		}
		return fail(KindInput, err)
	}

	assigneeIDs, err := s.resolveAssigneeIDs(ctx, input.Assignee)
	if err != nil {
		var appErr *Error
		if errors.As(err, &appErr) {
			return fail(appErr.Kind, appErr.Err)
		}
		return fail(KindInput, err)
	}

	issues, err := s.Collector.CollectPeriodIssues(ctx, backlogclient.IssueListInput{
		ProjectIDOrKey: s.Config.BacklogProjectKey,
		AssigneeIDs:    assigneeIDs,
		StatusIDs:      statusIDs,
		DateField:      input.DateField,
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

	outputSchemaJSON, err := prompts.OutputSchemaJSON(prompts.TaskPeriodSummary)
	if err != nil {
		return fail(KindStorage, fmt.Errorf("load output schema: %w", err))
	}

	promptData, err := buildPromptTemplateData(s.Config.BacklogProjectKey, input.From, input.To, issues, outputSchemaJSON)
	if err != nil {
		return fail(KindStorage, err)
	}

	rendered, err := s.PromptManager.Render(prompts.TaskPeriodSummary, promptData)
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
		TaskType:           string(prompts.TaskPeriodSummary),
		SystemTemplate:     rendered.SystemTemplate,
		UserTemplate:       rendered.UserTemplate,
		PromptHash:         rendered.Hash,
		RenderedPromptPath: stringPointer(result.PreviewPath),
		CreatedAt:          startedAt,
	}); err != nil {
		return fail(KindStorage, fmt.Errorf("save prompt_run: %w", err))
	}

	llmResult, err := s.LLMProvider.Generate(ctx, llm.GenerateRequest{
		Task:         prompts.TaskPeriodSummary,
		SystemPrompt: rendered.System,
		UserPrompt:   rendered.User,
		SchemaJSON:   outputSchemaJSON,
	})
	if err != nil {
		return fail(KindLLM, err)
	}

	output, ok := llmResult.Output.(llm.PeriodSummaryOutput)
	if !ok {
		return fail(KindLLM, fmt.Errorf("unexpected llm output type %T", llmResult.Output))
	}
	result.Output = output

	result.RawResponsePath, err = s.SaveRawResponse(s.BaseDir, s.Config.RawResponseDir, result.JobID, s.Config.LLMProvider, prompts.TaskPeriodSummary, llmResult.RawResponse, startedAt)
	if err != nil {
		return fail(KindStorage, fmt.Errorf("save raw response: %w", err))
	}
	if err := s.Store.JobRuns().UpdateArtifacts(ctx, result.JobID, sqlite.JobRunArtifactUpdate{
		RawResponsePath: stringPointer(result.RawResponsePath),
	}); err != nil {
		return fail(KindStorage, fmt.Errorf("update raw_response_path: %w", err))
	}

	result.ReportPath, err = saveReport(s.BaseDir, s.Config.ReportDir, result.JobID, s.Config, input, issues, output, startedAt)
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
		message := BuildSlackMessage(s.Config, input, result.IssueCount, output)
		response, notifyErr := s.Notifier.Send(ctx, message)
		if notifyErr != nil {
			saveErr := s.Store.NotificationLogs().Save(ctx, sqlite.NotificationLog{
				JobID:           result.JobID,
				ChannelType:     "slack",
				Destination:     stringPointer(notificationDestination(s.Config, "")),
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

func BuildSlackMessage(cfg config.Config, input Input, issueCount int, output llm.PeriodSummaryOutput) notificationslack.Message {
	headline := sanitizeSlackText(output.Headline)
	overview := sanitizeSlackText(output.Overview)
	keyPoints := sanitizeLines(output.KeyPoints)
	riskLines := sanitizeRiskLines(output.RiskItems)

	textLines := []string{
		fmt.Sprintf("[%s] %s", cfg.BacklogProjectKey, headline),
		fmt.Sprintf("期間: %s - %s", input.From.Format("2006-01-02"), input.To.Format("2006-01-02")),
		fmt.Sprintf("対象件数: %d", issueCount),
		fmt.Sprintf("概要: %s", overview),
	}
	if len(keyPoints) > 0 {
		textLines = append(textLines, "要点:")
		for _, item := range keyPoints {
			textLines = append(textLines, "- "+item)
		}
	}
	if len(riskLines) > 0 {
		textLines = append(textLines, "注意:")
		for _, item := range riskLines {
			textLines = append(textLines, "- "+item)
		}
	}

	blocks := []notificationslack.Block{
		{
			"type": "header",
			"text": map[string]any{
				"type":  "plain_text",
				"text":  truncateSlackPlainText(fmt.Sprintf("[%s] %s", cfg.BacklogProjectKey, headline), 150),
				"emoji": true,
			},
		},
		{
			"type": "section",
			"fields": []map[string]any{
				{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*期間*\n%s - %s", input.From.Format("2006-01-02"), input.To.Format("2006-01-02")),
				},
				{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*対象件数*\n%d", issueCount),
				},
			},
		},
		{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": "*概要*\n" + truncateSlackMarkdown(overview, 2800),
			},
		},
	}
	if len(keyPoints) > 0 {
		blocks = append(blocks, notificationslack.Block{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": truncateSlackMarkdown("*要点*\n• "+strings.Join(keyPoints, "\n• "), 2800),
			},
		})
	}
	if len(riskLines) > 0 {
		blocks = append(blocks, notificationslack.Block{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": truncateSlackMarkdown("*注意項目*\n• "+strings.Join(riskLines, "\n• "), 2800),
			},
		})
	}
	blocks = append(blocks, notificationslack.Block{
		"type": "context",
		"elements": []map[string]any{
			{
				"type": "mrkdwn",
				"text": fmt.Sprintf("provider=%s counts(total=%d open=%s inProgress=%s resolved=%s closed=%s)",
					cfg.LLMProvider,
					output.Counts.Total,
					formatOptionalCount(output.Counts.Open),
					formatOptionalCount(output.Counts.InProgress),
					formatOptionalCount(output.Counts.Resolved),
					formatOptionalCount(output.Counts.Closed),
				),
			},
		},
	})

	return notificationslack.Message{
		Text:   strings.Join(textLines, "\n"),
		Blocks: blocks,
	}
}

func buildPromptTemplateData(projectKey string, from, to time.Time, issues []backlogclient.Issue, outputSchema string) (map[string]any, error) {
	issuesJSON, err := marshalIssuesJSON(issues)
	if err != nil {
		return nil, fmt.Errorf("marshal issues JSON: %w", err)
	}
	return map[string]any{
		"ProjectKey":       projectKey,
		"ProjectName":      projectKey,
		"DateFrom":         from.Format("2006-01-02"),
		"DateTo":           to.Format("2006-01-02"),
		"IssueCount":       len(issues),
		"IssuesJSON":       issuesJSON,
		"OutputSchemaJSON": outputSchema,
		"Language":         "ja",
	}, nil
}

func marshalIssuesJSON(issues []backlogclient.Issue) (string, error) {
	snapshots := make([]reportIssue, 0, len(issues))
	for _, issue := range issues {
		snapshots = append(snapshots, reportIssue{
			IssueKey:    issue.IssueKey,
			Summary:     issue.Summary,
			Description: issue.Description,
			Status:      issueStatus(issue),
			Assignee:    issueAssignee(issue),
			CreatedAt:   formatTimestamp(issue.CreatedAt),
			UpdatedAt:   formatTimestamp(issue.UpdatedAt),
		})
	}
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func saveReport(baseDir, reportDir, jobID string, cfg config.Config, input Input, issues []backlogclient.Issue, output llm.PeriodSummaryOutput, now time.Time) (string, error) {
	targetDir := filepath.Join(config.ResolvePath(baseDir, reportDir), string(prompts.TaskPeriodSummary))
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("create report directory: %w", err)
	}
	filePath := filepath.Join(targetDir, fmt.Sprintf("%s-%s.md", jobID, now.UTC().Format("20060102T150405Z")))
	contents := buildReportMarkdown(cfg, input, issues, output, now)
	if err := os.WriteFile(filePath, []byte(contents), 0o600); err != nil {
		return "", fmt.Errorf("write report file: %w", err)
	}
	return filePath, nil
}

func buildReportMarkdown(cfg config.Config, input Input, issues []backlogclient.Issue, output llm.PeriodSummaryOutput, generatedAt time.Time) string {
	var builder strings.Builder
	builder.WriteString("# Period Summary\n\n")
	builder.WriteString(fmt.Sprintf("- project: %s\n", cfg.BacklogProjectKey))
	builder.WriteString(fmt.Sprintf("- provider: %s\n", cfg.LLMProvider))
	builder.WriteString(fmt.Sprintf("- generatedAt: %s\n", generatedAt.UTC().Format(time.RFC3339)))
	builder.WriteString(fmt.Sprintf("- from: %s\n", input.From.Format("2006-01-02")))
	builder.WriteString(fmt.Sprintf("- to: %s\n", input.To.Format("2006-01-02")))
	builder.WriteString(fmt.Sprintf("- issueCount: %d\n\n", len(issues)))
	builder.WriteString("## Headline\n\n")
	builder.WriteString(output.Headline)
	builder.WriteString("\n\n## Overview\n\n")
	builder.WriteString(output.Overview)
	builder.WriteString("\n\n## Key Points\n\n")
	if len(output.KeyPoints) == 0 {
		builder.WriteString("- none\n")
	} else {
		for _, item := range output.KeyPoints {
			builder.WriteString("- ")
			builder.WriteString(item)
			builder.WriteString("\n")
		}
	}
	builder.WriteString("\n## Risks\n\n")
	if len(output.RiskItems) == 0 {
		builder.WriteString("- none\n")
	} else {
		for _, item := range output.RiskItems {
			builder.WriteString(fmt.Sprintf("- %s: %s\n", item.IssueKey, item.Reason))
		}
	}
	builder.WriteString("\n## Counts\n\n")
	builder.WriteString(fmt.Sprintf("- total: %d\n", output.Counts.Total))
	builder.WriteString(fmt.Sprintf("- open: %s\n", formatOptionalCount(output.Counts.Open)))
	builder.WriteString(fmt.Sprintf("- inProgress: %s\n", formatOptionalCount(output.Counts.InProgress)))
	builder.WriteString(fmt.Sprintf("- resolved: %s\n", formatOptionalCount(output.Counts.Resolved)))
	builder.WriteString(fmt.Sprintf("- closed: %s\n", formatOptionalCount(output.Counts.Closed)))
	return builder.String()
}

func sanitizeSlackText(value string) string {
	sanitized := strings.Join(strings.Fields(value), " ")
	sanitized = emailPattern.ReplaceAllString(sanitized, "[redacted-email]")
	sanitized = phonePattern.ReplaceAllString(sanitized, "[redacted-phone]")
	return strings.TrimSpace(sanitized)
}

func sanitizeLines(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		sanitized := sanitizeSlackText(value)
		if sanitized == "" {
			continue
		}
		result = append(result, truncateSlackMarkdown(sanitized, 280))
	}
	return result
}

func sanitizeRiskLines(items []llm.PeriodSummaryRiskItem) []string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		reason := sanitizeSlackText(item.Reason)
		if strings.TrimSpace(item.IssueKey) == "" && reason == "" {
			continue
		}
		lines = append(lines, truncateSlackMarkdown(strings.TrimSpace(item.IssueKey+": "+reason), 280))
	}
	return lines
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

func formatOptionalCount(value *int) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *value)
}

func issueStatus(issue backlogclient.Issue) string {
	if issue.Status == nil {
		return ""
	}
	return issue.Status.Name
}

func issueAssignee(issue backlogclient.Issue) string {
	if issue.Assignee == nil {
		return ""
	}
	if strings.TrimSpace(issue.Assignee.UserID) != "" {
		return issue.Assignee.UserID
	}
	return issue.Assignee.Name
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func buildJobID(now time.Time) string {
	return fmt.Sprintf("%s-%s", prompts.TaskPeriodSummary, now.UTC().Format("20060102T150405.000000000"))
}

func (s Service) resolveStatusIDs(ctx context.Context, names []string) ([]int, error) {
	normalized := normalizeStrings(names)
	if len(normalized) == 0 {
		return nil, nil
	}

	statuses, err := s.Statuses.ListProjectStatuses(ctx, s.Config.BacklogProjectKey)
	if err != nil {
		return nil, &Error{Kind: KindBacklog, Err: err}
	}

	byName := map[string]int{}
	for _, status := range statuses {
		key := strings.ToLower(strings.TrimSpace(status.Name))
		if key == "" {
			continue
		}
		byName[key] = status.ID
	}

	statusIDs := make([]int, 0, len(normalized))
	var missing []string
	for _, name := range normalized {
		id, ok := byName[strings.ToLower(name)]
		if !ok {
			missing = append(missing, name)
			continue
		}
		statusIDs = append(statusIDs, id)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, &Error{Kind: KindInput, Err: fmt.Errorf("unknown statuses: %s", strings.Join(missing, ", "))}
	}
	return statusIDs, nil
}

func (s Service) resolveAssigneeIDs(ctx context.Context, assignee string) ([]int, error) {
	trimmed := strings.TrimSpace(assignee)
	if trimmed == "" {
		return nil, nil
	}

	user, err := s.Collector.ResolveAssignee(ctx, s.Config.BacklogProjectKey, trimmed)
	if err != nil {
		if errors.Is(err, backlogclient.ErrAssigneeNotFound) {
			return nil, &Error{Kind: KindInput, Err: err}
		}
		return nil, &Error{Kind: KindBacklog, Err: err}
	}
	return []int{user.ID}, nil
}

func (s Service) validate() error {
	switch {
	case s.Collector == nil:
		return fmt.Errorf("collector is required")
	case s.Statuses == nil:
		return fmt.Errorf("status lister is required")
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

func normalizeStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func stringPointer(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func intPointer(value int) *int {
	return &value
}

func notificationDestination(cfg config.Config, fallback string) string {
	if strings.TrimSpace(cfg.SlackChannel) != "" {
		return cfg.SlackChannel
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "incoming-webhook"
}
