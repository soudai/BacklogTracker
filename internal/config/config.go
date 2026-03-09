package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

type Provider string

const (
	ProviderGemini  Provider = "gemini"
	ProviderChatGPT Provider = "chatgpt"

	DefaultEnvFile = ".env.local"
)

var envGroups = [][]string{
	{
		"APP_ENV",
		"APP_TIMEZONE",
		"APP_DATA_DIR",
		"SQLITE_DB_PATH",
		"MIGRATION_DIR",
		"REPORT_DIR",
		"RAW_RESPONSE_DIR",
		"PROMPT_PREVIEW_DIR",
		"PROMPT_ARTIFACT_RETENTION_DAYS",
		"PROMPT_DIR",
	},
	{
		"BACKLOG_BASE_URL",
		"BACKLOG_API_KEY",
		"BACKLOG_PROJECT_KEY",
	},
	{
		"LLM_PROVIDER",
		"OPENAI_API_KEY",
		"OPENAI_MODEL",
		"GEMINI_API_KEY",
		"GEMINI_MODEL",
	},
	{
		"SLACK_WEBHOOK_URL",
		"SLACK_BOT_TOKEN",
		"SLACK_CHANNEL",
	},
}

type Config struct {
	AppEnv                      string
	Timezone                    string
	DataDir                     string
	SQLiteDBPath                string
	MigrationDir                string
	ReportDir                   string
	RawResponseDir              string
	PromptPreviewDir            string
	PromptArtifactRetentionDays int
	PromptDir                   string
	BacklogBaseURL              string
	BacklogAPIKey               string
	BacklogProjectKey           string
	LLMProvider                 Provider
	OpenAIAPIKey                string
	OpenAIModel                 string
	GeminiAPIKey                string
	GeminiModel                 string
	SlackWebhookURL             string
	SlackBotToken               string
	SlackChannel                string
}

func DefaultValues() map[string]string {
	return map[string]string{
		"APP_ENV":                        "local",
		"APP_TIMEZONE":                   "Asia/Tokyo",
		"APP_DATA_DIR":                   "./data",
		"SQLITE_DB_PATH":                 "./data/backlog-tracker.sqlite3",
		"MIGRATION_DIR":                  "./migrations",
		"REPORT_DIR":                     "./data/reports",
		"RAW_RESPONSE_DIR":               "./data/raw",
		"PROMPT_PREVIEW_DIR":             "./data/prompt-previews",
		"PROMPT_ARTIFACT_RETENTION_DAYS": "30",
		"PROMPT_DIR":                     "./prompts",
		"LLM_PROVIDER":                   string(ProviderGemini),
	}
}

func OrderedKeys() []string {
	var keys []string
	for _, group := range envGroups {
		keys = append(keys, group...)
	}
	return keys
}

func MergeValues(values ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, current := range values {
		for key, value := range current {
			if strings.TrimSpace(key) == "" {
				continue
			}
			merged[key] = value
		}
	}
	return merged
}

func ResolveValues(baseDir, envFile string, environ []string, overrides map[string]string) (map[string]string, error) {
	if envFile == "" {
		envFile = DefaultEnvFile
	}

	values := DefaultValues()

	globalEnv, err := ReadOptionalEnvFile(ResolvePath(baseDir, ".env"))
	if err != nil {
		return nil, err
	}
	values = MergeValues(values, globalEnv)

	envPath := ResolvePath(baseDir, envFile)
	if filepath.Clean(envPath) != filepath.Clean(ResolvePath(baseDir, ".env")) {
		localEnv, err := ReadOptionalEnvFile(envPath)
		if err != nil {
			return nil, err
		}
		values = MergeValues(values, localEnv)
	}

	values = MergeValues(values, ParseEnviron(environ), stripEmpty(overrides))
	return values, nil
}

func New(values map[string]string) (Config, error) {
	retention := 30
	if raw := strings.TrimSpace(values["PROMPT_ARTIFACT_RETENTION_DAYS"]); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("PROMPT_ARTIFACT_RETENTION_DAYS must be an integer: %w", err)
		}
		if parsed < 1 {
			return Config{}, fmt.Errorf("PROMPT_ARTIFACT_RETENTION_DAYS must be greater than 0")
		}
		retention = parsed
	}

	provider, err := NormalizeProvider(values["LLM_PROVIDER"])
	if err != nil {
		return Config{}, err
	}

	return Config{
		AppEnv:                      strings.TrimSpace(values["APP_ENV"]),
		Timezone:                    strings.TrimSpace(values["APP_TIMEZONE"]),
		DataDir:                     strings.TrimSpace(values["APP_DATA_DIR"]),
		SQLiteDBPath:                strings.TrimSpace(values["SQLITE_DB_PATH"]),
		MigrationDir:                strings.TrimSpace(values["MIGRATION_DIR"]),
		ReportDir:                   strings.TrimSpace(values["REPORT_DIR"]),
		RawResponseDir:              strings.TrimSpace(values["RAW_RESPONSE_DIR"]),
		PromptPreviewDir:            strings.TrimSpace(values["PROMPT_PREVIEW_DIR"]),
		PromptArtifactRetentionDays: retention,
		PromptDir:                   strings.TrimSpace(values["PROMPT_DIR"]),
		BacklogBaseURL:              strings.TrimSpace(values["BACKLOG_BASE_URL"]),
		BacklogAPIKey:               strings.TrimSpace(values["BACKLOG_API_KEY"]),
		BacklogProjectKey:           strings.TrimSpace(values["BACKLOG_PROJECT_KEY"]),
		LLMProvider:                 provider,
		OpenAIAPIKey:                strings.TrimSpace(values["OPENAI_API_KEY"]),
		OpenAIModel:                 strings.TrimSpace(values["OPENAI_MODEL"]),
		GeminiAPIKey:                strings.TrimSpace(values["GEMINI_API_KEY"]),
		GeminiModel:                 strings.TrimSpace(values["GEMINI_MODEL"]),
		SlackWebhookURL:             strings.TrimSpace(values["SLACK_WEBHOOK_URL"]),
		SlackBotToken:               strings.TrimSpace(values["SLACK_BOT_TOKEN"]),
		SlackChannel:                strings.TrimSpace(values["SLACK_CHANNEL"]),
	}, nil
}

func (c Config) ValidateForInit() error {
	missing := make([]string, 0, 8)
	required := []struct {
		name  string
		value string
	}{
		{name: "BACKLOG_BASE_URL", value: c.BacklogBaseURL},
		{name: "BACKLOG_API_KEY", value: c.BacklogAPIKey},
		{name: "BACKLOG_PROJECT_KEY", value: c.BacklogProjectKey},
		{name: "APP_TIMEZONE", value: c.Timezone},
		{name: "SQLITE_DB_PATH", value: c.SQLiteDBPath},
		{name: "MIGRATION_DIR", value: c.MigrationDir},
		{name: "PROMPT_DIR", value: c.PromptDir},
		{name: "REPORT_DIR", value: c.ReportDir},
		{name: "RAW_RESPONSE_DIR", value: c.RawResponseDir},
		{name: "PROMPT_PREVIEW_DIR", value: c.PromptPreviewDir},
	}
	for _, requiredSetting := range required {
		if isUnset(requiredSetting.value) {
			missing = append(missing, requiredSetting.name)
		}
	}
	if isUnset(c.SlackWebhookURL) && isUnset(c.SlackBotToken) {
		missing = append(missing, "SLACK_WEBHOOK_URL or SLACK_BOT_TOKEN")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required settings: %s", strings.Join(missing, ", "))
	}

	if err := validateURL("BACKLOG_BASE_URL", c.BacklogBaseURL); err != nil {
		return err
	}
	if c.SlackWebhookURL != "" {
		if err := validateURL("SLACK_WEBHOOK_URL", c.SlackWebhookURL); err != nil {
			return err
		}
	}

	return c.ValidateProviderCredentials()
}

func (c Config) ValidateForReport() error {
	if isUnset(c.BacklogBaseURL) || isUnset(c.BacklogAPIKey) || isUnset(c.BacklogProjectKey) {
		return fmt.Errorf("BACKLOG_BASE_URL, BACKLOG_API_KEY, and BACKLOG_PROJECT_KEY are required")
	}
	return c.ValidateProviderCredentials()
}

func (c Config) ValidateProviderCredentials() error {
	switch c.LLMProvider {
	case ProviderGemini:
		if isUnset(c.GeminiAPIKey) || isUnset(c.GeminiModel) {
			return fmt.Errorf("GEMINI_API_KEY and GEMINI_MODEL are required when LLM_PROVIDER=gemini")
		}
	case ProviderChatGPT:
		if isUnset(c.OpenAIAPIKey) || isUnset(c.OpenAIModel) {
			return fmt.Errorf("OPENAI_API_KEY and OPENAI_MODEL are required when LLM_PROVIDER=chatgpt")
		}
	default:
		return fmt.Errorf("unsupported LLM provider %q", c.LLMProvider)
	}
	return nil
}

func (c Config) EnvMap() map[string]string {
	return map[string]string{
		"APP_ENV":                        c.AppEnv,
		"APP_TIMEZONE":                   c.Timezone,
		"APP_DATA_DIR":                   c.DataDir,
		"SQLITE_DB_PATH":                 c.SQLiteDBPath,
		"MIGRATION_DIR":                  c.MigrationDir,
		"REPORT_DIR":                     c.ReportDir,
		"RAW_RESPONSE_DIR":               c.RawResponseDir,
		"PROMPT_PREVIEW_DIR":             c.PromptPreviewDir,
		"PROMPT_ARTIFACT_RETENTION_DAYS": strconv.Itoa(c.PromptArtifactRetentionDays),
		"PROMPT_DIR":                     c.PromptDir,
		"BACKLOG_BASE_URL":               c.BacklogBaseURL,
		"BACKLOG_API_KEY":                c.BacklogAPIKey,
		"BACKLOG_PROJECT_KEY":            c.BacklogProjectKey,
		"LLM_PROVIDER":                   string(c.LLMProvider),
		"OPENAI_API_KEY":                 c.OpenAIAPIKey,
		"OPENAI_MODEL":                   c.OpenAIModel,
		"GEMINI_API_KEY":                 c.GeminiAPIKey,
		"GEMINI_MODEL":                   c.GeminiModel,
		"SLACK_WEBHOOK_URL":              c.SlackWebhookURL,
		"SLACK_BOT_TOKEN":                c.SlackBotToken,
		"SLACK_CHANNEL":                  c.SlackChannel,
	}
}

func NormalizeProvider(raw string) (Provider, error) {
	switch Provider(strings.ToLower(strings.TrimSpace(raw))) {
	case "", ProviderGemini:
		return ProviderGemini, nil
	case ProviderChatGPT:
		return ProviderChatGPT, nil
	default:
		return "", fmt.Errorf("LLM_PROVIDER must be gemini or chatgpt")
	}
}

func ResolvePath(baseDir, value string) string {
	if value == "" {
		return baseDir
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}

func stripEmpty(values map[string]string) map[string]string {
	filtered := map[string]string{}
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		filtered[key] = value
	}
	return filtered
}

func validateURL(name, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s must be a valid URL: %w", name, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must include a scheme and host", name)
	}
	return nil
}

func isUnset(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}

	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "replace-with") ||
		strings.Contains(lower, "your-space.backlog.com") ||
		strings.Contains(lower, "hooks.slack.com/services/replace/me")
}
