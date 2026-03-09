package initconfig

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunNonInteractiveWritesEnvFileAndCreatesDirectories(t *testing.T) {
	baseDir := t.TempDir()
	stdout := &bytes.Buffer{}

	err := Run(context.Background(), Options{
		BaseDir:        baseDir,
		EnvFile:        ".env.local",
		NonInteractive: true,
		SkipMigrate:    true,
		Yes:            true,
		StdIn:          strings.NewReader(""),
		StdOut:         stdout,
		Environ: []string{
			"BACKLOG_BASE_URL=https://example.backlog.com",
			"BACKLOG_API_KEY=test-backlog-key",
			"BACKLOG_PROJECT_KEY=PROJ",
			"LLM_PROVIDER=gemini",
			"GEMINI_API_KEY=test-gemini-key",
			"GEMINI_MODEL=gemini-2.5-pro",
			"SLACK_WEBHOOK_URL=https://hooks.slack.com/services/test",
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(baseDir, ".env.local")); err != nil {
		t.Fatalf("env file not created: %v", err)
	}
	for _, dir := range []string{
		"data",
		filepath.Join("data", "reports"),
		filepath.Join("data", "raw"),
		filepath.Join("data", "prompt-previews"),
		"prompts",
	} {
		if _, err := os.Stat(filepath.Join(baseDir, dir)); err != nil {
			t.Fatalf("directory %s not created: %v", dir, err)
		}
	}
}
