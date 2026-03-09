package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
