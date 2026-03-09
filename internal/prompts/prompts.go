package prompts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

type Task string

const (
	TaskPeriodSummary Task = "period_summary"
	TaskAccountReport Task = "account_report"
)

type Manager struct {
	PromptDir     string
	PreviewDir    string
	RetentionDays int
	Now           func() time.Time
}

type RenderedPrompt struct {
	Task           Task
	System         string
	User           string
	Hash           string
	SystemTemplate string
	UserTemplate   string
	RenderedAt     time.Time
}

func ParseTask(commandName string) (Task, error) {
	switch strings.TrimSpace(commandName) {
	case "period-summary":
		return TaskPeriodSummary, nil
	case "account-report":
		return TaskAccountReport, nil
	default:
		return "", fmt.Errorf("unsupported prompt task %q", commandName)
	}
}

func (m Manager) Render(task Task, data any) (RenderedPrompt, error) {
	if err := validateTask(task); err != nil {
		return RenderedPrompt{}, err
	}
	if strings.TrimSpace(m.PromptDir) == "" {
		return RenderedPrompt{}, fmt.Errorf("prompt directory is required")
	}

	systemRelativePath := filepath.Join(string(task), "system.tmpl")
	userRelativePath := filepath.Join(string(task), "user.tmpl")
	systemPath := filepath.Join(m.PromptDir, systemRelativePath)
	userPath := filepath.Join(m.PromptDir, userRelativePath)

	systemTemplate, err := parseTemplateFile(systemPath)
	if err != nil {
		return RenderedPrompt{}, fmt.Errorf("load system template: %w", err)
	}
	userTemplate, err := parseTemplateFile(userPath)
	if err != nil {
		return RenderedPrompt{}, fmt.Errorf("load user template: %w", err)
	}

	renderedSystem, err := executeTemplate(systemTemplate, data)
	if err != nil {
		return RenderedPrompt{}, fmt.Errorf("render system template: %w", err)
	}
	renderedUser, err := executeTemplate(userTemplate, data)
	if err != nil {
		return RenderedPrompt{}, fmt.Errorf("render user template: %w", err)
	}

	renderedAt := m.now()
	if renderedAt.IsZero() {
		renderedAt = time.Now().UTC()
	}

	return RenderedPrompt{
		Task:           task,
		System:         renderedSystem,
		User:           renderedUser,
		Hash:           hashPrompt(renderedSystem, renderedUser),
		SystemTemplate: filepath.ToSlash(systemRelativePath),
		UserTemplate:   filepath.ToSlash(userRelativePath),
		RenderedAt:     renderedAt.UTC(),
	}, nil
}

func (m Manager) SavePreview(jobID string, rendered RenderedPrompt) (string, error) {
	if strings.TrimSpace(jobID) == "" {
		return "", fmt.Errorf("jobID is required")
	}
	if err := validateTask(rendered.Task); err != nil {
		return "", err
	}
	if strings.TrimSpace(m.PreviewDir) == "" {
		return "", fmt.Errorf("preview directory is required")
	}

	if err := m.CleanupExpired(); err != nil {
		return "", err
	}

	taskDir := filepath.Join(m.PreviewDir, string(rendered.Task))
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return "", fmt.Errorf("create preview directory: %w", err)
	}

	fileName := fmt.Sprintf("%s-%s.txt", jobID, rendered.RenderedAt.UTC().Format("20060102T150405Z"))
	filePath := filepath.Join(taskDir, fileName)
	if err := os.WriteFile(filePath, []byte(rendered.previewContents(jobID)), 0o644); err != nil {
		return "", fmt.Errorf("write prompt preview: %w", err)
	}

	return filePath, nil
}

func (m Manager) CleanupExpired() error {
	if strings.TrimSpace(m.PreviewDir) == "" {
		return fmt.Errorf("preview directory is required")
	}
	if m.RetentionDays <= 0 {
		return fmt.Errorf("retention days must be greater than 0")
	}
	if _, err := os.Stat(m.PreviewDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect preview directory: %w", err)
	}

	cutoff := m.now().UTC().AddDate(0, 0, -m.RetentionDays)
	if err := filepath.WalkDir(m.PreviewDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		if info.ModTime().UTC().Before(cutoff) {
			if removeErr := os.Remove(path); removeErr != nil {
				return fmt.Errorf("remove expired preview %s: %w", path, removeErr)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("cleanup expired previews: %w", err)
	}

	return removeEmptyDirectories(m.PreviewDir)
}

func OutputSchemaJSON(task Task) (string, error) {
	switch task {
	case TaskPeriodSummary:
		return periodSummaryOutputSchema, nil
	case TaskAccountReport:
		return accountReportOutputSchema, nil
	default:
		return "", fmt.Errorf("unsupported prompt task %q", task)
	}
}

func (m Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now().UTC()
}

func validateTask(task Task) error {
	switch task {
	case TaskPeriodSummary, TaskAccountReport:
		return nil
	default:
		return fmt.Errorf("unsupported prompt task %q", task)
	}
}

func parseTemplateFile(path string) (*template.Template, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	tmpl, err := template.New(filepath.Base(path)).Option("missingkey=error").Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return tmpl, nil
}

func executeTemplate(tmpl *template.Template, data any) (string, error) {
	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(buffer.String()), nil
}

func hashPrompt(systemPrompt, userPrompt string) string {
	sum := sha256.Sum256([]byte(systemPrompt + "\n---\n" + userPrompt))
	return hex.EncodeToString(sum[:])
}

func (r RenderedPrompt) previewContents(jobID string) string {
	return fmt.Sprintf(
		"job_id: %s\ntask: %s\nprompt_hash: %s\nrendered_at: %s\nsystem_template: %s\nuser_template: %s\n\n--- SYSTEM ---\n%s\n\n--- USER ---\n%s\n",
		jobID,
		r.Task,
		r.Hash,
		r.RenderedAt.UTC().Format(time.RFC3339),
		r.SystemTemplate,
		r.UserTemplate,
		r.System,
		r.User,
	)
}

func removeEmptyDirectories(root string) error {
	var directories []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, path)
		}
		return nil
	}); err != nil {
		return err
	}

	for index := len(directories) - 1; index >= 0; index-- {
		current := directories[index]
		if current == root {
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			if err := os.Remove(current); err != nil {
				return err
			}
		}
	}

	return nil
}
