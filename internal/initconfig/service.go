package initconfig

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/soudai/BacklogTracker/internal/config"
	"github.com/soudai/BacklogTracker/internal/migrations"
)

type Options struct {
	BaseDir        string
	EnvFile        string
	NonInteractive bool
	Force          bool
	SkipMigrate    bool
	MigrateOnly    bool
	Yes            bool
	StdIn          io.Reader
	StdOut         io.Writer
	StdErr         io.Writer
	Environ        []string
}

func Run(ctx context.Context, opts Options) error {
	if opts.EnvFile == "" {
		opts.EnvFile = config.DefaultEnvFile
	}
	if opts.BaseDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
		opts.BaseDir = wd
	}
	if opts.StdIn == nil {
		opts.StdIn = os.Stdin
	}
	if opts.StdOut == nil {
		opts.StdOut = os.Stdout
	}
	if opts.StdErr == nil {
		opts.StdErr = os.Stderr
	}
	if opts.Environ == nil {
		opts.Environ = os.Environ()
	}
	if opts.MigrateOnly && opts.SkipMigrate {
		return fmt.Errorf("--migrate-only and --skip-migrate cannot be used together")
	}

	seedValues, err := loadSeedValues(opts.BaseDir, opts.EnvFile, opts.Environ)
	if err != nil {
		return err
	}

	if opts.MigrateOnly {
		cfg, err := config.New(seedValues)
		if err != nil {
			return err
		}
		if strings.TrimSpace(cfg.SQLiteDBPath) == "" || strings.TrimSpace(cfg.MigrationDir) == "" {
			return fmt.Errorf("SQLITE_DB_PATH and MIGRATION_DIR are required for --migrate-only")
		}
		if err := migrations.ApplyAll(ctx, config.ResolvePath(opts.BaseDir, cfg.SQLiteDBPath), config.ResolvePath(opts.BaseDir, cfg.MigrationDir)); err != nil {
			return err
		}
		fmt.Fprintf(opts.StdOut, "migrations applied: db=%s dir=%s\n", cfg.SQLiteDBPath, cfg.MigrationDir)
		return nil
	}

	targetEnvPath := config.ResolvePath(opts.BaseDir, opts.EnvFile)
	if _, err := os.Stat(targetEnvPath); err == nil && !opts.Force {
		return fmt.Errorf("%s already exists; rerun with --force to overwrite", opts.EnvFile)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect %s: %w", opts.EnvFile, err)
	}

	values := seedValues
	var prompt *prompter
	if !opts.NonInteractive {
		prompt = newPrompter(opts.StdIn, opts.StdOut)
		values, err = promptForValues(prompt, seedValues)
		if err != nil {
			return err
		}
	}

	cfg, err := config.New(values)
	if err != nil {
		return err
	}
	if err := cfg.ValidateForInit(); err != nil {
		return err
	}

	if !opts.Yes && !opts.NonInteractive {
		confirmed, err := prompt.confirm(cfg, opts.EnvFile)
		if err != nil {
			return err
		}
		if !confirmed {
			return fmt.Errorf("initialization cancelled")
		}
	}

	if err := os.MkdirAll(filepath.Dir(targetEnvPath), 0o755); err != nil {
		return fmt.Errorf("create env file directory: %w", err)
	}
	if err := config.WriteEnvFile(targetEnvPath, cfg.EnvMap()); err != nil {
		return fmt.Errorf("write %s: %w", opts.EnvFile, err)
	}

	if err := ensureDirectories(opts.BaseDir, cfg); err != nil {
		return err
	}
	if !opts.SkipMigrate {
		if err := migrations.ApplyAll(ctx, config.ResolvePath(opts.BaseDir, cfg.SQLiteDBPath), config.ResolvePath(opts.BaseDir, cfg.MigrationDir)); err != nil {
			return err
		}
	}

	fmt.Fprintf(opts.StdOut, "initialized: env=%s provider=%s db=%s\n", opts.EnvFile, cfg.LLMProvider, cfg.SQLiteDBPath)
	fmt.Fprintf(opts.StdOut, "next: backlog-tracker period-summary --project %s --from 2026-03-01 --to 2026-03-07 --provider %s\n", cfg.BacklogProjectKey, cfg.LLMProvider)
	return nil
}

func loadSeedValues(baseDir, envFile string, environ []string) (map[string]string, error) {
	values := config.DefaultValues()
	for _, path := range []string{".env.example", ".env.local.example", ".env"} {
		loaded, err := config.ReadOptionalEnvFile(config.ResolvePath(baseDir, path))
		if err != nil {
			return nil, err
		}
		values = config.MergeValues(values, loaded)
	}

	envPath := config.ResolvePath(baseDir, envFile)
	if filepath.Clean(envPath) != filepath.Clean(config.ResolvePath(baseDir, ".env")) {
		loaded, err := config.ReadOptionalEnvFile(envPath)
		if err != nil {
			return nil, err
		}
		values = config.MergeValues(values, loaded)
	}

	return config.MergeValues(values, config.ParseEnviron(environ)), nil
}

func promptForValues(prompt *prompter, seedValues map[string]string) (map[string]string, error) {
	values := config.MergeValues(seedValues)

	var err error
	values["BACKLOG_BASE_URL"], err = prompt.ask("Backlog base URL", values["BACKLOG_BASE_URL"], false)
	if err != nil {
		return nil, err
	}
	values["BACKLOG_API_KEY"], err = prompt.askSecret("Backlog API key", values["BACKLOG_API_KEY"], false)
	if err != nil {
		return nil, err
	}
	values["BACKLOG_PROJECT_KEY"], err = prompt.ask("Default Backlog project key", values["BACKLOG_PROJECT_KEY"], false)
	if err != nil {
		return nil, err
	}
	values["LLM_PROVIDER"], err = prompt.askProvider(values["LLM_PROVIDER"])
	if err != nil {
		return nil, err
	}

	switch provider, _ := config.NormalizeProvider(values["LLM_PROVIDER"]); provider {
	case config.ProviderGemini:
		values["GEMINI_API_KEY"], err = prompt.askSecret("Gemini API key", values["GEMINI_API_KEY"], false)
		if err != nil {
			return nil, err
		}
		values["GEMINI_MODEL"], err = prompt.ask("Gemini model", values["GEMINI_MODEL"], false)
		if err != nil {
			return nil, err
		}
	case config.ProviderChatGPT:
		values["OPENAI_API_KEY"], err = prompt.askSecret("OpenAI API key", values["OPENAI_API_KEY"], false)
		if err != nil {
			return nil, err
		}
		values["OPENAI_MODEL"], err = prompt.ask("OpenAI model", values["OPENAI_MODEL"], false)
		if err != nil {
			return nil, err
		}
	}

	values["SLACK_WEBHOOK_URL"], err = prompt.askSecret("Slack webhook URL", values["SLACK_WEBHOOK_URL"], true)
	if err != nil {
		return nil, err
	}
	values["SLACK_BOT_TOKEN"], err = prompt.askSecret("Slack bot token", values["SLACK_BOT_TOKEN"], true)
	if err != nil {
		return nil, err
	}
	values["SLACK_CHANNEL"], err = prompt.ask("Slack channel", values["SLACK_CHANNEL"], true)
	if err != nil {
		return nil, err
	}

	values["APP_DATA_DIR"], err = prompt.ask("Data directory", values["APP_DATA_DIR"], false)
	if err != nil {
		return nil, err
	}
	values["SQLITE_DB_PATH"], err = prompt.ask("SQLite DB path", values["SQLITE_DB_PATH"], false)
	if err != nil {
		return nil, err
	}
	values["MIGRATION_DIR"], err = prompt.ask("Migration directory", values["MIGRATION_DIR"], false)
	if err != nil {
		return nil, err
	}
	values["REPORT_DIR"], err = prompt.ask("Report output directory", values["REPORT_DIR"], false)
	if err != nil {
		return nil, err
	}
	values["RAW_RESPONSE_DIR"], err = prompt.ask("Raw response directory", values["RAW_RESPONSE_DIR"], false)
	if err != nil {
		return nil, err
	}
	values["PROMPT_PREVIEW_DIR"], err = prompt.ask("Prompt preview directory", values["PROMPT_PREVIEW_DIR"], false)
	if err != nil {
		return nil, err
	}
	values["PROMPT_DIR"], err = prompt.ask("Prompt template directory", values["PROMPT_DIR"], false)
	if err != nil {
		return nil, err
	}
	values["PROMPT_ARTIFACT_RETENTION_DAYS"], err = prompt.ask("Prompt artifact retention days", values["PROMPT_ARTIFACT_RETENTION_DAYS"], false)
	if err != nil {
		return nil, err
	}
	values["APP_TIMEZONE"], err = prompt.ask("Timezone", values["APP_TIMEZONE"], false)
	if err != nil {
		return nil, err
	}

	return values, nil
}

func (p *prompter) confirm(cfg config.Config, envFile string) (bool, error) {
	fmt.Fprintf(p.stdout, "\nConfig summary (%s)\n", envFile)
	fmt.Fprintf(p.stdout, "  Backlog base URL: %s\n", cfg.BacklogBaseURL)
	fmt.Fprintf(p.stdout, "  Backlog project : %s\n", cfg.BacklogProjectKey)
	fmt.Fprintf(p.stdout, "  Provider        : %s\n", cfg.LLMProvider)
	fmt.Fprintf(p.stdout, "  Slack webhook   : %s\n", mask(cfg.SlackWebhookURL))
	fmt.Fprintf(p.stdout, "  Slack bot token : %s\n", mask(cfg.SlackBotToken))
	fmt.Fprintf(p.stdout, "  SQLite DB path  : %s\n", cfg.SQLiteDBPath)
	fmt.Fprintf(p.stdout, "  Report dir      : %s\n", cfg.ReportDir)
	fmt.Fprintf(p.stdout, "  Prompt dir      : %s\n", cfg.PromptDir)
	fmt.Fprint(p.stdout, "Proceed? [y/N]: ")

	line, err := p.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func ensureDirectories(baseDir string, cfg config.Config) error {
	dirs := []string{
		config.ResolvePath(baseDir, cfg.DataDir),
		config.ResolvePath(baseDir, cfg.ReportDir),
		config.ResolvePath(baseDir, cfg.RawResponseDir),
		config.ResolvePath(baseDir, cfg.PromptPreviewDir),
		config.ResolvePath(baseDir, cfg.PromptDir),
		filepath.Dir(config.ResolvePath(baseDir, cfg.SQLiteDBPath)),
	}

	seen := map[string]struct{}{}
	for _, dir := range dirs {
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

type prompter struct {
	reader *bufio.Reader
	stdout io.Writer
}

func newPrompter(stdin io.Reader, stdout io.Writer) *prompter {
	return &prompter{
		reader: bufio.NewReader(stdin),
		stdout: stdout,
	}
}

func (p *prompter) ask(label, current string, allowBlank bool) (string, error) {
	for {
		if current != "" {
			fmt.Fprintf(p.stdout, "%s [%s]: ", label, current)
		} else {
			fmt.Fprintf(p.stdout, "%s: ", label)
		}
		line, err := p.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		value := strings.TrimSpace(line)
		if value == "" {
			if current != "" {
				return current, nil
			}
			if allowBlank {
				return "", nil
			}
			fmt.Fprintf(p.stdout, "%s is required.\n", label)
			continue
		}
		return value, nil
	}
}

func (p *prompter) askSecret(label, current string, allowBlank bool) (string, error) {
	display := ""
	if current != "" {
		display = "<configured>"
	}

	for {
		if display != "" {
			fmt.Fprintf(p.stdout, "%s [%s]: ", label, display)
		} else {
			fmt.Fprintf(p.stdout, "%s: ", label)
		}
		line, err := p.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		value := strings.TrimSpace(line)
		if value == "" {
			if current != "" {
				return current, nil
			}
			if allowBlank {
				return "", nil
			}
			fmt.Fprintf(p.stdout, "%s is required.\n", label)
			continue
		}
		return value, nil
	}
}

func (p *prompter) askProvider(current string) (string, error) {
	current = strings.TrimSpace(current)
	if current == "" {
		current = string(config.ProviderGemini)
	}
	for {
		value, err := p.ask("Default LLM provider (gemini/chatgpt)", current, false)
		if err != nil {
			return "", err
		}
		provider, err := config.NormalizeProvider(value)
		if err == nil {
			return string(provider), nil
		}
		fmt.Fprintln(p.stdout, "Provider must be gemini or chatgpt.")
	}
}

func mask(value string) string {
	if value == "" {
		return "(not set)"
	}
	if len(value) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(value)-4) + value[len(value)-4:]
}
