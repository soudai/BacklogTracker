package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/soudai/BacklogTracker/internal/config"
	"github.com/soudai/BacklogTracker/internal/initconfig"
)

const (
	ExitCodeOK    = 0
	ExitCodeInput = 1
	ExitCodeInit  = 6
)

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return ExitCodeInput
	}

	switch args[0] {
	case "init":
		return runInit(ctx, args[1:], stdin, stdout, stderr)
	case "period-summary":
		return runReportStub("period-summary", args[1:], stdout, stderr)
	case "account-report":
		return runReportStub("account-report", args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printUsage(stdout)
		return ExitCodeOK
	default:
		fmt.Fprintf(stderr, "unknown subcommand: %s\n\n", args[0])
		printUsage(stderr)
		return ExitCodeInput
	}
}

func runInit(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)

	envFile := flags.String("env-file", config.DefaultEnvFile, "target env file")
	nonInteractive := flags.Bool("non-interactive", false, "disable prompts")
	force := flags.Bool("force", false, "overwrite existing env file")
	skipMigrate := flags.Bool("skip-migrate", false, "skip sqlite migrations")
	migrateOnly := flags.Bool("migrate-only", false, "apply migrations only")
	yes := flags.Bool("yes", false, "skip confirmation")

	if err := flags.Parse(args); err != nil {
		return ExitCodeInit
	}

	baseDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "resolve working directory: %v\n", err)
		return ExitCodeInit
	}

	if err := initconfig.Run(ctx, initconfig.Options{
		BaseDir:        baseDir,
		EnvFile:        *envFile,
		NonInteractive: *nonInteractive,
		Force:          *force,
		SkipMigrate:    *skipMigrate,
		MigrateOnly:    *migrateOnly,
		Yes:            *yes,
		StdIn:          stdin,
		StdOut:         stdout,
		StdErr:         stderr,
		Environ:        os.Environ(),
	}); err != nil {
		fmt.Fprintf(stderr, "init failed: %v\n", err)
		return ExitCodeInit
	}

	return ExitCodeOK
}

func runReportStub(name string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)

	project := flags.String("project", "", "Backlog project key")
	provider := flags.String("provider", "", "LLM provider")
	timezone := flags.String("timezone", "", "timezone")
	flags.String("notify", "slack", "notification target")
	flags.Bool("dry-run", false, "run read-only execution")
	outputDir := flags.String("output-dir", "", "report output directory")
	dbPath := flags.String("db-path", "", "sqlite database path")
	promptDir := flags.String("prompt-dir", "", "prompt directory")
	envFile := flags.String("env-file", config.DefaultEnvFile, "env file")
	flags.Bool("verbose", false, "verbose logging")

	if name == "period-summary" {
		flags.String("from", "", "from date")
		flags.String("to", "", "to date")
		flags.String("date-field", "updated", "date field")
		flags.String("assignee", "", "assignee")
		flags.Var(&stringSliceFlag{}, "status", "status")
	}
	if name == "account-report" {
		flags.String("account", "", "Backlog account")
		flags.String("from", "", "from date")
		flags.String("to", "", "to date")
		flags.Int("max-comments", 0, "maximum comments")
	}

	if err := flags.Parse(args); err != nil {
		return ExitCodeInput
	}

	baseDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "resolve working directory: %v\n", err)
		return ExitCodeInput
	}

	values, err := config.ResolveValues(baseDir, *envFile, os.Environ(), map[string]string{
		"BACKLOG_PROJECT_KEY": *project,
		"LLM_PROVIDER":        *provider,
		"APP_TIMEZONE":        *timezone,
		"REPORT_DIR":          *outputDir,
		"SQLITE_DB_PATH":      *dbPath,
		"PROMPT_DIR":          *promptDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return ExitCodeInput
	}

	cfg, err := config.New(values)
	if err != nil {
		fmt.Fprintf(stderr, "parse config: %v\n", err)
		return ExitCodeInput
	}
	if err := cfg.ValidateForReport(); err != nil {
		fmt.Fprintf(stderr, "invalid config: %v\n", err)
		return ExitCodeInput
	}

	fmt.Fprintf(stdout, "%s is not implemented on this branch yet.\n", name)
	return ExitCodeInput
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  backlog-tracker init [flags]")
	fmt.Fprintln(out, "  backlog-tracker period-summary [flags]")
	fmt.Fprintln(out, "  backlog-tracker account-report [flags]")
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}
