package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type JobRun struct {
	ID              int64
	JobID           string
	JobType         string
	Provider        string
	ProjectKey      string
	TargetAccount   *string
	Status          string
	PromptName      *string
	PromptHash      *string
	IssueCount      *int
	ReportPath      *string
	RawResponsePath *string
	ErrorMessage    *string
	StartedAt       time.Time
	FinishedAt      *time.Time
}

type JobRunStatusUpdate struct {
	Status       string
	FinishedAt   *time.Time
	ErrorMessage *string
}

type NotificationLog struct {
	ID              int64
	JobID           string
	ChannelType     string
	Destination     *string
	Status          string
	ResponseSummary *string
	SentAt          *time.Time
}

type PromptRun struct {
	ID                 int64
	JobID              string
	TaskType           string
	SystemTemplate     string
	UserTemplate       string
	PromptHash         string
	RenderedPromptPath *string
	CreatedAt          time.Time
}

type JobRunRepository struct {
	db *sql.DB
}

type NotificationLogRepository struct {
	db *sql.DB
}

type PromptRunRepository struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) JobRuns() *JobRunRepository {
	return &JobRunRepository{db: s.db}
}

func (s *Store) NotificationLogs() *NotificationLogRepository {
	return &NotificationLogRepository{db: s.db}
}

func (s *Store) PromptRuns() *PromptRunRepository {
	return &PromptRunRepository{db: s.db}
}

func (r *JobRunRepository) Save(ctx context.Context, run JobRun) error {
	if strings.TrimSpace(run.JobID) == "" {
		return fmt.Errorf("job_id is required")
	}
	if strings.TrimSpace(run.JobType) == "" {
		return fmt.Errorf("job_type is required")
	}
	if strings.TrimSpace(run.Provider) == "" {
		return fmt.Errorf("provider is required")
	}
	if strings.TrimSpace(run.ProjectKey) == "" {
		return fmt.Errorf("project_key is required")
	}
	if strings.TrimSpace(run.Status) == "" {
		return fmt.Errorf("status is required")
	}
	if run.StartedAt.IsZero() {
		return fmt.Errorf("started_at is required")
	}

	_, err := r.db.ExecContext(ctx, `
INSERT INTO job_runs(
    job_id, job_type, provider, project_key, target_account, status, prompt_name, prompt_hash,
    issue_count, report_path, raw_response_path, error_message, started_at, finished_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.JobID,
		run.JobType,
		run.Provider,
		run.ProjectKey,
		nullableString(run.TargetAccount),
		run.Status,
		nullableString(run.PromptName),
		nullableString(run.PromptHash),
		nullableInt(run.IssueCount),
		nullableString(run.ReportPath),
		nullableString(run.RawResponsePath),
		nullableString(run.ErrorMessage),
		run.StartedAt.UTC().Format(time.RFC3339),
		nullableTime(run.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("insert job_run %s: %w", run.JobID, err)
	}
	return nil
}

func (r *JobRunRepository) UpdateStatus(ctx context.Context, jobID string, update JobRunStatusUpdate) error {
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("job_id is required")
	}
	if strings.TrimSpace(update.Status) == "" {
		return fmt.Errorf("status is required")
	}

	result, err := r.db.ExecContext(ctx, `
UPDATE job_runs
SET status = ?, finished_at = ?, error_message = ?
WHERE job_id = ?`,
		update.Status,
		nullableTime(update.FinishedAt),
		nullableString(update.ErrorMessage),
		jobID,
	)
	if err != nil {
		return fmt.Errorf("update job_run %s: %w", jobID, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read update result for job_run %s: %w", jobID, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("job_run %s: %w", jobID, sql.ErrNoRows)
	}
	return nil
}

func (r *JobRunRepository) GetByJobID(ctx context.Context, jobID string) (JobRun, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, job_id, job_type, provider, project_key, target_account, status, prompt_name, prompt_hash,
       issue_count, report_path, raw_response_path, error_message, started_at, finished_at
FROM job_runs
WHERE job_id = ?`, jobID)

	run, err := scanJobRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return JobRun{}, fmt.Errorf("job_run %s: %w", jobID, sql.ErrNoRows)
		}
		return JobRun{}, err
	}
	return run, nil
}

func (r *NotificationLogRepository) Save(ctx context.Context, log NotificationLog) error {
	if strings.TrimSpace(log.JobID) == "" {
		return fmt.Errorf("job_id is required")
	}
	if strings.TrimSpace(log.ChannelType) == "" {
		return fmt.Errorf("channel_type is required")
	}
	if strings.TrimSpace(log.Status) == "" {
		return fmt.Errorf("status is required")
	}

	_, err := r.db.ExecContext(ctx, `
INSERT INTO notification_logs(job_id, channel_type, destination, status, response_summary, sent_at)
VALUES(?, ?, ?, ?, ?, ?)`,
		log.JobID,
		log.ChannelType,
		nullableString(log.Destination),
		log.Status,
		nullableString(log.ResponseSummary),
		nullableTime(log.SentAt),
	)
	if err != nil {
		return fmt.Errorf("insert notification_log for job %s: %w", log.JobID, err)
	}
	return nil
}

func (r *NotificationLogRepository) ListByJobID(ctx context.Context, jobID string) ([]NotificationLog, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, job_id, channel_type, destination, status, response_summary, sent_at
FROM notification_logs
WHERE job_id = ?
ORDER BY id`, jobID)
	if err != nil {
		return nil, fmt.Errorf("query notification_logs for job %s: %w", jobID, err)
	}
	defer rows.Close()

	logs := []NotificationLog{}
	for rows.Next() {
		log, err := scanNotificationLog(rows)
		if err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification_logs for job %s: %w", jobID, err)
	}
	return logs, nil
}

func (r *PromptRunRepository) Save(ctx context.Context, run PromptRun) error {
	if strings.TrimSpace(run.JobID) == "" {
		return fmt.Errorf("job_id is required")
	}
	if strings.TrimSpace(run.TaskType) == "" {
		return fmt.Errorf("task_type is required")
	}
	if strings.TrimSpace(run.SystemTemplate) == "" {
		return fmt.Errorf("system_template is required")
	}
	if strings.TrimSpace(run.UserTemplate) == "" {
		return fmt.Errorf("user_template is required")
	}
	if strings.TrimSpace(run.PromptHash) == "" {
		return fmt.Errorf("prompt_hash is required")
	}
	if run.CreatedAt.IsZero() {
		return fmt.Errorf("created_at is required")
	}

	_, err := r.db.ExecContext(ctx, `
INSERT INTO prompt_runs(job_id, task_type, system_template, user_template, prompt_hash, rendered_prompt_path, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?)`,
		run.JobID,
		run.TaskType,
		run.SystemTemplate,
		run.UserTemplate,
		run.PromptHash,
		nullableString(run.RenderedPromptPath),
		run.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert prompt_run for job %s: %w", run.JobID, err)
	}
	return nil
}

func (r *PromptRunRepository) ListByJobID(ctx context.Context, jobID string) ([]PromptRun, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, job_id, task_type, system_template, user_template, prompt_hash, rendered_prompt_path, created_at
FROM prompt_runs
WHERE job_id = ?
ORDER BY id`, jobID)
	if err != nil {
		return nil, fmt.Errorf("query prompt_runs for job %s: %w", jobID, err)
	}
	defer rows.Close()

	runs := []PromptRun{}
	for rows.Next() {
		run, err := scanPromptRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompt_runs for job %s: %w", jobID, err)
	}
	return runs, nil
}

func scanJobRun(scanner interface{ Scan(dest ...any) error }) (JobRun, error) {
	var run JobRun
	var targetAccount sql.NullString
	var promptName sql.NullString
	var promptHash sql.NullString
	var issueCount sql.NullInt64
	var reportPath sql.NullString
	var rawResponsePath sql.NullString
	var errorMessage sql.NullString
	var startedAtRaw string
	var finishedAtRaw sql.NullString

	if err := scanner.Scan(
		&run.ID,
		&run.JobID,
		&run.JobType,
		&run.Provider,
		&run.ProjectKey,
		&targetAccount,
		&run.Status,
		&promptName,
		&promptHash,
		&issueCount,
		&reportPath,
		&rawResponsePath,
		&errorMessage,
		&startedAtRaw,
		&finishedAtRaw,
	); err != nil {
		return JobRun{}, err
	}

	startedAt, err := time.Parse(time.RFC3339, startedAtRaw)
	if err != nil {
		return JobRun{}, fmt.Errorf("parse job_run started_at: %w", err)
	}

	finishedAt, err := parseNullTime(finishedAtRaw)
	if err != nil {
		return JobRun{}, fmt.Errorf("parse job_run finished_at: %w", err)
	}

	run.TargetAccount = nullStringPointer(targetAccount)
	run.PromptName = nullStringPointer(promptName)
	run.PromptHash = nullStringPointer(promptHash)
	run.IssueCount = nullIntPointer(issueCount)
	run.ReportPath = nullStringPointer(reportPath)
	run.RawResponsePath = nullStringPointer(rawResponsePath)
	run.ErrorMessage = nullStringPointer(errorMessage)
	run.StartedAt = startedAt
	run.FinishedAt = finishedAt
	return run, nil
}

func scanNotificationLog(scanner interface{ Scan(dest ...any) error }) (NotificationLog, error) {
	var log NotificationLog
	var destination sql.NullString
	var responseSummary sql.NullString
	var sentAtRaw sql.NullString

	if err := scanner.Scan(&log.ID, &log.JobID, &log.ChannelType, &destination, &log.Status, &responseSummary, &sentAtRaw); err != nil {
		return NotificationLog{}, fmt.Errorf("scan notification_log: %w", err)
	}

	sentAt, err := parseNullTime(sentAtRaw)
	if err != nil {
		return NotificationLog{}, fmt.Errorf("parse notification_log sent_at: %w", err)
	}

	log.Destination = nullStringPointer(destination)
	log.ResponseSummary = nullStringPointer(responseSummary)
	log.SentAt = sentAt
	return log, nil
}

func scanPromptRun(scanner interface{ Scan(dest ...any) error }) (PromptRun, error) {
	var run PromptRun
	var renderedPromptPath sql.NullString
	var createdAtRaw string

	if err := scanner.Scan(&run.ID, &run.JobID, &run.TaskType, &run.SystemTemplate, &run.UserTemplate, &run.PromptHash, &renderedPromptPath, &createdAtRaw); err != nil {
		return PromptRun{}, fmt.Errorf("scan prompt_run: %w", err)
	}

	createdAt, err := time.Parse(time.RFC3339, createdAtRaw)
	if err != nil {
		return PromptRun{}, fmt.Errorf("parse prompt_run created_at: %w", err)
	}

	run.RenderedPromptPath = nullStringPointer(renderedPromptPath)
	run.CreatedAt = createdAt
	return run, nil
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func nullIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

func parseNullTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
