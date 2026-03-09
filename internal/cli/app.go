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

	"github.com/soudai/BacklogTracker/internal/accountreport"
	"github.com/soudai/BacklogTracker/internal/backlogclient"
	"github.com/soudai/BacklogTracker/internal/config"
	"github.com/soudai/BacklogTracker/internal/initconfig"
	"github.com/soudai/BacklogTracker/internal/llm"
	notificationslack "github.com/soudai/BacklogTracker/internal/notifications/slack"
	"github.com/soudai/BacklogTracker/internal/periodsummary"
	"github.com/soudai/BacklogTracker/internal/prompts"
	"github.com/soudai/BacklogTracker/internal/storage/sqlite"
)

const (
	ExitCodeOK      = 0
	ExitCodeInput   = 1
	ExitCodeBacklog = 2
	ExitCodeLLM     = 3
	ExitCodeSlack   = 4
	ExitCodeStorage = 5
	ExitCodeInit    = 6
)

var (
	newLLMProvider   = llm.NewFromConfig
	saveRawResponse  = llm.SaveRawResponse
	newBacklogClient = backlogclient.New
	newSlackNotifier = notificationslack.NewFromConfig
	currentTime      = time.Now
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
		return runPeriodSummary(ctx, args[1:], stdout, stderr)
	case "account-report":
		return runAccountReport(ctx, args[1:], stdout, stderr)
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

func runPeriodSummary(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("period-summary", flag.ContinueOnError)
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
	flags.StringVar(&from, "from", "", "from date")
	flags.StringVar(&to, "to", "", "to date")
	flags.StringVar(&dateField, "date-field", "updated", "date field")
	flags.StringVar(&assignee, "assignee", "", "assignee")
	flags.Var(&statuses, "status", "status")

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

	if err := cfg.ValidateForReport(); err != nil {
		fmt.Fprintf(stderr, "invalid config: %v\n", err)
		return ExitCodeInput
	}

	if *dryRun {
		if err := validateDryRunConfig(cfg); err != nil {
			fmt.Fprintf(stderr, "invalid dry-run config: %v\n", err)
			return ExitCodeInput
		}
	}

	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		fmt.Fprintf(stderr, "invalid timezone: %v\n", err)
		return ExitCodeInput
	}
	fromDate, err := parseDateInLocation(from, location)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --from: %v\n", err)
		return ExitCodeInput
	}
	toDate, err := parseDateInLocation(to, location)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --to: %v\n", err)
		return ExitCodeInput
	}

	backlogClient, err := newBacklogClient(cfg.BacklogAPIKey, cfg.BacklogBaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "period-summary failed: %v\n", err)
		return ExitCodeInput
	}
	collector := backlogclient.NewCollector(backlogClient)

	providerInstance, err := newLLMProvider(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "period-summary failed: %v\n", err)
		return ExitCodeInput
	}

	var notifier notificationslack.Notifier
	if !*dryRun {
		notifier, err = newSlackNotifier(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "period-summary failed: %v\n", err)
			return ExitCodeInput
		}
	}

	store, err := sqlite.Open(config.ResolvePath(baseDir, cfg.SQLiteDBPath))
	if err != nil {
		fmt.Fprintf(stderr, "period-summary failed: %v\n", err)
		return ExitCodeStorage
	}
	defer store.Close()

	manager := prompts.Manager{
		PromptDir:     config.ResolvePath(baseDir, cfg.PromptDir),
		PreviewDir:    config.ResolvePath(baseDir, cfg.PromptPreviewDir),
		RetentionDays: cfg.PromptArtifactRetentionDays,
		Now:           currentTime,
	}
	service := periodsummary.Service{
		BaseDir:         baseDir,
		Config:          cfg,
		Collector:       collector,
		Statuses:        backlogClient,
		PromptManager:   manager,
		LLMProvider:     providerInstance,
		Notifier:        notifier,
		Store:           store,
		SaveRawResponse: saveRawResponse,
		Now:             currentTime,
	}

	result, err := service.Run(ctx, periodsummary.Input{
		From:      fromDate,
		To:        toDate,
		DateField: backlogclient.IssueDateField(dateField),
		Assignee:  assignee,
		Statuses:  append([]string(nil), statuses...),
		DryRun:    *dryRun,
	})
	if err != nil {
		fmt.Fprintf(stderr, "period-summary failed: %v\n", err)
		return exitCodeForPeriodSummaryError(err)
	}

	notificationStatus := "skipped (dry-run)"
	if result.NotificationSent {
		notificationStatus = result.NotificationResponse
	}
	fmt.Fprintf(stdout, "job_id: %s\nissue_count: %d\npreview_path: %s\nraw_response_path: %s\nreport_path: %s\nnotification: %s\n",
		result.JobID,
		result.IssueCount,
		result.PreviewPath,
		result.RawResponsePath,
		result.ReportPath,
		notificationStatus,
	)
	return ExitCodeOK
}

func runAccountReport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("account-report", flag.ContinueOnError)
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
	var account string
	var maxComments int
	flags.StringVar(&account, "account", "", "Backlog account")
	flags.StringVar(&from, "from", "", "from date")
	flags.StringVar(&to, "to", "", "to date")
	flags.IntVar(&maxComments, "max-comments", 0, "maximum comments")

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
	}

	if err := cfg.ValidateForReport(); err != nil {
		fmt.Fprintf(stderr, "invalid config: %v\n", err)
		return ExitCodeInput
	}

	fromDate, err := parseOptionalDateInLocation(from, cfg.Timezone)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --from: %v\n", err)
		return ExitCodeInput
	}
	toDate, err := parseOptionalDateInLocation(to, cfg.Timezone)
	if err != nil {
		fmt.Fprintf(stderr, "invalid --to: %v\n", err)
		return ExitCodeInput
	}

	backlogClient, err := newBacklogClient(cfg.BacklogAPIKey, cfg.BacklogBaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "account-report failed: %v\n", err)
		return ExitCodeInput
	}
	collector := backlogclient.NewCollector(backlogClient)

	providerInstance, err := newLLMProvider(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "account-report failed: %v\n", err)
		return ExitCodeInput
	}

	var notifier notificationslack.Notifier
	if !*dryRun {
		notifier, err = newSlackNotifier(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "account-report failed: %v\n", err)
			return ExitCodeInput
		}
	}

	store, err := sqlite.Open(config.ResolvePath(baseDir, cfg.SQLiteDBPath))
	if err != nil {
		fmt.Fprintf(stderr, "account-report failed: %v\n", err)
		return ExitCodeStorage
	}
	defer store.Close()

	manager := prompts.Manager{
		PromptDir:     config.ResolvePath(baseDir, cfg.PromptDir),
		PreviewDir:    config.ResolvePath(baseDir, cfg.PromptPreviewDir),
		RetentionDays: cfg.PromptArtifactRetentionDays,
		Now:           currentTime,
	}
	service := accountreport.Service{
		BaseDir:         baseDir,
		Config:          cfg,
		Collector:       collector,
		Comments:        backlogClient,
		PromptManager:   manager,
		LLMProvider:     providerInstance,
		Notifier:        notifier,
		Store:           store,
		SaveRawResponse: saveRawResponse,
		Now:             currentTime,
	}

	result, err := service.Run(ctx, accountreport.Input{
		Account:     account,
		From:        fromDate,
		To:          toDate,
		MaxComments: maxComments,
		DryRun:      *dryRun,
	})
	if err != nil {
		fmt.Fprintf(stderr, "account-report failed: %v\n", err)
		return exitCodeForAccountReportError(err)
	}

	notificationStatus := "skipped (dry-run)"
	if result.NotificationSent {
		notificationStatus = result.NotificationResponse
	}
	fmt.Fprintf(stdout, "job_id: %s\nissue_count: %d\npreview_path: %s\nraw_response_path: %s\nreport_path: %s\nnotification: %s\n",
		result.JobID,
		result.IssueCount,
		result.PreviewPath,
		result.RawResponsePath,
		result.ReportPath,
		notificationStatus,
	)
	return ExitCodeOK
}

func validateDryRunConfig(cfg config.Config) error {
	required := []struct {
		name  string
		value string
	}{
		{name: "BACKLOG_PROJECT_KEY", value: cfg.BacklogProjectKey},
		{name: "SQLITE_DB_PATH", value: cfg.SQLiteDBPath},
		{name: "RAW_RESPONSE_DIR", value: cfg.RawResponseDir},
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

	return cfg.ValidateProviderCredentials()
}

func exitCodeForPeriodSummaryError(err error) int {
	var appErr *periodsummary.Error
	if !errors.As(err, &appErr) {
		return ExitCodeInput
	}

	switch appErr.Kind {
	case periodsummary.KindInput:
		return ExitCodeInput
	case periodsummary.KindBacklog:
		return ExitCodeBacklog
	case periodsummary.KindLLM:
		return ExitCodeLLM
	case periodsummary.KindSlack:
		return ExitCodeSlack
	case periodsummary.KindStorage:
		return ExitCodeStorage
	default:
		return ExitCodeInput
	}
}

func exitCodeForAccountReportError(err error) int {
	var appErr *accountreport.Error
	if !errors.As(err, &appErr) {
		return ExitCodeInput
	}

	switch appErr.Kind {
	case accountreport.KindInput:
		return ExitCodeInput
	case accountreport.KindBacklog:
		return ExitCodeBacklog
	case accountreport.KindLLM:
		return ExitCodeLLM
	case accountreport.KindSlack:
		return ExitCodeSlack
	case accountreport.KindStorage:
		return ExitCodeStorage
	default:
		return ExitCodeInput
	}
}

func parseDateInLocation(value string, location *time.Location) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, fmt.Errorf("date is required")
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func parseOptionalDateInLocation(value, timezone string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}

	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	return parseDateInLocation(value, location)
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
