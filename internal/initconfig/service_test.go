package initconfig

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soudai/BacklogTracker/internal/backlogclient"
)

func TestRunNonInteractiveWritesEnvFileAndCreatesDirectories(t *testing.T) {
	baseDir := t.TempDir()
	stdout := &bytes.Buffer{}
	connectionChecked := false

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
		NewConnectionChecker: func(apiKey, baseURL string) (ConnectionChecker, error) {
			return fakeConnectionChecker{
				checkConnection: func(ctx context.Context, projectIDOrKey string) (backlogclient.Project, error) {
					connectionChecked = true
					return backlogclient.Project{ID: 1, Key: projectIDOrKey, Name: "Project"}, nil
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !connectionChecked {
		t.Fatalf("expected backlog connection check to run")
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

func TestRunClassifiesBacklogAuthenticationErrors(t *testing.T) {
	baseDir := t.TempDir()

	err := Run(context.Background(), Options{
		BaseDir:        baseDir,
		EnvFile:        ".env.local",
		NonInteractive: true,
		SkipMigrate:    true,
		Yes:            true,
		StdIn:          strings.NewReader(""),
		StdOut:         &bytes.Buffer{},
		Environ: []string{
			"BACKLOG_BASE_URL=https://example.backlog.com",
			"BACKLOG_API_KEY=test-backlog-key",
			"BACKLOG_PROJECT_KEY=PROJ",
			"LLM_PROVIDER=gemini",
			"GEMINI_API_KEY=test-gemini-key",
			"GEMINI_MODEL=gemini-2.5-pro",
			"SLACK_WEBHOOK_URL=https://hooks.slack.com/services/test",
		},
		NewConnectionChecker: func(apiKey, baseURL string) (ConnectionChecker, error) {
			return fakeConnectionChecker{
				checkConnection: func(ctx context.Context, projectIDOrKey string) (backlogclient.Project, error) {
					return backlogclient.Project{}, &backlogclient.HTTPStatusError{
						Status:     "401 Unauthorized",
						StatusCode: 401,
						Method:     "GET",
						URL:        "https://example.backlog.com/api/v2/projects/PROJ?apiKey=test",
					}
				},
			}, nil
		},
	})
	if err == nil {
		t.Fatalf("Run expected error")
	}
	if !strings.Contains(err.Error(), "authentication error") {
		t.Fatalf("error = %q, want authentication classification", err)
	}
}

func TestRunClassifiesBacklogTemporaryErrors(t *testing.T) {
	baseDir := t.TempDir()

	err := Run(context.Background(), Options{
		BaseDir:        baseDir,
		EnvFile:        ".env.local",
		NonInteractive: true,
		SkipMigrate:    true,
		Yes:            true,
		StdIn:          strings.NewReader(""),
		StdOut:         &bytes.Buffer{},
		Environ: []string{
			"BACKLOG_BASE_URL=https://example.backlog.com",
			"BACKLOG_API_KEY=test-backlog-key",
			"BACKLOG_PROJECT_KEY=PROJ",
			"LLM_PROVIDER=gemini",
			"GEMINI_API_KEY=test-gemini-key",
			"GEMINI_MODEL=gemini-2.5-pro",
			"SLACK_WEBHOOK_URL=https://hooks.slack.com/services/test",
		},
		NewConnectionChecker: func(apiKey, baseURL string) (ConnectionChecker, error) {
			return fakeConnectionChecker{
				checkConnection: func(ctx context.Context, projectIDOrKey string) (backlogclient.Project, error) {
					return backlogclient.Project{}, &backlogclient.HTTPStatusError{
						Status:     "503 Service Unavailable",
						StatusCode: 503,
						Method:     "GET",
						URL:        "https://example.backlog.com/api/v2/projects/PROJ?apiKey=test",
					}
				},
			}, nil
		},
	})
	if err == nil {
		t.Fatalf("Run expected error")
	}
	if !strings.Contains(err.Error(), "temporary error") {
		t.Fatalf("error = %q, want temporary classification", err)
	}
}

func TestRunPropagatesConnectionCheckerFactoryError(t *testing.T) {
	baseDir := t.TempDir()

	err := Run(context.Background(), Options{
		BaseDir:        baseDir,
		EnvFile:        ".env.local",
		NonInteractive: true,
		SkipMigrate:    true,
		Yes:            true,
		StdIn:          strings.NewReader(""),
		StdOut:         &bytes.Buffer{},
		Environ: []string{
			"BACKLOG_BASE_URL=https://example.backlog.com",
			"BACKLOG_API_KEY=test-backlog-key",
			"BACKLOG_PROJECT_KEY=PROJ",
			"LLM_PROVIDER=gemini",
			"GEMINI_API_KEY=test-gemini-key",
			"GEMINI_MODEL=gemini-2.5-pro",
			"SLACK_WEBHOOK_URL=https://hooks.slack.com/services/test",
		},
		NewConnectionChecker: func(apiKey, baseURL string) (ConnectionChecker, error) {
			return nil, errors.New("boom")
		},
	})
	if err == nil {
		t.Fatalf("Run expected error")
	}
	if !strings.Contains(err.Error(), "create backlog connection checker") {
		t.Fatalf("error = %q, want factory error", err)
	}
}

type fakeConnectionChecker struct {
	checkConnection func(ctx context.Context, projectIDOrKey string) (backlogclient.Project, error)
}

func (f fakeConnectionChecker) CheckConnection(ctx context.Context, projectIDOrKey string) (backlogclient.Project, error) {
	return f.checkConnection(ctx, projectIDOrKey)
}
