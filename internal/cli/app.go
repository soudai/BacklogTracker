package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/soudai/BacklogTracker/internal/config"
	"github.com/soudai/BacklogTracker/internal/initconfig"
	"github.com/soudai/BacklogTracker/internal/prompts"
	"github.com/soudai/BacklogTracker/internal/storage/sqlite"
)

const (
	ExitCodeOK      = 0
	ExitCodeInput   = 1
	ExitCodeStorage = 5
	ExitCodeInit    = 6
)

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return ExitCodeInput
	}

	switch args[0] {
	case "init":
		return runInit(ctx, args[1:], stdin, stdout, stderr)
	case "period-summary":
		return runReportStub(ctx, "period-summary", args[1:], stdout, stderr)
	case "account-report":
		return runReportStub(ctx, "account-report", args[1:], stdout, stderr)
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
	dbPath := flags.String("db-path", "", "sqlite database path")
	nonInteractive := flags.Bool("non-interactive", false, "disable prompts")
	force := flags.Bool("force", false, "overwrite existing env file")
	skipMigrate := flags.Bool("skip-migrate", false, "skip sqlite migrations")
	migrateOnly := flags.Bool("migrate-only", false, "apply migrations only")
	yes := flags.Bool("yes", false, "skip confirmation")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitCodeOK
		}
		return ExitCodeInit
	}

	baseDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "resolve working directory: %v\n", err)
		return ExitCodeInit
	}

	overrides := map[string]string{}
	if *dbPath != "" {
		overrides["SQLITE_DB_PATH"] = *dbPath
	}

	if err := initconfig.Run(ctx, initconfig.Options{
		BaseDir:         baseDir,
		EnvFile:         *envFile,
		NonInteractive:  *nonInteractive,
		Force:           *force,
		SkipMigrate:     *skipMigrate,
		MigrateOnly:     *migrateOnly,
		Yes:             *yes,
		StdIn:           stdin,
		StdOut:          stdout,
		StdErr:          stderr,
		Environ:         os.Environ(),
		ConfigOverrides: overrides,
	}); err != nil {
		fmt.Fprintf(stderr, "init failed: %v\n", err)
		return ExitCodeInit
	}

	return ExitCodeOK
}

func runReportStub(ctx context.Context, name string, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)

	project := flags.String("project", "", "Backlog project key")
	provider := flags.String("provider", "", "LLM provider")
	timezone := flags.String("timezone", "", "timezone")
	flags.String("notify", "slack", "notification target")
	dryRun := flags.Bool("dry-run", false, "run read-only execution")
	outputDir := flags.String("output-dir", "", "report output directory")
	dbPath := flags.String("db-path", "", "sqlite database path")
	promptDir := flags.String("prompt-dir", "", "prompt directory")
	envFile := flags.String("env-file", config.DefaultEnvFile, "env file")
	flags.Bool("verbose", false, "verbose logging")

	var from string
	var to string
	var dateField string
	var assignee string
	var statuses stringSliceFlag
	var account string
	var maxComments int
	if name == "period-summary" {
		flags.StringVar(&from, "from", "", "from date")
		flags.StringVar(&to, "to", "", "to date")
		flags.StringVar(&dateField, "date-field", "updated", "date field")
		flags.StringVar(&assignee, "assignee", "", "assignee")
		flags.Var(&statuses, "status", "status")
	}
	if name == "account-report" {
		flags.StringVar(&account, "account", "", "Backlog account")
		flags.StringVar(&from, "from", "", "from date")
		flags.StringVar(&to, "to", "", "to date")
		flags.IntVar(&maxComments, "max-comments", 0, "maximum comments")
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitCodeOK
		}
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

	if *dryRun {
		if err := validateDryRunConfig(cfg); err != nil {
			fmt.Fprintf(stderr, "invalid dry-run config: %v\n", err)
			return ExitCodeInput
		}
		return runPromptDryRun(ctx, reportDryRunOptions{
			CommandName: name,
			BaseDir:     baseDir,
			Config:      cfg,
			From:        from,
			To:          to,
			DateField:   dateField,
			Assignee:    assignee,
			Statuses:    append([]string(nil), statuses...),
			Account:     account,
			MaxComments: maxComments,
			StdOut:      stdout,
			StdErr:      stderr,
		})
	}

	if err := cfg.ValidateForReport(); err != nil {
		fmt.Fprintf(stderr, "invalid config: %v\n", err)
		return ExitCodeInput
	}

	fmt.Fprintf(stdout, "%s is not implemented on this branch yet.\n", name)
	return ExitCodeOK
}

type reportDryRunOptions struct {
	CommandName string
	BaseDir     string
	Config      config.Config
	From        string
	To          string
	DateField   string
	Assignee    string
	Statuses    []string
	Account     string
	MaxComments int
	StdOut      io.Writer
	StdErr      io.Writer
}

func runPromptDryRun(ctx context.Context, opts reportDryRunOptions) int {
	task, err := prompts.ParseTask(opts.CommandName)
	if err != nil {
		fmt.Fprintf(opts.StdErr, "dry-run failed: %v\n", err)
		return ExitCodeInput
	}

	data, err := buildPromptTemplateData(task, opts)
	if err != nil {
		fmt.Fprintf(opts.StdErr, "dry-run failed: %v\n", err)
		return ExitCodeInput
	}

	now := time.Now().UTC()
	manager := prompts.Manager{
		PromptDir:     config.ResolvePath(opts.BaseDir, opts.Config.PromptDir),
		PreviewDir:    config.ResolvePath(opts.BaseDir, opts.Config.PromptPreviewDir),
		RetentionDays: opts.Config.PromptArtifactRetentionDays,
		Now: func() time.Time {
			return now
		},
	}

	rendered, err := manager.Render(task, data)
	if err != nil {
		fmt.Fprintf(opts.StdErr, "dry-run failed: %v\n", err)
		return ExitCodeStorage
	}

	jobID := buildDryRunJobID(task, now)
	previewPath, err := manager.SavePreview(jobID, rendered)
	if err != nil {
		fmt.Fprintf(opts.StdErr, "dry-run failed: %v\n", err)
		return ExitCodeStorage
	}

	if err := savePromptDryRun(ctx, opts, task, jobID, previewPath, rendered, now); err != nil {
		fmt.Fprintf(opts.StdErr, "dry-run failed: %v\n", err)
		return ExitCodeStorage
	}

	fmt.Fprintf(opts.StdOut, "job_id: %s\nprompt_hash: %s\npreview_path: %s\n\n", jobID, rendered.Hash, previewPath)
	fmt.Fprintln(opts.StdOut, "--- SYSTEM ---")
	fmt.Fprintln(opts.StdOut, rendered.System)
	fmt.Fprintln(opts.StdOut)
	fmt.Fprintln(opts.StdOut, "--- USER ---")
	fmt.Fprintln(opts.StdOut, rendered.User)
	return ExitCodeOK
}

func buildPromptTemplateData(task prompts.Task, opts reportDryRunOptions) (map[string]any, error) {
	outputSchema, err := prompts.OutputSchemaJSON(task)
	if err != nil {
		return nil, err
	}

	switch task {
	case prompts.TaskPeriodSummary:
		if strings.TrimSpace(opts.From) == "" || strings.TrimSpace(opts.To) == "" {
			return nil, fmt.Errorf("--from and --to are required for period-summary --dry-run")
		}
		return map[string]any{
			"ProjectKey":       opts.Config.BacklogProjectKey,
			"ProjectName":      opts.Config.BacklogProjectKey,
			"DateFrom":         opts.From,
			"DateTo":           opts.To,
			"IssueCount":       0,
			"IssuesJSON":       "[]",
			"OutputSchemaJSON": outputSchema,
			"Language":         "ja",
		}, nil
	case prompts.TaskAccountReport:
		if strings.TrimSpace(opts.Account) == "" {
			return nil, fmt.Errorf("--account is required for account-report --dry-run")
		}
		return map[string]any{
			"AccountID":        opts.Account,
			"AccountName":      opts.Account,
			"DateFrom":         defaultString(opts.From, "(not specified)"),
			"DateTo":           defaultString(opts.To, "(not specified)"),
			"IssuesJSON":       "[]",
			"OutputSchemaJSON": outputSchema,
			"Language":         "ja",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported prompt task %q", task)
	}
}

func savePromptDryRun(ctx context.Context, opts reportDryRunOptions, task prompts.Task, jobID, previewPath string, rendered prompts.RenderedPrompt, now time.Time) error {
	store, err := sqlite.Open(config.ResolvePath(opts.BaseDir, opts.Config.SQLiteDBPath))
	if err != nil {
		return fmt.Errorf("open sqlite store: %w", err)
	}
	defer store.Close()

	var targetAccount *string
	if strings.TrimSpace(opts.Account) != "" {
		targetAccount = &opts.Account
	}
	promptName := string(task)
	promptHash := rendered.Hash
	finishedAt := now

	if err := store.JobRuns().Save(ctx, sqlite.JobRun{
		JobID:         jobID,
		JobType:       string(task),
		Provider:      string(opts.Config.LLMProvider),
		ProjectKey:    opts.Config.BacklogProjectKey,
		TargetAccount: targetAccount,
		Status:        "completed",
		PromptName:    &promptName,
		PromptHash:    &promptHash,
		StartedAt:     now,
		FinishedAt:    &finishedAt,
	}); err != nil {
		return fmt.Errorf("save job_run: %w", err)
	}

	if err := store.PromptRuns().Save(ctx, sqlite.PromptRun{
		JobID:              jobID,
		TaskType:           string(task),
		SystemTemplate:     rendered.SystemTemplate,
		UserTemplate:       rendered.UserTemplate,
		PromptHash:         rendered.Hash,
		RenderedPromptPath: stringPointer(previewPath),
		CreatedAt:          now,
	}); err != nil {
		return fmt.Errorf("save prompt_run: %w", err)
	}

	return nil
}

func buildDryRunJobID(task prompts.Task, now time.Time) string {
	return fmt.Sprintf("%s-%s", task, now.UTC().Format("20060102T150405.000000000"))
}

func stringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func validateDryRunConfig(cfg config.Config) error {
	required := []struct {
		name  string
		value string
	}{
		{name: "BACKLOG_PROJECT_KEY", value: cfg.BacklogProjectKey},
		{name: "SQLITE_DB_PATH", value: cfg.SQLiteDBPath},
		{name: "PROMPT_DIR", value: cfg.PromptDir},
		{name: "PROMPT_PREVIEW_DIR", value: cfg.PromptPreviewDir},
	}

	var missing []string
	for _, setting := range required {
		if strings.TrimSpace(setting.value) == "" {
			missing = append(missing, setting.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required settings: %s", strings.Join(missing, ", "))
	}

	return nil
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
