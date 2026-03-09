package prompts

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestManagerRenderPeriodSummaryPrompt(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	writePromptFiles(t, baseDir, TaskPeriodSummary)

	manager := Manager{
		PromptDir:     baseDir,
		PreviewDir:    filepath.Join(baseDir, "previews"),
		RetentionDays: 30,
		Now: func() time.Time {
			return time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		},
	}

	rendered, err := manager.Render(TaskPeriodSummary, map[string]any{
		"ProjectKey":       "PROJ",
		"ProjectName":      "Project",
		"DateFrom":         "2026-03-01",
		"DateTo":           "2026-03-07",
		"IssueCount":       2,
		"IssuesJSON":       "[]",
		"OutputSchemaJSON": `{"type":"object"}`,
	})
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	if !strings.Contains(rendered.System, "system period_summary") {
		t.Fatalf("rendered.System = %q, want task marker", rendered.System)
	}
	if !strings.Contains(rendered.User, "PROJ") {
		t.Fatalf("rendered.User = %q, want project key", rendered.User)
	}
	if rendered.Hash == "" {
		t.Fatalf("rendered.Hash is empty")
	}
	if got, want := rendered.SystemTemplate, "period_summary/system.tmpl"; got != want {
		t.Fatalf("rendered.SystemTemplate = %q, want %q", got, want)
	}
	if strings.HasPrefix(rendered.System, " ") || strings.HasSuffix(rendered.System, " ") {
		t.Fatalf("rendered.System unexpectedly trimmed spaces: %q", rendered.System)
	}
}

func TestManagerRenderAccountReportPrompt(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	writePromptFiles(t, baseDir, TaskAccountReport)

	manager := Manager{
		PromptDir:     baseDir,
		PreviewDir:    filepath.Join(baseDir, "previews"),
		RetentionDays: 30,
	}

	rendered, err := manager.Render(TaskAccountReport, map[string]any{
		"AccountID":        "yamada",
		"AccountName":      "山田 太郎",
		"DateFrom":         "2026-03-01",
		"DateTo":           "2026-03-07",
		"IssuesJSON":       "[]",
		"OutputSchemaJSON": `{"type":"object"}`,
	})
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	if !strings.Contains(rendered.User, "yamada") {
		t.Fatalf("rendered.User = %q, want account ID", rendered.User)
	}
}

func TestManagerSavePreviewAndCleanupExpired(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	previewDir := filepath.Join(baseDir, "previews")
	oldDir := filepath.Join(previewDir, string(TaskPeriodSummary))
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("create old preview dir: %v", err)
	}
	oldPath := filepath.Join(oldDir, "old.txt")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("write old preview: %v", err)
	}
	oldTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("set old preview time: %v", err)
	}

	manager := Manager{
		PromptDir:     filepath.Join(baseDir, "prompts"),
		PreviewDir:    previewDir,
		RetentionDays: 30,
		Now: func() time.Time {
			return time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
		},
	}

	path, err := manager.SavePreview("job-1", RenderedPrompt{
		Task:           TaskPeriodSummary,
		System:         "system",
		User:           "user",
		Hash:           "hash",
		SystemTemplate: "period_summary/system.tmpl",
		UserTemplate:   "period_summary/user.tmpl",
		RenderedAt:     manager.Now(),
	})
	if err != nil {
		t.Fatalf("SavePreview returned error: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved preview: %v", err)
	}
	if !strings.Contains(string(content), "--- SYSTEM ---") {
		t.Fatalf("preview content = %q, want system header", string(content))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat preview: %v", err)
		}
		if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
			t.Fatalf("preview file mode = %v, want %v", got, want)
		}
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old preview still exists, stat error = %v", err)
	}
}

func TestManagerSavePreviewRejectsUnsafeJobID(t *testing.T) {
	t.Parallel()

	manager := Manager{
		PreviewDir:    filepath.Join(t.TempDir(), "previews"),
		RetentionDays: 30,
	}

	_, err := manager.SavePreview("../escape", RenderedPrompt{
		Task:           TaskPeriodSummary,
		System:         "system",
		User:           "user",
		Hash:           "hash",
		SystemTemplate: "period_summary/system.tmpl",
		UserTemplate:   "period_summary/user.tmpl",
		RenderedAt:     time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatalf("SavePreview expected validation error")
	}
	if !strings.Contains(err.Error(), "jobID contains invalid characters") {
		t.Fatalf("SavePreview error = %q, want invalid jobID message", err.Error())
	}
}

func TestManagerSavePreviewRejectsWindowsUnsafeJobID(t *testing.T) {
	t.Parallel()

	manager := Manager{
		PreviewDir:    filepath.Join(t.TempDir(), "previews"),
		RetentionDays: 30,
	}

	_, err := manager.SavePreview("job:1", RenderedPrompt{
		Task:           TaskPeriodSummary,
		System:         "system",
		User:           "user",
		Hash:           "hash",
		SystemTemplate: "period_summary/system.tmpl",
		UserTemplate:   "period_summary/user.tmpl",
		RenderedAt:     time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatalf("SavePreview expected validation error")
	}
	if !strings.Contains(err.Error(), "jobID contains invalid characters") {
		t.Fatalf("SavePreview error = %q, want invalid character message", err.Error())
	}
}

func TestExecuteTemplatePreservesWhitespaceExceptTrailingNewline(t *testing.T) {
	t.Parallel()

	tmpl, err := parseTemplateFile(writeTemplateFile(t, t.TempDir(), "sample.tmpl", "  hello {{ .Name }}  \n"))
	if err != nil {
		t.Fatalf("parseTemplateFile returned error: %v", err)
	}

	rendered, err := executeTemplate(tmpl, map[string]string{"Name": "world"})
	if err != nil {
		t.Fatalf("executeTemplate returned error: %v", err)
	}

	if got, want := rendered, "  hello world  "; got != want {
		t.Fatalf("executeTemplate = %q, want %q", got, want)
	}
}

func TestManagerRenderRejectsMissingTemplateFiles(t *testing.T) {
	t.Parallel()

	manager := Manager{
		PromptDir:     t.TempDir(),
		PreviewDir:    t.TempDir(),
		RetentionDays: 30,
	}

	_, err := manager.Render(TaskPeriodSummary, map[string]any{})
	if err == nil {
		t.Fatalf("Render expected error")
	}
	if !strings.Contains(err.Error(), "load system template") {
		t.Fatalf("Render error = %q, want missing template message", err.Error())
	}
}

func writePromptFiles(t *testing.T, baseDir string, task Task) {
	t.Helper()

	taskDir := filepath.Join(baseDir, string(task))
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("create prompt task dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "system.tmpl"), []byte("system "+string(task)+" {{ .DateFrom }}"), 0o644); err != nil {
		t.Fatalf("write system template: %v", err)
	}
	userTemplate := "user " + string(task) + " {{ .IssuesJSON }}"
	if task == TaskPeriodSummary {
		userTemplate = "user {{ .ProjectKey }} {{ .IssuesJSON }}"
	}
	if task == TaskAccountReport {
		userTemplate = "user {{ .AccountID }} {{ .IssuesJSON }}"
	}
	if err := os.WriteFile(filepath.Join(taskDir, "user.tmpl"), []byte(userTemplate), 0o644); err != nil {
		t.Fatalf("write user template: %v", err)
	}
}

func writeTemplateFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write template file: %v", err)
	}
	return path
}
