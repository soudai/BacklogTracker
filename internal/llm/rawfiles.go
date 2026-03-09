package llm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/soudai/BacklogTracker/internal/config"
	"github.com/soudai/BacklogTracker/internal/prompts"
)

func SaveRawResponse(baseDir, rawResponseDir, jobID string, provider config.Provider, task prompts.Task, payload []byte, now time.Time) (string, error) {
	safeJobID, err := validateArtifactJobID(jobID)
	if err != nil {
		return "", err
	}
	if len(payload) == 0 {
		return "", fmt.Errorf("raw response payload is empty")
	}

	targetDir := config.ResolvePath(baseDir, rawResponseDir)
	targetDir = filepath.Join(targetDir, string(provider), string(task))
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", fmt.Errorf("create raw response directory: %w", err)
	}

	filePath := filepath.Join(targetDir, fmt.Sprintf("%s-%s.json", safeJobID, now.UTC().Format("20060102T150405Z")))
	if err := os.WriteFile(filePath, payload, 0o600); err != nil {
		return "", fmt.Errorf("write raw response file: %w", err)
	}
	return filePath, nil
}

func validateArtifactJobID(jobID string) (string, error) {
	trimmed := strings.TrimSpace(jobID)
	if trimmed == "" {
		return "", fmt.Errorf("jobID is required")
	}
	for _, r := range trimmed {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-", r) {
			return "", fmt.Errorf("jobID contains invalid characters; only letters, digits, '.', '_', and '-' are allowed")
		}
	}
	return trimmed, nil
}
